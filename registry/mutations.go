package registry

import (
	"encoding/json"
	"fmt"

	"github.com/shikejing/schema-registry-go/audit"
	"github.com/shikejing/schema-registry-go/auth"
	"github.com/shikejing/schema-registry-go/compat"
	"github.com/shikejing/schema-registry-go/schema"
)

// Register validates, compatibility-checks and commits a new immutable
// version under subject.
//
// The call is linearized with every other mutation by the registry mutex, so
// concurrent registrations produce the same result as serial execution:
// dense, non-repeating version numbers. All checks happen against in-memory
// state and the new version is appended and persisted in one critical
// section; if anything fails before persistence, no state changes and no
// half-finished version is observable.
func (r *Registry) Register(subject string, s *schema.Schema, token string) (*schema.Version, error) {
	p, err := r.checkAuth(token, auth.ActionWrite, subject, 0)
	if err != nil {
		return nil, err
	}
	if subject == "" {
		return nil, fmt.Errorf("%w: empty subject", ErrInvalidSchema)
	}
	if err := schema.ValidateDefinition(s); err != nil {
		r.record(p, "REGISTER", subject, 0, audit.ResultDenied, err.Error())
		return nil, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	cfg := r.effectiveConfig(subject)
	st := r.snap.Subjects[subject]
	latest := latestVersion(st.Versions)

	// Deep-copy the candidate and pin every reference to a concrete existing
	// version, so stored documents never depend on "latest" and remain
	// immutable even if the referenced subject evolves.
	candidate, pinnedRefs, err := r.cloneAndPin(s)
	if err != nil {
		r.record(p, "REGISTER", subject, 0, audit.ResultDenied, err.Error())
		return nil, err
	}

	// Cycle detection happens before compatibility work; the candidate is
	// treated as already present (its refs are known) so that a reference
	// path leading back to the candidate subject is rejected.
	if cycle := r.detectCycle(subject, candidate); cycle != nil {
		err := fmt.Errorf("%w: %s", ErrReferenceCycle, formatCycle(cycle))
		r.record(p, "REGISTER", subject, 0, audit.ResultDenied, err.Error())
		return nil, err
	}

	// Compatibility against the previous version. The resolver reads only
	// committed versions (the candidate itself is excluded), so references
	// always resolve to immutable history.
	if latest != nil {
		if err := compat.Check(latest.Schema, candidate, cfg.Compatibility, &lockedResolver{r: r}); err != nil {
			r.record(p, "REGISTER", subject, 0, audit.ResultDenied, err.Error())
			return nil, fmt.Errorf("%w: %v", ErrIncompatible, err)
		}
	}

	next := 1
	if latest != nil {
		next = latest.Version + 1
	}
	ver := &schema.Version{
		Subject:       subject,
		Version:       next,
		Schema:        candidate,
		Fingerprint:   schema.Fingerprint(candidate),
		Compatibility: cfg.Compatibility,
		Refs:          pinnedRefs,
		CreatedAt:     now().Format(timeFormat),
		CreatedBy:     p.Subject,
	}
	st.Versions = append(st.Versions, ver)
	st.Config = cfg
	r.snap.Subjects[subject] = st

	if err := r.persistLocked(); err != nil {
		// Roll back the append: persistence failure must not leave the
		// in-memory state ahead of the durable state.
		st.Versions = st.Versions[:len(st.Versions)-1]
		if len(st.Versions) == 0 {
			delete(r.snap.Subjects, subject)
		} else {
			r.snap.Subjects[subject] = st
		}
		r.record(p, "REGISTER", subject, next, audit.ResultDenied, err.Error())
		return nil, err
	}

	r.record(p, "REGISTER", subject, next, audit.ResultAllowed,
		fmt.Sprintf("registered version %d (%s, %d refs)", next, cfg.Compatibility, len(pinnedRefs)))
	return ver, nil
}

// SetConfig replaces a subject's compatibility configuration. It does not
// re-evaluate already committed versions; the new mode applies to subsequent
// registrations.
func (r *Registry) SetConfig(subject string, cfg schema.SubjectConfig, token string) error {
	p, err := r.checkAuth(token, auth.ActionAdmin, subject, 0)
	if err != nil {
		return err
	}
	switch cfg.Compatibility {
	case schema.CompatNone, schema.CompatBackward, schema.CompatForward, schema.CompatFull:
	default:
		err := fmt.Errorf("%w: unknown compatibility %q", ErrInvalidSchema, cfg.Compatibility)
		r.record(p, "SET_CONFIG", subject, 0, audit.ResultDenied, err.Error())
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.snap.Subjects[subject]
	st.Config = cfg
	r.snap.Subjects[subject] = st
	if err := r.persistLocked(); err != nil {
		return err
	}
	r.record(p, "SET_CONFIG", subject, 0, audit.ResultAllowed, "compatibility="+string(cfg.Compatibility))
	return nil
}

// persistLocked writes the current snapshot. Callers must hold mu.
func (r *Registry) persistLocked() error {
	return saveSnapshot(r.dataDir+"/"+snapshotFileName, r.snap)
}

// cloneAndPin returns a deep copy of s in which every reference is resolved
// against currently committed versions and pinned to its concrete version
// number, along with the flat list of pinned refs.
func (r *Registry) cloneAndPin(s *schema.Schema) (*schema.Schema, []schema.Ref, error) {
	// Round-trip through JSON gives a safe deep copy of the JSON-shaped model.
	b, err := json.Marshal(s)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	var cp schema.Schema
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidSchema, err)
	}
	var out []schema.Ref
	seen := map[schema.Ref]bool{}
	var walk func(f *schema.Field) error
	walk = func(f *schema.Field) error {
		if f == nil {
			return nil
		}
		if f.Ref != nil {
			ref := *f.Ref
			if ref.Version == 0 {
				target, err := r.latestLocked(ref.Subject)
				if err != nil {
					return fmt.Errorf("%w: subject %q: %v", ErrBadReference, ref.Subject, err)
				}
				ref.Version = target.Version
			} else if _, err := r.findVersion(ref.Subject, ref.Version); err != nil {
				return fmt.Errorf("%w: %v", ErrBadReference, err)
			}
			f.Ref = &ref
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
		for _, p := range f.Properties {
			if err := walk(p); err != nil {
				return err
			}
		}
		if f.Items != nil {
			if err := walk(f.Items); err != nil {
				return err
			}
		}
		return nil
	}
	for _, f := range cp.Fields {
		if err := walk(f); err != nil {
			return nil, nil, err
		}
	}
	return &cp, out, nil
}

func latestVersion(versions []*schema.Version) *schema.Version {
	if len(versions) == 0 {
		return nil
	}
	return versions[len(versions)-1]
}

func (r *Registry) latestLocked(subject string) (*schema.Version, error) {
	st, ok := r.snap.Subjects[subject]
	if !ok || len(st.Versions) == 0 {
		return nil, fmt.Errorf("subject %q has no versions", subject)
	}
	return st.Versions[len(st.Versions)-1], nil
}

// lockedResolver adapts resolveLocked to schema.Resolver while mu is held.
type lockedResolver struct{ r *Registry }

func (l *lockedResolver) Resolve(ref schema.Ref) (*schema.Schema, error) {
	return l.r.resolveLocked(ref)
}
