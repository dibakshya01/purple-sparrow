package records

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dibakshya01/orange-crow/internal/catalog"
	"github.com/dibakshya01/orange-crow/internal/data"
	"github.com/dibakshya01/orange-crow/internal/data/migrate"
	"github.com/dibakshya01/orange-crow/internal/policy"
	"github.com/dibakshya01/orange-crow/internal/principal"
)

// TestPostgresConformance runs the crown-jewel security + correctness behaviors
// against a REAL Postgres, proving the policy engine, catalog, and records behave
// identically on both engines. Gated on OC_TEST_POSTGRES_DSN; CI sets it against a
// Postgres service container. Skipped locally when unset.
func TestPostgresConformance(t *testing.T) {
	dsn := os.Getenv("OC_TEST_POSTGRES_DSN")
	if dsn == "" {
		// In the dedicated CI job OC_REQUIRE_POSTGRES=1, so a missing DSN is a hard
		// failure (never a silently-green skip that oversells "M5 works").
		if os.Getenv("OC_REQUIRE_POSTGRES") == "1" {
			t.Fatal("OC_REQUIRE_POSTGRES=1 but OC_TEST_POSTGRES_DSN is unset")
		}
		t.Skip("set OC_TEST_POSTGRES_DSN to run Postgres conformance (CI does)")
	}
	ctx := context.Background()
	eng, err := data.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := migrate.Run(ctx, eng); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if eng.Dialect().Name() != "postgres" {
		t.Fatalf("expected postgres dialect, got %q", eng.Dialect().Name())
	}

	cat := catalog.New(eng)
	enf := policy.NewEnforcer(eng)
	st := &stack{eng: eng, cat: cat, pol: policy.NewService(eng), rec: New(eng, cat, enf)}

	table := fmt.Sprintf("conf_%d", time.Now().UnixNano())
	if _, err := cat.CreateTable(ctx, table, []catalog.Column{
		{Name: "owner_id", Type: "text"},
		{Name: "flag", Type: "boolean", Nullable: true},
		{Name: "body", Type: "text", Nullable: true},
	}); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { _ = cat.DropTable(ctx, table) })
	cols := columnNames(t, cat, table)

	// Deny-by-default: anon select denied before any policy.
	if _, err := st.rec.Query(ctx, principal.Anon(), table, QueryOpts{}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("[pg] anon select should be denied, got %v", err)
	}

	// Seed rows as admin.
	for _, o := range []string{"user-a", "user-a", "user-b"} {
		if _, err := st.rec.Insert(ctx, admin(), table, map[string]any{"owner_id": o, "flag": true, "body": "x"}); err != nil {
			t.Fatalf("[pg] insert: %v", err)
		}
	}

	// Owner-scoped select policy.
	mustPolicy(t, st, ctx, policy.CreateInput{Table: table, Action: policy.ActionSelect,
		Roles: []string{"authenticated"}, Using: "auth.uid() = owner_id"}, cols)

	if rows, _ := st.rec.Query(ctx, user("user-a"), table, QueryOpts{}); len(rows) != 2 {
		t.Fatalf("[pg] user-a should see 2 rows, saw %d", len(rows))
	}
	if rows, _ := st.rec.Query(ctx, user("user-b"), table, QueryOpts{}); len(rows) != 1 {
		t.Fatalf("[pg] user-b should see 1 row, saw %d", len(rows))
	}

	// Boolean filter coercion (string "true" must match stored boolean on PG too).
	rows, err := st.rec.Query(ctx, admin(), table, QueryOpts{
		Filters: []Filter{{Column: "flag", Op: "eq", Value: "true"}},
	})
	if err != nil || len(rows) != 3 {
		t.Fatalf("[pg] boolean filter should match 3, got %d (err %v)", len(rows), err)
	}

	// UPDATE WITH CHECK: reassigning ownership is blocked.
	mustPolicy(t, st, ctx, policy.CreateInput{Table: table, Action: policy.ActionUpdate,
		Roles: []string{"authenticated"}, Using: "auth.uid() = owner_id"}, cols)
	created, err := st.rec.Insert(ctx, admin(), table, map[string]any{"owner_id": "user-c", "body": "mine"})
	if err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	if _, err := st.rec.Update(ctx, user("user-c"), table, id, map[string]any{"owner_id": "user-b"}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("[pg] ownership reassignment must be denied, got %v", err)
	}

	// Injection-as-data stays inert.
	payload := "x'); DROP TABLE " + table + ";--"
	if _, err := st.rec.Insert(ctx, admin(), table, map[string]any{"owner_id": "u", "body": payload}); err != nil {
		t.Fatalf("[pg] insert payload: %v", err)
	}
	if _, err := st.rec.Query(ctx, admin(), table, QueryOpts{Filters: []Filter{{Column: "body", Op: "eq", Value: payload}}}); err != nil {
		t.Fatalf("[pg] table should survive injection payload: %v", err)
	}
}

func mustPolicy(t *testing.T, s *stack, ctx context.Context, in policy.CreateInput, cols []string) {
	t.Helper()
	if _, err := s.pol.Create(ctx, in, cols); err != nil {
		t.Fatalf("create policy (%s): %v", in.Action, err)
	}
}

func columnNames(t *testing.T, cat *catalog.Service, table string) []string {
	t.Helper()
	tbl, err := cat.GetTable(context.Background(), table)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range tbl.Columns {
		out = append(out, c.Name)
	}
	return out
}
