package registry

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func simpleSchema(id string, fields ...*schema.Field) *schema.Schema {
	return &schema.Schema{ID: id, Name: "form-" + id, Fields: fields}
}

func TestRegisterCompatibilityRejectLeavesStateUntouched(t *testing.T) {
	r, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	v1schema := simpleSchema("1", &schema.Field{Name: "name", Type: schema.TypeString, Required: true})
	v1, err := r.Register("forms", v1schema, "")
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	originalFp := v1.Fingerprint

	// Under default BACKWARD mode, adding a REQUIRED field is incompatible.
	bad := simpleSchema("2",
		&schema.Field{Name: "name", Type: schema.TypeString, Required: true},
		&schema.Field{Name: "email", Type: schema.TypeString, Required: true},
	)
	_, err = r.Register("forms", bad, "")
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}

	// No half-finished version: still one version, latest unchanged and
	// historical content is byte-identical.
	versions, err := r.ListVersions("forms", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("expected exactly [1], got %v", versions)
	}
	latest, err := r.GetLatest("forms", "")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 1 || latest.Fingerprint != originalFp {
		t.Fatalf("history mutated: %+v", latest)
	}
}

func TestBackwardAllowsOptionalAddition(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	if _, err := r.Register("forms", simpleSchema("1",
		&schema.Field{Name: "name", Type: schema.TypeString, Required: true}), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register("forms", simpleSchema("2",
		&schema.Field{Name: "name", Type: schema.TypeString, Required: true},
		&schema.Field{Name: "nick", Type: schema.TypeString, Required: false}), ""); err != nil {
		t.Fatalf("optional add should be allowed under BACKWARD: %v", err)
	}
}

func TestConcurrentRegistration(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			s := simpleSchema(fmt.Sprintf("s%d", i),
				&schema.Field{Name: "name", Type: schema.TypeString, Required: false},
			)
			_, errs[i] = r.Register("forms", s, "")
		}()
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine %d failed: %v", i, e)
		}
	}
	versions, err := r.ListVersions("forms", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != n {
		t.Fatalf("expected %d versions, got %d", n, len(versions))
	}
	// Dense, non-repeating, ascending.
	seen := map[int]bool{}
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("hole or reorder at index %d: %v", i, versions)
		}
		if seen[v] {
			t.Fatalf("duplicate version %d", v)
		}
		seen[v] = true
	}
	// All versions are independently retrievable (serial equivalence).
	for v := 1; v <= n; v++ {
		if _, err := r.Get("forms", v, ""); err != nil {
			t.Fatalf("get version %d: %v", v, err)
		}
	}
}

func TestConcurrentIncompatibleRegistrationsExactlyOneWins(t *testing.T) {
	r, _ := New(t.TempDir(), nil, nil)
	defer r.Close()
	if _, err := r.Register("forms", simpleSchema("1",
		&schema.Field{Name: "name", Type: schema.TypeString, Required: true}), ""); err != nil {
		t.Fatal(err)
	}
	// All competitors add a required field; only the first (serialized)
	// registration is rejected against v1, but... note every subsequent
	// candidate is also incompatible with the state it sees, so every one
	// must be rejected and versions must remain [1].
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	rejected, accepted := 0, 0
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := r.Register("forms", simpleSchema("x",
				&schema.Field{Name: "name", Type: schema.TypeString, Required: true},
				&schema.Field{Name: "mail", Type: schema.TypeString, Required: true},
			), "")
			mu.Lock()
			if errors.Is(err, ErrIncompatible) {
				rejected++
			} else if err == nil {
				accepted++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	versions, _ := r.ListVersions("forms", "")
	// First competitor is checked against v1 and rejected. No competitor
	// ever becomes committed, so all reject and history stays at v1.
	if len(versions) != 1 || accepted != 0 || rejected != n {
		t.Fatalf("versions=%v accepted=%d rejected=%d, want [1], 0, %d", versions, accepted, rejected, n)
	}
}
