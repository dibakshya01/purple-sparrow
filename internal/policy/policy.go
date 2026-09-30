package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// Action is a guarded operation.
type Action string

const (
	ActionSelect Action = "select"
	ActionInsert Action = "insert"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

func validAction(a Action) bool {
	switch a {
	case ActionSelect, ActionInsert, ActionUpdate, ActionDelete:
		return true
	}
	return false
}

var validRoles = map[string]bool{
	principal.RoleAnon:          true,
	principal.RoleAuthenticated: true,
	principal.RoleProjectAdmin:  true,
}

// Sentinel errors surfaced to the API layer.
var (
	ErrInvalidAction = errors.New("invalid policy action")
	ErrInvalidRole   = errors.New("invalid policy role")
	ErrNoRoles       = errors.New("policy must list at least one role")
	ErrInvalidExpr   = errors.New("invalid policy expression")
	ErrMissingExpr   = errors.New("policy is missing a required expression")
)

// Policy is a stored authorization rule.
type Policy struct {
	ID     string   `json:"id"`
	Table  string   `json:"table"`
	Action Action   `json:"action"`
	Roles  []string `json:"roles"`
	Using  string   `json:"using,omitempty"`
	Check  string   `json:"check,omitempty"`
}

// CreateInput is the request to create a policy.
type CreateInput struct {
	Table  string   `json:"table"`
	Action Action   `json:"action"`
	Roles  []string `json:"roles"`
	Using  string   `json:"using"`
	Check  string   `json:"check"`
}

// Service creates, validates, and lists policies.
type Service struct{ eng data.Engine }

// NewService returns a policy Service.
func NewService(eng data.Engine) *Service { return &Service{eng: eng} }

// Create validates and stores a policy. allowedCols are the target table's real
// column names (including id, created_at); expressions may only reference these.
func (s *Service) Create(ctx context.Context, in CreateInput, allowedCols []string) (*Policy, error) {
	if !validAction(in.Action) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAction, in.Action)
	}
	if len(in.Roles) == 0 {
		return nil, ErrNoRoles
	}
	for _, r := range in.Roles {
		if !validRoles[r] {
			return nil, fmt.Errorf("%w: %s", ErrInvalidRole, r)
		}
	}

	allowed := toSet(allowedCols)
	using := strings.TrimSpace(in.Using)
	check := strings.TrimSpace(in.Check)

	// Required-expression rules per action.
	switch in.Action {
	case ActionSelect, ActionDelete:
		if using == "" {
			return nil, fmt.Errorf("%w: %s policy needs a `using` expression", ErrMissingExpr, in.Action)
		}
	case ActionInsert:
		if check == "" {
			return nil, fmt.Errorf("%w: insert policy needs a `check` expression", ErrMissingExpr)
		}
	case ActionUpdate:
		if using == "" {
			return nil, fmt.Errorf("%w: update policy needs a `using` expression", ErrMissingExpr)
		}
		// check defaults to using at enforcement time if omitted.
	}

	if using != "" {
		if err := validateExpr(using, allowed); err != nil {
			return nil, fmt.Errorf("%w (using): %v", ErrInvalidExpr, err)
		}
	}
	if check != "" {
		if err := validateExpr(check, allowed); err != nil {
			return nil, fmt.Errorf("%w (check): %v", ErrInvalidExpr, err)
		}
	}

	rolesJSON, _ := json.Marshal(in.Roles)
	p := &Policy{
		ID:     idgen.NewUUID(),
		Table:  in.Table,
		Action: in.Action,
		Roles:  in.Roles,
		Using:  using,
		Check:  check,
	}
	_, err := s.eng.ExecCtx(ctx,
		`INSERT INTO _ps_policies (id, table_name, action, roles, using_expr, check_expr, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Table, string(p.Action), string(rolesJSON),
		nullIfEmpty(using), nullIfEmpty(check), idgen.NowRFC3339())
	if err != nil {
		return nil, err
	}
	return p, nil
}

// List returns the policies for a table.
func (s *Service) List(ctx context.Context, table string) ([]Policy, error) {
	rows, err := s.eng.QueryCtx(ctx,
		`SELECT id, table_name, action, roles, using_expr, check_expr
		 FROM _ps_policies WHERE table_name = ? ORDER BY action, id`, table)
	if err != nil {
		return nil, err
	}
	return decodePolicies(rows), nil
}

// validateExpr parses an expression and ensures every column it references is in
// the allow-list. Called at policy-create time so bad policies fail early.
func validateExpr(expr string, allowed map[string]bool) error {
	ast, err := Parse(expr)
	if err != nil {
		return err
	}
	var bad string
	walkColumns(ast, func(name string) {
		if bad == "" && !allowed[name] {
			bad = name
		}
	})
	if bad != "" {
		return fmt.Errorf("unknown column %q", bad)
	}
	return nil
}

// walkColumns invokes fn for each Column referenced in the AST.
func walkColumns(n Node, fn func(string)) {
	switch node := n.(type) {
	case *Binary:
		walkColumns(node.Left, fn)
		walkColumns(node.Right, fn)
	case *Not:
		walkColumns(node.X, fn)
	case *In:
		walkColumns(node.Left, fn)
		for _, e := range node.Elems {
			walkColumns(e, fn)
		}
	case *Column:
		fn(node.Name)
	}
}

func decodePolicies(rows []data.Row) []Policy {
	out := make([]Policy, 0, len(rows))
	for _, r := range rows {
		var roles []string
		_ = json.Unmarshal([]byte(asString(r["roles"])), &roles)
		out = append(out, Policy{
			ID:     asString(r["id"]),
			Table:  asString(r["table_name"]),
			Action: Action(asString(r["action"])),
			Roles:  roles,
			Using:  asString(r["using_expr"]),
			Check:  asString(r["check_expr"]),
		})
	}
	return out
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
