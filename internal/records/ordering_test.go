package records

import (
	"context"
	"testing"

	"github.com/dibakshya01/orange-crow/internal/catalog"
)

// Ordering must be deterministic and place NULLs last regardless of engine, so
// LIMIT/OFFSET pagination is stable and identical across SQLite and Postgres.
// (SQLite defaults NULLs first on ASC, Postgres last; we emit explicit NULLS LAST.)
func TestOrderingNullsLastAndPagination(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	if _, err := s.cat.CreateTable(ctx, "items", []catalog.Column{
		{Name: "label", Type: "text"},
		{Name: "rank", Type: "integer", Nullable: true},
	}); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"label": "a", "rank": 3},
		{"label": "b"}, // NULL rank
		{"label": "c", "rank": 1},
		{"label": "d"}, // NULL rank
		{"label": "e", "rank": 2},
	}
	for _, r := range rows {
		if _, err := s.rec.Insert(ctx, admin(), "items", r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.rec.Query(ctx, admin(), "items", QueryOpts{
		Order: []OrderBy{{Column: "rank", Desc: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Non-null ranks ascending (1,2,3) then NULLs last.
	wantLabels := []string{"c", "e", "a", "b", "d"}
	if len(got) != len(wantLabels) {
		t.Fatalf("got %d rows, want %d", len(got), len(wantLabels))
	}
	for i, w := range wantLabels[:3] { // first three are the non-null, ordered
		if got[i]["label"] != w {
			t.Fatalf("position %d = %v, want %s (order: %v)", i, got[i]["label"], w, labels(got))
		}
	}
	// Last two must be the NULL-rank rows (order among equal NULLs is id-tiebroken).
	lastTwo := map[string]bool{got[3]["label"].(string): true, got[4]["label"].(string): true}
	if !lastTwo["b"] || !lastTwo["d"] {
		t.Fatalf("NULL-rank rows must sort last, got order: %v", labels(got))
	}

	// Pagination determinism: page1 (limit 2) + page2 (limit 2 offset 2) are disjoint & ordered.
	p1, _ := s.rec.Query(ctx, admin(), "items", QueryOpts{Order: []OrderBy{{Column: "rank"}}, Limit: 2})
	p2, _ := s.rec.Query(ctx, admin(), "items", QueryOpts{Order: []OrderBy{{Column: "rank"}}, Limit: 2, Offset: 2})
	if labels(p1)[0] != "c" || labels(p1)[1] != "e" {
		t.Fatalf("page1 should be [c e], got %v", labels(p1))
	}
	if labels(p2)[0] != "a" {
		t.Fatalf("page2 should start at a, got %v", labels(p2))
	}
}

func labels(rows []rowAlias) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["label"].(string))
	}
	return out
}

// rowAlias matches the element type returned by Query (data.Row).
type rowAlias = map[string]any
