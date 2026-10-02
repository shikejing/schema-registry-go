package compat

import (
	"errors"
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func strField(name string, required bool) *schema.Field {
	return &schema.Field{Name: name, Type: schema.TypeString, Required: required}
}

func TestFirstVersionAlwaysCompatible(t *testing.T) {
	s := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}}
	for _, mode := range []schema.Compatibility{schema.CompatBackward, schema.CompatForward, schema.CompatFull} {
		if err := Check(nil, s, mode, nil); err != nil {
			t.Fatalf("mode %s: first version should pass: %v", mode, err)
		}
	}
}

func TestBackwardCompatibility(t *testing.T) {
	old := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}}

	// Adding an optional field is backward compatible.
	ok := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("b", false)}}
	if err := Check(old, ok, schema.CompatBackward, nil); err != nil {
		t.Fatalf("optional field added should pass: %v", err)
	}

	// Adding a required field is not backward compatible.
	bad := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("b", true)}}
	if err := Check(old, bad, schema.CompatBackward, nil); err == nil {
		t.Fatal("required field added should be rejected")
	}

	// Removing a field is backward compatible.
	removed := &schema.Schema{ID: "1", Name: "n", Fields: nil}
	if err := Check(old, removed, schema.CompatBackward, nil); err != nil {
		t.Fatalf("field removal should pass backward: %v", err)
	}

	// Type change is never compatible.
	typed := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{{Name: "a", Type: schema.TypeInteger, Required: true}}}
	if err := Check(old, typed, schema.CompatBackward, nil); err == nil {
		t.Fatal("type change should be rejected")
	}
}

func TestForwardCompatibility(t *testing.T) {
	old := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}}

	// Adding any field is forward compatible (old readers ignore it).
	added := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("b", true)}}
	if err := Check(old, added, schema.CompatForward, nil); err != nil {
		t.Fatalf("adding field should pass forward: %v", err)
	}

	// Removing a required field is NOT forward compatible.
	if err := Check(old, &schema.Schema{ID: "1", Name: "n"}, schema.CompatForward, nil); err == nil {
		t.Fatal("removing required field should be rejected forward")
	}

	// Removing an optional field is forward compatible.
	withOpt := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("b", false)}}
	if err := Check(withOpt, &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}}, schema.CompatForward, nil); err != nil {
		t.Fatalf("removing optional field should pass forward: %v", err)
	}
}

func TestFullCompatibility(t *testing.T) {
	old := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("opt", false)}}
	// FULL = BACKWARD and FORWARD. Adding an optional field and removing an
	// optional field are legal in both directions, so they pass FULL.
	pass := map[string]*schema.Schema{
		"added optional":   {ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("opt", false), strField("b", false)}},
		"removed optional": {ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}},
	}
	for name, s := range pass {
		if err := Check(old, s, schema.CompatFull, nil); err != nil {
			t.Errorf("%s should pass FULL: %v", name, err)
		}
	}
	reject := map[string]*schema.Schema{
		"added required":   {ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("opt", false), strField("b", true)}},
		"removed required": {ID: "1", Name: "n", Fields: []*schema.Field{strField("opt", false)}},
	}
	for name, s := range reject {
		if err := Check(old, s, schema.CompatFull, nil); err == nil {
			t.Errorf("%s should be rejected under FULL", name)
		}
	}
	same := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("opt", false)}}
	if err := Check(old, same, schema.CompatFull, nil); err != nil {
		t.Fatalf("identical schema should pass FULL: %v", err)
	}
}

func pFloat(f float64) *float64 { return &f }

func TestRangeAndEnumRules(t *testing.T) {
	old := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{
		{Name: "n", Type: schema.TypeInteger, Required: true, Min: pFloat(0), Max: pFloat(10), Enum: []any{float64(1), float64(2)}},
	}}
	// Widen range + remove enum value => backward compatible.
	widen := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{
		{Name: "n", Type: schema.TypeInteger, Required: true, Min: pFloat(-5), Max: pFloat(20), Enum: []any{float64(1)}},
	}}
	if err := Check(old, widen, schema.CompatBackward, nil); err != nil {
		t.Fatalf("widening should pass backward: %v", err)
	}
	// Narrow range + add enum value => forward compatible.
	narrow := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{
		{Name: "n", Type: schema.TypeInteger, Required: true, Min: pFloat(2), Max: pFloat(8), Enum: []any{float64(1), float64(2), float64(3)}},
	}}
	if err := Check(old, narrow, schema.CompatForward, nil); err != nil {
		t.Fatalf("narrowing should pass forward: %v", err)
	}
	// Backward rejects added enum value.
	addEnum := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{
		{Name: "n", Type: schema.TypeInteger, Required: true, Enum: []any{float64(1), float64(2), float64(3)}},
	}}
	if err := Check(old, addEnum, schema.CompatBackward, nil); err == nil {
		t.Fatal("added enum value should fail backward")
	}
}

func TestNoneMode(t *testing.T) {
	old := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}}
	drastic := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{{Name: "a", Type: schema.TypeBoolean}}}
	if err := Check(old, drastic, schema.CompatNone, nil); err != nil {
		t.Fatalf("NONE must accept any change: %v", err)
	}
}

func TestIncompatibleErrorIsIdentifiable(t *testing.T) {
	old := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true)}}
	bad := &schema.Schema{ID: "1", Name: "n", Fields: []*schema.Field{strField("a", true), strField("b", true)}}
	err := Check(old, bad, schema.CompatBackward, nil)
	var inc *IncompatibleError
	if !errors.As(err, &inc) {
		t.Fatalf("expected *IncompatibleError, got %v", err)
	}
	if len(inc.Issues) == 0 {
		t.Fatal("expected aggregated issues")
	}
}
