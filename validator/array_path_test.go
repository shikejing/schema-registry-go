package validator

import (
	"errors"
	"testing"

	"github.com/shikejing/schema-registry-go/schema"
)

func TestArrayOfObjectsErrorPaths(t *testing.T) {
	s := &schema.Schema{
		ID: "x", Name: "n",
		Fields: []*schema.Field{
			{
				Name: "items", Type: schema.TypeArray, Required: true,
				Items: &schema.Field{
					Name: "item", Type: schema.TypeObject, Required: true,
					Properties: []*schema.Field{
						{Name: "id", Type: schema.TypeString, Required: true},
					},
				},
			},
		},
	}
	data := map[string]any{
		"items": []any{
			map[string]any{"id": "ok"},
			map[string]any{}, // missing required nested field
			"not-an-object",  // wrong element type
		},
	}
	err := Validate(s, data, nil)
	var errs Errors
	if !errors.As(err, &errs) {
		t.Fatalf("expected errors, got %v", err)
	}
	want := map[string]bool{
		"$.items[1].id": false,
		"$.items[2]":    false,
	}
	for _, e := range errs {
		if _, ok := want[e.Path]; ok {
			want[e.Path] = true
		}
		t.Logf("error at %s: %s", e.Path, e.Message)
	}
	for p, f := range want {
		if !f {
			t.Errorf("missing expected error path %q", p)
		}
	}
}
