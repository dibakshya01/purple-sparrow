package records

import (
	"context"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/catalog"
)

// Query filters arrive from the HTTP layer as strings (?col=op.value). They must
// be coerced to the column's type so boolean and numeric comparisons work on
// SQLite (loose affinity) and would work on Postgres (strict typing).
func TestFilterValueCoercion(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	if _, err := s.cat.CreateTable(ctx, "flags", []catalog.Column{
		{Name: "name", Type: "text"},
		{Name: "done", Type: "boolean"},
		{Name: "score", Type: "integer"},
	}); err != nil {
		t.Fatal(err)
	}
	ins := func(name string, done bool, score int) {
		if _, err := s.rec.Insert(ctx, admin(), "flags", map[string]any{"name": name, "done": done, "score": score}); err != nil {
			t.Fatal(err)
		}
	}
	ins("a", true, 10)
	ins("b", false, 20)
	ins("c", true, 30)

	// boolean filter as string "true"
	if rows, err := s.rec.Query(ctx, admin(), "flags", QueryOpts{
		Filters: []Filter{{Column: "done", Op: "eq", Value: "true"}},
	}); err != nil || len(rows) != 2 {
		t.Fatalf("done=eq.true should match 2, got %d (err %v)", len(rows), err)
	}
	// boolean filter as string "false"
	if rows, err := s.rec.Query(ctx, admin(), "flags", QueryOpts{
		Filters: []Filter{{Column: "done", Op: "eq", Value: "false"}},
	}); err != nil || len(rows) != 1 {
		t.Fatalf("done=eq.false should match 1, got %d", len(rows))
	}
	// integer range filter as string
	if rows, err := s.rec.Query(ctx, admin(), "flags", QueryOpts{
		Filters: []Filter{{Column: "score", Op: "gt", Value: "15"}},
	}); err != nil || len(rows) != 2 {
		t.Fatalf("score=gt.15 should match 2, got %d", len(rows))
	}
	// integer in-list as strings
	if rows, err := s.rec.Query(ctx, admin(), "flags", QueryOpts{
		Filters: []Filter{{Column: "score", Op: "in", Value: []any{"10", "30"}}},
	}); err != nil || len(rows) != 2 {
		t.Fatalf("score=in.(10,30) should match 2, got %d", len(rows))
	}
}
