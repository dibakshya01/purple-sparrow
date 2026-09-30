// Package policy implements Purple Sparrow's app-layer authorization: a small,
// safe expression language for policy rules, a compiler that turns rules into
// PARAMETERIZED SQL predicates, and an enforcer that is the single deny-by-default
// authorization chokepoint (ADR-0003).
//
// Security invariants (enforced by the compiler, covered by fuzzing):
//   - Literal and auth.* values are ALWAYS bound parameters, never concatenated.
//   - Column references are validated against the target table's real columns AND
//     quoted by the dialect.
//   - Only allow-listed operators emit SQL; any unknown/ambiguous AST node is a
//     compile error, which the caller turns into a denial (fail closed).
//   - anon auth.uid() is SQL NULL; NULL comparison yields NULL (row excluded from
//     filters; treated as false in checks).
package policy

// Node is an expression AST node.
type Node interface{ isNode() }

// Binary is a logical (and/or) or comparison (= <> < <= > >=) expression.
type Binary struct {
	Op    string
	Left  Node
	Right Node
}

// Not negates an expression.
type Not struct{ X Node }

// In is `Left in (elems...)`.
type In struct {
	Left  Node
	Elems []Node
}

// Column references a table column by (validated) name.
type Column struct{ Name string }

// Auth is auth.uid() or auth.role().
type Auth struct{ Fn string } // "uid" | "role"

// Literal is a string, number, bool, or null value.
type Literal struct {
	Value  any // string | float64 | bool | nil
	IsNull bool
}

func (*Binary) isNode()  {}
func (*Not) isNode()     {}
func (*In) isNode()      {}
func (*Column) isNode()  {}
func (*Auth) isNode()    {}
func (*Literal) isNode() {}
