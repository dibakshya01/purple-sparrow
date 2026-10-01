package policy

import (
	"fmt"

	"github.com/dibakshya01/orange-crow/internal/data"
	"github.com/dibakshya01/orange-crow/internal/principal"
)

// compileCtx carries the state a compilation needs. All value operands are bound
// into args as "?" placeholders; only validated column names are ever emitted as
// SQL identifiers. (The Postgres adapter rewrites "?" to "$n" at execution time,
// so the compiler stays placeholder-agnostic.)
type compileCtx struct {
	dialect   data.Dialect
	allowed   map[string]bool
	principal principal.Principal
	checkMode bool           // true: column refs bind row values instead of emitting identifiers
	row       map[string]any // used in checkMode
	args      []any
}

func (c *compileCtx) bind(v any) string {
	c.args = append(c.args, v)
	return "?"
}

// compile walks the AST, returning an SQL fragment. It fails closed: any node it
// doesn't recognize, or any column not in the allow-list, is an error.
func compile(n Node, c *compileCtx) (string, error) {
	switch node := n.(type) {
	case *Binary:
		l, err := compile(node.Left, c)
		if err != nil {
			return "", err
		}
		r, err := compile(node.Right, c)
		if err != nil {
			return "", err
		}
		op := node.Op
		switch op {
		case "and":
			op = "AND"
		case "or":
			op = "OR"
		case "=", "<>", "<", "<=", ">", ">=":
			// comparison operator, emitted as-is
		default:
			return "", fmt.Errorf("disallowed operator %q", node.Op)
		}
		return "(" + l + " " + op + " " + r + ")", nil

	case *Not:
		x, err := compile(node.X, c)
		if err != nil {
			return "", err
		}
		return "(NOT " + x + ")", nil

	case *In:
		l, err := compile(node.Left, c)
		if err != nil {
			return "", err
		}
		frag := "(" + l + " IN ("
		for i, e := range node.Elems {
			s, err := compile(e, c)
			if err != nil {
				return "", err
			}
			if i > 0 {
				frag += ", "
			}
			frag += s
		}
		return frag + "))", nil

	case *Column:
		if !c.allowed[node.Name] {
			return "", fmt.Errorf("unknown column %q", node.Name)
		}
		if c.checkMode {
			// Bind the candidate row's value for this column (nil -> NULL).
			return c.bind(c.row[node.Name]), nil
		}
		return c.dialect.QuoteIdent(node.Name), nil

	case *Auth:
		switch node.Fn {
		case "uid":
			// anon (empty subject) compiles to SQL NULL, so `auth.uid() = col`
			// is NULL and excludes every row / fails every check.
			if c.principal.Subject == "" {
				return c.bind(nil), nil
			}
			return c.bind(c.principal.Subject), nil
		case "role":
			return c.bind(firstRole(c.principal)), nil
		default:
			return "", fmt.Errorf("disallowed auth function %q", node.Fn)
		}

	case *Literal:
		if node.IsNull {
			return c.bind(nil), nil
		}
		switch v := node.Value.(type) {
		case bool:
			// SQLite has no bool; store/compare as 0/1 to match stored booleans.
			if v {
				return c.bind(int64(1)), nil
			}
			return c.bind(int64(0)), nil
		case float64, string:
			return c.bind(v), nil
		default:
			return "", fmt.Errorf("unsupported literal %T", node.Value)
		}

	default:
		return "", fmt.Errorf("unsupported expression node %T", n)
	}
}

func firstRole(p principal.Principal) any {
	if len(p.Roles) == 0 {
		return nil
	}
	return p.Roles[0]
}

// compileExpr parses and compiles a single expression in filter mode (column refs
// emit identifiers). Returns the fragment and its bound args.
func compileExpr(expr string, dialect data.Dialect, allowed map[string]bool, p principal.Principal) (string, []any, error) {
	ast, err := Parse(expr)
	if err != nil {
		return "", nil, err
	}
	c := &compileCtx{dialect: dialect, allowed: allowed, principal: p}
	frag, err := compile(ast, c)
	if err != nil {
		return "", nil, err
	}
	return frag, c.args, nil
}

// compileCheckExpr parses and compiles a single expression in check mode (column
// refs bind the candidate row's values). Returns the fragment and bound args.
func compileCheckExpr(expr string, dialect data.Dialect, allowed map[string]bool, p principal.Principal, row map[string]any) (string, []any, error) {
	ast, err := Parse(expr)
	if err != nil {
		return "", nil, err
	}
	c := &compileCtx{dialect: dialect, allowed: allowed, principal: p, checkMode: true, row: row}
	frag, err := compile(ast, c)
	if err != nil {
		return "", nil, err
	}
	return frag, c.args, nil
}
