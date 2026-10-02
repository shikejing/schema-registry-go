package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Fingerprint computes a stable, collision-resistant content fingerprint for
// a schema document. Two schemas with identical structural content produce the
// same fingerprint.
//
// Determinism relies on encoding/json: struct fields are emitted in a fixed
// declaration order and map keys are emitted in lexicographic order, so the
// serialized form is canonical for any schema built from the model types.
func Fingerprint(s *Schema) string {
	b, err := json.Marshal(s)
	if err != nil {
		// The model contains only JSON-safe types; a marshal failure here can
		// only come from an unsupported enum element.
		panic("schema: cannot fingerprint: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
