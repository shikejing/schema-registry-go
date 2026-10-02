package registry

import (
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func TestConfigChangeAppliesToFutureRegistrations(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	base := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{
		{Name: "a", Type: schema.TypeString, Required: true},
	}}
	if _, err := r.Register("forms", base, ""); err != nil {
		t.Fatal(err)
	}
	// BACKWARD (default) rejects a new required field.
	bad := &schema.Schema{ID: "2", Name: "n", Fields: []*schema.Field{
		{Name: "a", Type: schema.TypeString, Required: true},
		{Name: "b", Type: schema.TypeString, Required: true},
	}}
	if _, err := r.Register("forms", bad, ""); err == nil {
		t.Fatal("default BACKWARD should reject required add")
	}
	// Switch to FORWARD: adding required fields is allowed.
	if err := r.SetConfig("forms", schema.SubjectConfig{Compatibility: schema.CompatForward}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register("forms", bad, ""); err != nil {
		t.Fatalf("FORWARD should allow required add: %v", err)
	}
	// The committed version records the mode in force at registration.
	v2, _ := r.Get("forms", 2, "")
	if v2.Compatibility != schema.CompatForward {
		t.Fatalf("version metadata = %s, want FORWARD", v2.Compatibility)
	}
	v1, _ := r.Get("forms", 1, "")
	if v1.Compatibility != schema.CompatBackward {
		t.Fatalf("v1 metadata changed: %s", v1.Compatibility)
	}
}

func TestValidateInstanceThroughRegistry(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	s := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{
		{Name: "a", Type: schema.TypeString, Required: true},
	}}
	if _, err := r.Register("forms", s, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate("forms", 1, []byte(`{"a":"x"}`), ""); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := r.Validate("forms", 1, []byte(`{}`), ""); err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	if err := r.Validate("forms", 99, []byte(`{}`), ""); err == nil {
		t.Fatal("expected not found for unknown version")
	}
}
