// Package auth provides token based authentication and role based
// authorization for registry mutations and reads.
package auth

import (
	"fmt"
	"sort"
	"sync"
)

// Role is an authorization role.
type Role string

const (
	RoleAdmin  Role = "ADMIN"
	RoleWriter Role = "WRITER"
	RoleReader Role = "READER"
)

// Action is an operation against the registry.
type Action string

const (
	ActionRead   Action = "read"
	ActionWrite  Action = "write"
	ActionDelete Action = "delete"
	ActionAdmin  Action = "admin"
)

// Principal is an authenticated caller.
type Principal struct {
	Token   string
	Subject string
	Roles   []Role
}

// HasRole reports whether the principal carries role.
func (p *Principal) HasRole(role Role) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Authorizer authenticates bearer tokens and authorizes actions.
//
// Authorization matrix:
//
//	ADMIN  -> read, write, delete, admin
//	WRITER -> read, write
//	READER -> read
type Authorizer struct {
	mu     sync.RWMutex
	tokens map[string]*Principal
}

// NewAuthorizer creates an empty authorizer.
func NewAuthorizer() *Authorizer {
	return &Authorizer{tokens: map[string]*Principal{}}
}

// RegisterToken associates a token with a caller identity and roles.
// Registering an existing token replaces its identity and roles.
func (a *Authorizer) RegisterToken(token, subject string, roles ...Role) {
	if token == "" {
		return
	}
	cp := make([]Role, len(roles))
	copy(cp, roles)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i] < cp[j] })
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens[token] = &Principal{Token: token, Subject: subject, Roles: cp}
}

// Authenticate resolves a token to a principal. Unknown or empty tokens fail.
func (a *Authorizer) Authenticate(token string) (*Principal, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: missing token", errUnauthorized)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.tokens[token]
	if !ok {
		return nil, fmt.Errorf("%w: unknown token", errUnauthorized)
	}
	cp := *p
	return &cp, nil
}

// Authorize reports whether p may perform action.
func (a *Authorizer) Authorize(p *Principal, action Action) error {
	if p == nil {
		return fmt.Errorf("%w: unauthenticated caller", errForbidden)
	}
	allowed := false
	switch action {
	case ActionRead:
		allowed = p.HasRole(RoleReader) || p.HasRole(RoleWriter) || p.HasRole(RoleAdmin)
	case ActionWrite:
		allowed = p.HasRole(RoleWriter) || p.HasRole(RoleAdmin)
	case ActionDelete, ActionAdmin:
		allowed = p.HasRole(RoleAdmin)
	default:
		allowed = false
	}
	if !allowed {
		return fmt.Errorf("%w: principal %q may not %s (roles %v)", errForbidden, p.Subject, action, p.Roles)
	}
	return nil
}

// AuthenticateAndAuthorize is a convenience combining both steps.
func (a *Authorizer) AuthenticateAndAuthorize(token string, action Action) (*Principal, error) {
	p, err := a.Authenticate(token)
	if err != nil {
		return nil, err
	}
	if err := a.Authorize(p, action); err != nil {
		return nil, err
	}
	return p, nil
}
