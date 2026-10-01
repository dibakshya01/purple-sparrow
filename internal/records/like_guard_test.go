package records

import (
	"context"
	"errors"
	"testing"

	"github.com/dibakshya01/orange-crow/internal/catalog"
)

// `like` must be rejected on non-text columns (clean 400) rather than relying on
// SQLite's loose affinity — which would be a 500 on Postgres.
func TestLikeOnlyOnTextColumns(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	if _, err := s.cat.CreateTable(ctx, "t", []catalog.Column{
		{Name: "name", Type: "text"},
		{Name: "score", Type: "integer"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rec.Insert(ctx, admin(), "t", map[string]any{"name": "alice", "score": 5}); err != nil {
		t.Fatal(err)
	}

	// Allowed on text.
	if rows, err := s.rec.Query(ctx, admin(), "t", QueryOpts{
		Filters: []Filter{{Column: "name", Op: "like", Value: "ali%"}},
	}); err != nil || len(rows) != 1 {
		t.Fatalf("like on text should match 1, got %d (err %v)", len(rows), err)
	}
	// Rejected on numeric.
	if _, err := s.rec.Query(ctx, admin(), "t", QueryOpts{
		Filters: []Filter{{Column: "score", Op: "like", Value: "5%"}},
	}); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("like on an integer column should be ErrInvalidFilter, got %v", err)
	}
}
