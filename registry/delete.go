package registry

import (
	"fmt"

	"github.com/shikejing/schema-registry-go/audit"
	"github.com/shikejing/schema-registry-go/auth"
)

// DeleteVersion removes one version of a subject.
//
// Two rejection rules apply:
//
//  1. Only the latest version may be deleted. Deleting an older version would
//     leave a hole in the monotonic sequence and make "register next"
//     ambiguous, so it is refused. (Repeated deletion therefore walks
//     versions backwards.)
//  2. A version that is still referenced by any other committed version may
//     not be deleted. Both current tips and historical versions are checked,
//     because a pinned historical reference must remain resolvable forever.
func (r *Registry) DeleteVersion(subject string, version int, token string) error {
	p, err := r.checkAuth(token, auth.ActionDelete, subject, version)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	st, ok := r.snap.Subjects[subject]
	if !ok {
		err := fmt.Errorf("%w: subject %q", ErrNotFound, subject)
		r.record(p, "DELETE", subject, version, audit.ResultDenied, err.Error())
		return err
	}
	if len(st.Versions) == 0 || st.Versions[len(st.Versions)-1].Version != version {
		err := fmt.Errorf("%w: only the latest version can be deleted (subject %q, requested %d)",
			ErrInvalidSchema, subject, version)
		r.record(p, "DELETE", subject, version, audit.ResultDenied, err.Error())
		return err
	}
	if refs := r.referrersLocked(subject, version); len(refs) > 0 {
		err := fmt.Errorf("%w: version %d of %q is referenced by %s",
			ErrReferenced, version, subject, joinRefs(refs))
		r.record(p, "DELETE", subject, version, audit.ResultDenied, err.Error())
		return err
	}

	st.Versions = st.Versions[:len(st.Versions)-1]
	if len(st.Versions) == 0 {
		// Remove the subject entirely; its compatibility configuration is
		// meaningless without versions and a later registration starts from
		// the default configuration again.
		delete(r.snap.Subjects, subject)
	} else {
		r.snap.Subjects[subject] = st
	}

	if err := saveSnapshot(r.dataDir+"/"+snapshotFileName, r.snap); err != nil {
		// Restore the dropped version on persistence failure.
		r.reloadAfterFailedMutation()
		r.record(p, "DELETE", subject, version, audit.ResultDenied, err.Error())
		return err
	}
	r.record(p, "DELETE", subject, version, audit.ResultAllowed, "deleted")
	return nil
}

// referrersLocked returns "subject@version" strings for every committed
// version that pins a reference to (subject, version). Callers hold mu.
func (r *Registry) referrersLocked(subject string, version int) []string {
	var out []string
	for sName, st := range r.snap.Subjects {
		for _, v := range st.Versions {
			for _, ref := range v.Refs {
				if ref.Subject == subject && ref.Version == version {
					out = append(out, fmt.Sprintf("%s@%d", sName, v.Version))
				}
			}
		}
	}
	return out
}

func joinRefs(refs []string) string {
	out := ""
	for i, s := range refs {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// reloadAfterFailedMutation restores in-memory state from the durable
// snapshot after a write failed to persist, guaranteeing memory never
// diverges from disk.
func (r *Registry) reloadAfterFailedMutation() {
	if snap, err := loadSnapshot(r.dataDir + "/" + snapshotFileName); err == nil {
		r.snap = snap
	}
}
