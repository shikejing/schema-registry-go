package validator

import (
	"errors"
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func pFloat(f float64) *float64 { return &f }

func sampleSchema() *schema.Schema {
	return &schema.Schema{
		ID:   "form-1",
		Name: "sample",
		Fields: []*schema.Field{
			{Name: "title", Type: schema.TypeString, Required: true, Min: pFloat(1), Max: pFloat(10)},
			{Name: "age", Type: schema.TypeInteger, Min: pFloat(0), Max: pFloat(120)},
			{Name: "score", Type: schema.TypeNumber, Min: pFloat(0), Max: pFloat(100)},
			{Name: "active", Type: schema.TypeBoolean},
			{Name: "level", Type: schema.TypeString, Enum: []any{"low", "mid", "high"}},
			{
				Name: "address", Type: schema.TypeObject, Required: true,
				Properties: []*schema.Field{
					{Name: "city", Type: schema.TypeString, Required: true},
					{Name: "zip", Type: schema.TypeString, Min: pFloat(4), Max: pFloat(6)},
				},
			},
			{
				Name: "tags", Type: schema.TypeArray, Min: pFloat(1), Max: pFloat(3),
				Items: &schema.Field{Name: "tag", Type: schema.TypeString, Required: true},
			},
		},
	}
}

func TestValidateValid(t *testing.T) {
	s := sampleSchema()
	data := map[string]any{
		"title": "hello", "age": float64(30), "score": 88.5, "active": true,
		"level":   "mid",
		"address": map[string]any{"city": "Shanghai", "zip": "200000"},
		"tags":    []any{"a", "b"},
	}
	if err := Validate(s, data, nil); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidateAggregatesAllErrorsWithPaths(t *testing.T) {
	s := sampleSchema()
	data := map[string]any{
		// title missing (required)
		"age":    float64(200),   // out of range
		"score":  "not-a-number", // wrong type
		"active": "yes",          // wrong type
		"level":  "ultra",        // not in enum
		"address": map[string]any{
			"zip": "12", // nested: city missing (required), zip too short
		},
		"tags": []any{"ok", 42}, // second element wrong type
	}
	err := Validate(s, data, nil)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("want validator.Errors, got %T: %v", err, err)
	}
	wantPaths := map[string]bool{
		"$.title":        false,
		"$.age":          false,
		"$.score":        false,
		"$.active":       false,
		"$.level":        false,
		"$.address.city": false,
		"$.address.zip":  false,
		"$.tags[1]":      false,
	}
	for _, e := range errs {
		if _, ok := wantPaths[e.Path]; ok {
			wantPaths[e.Path] = true
		}
	}
	for p, found := range wantPaths {
		if !found {
			t.Errorf("expected an error at %s; all errors: %v", p, errs)
		}
	}
	if len(errs) < 8 {
		t.Errorf("expected at least 8 aggregated errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateJSON(t *testing.T) {
	s := sampleSchema()
	raw := []byte(`{"title":"x","address":{"city":"Y"}}`)
	if err := ValidateJSON(s, raw, nil); err != nil {
		t.Fatalf("valid json failed: %v", err)
	}
	if err := ValidateJSON(s, []byte(`{not json`), nil); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// staticResolver resolves a referenced schema by subject name.
type staticResolver map[string]*schema.Schema

func (m staticResolver) Resolve(r schema.Ref) (*schema.Schema, error) {
	if s, ok := m[r.Subject]; ok {
		return s, nil
	}
	return nil, errors.New("not found: " + r.Subject)
}

func TestValidateWithReference(t *testing.T) {
	addr := &schema.Schema{
		ID: "addr", Name: "address schema",
		Fields: []*schema.Field{
			{Name: "city", Type: schema.TypeString, Required: true},
		},
	}
	order := &schema.Schema{
		ID: "order", Name: "order form",
		Fields: []*schema.Field{
			{Name: "id", Type: schema.TypeString, Required: true},
			{Name: "shipTo", Type: schema.TypeObject, Required: true,
				Ref: &schema.Ref{Subject: "addresses", Version: 1}},
		},
	}
	res := staticResolver{"addresses": addr}
	err := Validate(order, map[string]any{
		"id":     "o1",
		"shipTo": map[string]any{"city": "Beijing"},
	}, res)
	if err != nil {
		t.Fatalf("valid referenced instance failed: %v", err)
	}
	err = Validate(order, map[string]any{"id": "o1", "shipTo": map[string]any{}}, res)
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("expected nested required error, got %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Path == "$.shipTo.city" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected $.shipTo.city error, got %v", errs)
	}
}
