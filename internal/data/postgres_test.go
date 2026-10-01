package data

import "testing"

func TestRewritePlaceholders(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"a = ?", "a = $1"},
		{"a = ? AND b = ?", "a = $1 AND b = $2"},
		{`"c" IN (?, ?, ?)`, `"c" IN ($1, $2, $3)`},
		{"INSERT INTO t (a,b) VALUES (?, ?)", "INSERT INTO t (a,b) VALUES ($1, $2)"},
		{"CASE WHEN (? = ?) THEN 1 ELSE 0 END", "CASE WHEN ($1 = $2) THEN 1 ELSE 0 END"},
	}
	for _, c := range cases {
		if got, _ := rewritePlaceholders(c.in); got != c.want {
			t.Errorf("rewrite(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCheckedRewriteMismatchFailsLoudly(t *testing.T) {
	// Correct count -> ok.
	if _, err := checkedRewrite("a = ? AND b = ?", 2); err != nil {
		t.Fatalf("matching count should succeed: %v", err)
	}
	// A stray placeholder (e.g. a jsonb operator or literal) vs args -> error, not
	// a silently mis-numbered query.
	if _, err := checkedRewrite("data ?| array['a'] AND x = ?", 1); err == nil {
		t.Fatal("placeholder/arg mismatch must be rejected")
	}
	if _, err := checkedRewrite("a = ?", 0); err == nil {
		t.Fatal("more placeholders than args must be rejected")
	}
}

func TestPostgresDialectTypes(t *testing.T) {
	d := postgresDialect{}
	if ty, ok := d.SQLType("boolean"); !ok || ty != "INTEGER" {
		t.Errorf("boolean -> %q (want INTEGER) for policy-compiler parity", ty)
	}
	if ty, ok := d.SQLType("integer"); !ok || ty != "BIGINT" {
		t.Errorf("integer -> %q (want BIGINT)", ty)
	}
	if _, ok := d.SQLType("blob"); ok {
		t.Error("non-whitelisted type must be rejected")
	}
	if d.LikeOperator() != "ILIKE" {
		t.Errorf("postgres like operator should be ILIKE, got %q", d.LikeOperator())
	}
}
