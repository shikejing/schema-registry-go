package registry

import "errors"

var (
	// ErrNotFound indicates that a subject or version does not exist.
	ErrNotFound = errors.New("schema registry: not found")
	// ErrUnauthorized indicates a missing or unrecognized credential.
	ErrUnauthorized = errors.New("schema registry: unauthorized")
	// ErrForbidden indicates an authenticated caller lacking permission.
	ErrForbidden = errors.New("schema registry: forbidden")
	// ErrInvalidSchema indicates a malformed schema definition.
	ErrInvalidSchema = errors.New("schema registry: invalid schema")
	// ErrIncompatible indicates a schema rejected by compatibility checking.
	ErrIncompatible = errors.New("schema registry: incompatible schema")
	// ErrReferenceCycle indicates a reference graph cycle.
	ErrReferenceCycle = errors.New("schema registry: reference cycle detected")
	// ErrReferenced indicates a version that is still referenced cannot be deleted.
	ErrReferenced = errors.New("schema registry: version is still referenced")
	// ErrBadReference indicates a reference target that cannot be resolved.
	ErrBadReference = errors.New("schema registry: unresolved reference")
)
