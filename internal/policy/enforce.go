package policy

import (
	"context"
	"strings"

	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// Enforcer is the single authorization chokepoint. Deny-by-default: unless a
// matching policy grants access, access is denied. project_admin bypasses here
// and only here.
type Enforcer struct{ eng data.Engine }

// NewEnforcer returns an Enforcer.
func NewEnforcer(eng data.Engine) *Enforcer { return &Enforcer{eng: eng} }

func (e *Enforcer) matching(ctx context.Context, table string, action Action, p principal.Principal) ([]Policy, error) {
	rows, err := e.eng.QueryCtx(ctx,
		`SELECT id, table_name, action, roles, using_expr, check_expr
		 FROM _ps_policies WHERE table_name = ? AND action = ?`, table, string(action))
	if err != nil {
		return nil, err
	}
	all := decodePolicies(rows)
	out := all[:0]
	for _, pol := range all {
		if p.IntersectsRoles(pol.Roles) {
			out = append(out, pol)
		}
	}
	return out, nil
}

// Filter returns a row-restricting SQL predicate for select/update/delete. When
// allowed is false the caller must deny. For admin, allowed is true and frag is
// empty (no restriction). The returned predicate must be AND-combined with any
// user filters (never OR) so user filters can never widen access.
func (e *Enforcer) Filter(ctx context.Context, p principal.Principal, table string, action Action, allowedCols []string) (frag string, args []any, allowed bool, err error) {
	if p.IsAdmin() {
		return "", nil, true, nil
	}
	pols, err := e.matching(ctx, table, action, p)
	if err != nil {
		return "", nil, false, err
	}
	if len(pols) == 0 {
		return "", nil, false, nil
	}
	allow := toSet(allowedCols)
	var frags []string
	for _, pol := range pols {
		if pol.Using == "" {
			continue // fail closed: a using-less policy grants nothing here
		}
		f, a, cErr := compileExpr(pol.Using, e.eng.Dialect(), allow, p)
		if cErr != nil {
			return "", nil, false, cErr
		}
		frags = append(frags, f)
		args = append(args, a...)
	}
	if len(frags) == 0 {
		return "", nil, false, nil
	}
	return "(" + strings.Join(frags, " OR ") + ")", args, true, nil
}

// Check evaluates the insert/update WITH CHECK predicates against a candidate row
// (the post-write values). Returns whether the write is permitted. Admin bypasses.
func (e *Enforcer) Check(ctx context.Context, p principal.Principal, table string, action Action, allowedCols []string, row map[string]any) (bool, error) {
	if p.IsAdmin() {
		return true, nil
	}
	pols, err := e.matching(ctx, table, action, p)
	if err != nil {
		return false, err
	}
	if len(pols) == 0 {
		return false, nil
	}
	allow := toSet(allowedCols)
	var frags []string
	var args []any
	for _, pol := range pols {
		expr := pol.Check
		if expr == "" && action == ActionUpdate {
			expr = pol.Using // WITH CHECK defaults to USING for update
		}
		if expr == "" {
			continue
		}
		f, a, cErr := compileCheckExpr(expr, e.eng.Dialect(), allow, p, row)
		if cErr != nil {
			return false, cErr
		}
		frags = append(frags, f)
		args = append(args, a...)
	}
	if len(frags) == 0 {
		return false, nil // fail closed
	}
	// NULL result -> ELSE -> 0 (deny). Truthy only when a check passes.
	sql := "SELECT CASE WHEN (" + strings.Join(frags, " OR ") + ") THEN 1 ELSE 0 END AS ok"
	res, err := e.eng.QueryRowCtx(ctx, sql, args...)
	if err != nil {
		return false, err
	}
	return asInt(res["ok"]) == 1, nil
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
