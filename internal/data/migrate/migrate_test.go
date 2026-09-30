package migrate

import (
	"context"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/data"
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

	for _, tbl := range []string{"_ps_tables", "_ps_columns", "_ps_policies", "_ps_migrations"} {
		row, err := eng.QueryRowCtx(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl)
		if err != nil {
			t.Fatalf("expected catalog table %q to exist: %v", tbl, err)
		}
		if row["name"] != tbl {
			t.Fatalf("catalog table %q missing", tbl)
		}
	}

	rows, err := eng.QueryCtx(ctx, `SELECT version FROM _ps_migrations`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["version"] != "0001_catalog" {
		t.Fatalf("unexpected migration ledger: %+v", rows)
	}
}
