// Package migrate applies ordered, embedded SQL migrations for Purple Sparrow's
// own system schema (the _ps_* catalog). User-table DDL is separate.
package migrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/data"
)

//go:embed migrations/*.sql
var files embed.FS

// Run applies any migrations not yet recorded, each in its own transaction, in
// filename order. It is idempotent and safe to call on every boot.
func Run(ctx context.Context, eng data.Engine) error {
	if err := ensureVersionTable(ctx, eng); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, eng)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := files.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := applyOne(ctx, eng, version, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, eng data.Engine, version, body string) error {
	return eng.Transact(ctx, func(q data.Querier) error {
		for _, stmt := range splitStatements(body) {
			if _, err := q.ExecCtx(ctx, stmt); err != nil {
				return fmt.Errorf("statement failed: %w\n---\n%s", err, stmt)
			}
		}
		_, err := q.ExecCtx(ctx,
			`INSERT INTO _ps_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

func ensureVersionTable(ctx context.Context, eng data.Engine) error {
	_, err := eng.ExecCtx(ctx, `CREATE TABLE IF NOT EXISTS _ps_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)
	return err
}

func appliedVersions(ctx context.Context, eng data.Engine) (map[string]bool, error) {
	rows, err := eng.QueryCtx(ctx, `SELECT version FROM _ps_migrations`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		if v, ok := r["version"].(string); ok {
			out[v] = true
		}
	}
	return out, nil
}

// splitStatements splits a migration file into individual statements on
// semicolons. Comments are stripped FIRST so a ";" inside a comment does not split
// a statement. Migration SQL is authored in-repo and contains no "--" or ";"
// inside string literals, so this is safe and keeps the runner dependency-free.
func splitStatements(body string) []string {
	clean := stripSQLComments(body)
	var out []string
	for _, part := range strings.Split(clean, ";") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// stripSQLComments removes "--" line comments (from the marker to end of line),
// including inline trailing comments. Safe because migrations never contain "--"
// inside a string literal.
func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
