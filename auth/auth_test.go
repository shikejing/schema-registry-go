package auth

import (
	"errors"
	"testing"
)

func TestAuthenticateAndAuthorize(t *testing.T) {
	a := NewAuthorizer()
	a.RegisterToken("t-admin", "alice", RoleAdmin)
	a.RegisterToken("t-writer", "bob", RoleWriter)
	a.RegisterToken("t-reader", "carol", RoleReader)

	if _, err := a.AuthenticateAndAuthorize("", ActionRead); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty token: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("unknown", ActionRead); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("t-reader", ActionRead); err != nil {
		t.Fatalf("reader read: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("t-reader", ActionWrite); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader write: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("t-writer", ActionWrite); err != nil {
		t.Fatalf("writer write: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("t-writer", ActionDelete); !errors.Is(err, ErrForbidden) {
		t.Fatalf("writer delete: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("t-admin", ActionDelete); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if _, err := a.AuthenticateAndAuthorize("t-admin", ActionAdmin); err != nil {
		t.Fatalf("admin admin: %v", err)
	}
}
