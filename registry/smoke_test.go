package registry

import (
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func TestSmokeRegisterAndGet(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	s := &schema.Schema{
		ID:   "s1",
		Name: "form",
		Fields: []*schema.Field{
			{Name: "title", Type: schema.TypeString, Required: true},
		},
	}
	v1, err := r.Register("forms", s, "")
	if err != nil {
		t.Fatalf("register v1: %v", err)
	}
	if v1.Version != 1 {
		t.Fatalf("want version 1, got %d", v1.Version)
	}
	v2, err := r.Register("forms", s, "") // identical schema, still a new version
	if err != nil {
		t.Fatalf("register v2: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("want version 2, got %d", v2.Version)
	}
	got, err := r.Get("forms", 1, "")
	if err != nil || got.Fingerprint != v1.Fingerprint {
		t.Fatalf("get v1 mismatch: %v", err)
	}
	latest, err := r.GetLatest("forms", "")
	if err != nil || latest.Version != 2 {
		t.Fatalf("latest: %v", err)
	}
}
