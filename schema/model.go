// Package schema defines the low-code form schema model: field types,
// schema documents, versions and subject-level compatibility configuration.
package schema

// FieldType enumerates the primitive and structural field types supported by
// a low-code form schema.
type FieldType string

const (
	TypeString  FieldType = "string"
	TypeInteger FieldType = "integer"
	TypeNumber  FieldType = "number"
	TypeBoolean FieldType = "boolean"
	TypeObject  FieldType = "object"
	TypeArray   FieldType = "array"
)

// Compatibility is the compatibility strategy enforced when a new schema
// version is registered.
type Compatibility string

const (
	// CompatNone disables compatibility checking.
	CompatNone Compatibility = "NONE"
	// CompatBackward requires new schemas to be able to read data written
	// for old schemas (backward compatible).
	CompatBackward Compatibility = "BACKWARD"
	// CompatForward requires old schemas to be able to read data written
	// for the new schema (forward compatible).
	CompatForward Compatibility = "FORWARD"
	// CompatFull requires both backward and forward compatibility.
	CompatFull Compatibility = "FULL"
)

// Ref is a reference from one schema field to another (sub)schema. References
// may cross subject boundaries. If Version is zero the reference is resolved
// against the referenced subject's latest version at registration time and is
// then pinned to that concrete version.
type Ref struct {
	Subject string `json:"subject"`
	Version int    `json:"version,omitempty"`
}

// Field describes one field of a form schema.
type Field struct {
	Name       string            `json:"name"`
	Type       FieldType         `json:"type"`
	Required   bool              `json:"required,omitempty"`
	Enum       []any             `json:"enum,omitempty"`
	Properties []*Field          `json:"properties,omitempty"` // TypeObject
	Items      *Field            `json:"items,omitempty"`      // TypeArray
	Ref        *Ref              `json:"ref,omitempty"`        // reference to another schema
	Min        *float64          `json:"min,omitempty"`        // number/integer range, string length, array length
	Max        *float64          `json:"max,omitempty"`        // number/integer range, string length, array length
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Schema is an immutable, versionable form structure definition.
type Schema struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Fields   []*Field          `json:"fields"`
	Version  string            `json:"version,omitempty"` // optional free-form schema revision tag
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Version is one persisted, immutable revision of a subject's schema together
// with the compatibility metadata captured at registration time.
type Version struct {
	Subject       string        `json:"subject"`
	Version       int           `json:"version"`
	Schema        *Schema       `json:"schema"`
	Fingerprint   string        `json:"fingerprint"`
	Compatibility Compatibility `json:"compatibility"`
	Refs          []Ref         `json:"refs,omitempty"` // resolved, pinned outgoing references
	CreatedAt     string        `json:"createdAt"`      // RFC3339Nano UTC
	CreatedBy     string        `json:"createdBy"`
}

// SubjectConfig is the per-subject configuration.
type SubjectConfig struct {
	Compatibility Compatibility `json:"compatibility"`
}

// DefaultCompatibility is used for subjects without an explicit configuration.
const DefaultCompatibility Compatibility = CompatBackward
