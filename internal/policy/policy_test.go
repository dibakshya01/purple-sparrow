package policy

import (
	"strings"
	"testing"

	"github.com/dibakshya01/orange-crow/internal/principal"
)

// testDialect implements data.Dialect without a real database.
type testDialect struct{}

func (testDialect) QuoteIdent(s string) string              { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (testDialect) Placeholder(int) string                  { return "?" }
func (testDialect) Name() string                            { return "test" }
func (testDialect) LikeOperator() string            { return "LIKE" }
func (testDialect) LockClause() string              { return "" }
func (testDialect) SQLType(l string) (string, bool) { return "TEXT", true }

var testAllowed = map[string]bool{"id": true, "owner_id": true, "body": true, "age": true, "active": true}

func testPrincipal() principal.Principal {
	return principal.Principal{Roles: []string{principal.RoleAuthenticated}, Subject: "sub-1"}
}

func TestParseValid(t *testing.T) {
	valid := []string{
		"auth.uid() = owner_id",
		"owner_id = 'abc'",
		"age >= 18 and active = true",
		"not (owner_id = 'x') or auth.role() = 'authenticated'",
		"age in (1, 2, 3)",
		"body <> null",
	}
	for _, v := range valid {
		if _, err := Parse(v); err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", v, err)
		}
	}
}

func TestParseRejectsMalicious(t *testing.T) {
	bad := []string{
		"1; DROP TABLE users",
		"owner_id = (select secret from _oc_policies)",
		"auth.evil()",
		"owner_id === 1",
		"'unterminated",
		"owner_id = 'x' extra",
		"",
		"()",
	}
	for _, b := range bad {
		if _, err := Parse(b); err == nil {
			t.Errorf("Parse(%q) should have failed", b)
		}
	}
}

func TestCompileParameterizesValues(t *testing.T) {
	frag, args, err := compileExpr("auth.uid() = owner_id and body = 'secret-value'", testDialect{}, testAllowed, testPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(frag, "secret-value") {
		t.Fatalf("literal leaked into SQL: %s", frag)
	}
	if strings.Contains(frag, "'") {
		t.Fatalf("compiled SQL must contain no string literal quotes: %s", frag)
	}
	if !strings.Contains(frag, `"owner_id"`) || !strings.Contains(frag, `"body"`) {
		t.Fatalf("columns should be quoted identifiers: %s", frag)
	}
	// args: sub-1 (auth.uid), then 'secret-value'
	if len(args) != 2 || args[0] != "sub-1" || args[1] != "secret-value" {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestCompileRejectsUnknownColumn(t *testing.T) {
	if _, _, err := compileExpr("ssn = '123'", testDialect{}, testAllowed, testPrincipal()); err == nil {
		t.Fatal("compiling a reference to a non-allowed column must fail")
	}
}

func TestAnonUidBindsNil(t *testing.T) {
	anon := principal.Anon()
	_, args, err := compileExpr("auth.uid() = owner_id", testDialect{}, testAllowed, anon)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != nil {
		t.Fatalf("anon auth.uid() must bind SQL NULL (nil), got %+v", args)
	}
}

func TestBooleanLiteralBindsAsInt(t *testing.T) {
	_, args, err := compileExpr("active = true", testDialect{}, testAllowed, testPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != int64(1) {
		t.Fatalf("true must bind as int64(1), got %+v", args)
	}
}

// FuzzCompileNeverLeaksLiterals asserts the compiler never emits a string literal
// value or a quote into the SQL fragment — every value must be a bound parameter.
func FuzzCompileNeverLeaksLiterals(f *testing.F) {
	seeds := []string{
		"owner_id = 'abc'",
		"auth.uid() = owner_id and body = 'x''y'",
		"age in (1,2,3) or active = false",
		"not owner_id = 'DROP TABLE'",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		frag, args, err := compileExpr(expr, testDialect{}, testAllowed, testPrincipal())
		if err != nil {
			return // rejected inputs are fine; we only assert on accepted ones
		}
		// Soundness invariant for "everything is parameterized": values enter the
		// SQL only via bind() -> "?", so there must be exactly one placeholder per
		// bound arg, and no string-literal quote may appear in the fragment.
		if strings.Contains(frag, "'") {
			t.Fatalf("quote leaked into SQL for %q: %s", expr, frag)
		}
		if got := strings.Count(frag, "?"); got != len(args) {
			t.Fatalf("placeholder/arg mismatch for %q: %d placeholders, %d args: %s", expr, got, len(args), frag)
		}
	})
}
