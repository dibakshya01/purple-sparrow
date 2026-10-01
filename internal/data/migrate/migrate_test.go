package migrate

import (
	"context"
	"testing"

	"github.com/dibakshya01/orange-crow/internal/data"
)

func TestRunIsIdempotentAndCreatesCatalog(t *testing.T) {
	ctx := context.Background()
	eng, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := Run(ctx, eng); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Running again must be a no-op, not an error.
	if err := Run(ctx, eng); err != nil {
		t.Fatalf("second run: %v", err)
	}

	for _, tbl := range []string{"_oc_tables", "_oc_columns", "_oc_policies", "_oc_migrations"} {
		row, err := eng.QueryRowCtx(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl)
		if err != nil {
			t.Fatalf("expected catalog table %q to exist: %v", tbl, err)
		}
		if row["name"] != tbl {
			t.Fatalf("catalog table %q missing", tbl)
		}
	}

	rows, err := eng.QueryCtx(ctx, `SELECT version FROM _oc_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, r := range rows {
		applied[str(r["version"])] = true
	}
	if !applied["0001_catalog"] {
		t.Fatalf("catalog migration not recorded in ledger: %+v", rows)
	}
	if len(rows) == 0 {
		t.Fatal("migration ledger is empty")
	}
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
