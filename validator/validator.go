// Package validator validates instance data against a specific registered
// schema version, aggregating all errors with JSON-pointer-like field paths.
package validator

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/shikejing/schema-registry-go/schema"
)

// FieldError describes one validation failure located by field path.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Errors aggregates every error found during validation.
type Errors []FieldError

func (e Errors) Error() string {
	var b bytes.Buffer
	b.WriteString("validation failed:")
	for _, fe := range e {
		b.WriteString("\n  " + fe.Path + ": " + fe.Message)
	}
	return b.String()
}

// Validate checks decoded instance data (as produced by encoding/json) against
// s, expanding references through resolve. All errors are collected;
// validation does not stop at the first failure.
func Validate(s *schema.Schema, data any, resolve schema.Resolver) error {
	if s == nil {
		return Errors{{Path: "$", Message: "nil schema"}}
	}
	obj, ok := data.(map[string]any)
	if !ok {
		return Errors{{Path: "$", Message: "instance must be a JSON object"}}
	}
	var errs Errors
	for _, f := range s.Fields {
		validateMember(f, obj, "$", &errs, resolve)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// ValidateJSON checks a JSON-encoded instance against s.
func ValidateJSON(s *schema.Schema, raw []byte, resolve schema.Resolver) error {
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return Errors{{Path: "$", Message: "invalid JSON: " + err.Error()}}
	}
	return Validate(s, data, resolve)
}

// validateMember validates the value of f found as a key of obj at
// parentPath, recording errors at parentPath + "." + f.Name.
func validateMember(f *schema.Field, obj map[string]any, parentPath string, errs *Errors, resolve schema.Resolver) {
	v, present := obj[f.Name]
	validateAt(f, v, present, parentPath+"."+f.Name, errs, resolve)
}

// validateAt validates a single value against f at path. present is false when
// the value was absent from its containing object.
func validateAt(f *schema.Field, v any, present bool, path string, errs *Errors, resolve schema.Resolver) {
	// A missing value or an explicit JSON null fails a required field and
	// skips type checks. Callers validating array elements always pass
	// present=true; null elements therefore skip item-level checks rather
	// than being reported as missing members.
	if !present || v == nil {
		if f.Required {
			*errs = append(*errs, FieldError{Path: path, Message: "field is required"})
		}
		return
	}
	switch f.Type {
	case schema.TypeString:
		s, ok := v.(string)
		if !ok {
			*errs = append(*errs, FieldError{Path: path, Message: "expected string"})
			return
		}
		if f.Min != nil && float64(len(s)) < *f.Min {
			*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("string length %d is below minimum %g", len(s), *f.Min)})
		}
		if f.Max != nil && float64(len(s)) > *f.Max {
			*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("string length %d exceeds maximum %g", len(s), *f.Max)})
		}
		checkEnum(f, s, path, errs)
	case schema.TypeInteger:
		fl, ok := v.(float64)
		if !ok || fl != float64(int64(fl)) {
			*errs = append(*errs, FieldError{Path: path, Message: "expected integer"})
			return
		}
		checkRange(f, fl, path, errs)
		checkEnum(f, fl, path, errs)
	case schema.TypeNumber:
		fl, ok := v.(float64)
		if !ok {
			*errs = append(*errs, FieldError{Path: path, Message: "expected number"})
			return
		}
		checkRange(f, fl, path, errs)
		checkEnum(f, fl, path, errs)
	case schema.TypeBoolean:
		if _, ok := v.(bool); !ok {
			*errs = append(*errs, FieldError{Path: path, Message: "expected boolean"})
		}
	case schema.TypeObject:
		child, ok := v.(map[string]any)
		if !ok {
			*errs = append(*errs, FieldError{Path: path, Message: "expected object"})
			return
		}
		validateObjectFields(f, child, path, errs, resolve)
	case schema.TypeArray:
		arr, ok := v.([]any)
		if !ok {
			*errs = append(*errs, FieldError{Path: path, Message: "expected array"})
			return
		}
		if f.Min != nil && float64(len(arr)) < *f.Min {
			*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("array length %d is below minimum %g", len(arr), *f.Min)})
		}
		if f.Max != nil && float64(len(arr)) > *f.Max {
			*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("array length %d exceeds maximum %g", len(arr), *f.Max)})
		}
		if f.Items != nil {
			for i, item := range arr {
				itemPath := fmt.Sprintf("%s[%d]", path, i)
				if item == nil {
					if f.Items.Required {
						*errs = append(*errs, FieldError{Path: itemPath, Message: "array element is required"})
					}
					continue
				}
				validateAt(f.Items, item, true, itemPath, errs, resolve)
			}
		}
	}
}

// validateObjectFields validates child against the fields of an object field,
// expanding a reference when the field carries one.
func validateObjectFields(f *schema.Field, child map[string]any, path string, errs *Errors, resolve schema.Resolver) {
	if f.Ref != nil {
		refSchema, err := resolve.Resolve(*f.Ref)
		if err != nil {
			*errs = append(*errs, FieldError{Path: path, Message: "unresolved reference " + f.Ref.Subject + ": " + err.Error()})
			return
		}
		for _, rf := range refSchema.Fields {
			validateMember(rf, child, path, errs, resolve)
		}
		return
	}
	for _, p := range f.Properties {
		validateMember(p, child, path, errs, resolve)
	}
}

func checkRange(f *schema.Field, v float64, path string, errs *Errors) {
	if f.Min != nil && v < *f.Min {
		*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("value %g is below minimum %g", v, *f.Min)})
	}
	if f.Max != nil && v > *f.Max {
		*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("value %g exceeds maximum %g", v, *f.Max)})
	}
}

func checkEnum(f *schema.Field, v any, path string, errs *Errors) {
	if len(f.Enum) == 0 {
		return
	}
	for _, allowed := range f.Enum {
		if allowed == v {
			return
		}
	}
	*errs = append(*errs, FieldError{Path: path, Message: fmt.Sprintf("value %v is not one of the allowed enum values", v)})
}
