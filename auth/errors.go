package auth

import "errors"

// ErrUnauthorized indicates a missing or unrecognized credential.
var ErrUnauthorized = errors.New("unauthorized")

// ErrForbidden indicates an authenticated caller lacking permission.
var ErrForbidden = errors.New("forbidden")

var (
	errUnauthorized = ErrUnauthorized
	errForbidden    = ErrForbidden
)
