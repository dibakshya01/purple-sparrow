// Package principal models the authenticated caller (role set + subject) and its
// propagation through request context. M1 resolves it from an admin API key (a
// bridge); M2 replaces the resolver with real JWT/key auth. The Principal shape
// and roles are stable so M2 is not a retrofit.
package principal

import "context"

// Role names. The full enum is defined now even though only anon and
// project_admin are reachable until M2 wires JWT identities.
const (
	RoleAnon          = "anon"
	RoleAuthenticated = "authenticated"
	RoleProjectAdmin  = "project_admin"
)

// Principal is the authenticated caller. Roles is a set (M2 tokens may carry
// several). Subject is the stable user id, empty for anon (compiles to SQL NULL).
type Principal struct {
	Roles   []string
	Subject string
}

// Anon is the default unauthenticated principal.
func Anon() Principal { return Principal{Roles: []string{RoleAnon}} }

// IsAdmin reports whether the principal holds the project_admin role (the single
// place callers check for the policy bypass).
func (p Principal) IsAdmin() bool { return p.HasRole(RoleProjectAdmin) }

// HasRole reports role membership.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IntersectsRoles reports whether the principal holds any of the given roles
// (set-intersection role matching used by the policy enforcer).
func (p Principal) IntersectsRoles(roles []string) bool {
	for _, want := range roles {
		if p.HasRole(want) {
			return true
		}
	}
	return false
}

type ctxKey struct{}

// WithContext attaches a principal to ctx.
func WithContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal, defaulting to Anon if none is set (fail
// closed — an unauthenticated request is never accidentally privileged).
func FromContext(ctx context.Context) Principal {
	if p, ok := ctx.Value(ctxKey{}).(Principal); ok {
		return p
	}
	return Anon()
}
