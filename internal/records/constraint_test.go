package records

import (
	"context"
	"errors"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/catalog"
)

// Reachable client errors (missing required field, duplicate unique value) must be
// typed validation/constraint errors (4xx), never a raw DB error (-> 500).
func TestConstraintErrorsAreTyped(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	if _, err := s.cat.CreateTable(ctx, "people", []catalog.Column{
		{Name: "email", Type: "text", Unique: true},
		{Name: "name", Type: "text", Nullable: true},
	}); err != nil {
		t.Fatal(err)
	}

	// Missing required column (email is NOT NULL) -> ErrMissingRequired, not 500.
	if _, err := s.rec.Insert(ctx, admin(), "people", map[string]any{"name": "no email"}); !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("missing required column should be ErrMissingRequired, got %v", err)
	}

	// First insert ok.
	if _, err := s.rec.Insert(ctx, admin(), "people", map[string]any{"email": "a@x.com"}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Duplicate unique value -> ErrConstraint, not a raw 500.
	if _, err := s.rec.Insert(ctx, admin(), "people", map[string]any{"email": "a@x.com"}); !errors.Is(err, ErrConstraint) {
		t.Fatalf("duplicate unique should be ErrConstraint, got %v", err)
	}
}
