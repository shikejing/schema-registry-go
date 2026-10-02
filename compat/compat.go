// Package compat implements pre-registration compatibility checking between
// a new candidate schema and the previously registered schema version.
//
// Semantics (identical in spirit to Confluent Schema Registry):
//
//   - BACKWARD: data written for the old schema can be read by the new schema.
//     Allowed: adding optional fields, removing fields, widening ranges,
//     removing enum values. Forbidden: adding required fields, changing
//     types, narrowing ranges, adding enum values, making a field required.
//   - FORWARD: data written for the new schema can be read by the old schema.
//     Allowed: deleting required fields (they become optional to old
//     readers), adding fields, narrowing ranges, adding enum values.
//     Forbidden: removing a field an old reader requires, changing types,
//     widening ranges, removing enum values, making a field required.
//   - FULL: both BACKWARD and FORWARD; effectively every compared attribute
//     must remain identical.
//
// Reference fields are expanded through schema.Resolver and compared
// recursively; a referenced subject that switches to an incompatible
// referenced version fails registration.
package compat

import (
	"fmt"
	"strings"

	"github.com/shikejing/schema-registry-go/schema"
)

// Issue describes a single incompatibility located by field path.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// IncompatibleError aggregates every issue found during a compatibility check.
type IncompatibleError struct {
	Issues []Issue
}

func (e *IncompatibleError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "schema is incompatible"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, is := range e.Issues {
		parts = append(parts, is.Path+": "+is.Message)
	}
	return "schema is incompatible:\n  " + strings.Join(parts, "\n  ")
}

// Check verifies that candidate is compatible with latest under mode.
// A nil latest (first registration) always passes. Resolve expands
// references when comparing referenced sub-schemas.
func Check(latest, candidate *schema.Schema, mode schema.Compatibility, resolve schema.Resolver) error {
	if mode == schema.CompatNone {
		return nil
	}
	if latest == nil {
		return nil
	}
	old := indexFields(latest.Fields)
	issues := []Issue{}
	for _, nf := range candidate.Fields {
		path := "$." + nf.Name
		of, existed := old[nf.Name]
		if !existed {
			// Field added in the new schema.
			switch mode {
			case schema.CompatBackward:
				if nf.Required {
					issues = append(issues, Issue{path, "required field added: old data has no value for it"})
				}
			case schema.CompatForward:
				// Old readers simply ignore unknown fields; always safe.
			case schema.CompatFull:
				if nf.Required {
					issues = append(issues, Issue{path, "required field added: full compatibility forbids it"})
				}
			}
			// Even optional added fields carry no sub-constraints that can
			// break old data (the value is absent); no further checks.
			continue
		}
		compareField(of, nf, path, mode, resolve, &issues)
	}
	// Fields present in the old schema but absent from the candidate (removed).
	newIndex := indexFields(candidate.Fields)
	for _, of := range latest.Fields {
		if _, kept := newIndex[of.Name]; kept {
			continue
		}
		path := "$." + of.Name
		switch mode {
		case schema.CompatBackward:
			// Old data carrying the removed field is ignored by the new
			// schema; always safe.
		case schema.CompatForward:
			if of.Required {
				issues = append(issues, Issue{path, "required field removed: old readers cannot handle data without it"})
			}
		case schema.CompatFull:
			// FULL = BACKWARD and FORWARD. Removing an optional field is
			// legal in both directions; removing a required field fails the
			// forward direction.
			if of.Required {
				issues = append(issues, Issue{path, "required field removed: full compatibility forbids it"})
			}
		}
	}
	if len(issues) > 0 {
		return &IncompatibleError{Issues: issues}
	}
	return nil
}

func indexFields(fields []*schema.Field) map[string]*schema.Field {
	m := make(map[string]*schema.Field, len(fields))
	for _, f := range fields {
		if f != nil {
			m[f.Name] = f
		}
	}
	return m
}

func compareField(of, nf *schema.Field, path string, mode schema.Compatibility, resolve schema.Resolver, issues *[]Issue) {
	add := func(format string, args ...any) {
		*issues = append(*issues, Issue{path, fmt.Sprintf(format, args...)})
	}
	if of.Type != nf.Type {
		add("type changed from %s to %s", of.Type, nf.Type)
		return // structural comparisons below would be meaningless
	}
	if !of.Required && nf.Required {
		switch mode {
		case schema.CompatBackward, schema.CompatForward, schema.CompatFull:
			add("field changed from optional to required")
		}
	}
	if of.Required && !nf.Required {
		switch mode {
		case schema.CompatFull:
			add("field changed from required to optional: full compatibility forbids it")
		}
	}
	switch of.Type {
	case schema.TypeInteger, schema.TypeNumber:
		compareRange(of, nf, path, mode, issues)
		compareEnum(of, nf, path, mode, issues)
	case schema.TypeString:
		compareRange(of, nf, path, mode, issues)
		compareEnum(of, nf, path, mode, issues)
	case schema.TypeObject:
		compareObject(of, nf, path, mode, resolve, issues)
	case schema.TypeArray:
		compareRange(of, nf, path, mode, issues)
		if (of.Items == nil) != (nf.Items == nil) {
			add("array items declaration changed")
			return
		}
		if of.Items != nil && nf.Items != nil {
			compareField(of.Items, nf.Items, path+"[]", mode, resolve, issues)
		}
	}
}

