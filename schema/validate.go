package schema

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateDefinition checks a schema document for structural well-formedness.
// It is independent of compatibility checking: malformed definitions are
// rejected before any version or compatibility logic runs.
func ValidateDefinition(s *Schema) error {
	var errs []error
	if s == nil {
		return errors.New("schema is nil")
	}
	if strings.TrimSpace(s.ID) == "" {
		errs = append(errs, errors.New("schema id is required"))
	}
	if strings.TrimSpace(s.Name) == "" {
		errs = append(errs, errors.New("schema name is required"))
	}
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if f == nil {
			errs = append(errs, errors.New("schema fields contain a nil entry"))
			continue
		}
		if seen[f.Name] {
			errs = append(errs, fmt.Errorf("duplicate field %q", f.Name))
		}
		seen[f.Name] = true
		validateField(f, f.Name, &errs)
	}
	return errors.Join(errs...)
}

func validateField(f *Field, path string, errs *[]error) {
	bad := func(format string, args ...any) {
		*errs = append(*errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
	}
	validType := map[FieldType]bool{
		TypeString: true, TypeInteger: true, TypeNumber: true,
		TypeBoolean: true, TypeObject: true, TypeArray: true,
	}
	if f.Name == "" {
		bad("field name is required")
	}
	if !validType[f.Type] {
		bad("unknown or missing type %q", f.Type)
		return
	}
	switch {
	case f.Ref != nil:
		if f.Ref.Subject == "" {
			bad("reference subject is required")
		}
		if f.Type != TypeObject {
			bad("referenced field must be of type object")
		}
		if len(f.Properties) > 0 || f.Items != nil || len(f.Enum) > 0 || f.Min != nil || f.Max != nil {
			bad("referenced field must not declare inline properties, items, enum or range")
		}
	case f.Type == TypeObject:
		names := map[string]bool{}
		for _, p := range f.Properties {
			if p == nil {
				bad("contains a nil property")
				continue
			}
			if names[p.Name] {
				bad("duplicate nested property %q", p.Name)
			}
			names[p.Name] = true
			validateField(p, path+"."+p.Name, errs)
		}
	case f.Type == TypeArray:
		if f.Items == nil {
			bad("array field requires an items declaration")
		} else {
			validateField(f.Items, path+"[]", errs)
		}
		if len(f.Enum) > 0 {
			bad("array field must not declare enum")
		}
	default:
		if f.Items != nil {
			bad("only array fields may declare items")
		}
		if len(f.Properties) > 0 {
			bad("only object fields may declare properties")
		}
	}
	switch f.Type {
	case TypeString, TypeInteger, TypeNumber:
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			bad("min exceeds max")
		}
	case TypeArray:
		if f.Min != nil && *f.Min < 0 {
			bad("min length must not be negative")
		}
		if f.Max != nil && *f.Max < 0 {
			bad("max length must not be negative")
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			bad("min length exceeds max length")
		}
	case TypeBoolean, TypeObject:
		if f.Min != nil || f.Max != nil {
			bad("%s field must not declare min/max", f.Type)
		}
	}
	for _, v := range f.Enum {
		if !enumValueMatches(f.Type, v) {
			bad("enum value %v is not of type %s", v, f.Type)
		}
	}
}

func enumValueMatches(t FieldType, v any) bool {
	switch t {
	case TypeString:
		_, ok := v.(string)
		return ok
	case TypeInteger:
		// JSON numbers decode as float64.
		fl, ok := v.(float64)
		return ok && fl == float64(int64(fl))
	case TypeNumber:
		_, ok := v.(float64)
		return ok
	case TypeBoolean:
		_, ok := v.(bool)
		return ok
	}
	return false
}
