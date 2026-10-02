package registry

import (
	"errors"
	"testing"

	"github.com/shikejing/schema-registry-go/audit"
	"github.com/shikejing/schema-registry-go/auth"
	"github.com/shikejing/schema-registry-go/schema"
)

func newAuthz() *auth.Authorizer {
	a := auth.NewAuthorizer()
	a.RegisterToken("admin-token", "admin", auth.RoleAdmin)
	a.RegisterToken("writer-token", "writer", auth.RoleWriter)
	a.RegisterToken("reader-token", "reader", auth.RoleReader)
	return a
}

func TestAuthorization(t *testing.T) {
	auditLog := audit.NewMemoryLogger()
	r, _ := New(t.TempDir(), newAuthz(), auditLog)
	defer r.Close()
	s := &schema.Schema{ID: "1", Name: "n",
		Fields: []*schema.Field{{Name: "a", Type: schema.TypeString, Required: true}}}

	// Missing token.
	if _, err := r.Register("forms", s, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing token: want ErrUnauthorized, got %v", err)
	}
	// Reader cannot write.
	if _, err := r.Register("forms", s, "reader-token"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader write: want ErrForbidden, got %v", err)
	}
	// Writer can register.
	if _, err := r.Register("forms", s, "writer-token"); err != nil {
		t.Fatalf("writer register: %v", err)
	}
	// Writer cannot delete.
	if err := r.DeleteVersion("forms", 1, "writer-token"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("writer delete: want ErrForbidden, got %v", err)
	}
	// Reader can read.
	if _, err := r.GetLatest("forms", "reader-token"); err != nil {
		t.Fatalf("reader read: %v", err)
	}
	// Unknown token.
	if _, err := r.GetLatest("forms", "nope"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown token: want ErrUnauthorized, got %v", err)
	}
	// Admin can delete.
	if err := r.DeleteVersion("forms", 1, "admin-token"); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
}

func TestAuditTrailRecordsActorTimeAndOutcome(t *testing.T) {
	auditLog := audit.NewMemoryLogger()
	r, _ := New(t.TempDir(), newAuthz(), auditLog)
	defer r.Close()
	s := &schema.Schema{ID: "1", Name: "n",
		Fields: []*schema.Field{{Name: "a", Type: schema.TypeString, Required: true}}}
	r.Register("forms", s, "writer-token")
	r.Register("forms", s, "reader-token") // denied: forbidden
	r.DeleteVersion("forms", 1, "admin-token")

	events, err := auditLog.Query()
	if err != nil {
		t.Fatal(err)
	}
	var allowed, denied int
	var sawActor bool
	var sawTime bool
	var sawDelete bool
	for _, e := range events {
		if e.Time != "" {
			sawTime = true
		}
		switch e.Result {
		case audit.ResultAllowed:
			allowed++
		case audit.ResultDenied:
			denied++
		}
		if e.Actor == "writer" && e.Action == "REGISTER" && e.Subject == "forms" && e.Version == 1 {
			sawActor = true
		}
		if e.Actor == "admin" && e.Action == "DELETE" && e.Result == audit.ResultAllowed {
			sawDelete = true
		}
	}
	if !sawActor || !sawTime || !sawDelete {
		t.Fatalf("audit trail incomplete: %+v", events)
	}
	if allowed < 2 || denied < 1 {
		t.Fatalf("expected >=2 allowed and >=1 denied events, got allowed=%d denied=%d: %+v", allowed, denied, events)
	}
}

func TestSetConfigRequiresAdmin(t *testing.T) {
	r, _ := New(t.TempDir(), newAuthz(), nil)
	defer r.Close()
	if err := r.SetConfig("forms", schema.SubjectConfig{Compatibility: schema.CompatFull}, "writer-token"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("writer set config: want ErrForbidden, got %v", err)
	}
	if err := r.SetConfig("forms", schema.SubjectConfig{Compatibility: schema.CompatFull}, "admin-token"); err != nil {
		t.Fatalf("admin set config: %v", err)
	}
	got, err := r.GetConfig("forms", "reader-token")
	if err != nil {
		t.Fatal(err)
	}
	if got.Compatibility != schema.CompatFull {
		t.Fatalf("config = %s", got.Compatibility)
	}
}
