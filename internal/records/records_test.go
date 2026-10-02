package records

import (
	"context"
	"errors"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

type stack struct {
	eng data.Engine
	cat *catalog.Service
	pol *policy.Service
	rec *Service
}

func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()
	eng, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := migrate.Run(ctx, eng); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(eng)
	enf := policy.NewEnforcer(eng)
	return &stack{eng: eng, cat: cat, pol: policy.NewService(eng), rec: New(eng, cat, enf)}
}

func (s *stack) mkNotes(t *testing.T) {
	t.Helper()
	_, err := s.cat.CreateTable(context.Background(), "notes", []catalog.Column{
		{Name: "owner_id", Type: "text"},
		{Name: "body", Type: "text", Nullable: true},
	})
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
}

func (s *stack) cols(t *testing.T) []string {
	t.Helper()
	tbl, err := s.cat.GetTable(context.Background(), "notes")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range tbl.Columns {
		out = append(out, c.Name)
	}
	return out
}

func admin() principal.Principal { return principal.Principal{Roles: []string{principal.RoleProjectAdmin}} }
func user(id string) principal.Principal {
	return principal.Principal{Roles: []string{principal.RoleAuthenticated}, Subject: id}
}

// AC-2: with no policy, non-admin select/insert is denied; admin works.
func TestDenyByDefault(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)

	if _, err := s.rec.Insert(ctx, admin(), "notes", map[string]any{"owner_id": "u1", "body": "hi"}); err != nil {
		t.Fatalf("admin insert should work: %v", err)
	}
	if _, err := s.rec.Query(ctx, principal.Anon(), "notes", QueryOpts{}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("anon select should be denied, got %v", err)
	}
	if _, err := s.rec.Insert(ctx, user("u1"), "notes", map[string]any{"owner_id": "u1"}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("user insert without policy should be denied, got %v", err)
	}
}

// AC-3: owner-scoped select returns only the caller's rows.
func TestOwnerScopedSelect(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)
	cols := s.cols(t)

	for _, owner := range []string{"user-a", "user-a", "user-b"} {
		if _, err := s.rec.Insert(ctx, admin(), "notes", map[string]any{"owner_id": owner, "body": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.pol.Create(ctx, policy.CreateInput{
		Table: "notes", Action: policy.ActionSelect,
		Roles: []string{principal.RoleAuthenticated}, Using: "auth.uid() = owner_id",
	}, cols); err != nil {
		t.Fatal(err)
	}

	rowsA, err := s.rec.Query(ctx, user("user-a"), "notes", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 2 {
		t.Fatalf("user-a should see 2 rows, saw %d", len(rowsA))
	}
	rowsB, err := s.rec.Query(ctx, user("user-b"), "notes", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsB) != 1 {
		t.Fatalf("user-b should see 1 row, saw %d", len(rowsB))
	}
}

// AC-2b / Must-fix 2: anon (NULL uid) sees zero rows even when owner_id = ''.
func TestAnonNullUidSeesNothing(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)
	cols := s.cols(t)

	if _, err := s.rec.Insert(ctx, admin(), "notes", map[string]any{"owner_id": "", "body": "empty-owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pol.Create(ctx, policy.CreateInput{
		Table: "notes", Action: policy.ActionSelect,
		Roles: []string{principal.RoleAnon}, Using: "auth.uid() = owner_id",
	}, cols); err != nil {
		t.Fatal(err)
	}
	rows, err := s.rec.Query(ctx, principal.Anon(), "notes", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("anon must see 0 rows (NULL = '' is NULL), saw %d: %+v", len(rows), rows)
	}
}

// AC-4: insert check policy accepts a valid row and rejects a spoofed one.
func TestInsertCheck(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)
	cols := s.cols(t)
	if _, err := s.pol.Create(ctx, policy.CreateInput{
		Table: "notes", Action: policy.ActionInsert,
		Roles: []string{principal.RoleAuthenticated}, Check: "auth.uid() = owner_id",
	}, cols); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rec.Insert(ctx, user("user-a"), "notes", map[string]any{"owner_id": "user-a", "body": "mine"}); err != nil {
		t.Fatalf("valid owned insert should pass: %v", err)
	}
	if _, err := s.rec.Insert(ctx, user("user-a"), "notes", map[string]any{"owner_id": "user-b", "body": "spoof"}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("insert spoofing owner should be denied, got %v", err)
	}
}

// CROWN JEWEL: update WITH CHECK blocks ownership reassignment.
func TestUpdateCheckBlocksOwnershipReassignment(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)
	cols := s.cols(t)

	created, err := s.rec.Insert(ctx, admin(), "notes", map[string]any{"owner_id": "user-a", "body": "mine"})
	if err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)

	// update policy: user may act on their own rows; check defaults to using.
	if _, err := s.pol.Create(ctx, policy.CreateInput{
		Table: "notes", Action: policy.ActionUpdate,
		Roles: []string{principal.RoleAuthenticated}, Using: "auth.uid() = owner_id",
	}, cols); err != nil {
		t.Fatal(err)
	}

	// Legit: user-a edits their own body.
	if _, err := s.rec.Update(ctx, user("user-a"), "notes", id, map[string]any{"body": "edited"}); err != nil {
		t.Fatalf("owner editing own row should pass: %v", err)
	}
	// Attack: user-a tries to reassign ownership to user-b. Must be rejected.
	if _, err := s.rec.Update(ctx, user("user-a"), "notes", id, map[string]any{"owner_id": "user-b"}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("ownership reassignment must be denied by WITH CHECK, got %v", err)
	}
	// Row must still belong to user-a.
	row, err := s.rec.Get(ctx, admin(), "notes", id)
	if err != nil {
		t.Fatal(err)
	}
	if row["owner_id"] != "user-a" {
		t.Fatalf("owner must remain user-a, got %v", row["owner_id"])
	}
}

// AC-5: injection payloads passed as data are inert; the table survives.
func TestInjectionAsDataIsInert(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)

	payload := "x'); DROP TABLE notes;--"
	if _, err := s.rec.Insert(ctx, admin(), "notes", map[string]any{"owner_id": "u", "body": payload}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.rec.Query(ctx, admin(), "notes", QueryOpts{
		Filters: []Filter{{Column: "body", Op: "eq", Value: payload}},
	})
	if err != nil {
		t.Fatalf("table should still exist and query should work: %v", err)
	}
	if len(rows) != 1 || rows[0]["body"] != payload {
		t.Fatalf("payload not stored/compared inertly: %+v", rows)
	}
}

// AC-6: bad filter/column produce typed 4xx-mapped errors, not panics/500s.
func TestBadInputsAreTypedErrors(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	s.mkNotes(t)

	if _, err := s.rec.Query(ctx, admin(), "notes", QueryOpts{
		Filters: []Filter{{Column: "nope", Op: "eq", Value: 1}},
	}); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("unknown filter column should be ErrInvalidFilter, got %v", err)
	}
	if _, err := s.rec.Insert(ctx, admin(), "notes", map[string]any{"ghost": 1}); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("unknown insert column should be ErrUnknownColumn, got %v", err)
	}
	if _, err := s.rec.Update(ctx, admin(), "notes", "x", map[string]any{"id": "hack"}); !errors.Is(err, ErrReadOnlyColumn) {
		t.Fatalf("patching id should be ErrReadOnlyColumn, got %v", err)
	}
}
