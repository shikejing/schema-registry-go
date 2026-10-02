package registry

import (
	"errors"
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func addrSchema(id string) *schema.Schema {
	return &schema.Schema{
		ID: id, Name: "address",
		Fields: []*schema.Field{
			{Name: "city", Type: schema.TypeString, Required: true},
		},
	}
}

func orderSchema(id string) *schema.Schema {
	return &schema.Schema{
		ID: id, Name: "order",
		Fields: []*schema.Field{
			{Name: "orderNo", Type: schema.TypeString, Required: true},
			{Name: "shipTo", Type: schema.TypeObject, Ref: &schema.Ref{Subject: "addresses"}},
		},
	}
}

func TestCrossSubjectReferenceIsPinned(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()

	if _, err := r.Register("addresses", addrSchema("a1"), ""); err != nil {
		t.Fatal(err)
	}
	ov, err := r.Register("orders", orderSchema("o1"), "")
	if err != nil {
		t.Fatalf("register with cross-subject ref: %v", err)
	}
	if len(ov.Refs) != 1 || ov.Refs[0].Subject != "addresses" || ov.Refs[0].Version != 1 {
		t.Fatalf("reference not pinned: %+v", ov.Refs)
	}
	// The stored schema itself carries the pinned version.
	if ov.Schema.Fields[1].Ref.Version != 1 {
		t.Fatalf("stored schema reference not pinned: %+v", ov.Schema.Fields[1].Ref)
	}

	// Pinned reference keeps validating against v1 even after addresses
	// evolve to v2 (which is itself a valid evolution).
	if _, err := r.Register("addresses",
		addrSchemaEvolved("a2"), ""); err != nil {
		t.Fatalf("evolve addresses: %v", err)
	}
	err = r.Validate("orders", 1,
		[]byte(`{"orderNo":"x","shipTo":{"city":"Shanghai"}}`), "")
	if err != nil {
		t.Fatalf("pinned v1 validation should still pass: %v", err)
	}
}

func addrSchemaEvolved(id string) *schema.Schema {
	return &schema.Schema{
		ID: id, Name: "address",
		Fields: []*schema.Field{
			{Name: "city", Type: schema.TypeString, Required: true},
			{Name: "zip", Type: schema.TypeString, Required: false},
		},
	}
}

func TestUnresolvedReferenceRejected(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	_, err := r.Register("orders", orderSchema("o1"), "")
	if !errors.Is(err, ErrBadReference) {
		t.Fatalf("expected ErrBadReference, got %v", err)
	}
	// No subject should have been created.
	if subjects, _ := r.ListSubjects(""); len(subjects) != 0 {
		t.Fatalf("expected no subjects after failed register, got %v", subjects)
	}
}

func TestDirectCycleRejected(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()

	// employee references department.
	employee := &schema.Schema{
		ID: "e", Name: "employee",
		Fields: []*schema.Field{
			{Name: "name", Type: schema.TypeString, Required: true},
			{Name: "dept", Type: schema.TypeObject, Ref: &schema.Ref{Subject: "departments"}},
		},
	}
	department := &schema.Schema{
		ID: "d", Name: "department",
		Fields: []*schema.Field{
			{Name: "title", Type: schema.TypeString, Required: true},
		},
	}
	if _, err := r.Register("departments", department, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register("employees", employee, ""); err != nil {
		t.Fatalf("employees should register: %v", err)
	}

	// Evolve departments to reference employees -> cycle.
	cyclic := &schema.Schema{
		ID: "d2", Name: "department",
		Fields: []*schema.Field{
			{Name: "title", Type: schema.TypeString, Required: true},
			{Name: "head", Type: schema.TypeObject, Ref: &schema.Ref{Subject: "employees"}},
		},
	}
	_, err := r.Register("departments", cyclic, "")
	if !errors.Is(err, ErrReferenceCycle) {
		t.Fatalf("expected ErrReferenceCycle, got %v", err)
	}
	// departments must remain at v1.
	versions, _ := r.ListVersions("departments", "")
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("cycle registration must not commit a version, got %v", versions)
	}
}

func TestIndirectCycleRejected(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	mk := func(id string, ref string) *schema.Schema {
		f := []*schema.Field{{Name: "id", Type: schema.TypeString, Required: true}}
		if ref != "" {
			f = append(f, &schema.Field{Name: "link", Type: schema.TypeObject, Ref: &schema.Ref{Subject: ref}})
		}
		return &schema.Schema{ID: id, Name: id, Fields: f}
	}
	// Build a -> b -> c in dependency order (references must resolve at
	// registration time), then evolve c to reference a, producing
	// a -> b -> c -> a.
	r2, _ := New(t.TempDir(), nil, nil)
	defer r2.Close()
	if _, err := r2.Register("c", mk("c1", ""), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Register("b", mk("b1", "c"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Register("a", mk("a1", "b"), ""); err != nil {
		t.Fatal(err)
	}
	// c evolves to reference a: a -> b -> c -> a.
	_, err := r2.Register("c", mk("c2", "a"), "")
	if !errors.Is(err, ErrReferenceCycle) {
		t.Fatalf("expected indirect cycle rejection, got %v", err)
	}
}

func TestSelfReferenceRejected(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	self := &schema.Schema{
		ID: "tree", Name: "tree node",
		Fields: []*schema.Field{
			{Name: "value", Type: schema.TypeString, Required: false},
			{Name: "child", Type: schema.TypeObject, Ref: &schema.Ref{Subject: "tree-nodes"}},
		},
	}
	// Self-reference on first registration also references a nonexistent
	// subject; after it exists it would be a direct cycle, and on first
	// creation it fails as an unresolved reference. Either way it must be
	// rejected without committing.
	_, err := r.Register("tree-nodes", self, "")
	if err == nil {
		t.Fatal("self-referential first version must be rejected")
	}
	if versions, _ := r.ListVersions("tree-nodes", ""); len(versions) != 0 {
		t.Fatalf("no version may be committed, got %v", versions)
	}
}

func TestDeleteReferencedVersionRejected(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	if _, err := r.Register("addresses", addrSchema("a1"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register("orders", orderSchema("o1"), ""); err != nil {
		t.Fatal(err)
	}
	// addresses@1 is referenced by orders@1: deletion refused.
	err := r.DeleteVersion("addresses", 1, "")
	if !errors.Is(err, ErrReferenced) {
		t.Fatalf("expected ErrReferenced, got %v", err)
	}
	// orders@1 is not referenced by anyone, and is the latest: deletion ok.
	if err := r.DeleteVersion("orders", 1, ""); err != nil {
		t.Fatalf("delete unreferenced latest: %v", err)
	}
	// Now addresses@1 can be deleted.
	if err := r.DeleteVersion("addresses", 1, ""); err != nil {
		t.Fatalf("delete addresses after referrer gone: %v", err)
	}
}

func TestDeleteOlderVersionRejected(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	mk := func(id string) *schema.Schema {
		return &schema.Schema{ID: id, Name: "n",
			Fields: []*schema.Field{{Name: "f", Type: schema.TypeString, Required: false}}}
	}
	r.Register("s", mk("1"), "")
	r.Register("s", mk("2"), "")
	if err := r.DeleteVersion("s", 1, ""); err == nil {
		t.Fatal("deleting non-latest version must be rejected (would leave a hole)")
	}
	if err := r.DeleteVersion("s", 2, ""); err != nil {
		t.Fatalf("delete latest v2: %v", err)
	}
	if err := r.DeleteVersion("s", 1, ""); err != nil {
		t.Fatalf("v1 is now latest and should delete: %v", err)
	}
	if versions, _ := r.ListVersions("s", ""); len(versions) != 0 {
		// subject should be gone
		if len(versions) > 0 {
			t.Fatalf("expected empty subject, got %v", versions)
		}
	}
}
