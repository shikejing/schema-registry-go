package schema

import "testing"

func TestValidateDefinition(t *testing.T) {
	good := &Schema{ID: "id", Name: "n", Fields: []*Field{
		{Name: "s", Type: TypeString},
		{Name: "i", Type: TypeInteger},
		{Name: "o", Type: TypeObject, Properties: []*Field{
			{Name: "x", Type: TypeString},
		}},
		{Name: "arr", Type: TypeArray, Items: &Field{Name: "item", Type: TypeNumber}},
	}}
	if err := ValidateDefinition(good); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
	min, max := 5.0, 1.0
	bad := []*Schema{
		{Name: "n"}, // missing id
		{ID: "x"},   // missing name
		{ID: "x", Name: "n", Fields: []*Field{{Name: "f"}}}, // missing type
		{ID: "x", Name: "n", Fields: []*Field{
			{Name: "f", Type: TypeString}, {Name: "f", Type: TypeString}}}, // duplicate
		{ID: "x", Name: "n", Fields: []*Field{{Name: "f", Type: TypeArray}}},                        // missing items
		{ID: "x", Name: "n", Fields: []*Field{{Name: "f", Type: TypeObject, Ref: &Ref{}}}},          // empty ref
		{ID: "x", Name: "n", Fields: []*Field{{Name: "f", Type: TypeString, Min: &min, Max: &max}}}, // range
	}
	for i, s := range bad {
		if err := ValidateDefinition(s); err == nil {
			t.Errorf("bad definition %d accepted: %+v", i, s)
		}
	}
}

func TestExtractRefs(t *testing.T) {
	s := &Schema{ID: "x", Name: "n", Fields: []*Field{
		{Name: "a", Type: TypeObject, Ref: &Ref{Subject: "s1"}},
		{Name: "b", Type: TypeObject, Properties: []*Field{
			{Name: "c", Type: TypeObject, Ref: &Ref{Subject: "s2", Version: 3}},
		}},
		{Name: "d", Type: TypeArray, Items: &Field{Name: "i", Type: TypeObject, Ref: &Ref{Subject: "s1"}}},
	}}
	refs := ExtractRefs(s)
	if len(refs) != 2 {
		t.Fatalf("want 2 deduped refs, got %d: %+v", len(refs), refs)
	}
}

func TestFingerprintStable(t *testing.T) {
	s1 := &Schema{ID: "x", Name: "n", Fields: []*Field{{Name: "a", Type: TypeString}}}
	s2 := &Schema{ID: "x", Name: "n", Fields: []*Field{{Name: "a", Type: TypeString}}}
	s3 := &Schema{ID: "x", Name: "n", Fields: []*Field{{Name: "a", Type: TypeInteger}}}
	if Fingerprint(s1) != Fingerprint(s2) {
		t.Fatal("identical schemas should have same fingerprint")
	}
	if Fingerprint(s1) == Fingerprint(s3) {
		t.Fatal("different schemas should have different fingerprints")
	}
}