func compareRange(of, nf *schema.Field, path string, mode schema.Compatibility, issues *[]Issue) {
	add := func(format string, args ...any) {
		*issues = append(*issues, Issue{path, fmt.Sprintf(format, args...)})
	}
	switch mode {
	case schema.CompatBackward:
		// Range may only widen.
		if (of.Min == nil) != (nf.Min == nil) {
			add("minimum boundary added or removed")
		} else if of.Min != nil && *nf.Min != *of.Min {
			if *nf.Min > *of.Min {
				add("minimum raised from %g to %g: existing values may fall below it", *of.Min, *nf.Min)
			}
		}
		if (of.Max == nil) != (nf.Max == nil) {
			add("maximum boundary added or removed")
		} else if of.Max != nil && *nf.Max != *of.Max {
			if *nf.Max < *of.Max {
				add("maximum lowered from %g to %g: existing values may exceed it", *of.Max, *nf.Max)
			}
		}
	case schema.CompatForward:
		// Range may only narrow.
		if (of.Min == nil) != (nf.Min == nil) {
			add("minimum boundary added or removed")
		} else if of.Min != nil && *nf.Min != *of.Min {
			if *nf.Min < *of.Min {
				add("minimum lowered from %g to %g: old readers may reject new values", *of.Min, *nf.Min)
			}
		}
		if (of.Max == nil) != (nf.Max == nil) {
			add("maximum boundary added or removed")
		} else if of.Max != nil && *nf.Max != *of.Max {
			if *nf.Max > *of.Max {
				add("maximum raised from %g to %g: old readers may reject new values", *of.Max, *nf.Max)
			}
		}
	case schema.CompatFull:
		if !floatPtrEqual(of.Min, nf.Min) || !floatPtrEqual(of.Max, nf.Max) {
			add("range changed: full compatibility forbids it")
		}
	}
}

func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func compareEnum(of, nf *schema.Field, path string, mode schema.Compatibility, issues *[]Issue) {
	if len(of.Enum) == 0 && len(nf.Enum) == 0 {
		return
	}
	add := func(format string, args ...any) {
		*issues = append(*issues, Issue{path, fmt.Sprintf(format, args...)})
	}
	oldSet := map[any]bool{}
	for _, v := range of.Enum {
		oldSet[v] = true
	}
	newSet := map[any]bool{}
	for _, v := range nf.Enum {
		newSet[v] = true
	}
	var added, removed []any
	for v := range newSet {
		if !oldSet[v] {
			added = append(added, v)
		}
	}
	for v := range oldSet {
		if !newSet[v] {
			removed = append(removed, v)
		}
	}
	switch mode {
	case schema.CompatBackward:
		if len(added) > 0 {
			add("enum values %v added: old data may carry a value the new schema rejects", added)
		}
	case schema.CompatForward:
		if len(removed) > 0 {
			add("enum values %v removed: new data may carry a value the old schema rejects", removed)
		}
	case schema.CompatFull:
		if len(added) > 0 || len(removed) > 0 {
			add("enum changed (added %v, removed %v): full compatibility forbids it", added, removed)
		}
	}
}

func compareObject(of, nf *schema.Field, path string, mode schema.Compatibility, resolve schema.Resolver, issues *[]Issue) {
	switch {
	case of.Ref != nil && nf.Ref != nil:
		if of.Ref.Subject != nf.Ref.Subject {
			*issues = append(*issues, Issue{path, fmt.Sprintf("reference target changed from %s to %s", of.Ref.Subject, nf.Ref.Subject)})
			return
		}
		oldResolved, err1 := resolve.Resolve(*of.Ref)
		newResolved, err2 := resolve.Resolve(*nf.Ref)
		if err1 != nil || err2 != nil {
			*issues = append(*issues, Issue{path, "cannot expand reference for comparison"})
			return
		}
		// Recurse treating the referenced schemas as the compared documents.
		if err := Check(oldResolved, newResolved, mode, resolve); err != nil {
			inheritIssues(err, issues)
		}
	case of.Ref != nil || nf.Ref != nil:
		*issues = append(*issues, Issue{path, "reference added or removed"})
	default:
		oldProps := indexFields(of.Properties)
		for _, np := range nf.Properties {
			pp := path + "." + np.Name
			op, existed := oldProps[np.Name]
			if !existed {
				switch mode {
				case schema.CompatBackward:
					if np.Required {
						*issues = append(*issues, Issue{pp, "required nested field added: old data has no value for it"})
					}
				case schema.CompatFull:
					if np.Required {
						*issues = append(*issues, Issue{pp, "required nested field added: full compatibility forbids it"})
					}
				}
				continue
			}
			compareField(op, np, pp, mode, resolve, issues)
		}
		newProps := indexFields(nf.Properties)
		for _, op := range of.Properties {
			if _, kept := newProps[op.Name]; kept {
				continue
			}
			pp := path + "." + op.Name
			switch mode {
			case schema.CompatForward:
				if op.Required {
					*issues = append(*issues, Issue{pp, "required nested field removed: old readers cannot handle data without it"})
				}
			case schema.CompatFull:
				if op.Required {
					*issues = append(*issues, Issue{pp, "required nested field removed: full compatibility forbids it"})
				}
			}
		}
	}
}

func inheritIssues(err error, issues *[]Issue) {
	var inc *IncompatibleError
	if asIncompatible(err, &inc) {
		for _, is := range inc.Issues {
			*issues = append(*issues, is)
		}
	}
}

func asIncompatible(err error, target **IncompatibleError) bool {
	for err != nil {
		if e, ok := err.(*IncompatibleError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() []error }
		if u, ok := err.(unwrapper); ok {
			for _, e := range u.Unwrap() {
				if asIncompatible(e, target) {
					return true
				}
			}
			return false
		}
		type single interface{ Unwrap() error }
		if u, ok := err.(single); ok {
			err = u.Unwrap()
			continue
		}
		return false
	}
	return false
}
