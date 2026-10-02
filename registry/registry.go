// Package registry is the versioned, persistent schema registry core.
package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/shikejing/schema-registry-go/audit"
	"github.com/shikejing/schema-registry-go/auth"
	"github.com/shikejing/schema-registry-go/schema"
	"github.com/shikejing/schema-registry-go/validator"
)

const snapshotFileName = "snapshot.json"

// Registry centrally manages versioned form schemas. All state-mutating
// operations hold mu for their whole duration, which makes concurrent
// registrations to the same subject serializable: versions are gap-free,
// never duplicated, and failures leave no partial version behind.
type Registry struct {
	mu      sync.Mutex
	dataDir string
	snap    *snapshot

	authz *auth.Authorizer
	audit audit.Logger
}

// New opens or creates a registry backed by dataDir and recovers any
// previously committed state from disk. authz may be nil to disable
// authentication (mainly for tests); auditLog may be nil to disable auditing.
func New(dataDir string, authz *auth.Authorizer, auditLog audit.Logger) (*Registry, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	snap, err := loadSnapshot(filepath.Join(dataDir, snapshotFileName))
	if err != nil {
		return nil, err
	}
	return &Registry{dataDir: dataDir, snap: snap, authz: authz, audit: auditLog}, nil
}

// ---- authentication / authorization helpers ----

// checkAuth authenticates token and authorizes action. A nil authorizer
// disables access control entirely. Every attempt (including denials) is
// recorded in the audit trail.
func (r *Registry) checkAuth(token string, action auth.Action, subject string, version int) (*auth.Principal, error) {
	if r.authz == nil {
		return &auth.Principal{Subject: "anonymous", Roles: []auth.Role{auth.RoleAdmin}}, nil
	}
	p, err := r.authz.Authenticate(token)
	if err != nil {
		r.record(p, "AUTH_"+string(action), subject, version, audit.ResultDenied, err.Error())
		return nil, mapAuthError(err)
	}
	if err := r.authz.Authorize(p, action); err != nil {
		r.record(p, "AUTH_"+string(action), subject, version, audit.ResultDenied, err.Error())
		return nil, mapAuthError(err)
	}
	return p, nil
}

func (r *Registry) record(p *auth.Principal, action, subject string, version int, result audit.Result, detail string) {
	if r.audit == nil {
		return
	}
	actor := "unknown"
	if p != nil {
		actor = p.Subject
	}
	_ = r.audit.Log(audit.NewEvent(actor, action, subject, version, result, detail))
}

func mapAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		return ErrUnauthorized
	case errors.Is(err, auth.ErrForbidden):
		return ErrForbidden
	default:
		return err
	}
}

// ---- state helpers (callers hold mu) ----

func (r *Registry) state(subject string) subjectState {
	return r.snap.Subjects[subject]
}

func (r *Registry) effectiveConfig(subject string) schema.SubjectConfig {
	st, ok := r.snap.Subjects[subject]
	cfg := schema.SubjectConfig{Compatibility: schema.DefaultCompatibility}
	if ok && st.Config.Compatibility != "" {
		cfg = st.Config
	}
	return cfg
}

func (r *Registry) findVersion(subject string, version int) (*schema.Version, error) {
	st, ok := r.snap.Subjects[subject]
	if !ok {
		return nil, fmt.Errorf("%w: subject %q", ErrNotFound, subject)
	}
	for _, v := range st.Versions {
		if v.Version == version {
			return v, nil
		}
	}
	return nil, fmt.Errorf("%w: subject %q version %d", ErrNotFound, subject, version)
}

// Resolve implements schema.Resolver for stored (pinned) references.
func (r *Registry) Resolve(ref schema.Ref) (*schema.Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolveLocked(ref)
}

// resolveLocked resolves a pinned reference while the caller already holds
// mu (e.g. during a mutation), avoiding re-entrant locking.
func (r *Registry) resolveLocked(ref schema.Ref) (*schema.Schema, error) {
	v, err := r.findVersion(ref.Subject, ref.Version)
	if err != nil {
		return nil, err
	}
	return v.Schema, nil
}

// ---- read API ----

// Get fetches one specific version.
func (r *Registry) Get(subject string, version int, token string) (*schema.Version, error) {
	if _, err := r.checkAuth(token, auth.ActionRead, subject, version); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.findVersion(subject, version)
}

// GetLatest fetches the highest version of a subject.
func (r *Registry) GetLatest(subject, token string) (*schema.Version, error) {
	if _, err := r.checkAuth(token, auth.ActionRead, subject, 0); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.snap.Subjects[subject]
	if !ok || len(st.Versions) == 0 {
		return nil, fmt.Errorf("%w: subject %q", ErrNotFound, subject)
	}
	return st.Versions[len(st.Versions)-1], nil
}

// ListVersions returns the sorted version numbers of a subject.
func (r *Registry) ListVersions(subject, token string) ([]int, error) {
	if _, err := r.checkAuth(token, auth.ActionRead, subject, 0); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.snap.Subjects[subject]
	if !ok {
		return nil, fmt.Errorf("%w: subject %q", ErrNotFound, subject)
	}
	out := make([]int, 0, len(st.Versions))
	for _, v := range st.Versions {
		out = append(out, v.Version)
	}
	sort.Ints(out)
	return out, nil
}

// ListSubjects returns all known subject names, sorted.
func (r *Registry) ListSubjects(token string) ([]string, error) {
	if _, err := r.checkAuth(token, auth.ActionRead, "", 0); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.snap.Subjects))
	for name := range r.snap.Subjects {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// GetConfig returns a subject's effective configuration.
func (r *Registry) GetConfig(subject, token string) (schema.SubjectConfig, error) {
	if _, err := r.checkAuth(token, auth.ActionRead, subject, 0); err != nil {
		return schema.SubjectConfig{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.snap.Subjects[subject]; !ok {
		return schema.SubjectConfig{}, fmt.Errorf("%w: subject %q", ErrNotFound, subject)
	}
	return r.effectiveConfig(subject), nil
}

// Validate checks JSON instance data against a specific schema version.
func (r *Registry) Validate(subject string, version int, raw []byte, token string) error {
	if _, err := r.checkAuth(token, auth.ActionRead, subject, version); err != nil {
		return err
	}
	r.mu.Lock()
	v, err := r.findVersion(subject, version)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return validator.ValidateJSON(v.Schema, raw, r)
}

// now is overridable in tests.
var now = func() time.Time { return time.Now().UTC() }

// ---- lifecycle ----

// Close flushes persistence and audit buffers.
func (r *Registry) Close() error {
	if r.audit != nil {
		return r.audit.Close()
	}
	return nil
}
