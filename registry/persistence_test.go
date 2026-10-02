package registry

import (
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func TestRestartRecoversVersionsAndMetadata(t *testing.T) {
	dir := t.TempDir()

	r1, err := New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s1 := &schema.Schema{ID: "s1", Name: "n",
		Fields: []*schema.Field{{Name: "a", Type: schema.TypeString, Required: true}}}
	s2 := &schema.Schema{ID: "s2", Name: "n",
		Fields: []*schema.Field{
			{Name: "a", Type: schema.TypeString, Required: true},
			{Name: "b", Type: schema.TypeString, Required: false},
		}}
	v1, err := r1.Register("forms", s1, "")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := r1.Register("forms", s2, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.SetConfig("forms", schema.SubjectConfig{Compatibility: schema.CompatForward}, ""); err != nil {
		t.Fatal(err)
	}
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen from the same directory.
	r2, err := New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()

	versions, err := r2.ListVersions("forms", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("versions not restored: %v", versions)
	}
	got1, err := r2.Get("forms", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if got1.Fingerprint != v1.Fingerprint || got1.Compatibility != schema.CompatBackward {
		t.Fatalf("v1 metadata not restored: %+v", got1)
	}
	if got1.CreatedAt == "" || got1.CreatedBy == "" {
		t.Fatalf("audit metadata missing: %+v", got1)
	}
	got2, _ := r2.Get("forms", 2, "")
	if got2.Fingerprint != v2.Fingerprint {
		t.Fatal("v2 fingerprint not restored")
	}
	// Subject config is restored.
	cfg, err := r2.GetConfig("forms", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Compatibility != schema.CompatForward {
		t.Fatalf("config not restored: %+v", cfg)
	}

	// History is immutable: the next registration is v3, not a rewrite.
	s3 := &schema.Schema{ID: "s3", Name: "n",
		Fields: []*schema.Field{
			{Name: "a", Type: schema.TypeString, Required: true},
			{Name: "b", Type: schema.TypeString, Required: false},
			{Name: "c", Type: schema.TypeString, Required: false},
		}}
	v3, err := r2.Register("forms", s3, "")
	if err != nil {
		t.Fatalf("post-restart register: %v", err)
	}
	if v3.Version != 3 {
		t.Fatalf("expected v3 after restart, got %d", v3.Version)
	}
}

func TestRestartRecoversReferences(t *testing.T) {
	dir := t.TempDir()
	r1, _ := New(dir, nil, nil)
	if _, err := r1.Register("addresses", addrSchema("a1"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r1.Register("orders", orderSchema("o1"), ""); err != nil {
		t.Fatal(err)
	}
	r1.Close()

	r2, err := New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	ov, err := r2.Get("orders", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Refs) != 1 || ov.Refs[0] != (schema.Ref{Subject: "addresses", Version: 1}) {
		t.Fatalf("pinned refs not restored: %+v", ov.Refs)
	}
	// Referenced version remains protected after restart.
	if err := r2.DeleteVersion("addresses", 1, ""); err == nil {
		t.Fatal("referenced version must remain protected after restart")
	}
}
