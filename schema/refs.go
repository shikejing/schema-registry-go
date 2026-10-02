package schema

// Resolver resolves a (possibly unversioned) reference to a concrete schema.
// It is implemented by the registry and used by compatibility checking and
// instance validation when expanding references.
type Resolver interface {
	Resolve(r Ref) (*Schema, error)
}

// ExtractRefs returns the direct outgoing references contained in a schema,
// traversing nested object properties and array items. Duplicate references
// are de-duplicated and unversioned references are reported as declared
// (Version == 0); the registry pins them to a concrete version at register
// time.
func ExtractRefs(s *Schema) []Ref {
	var out []Ref
	seen := map[Ref]bool{}
	add := func(r Ref) {
		if r.Subject == "" || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	var walk func(f *Field)
	walk = func(f *Field) {
		if f == nil {
			return
		}
		if f.Ref != nil {
			add(*f.Ref)
		}
		for _, p := range f.Properties {
			walk(p)
		}
		if f.Items != nil {
			walk(f.Items)
		}
	}
	for _, f := range s.Fields {
		walk(f)
	}
	return out
}
