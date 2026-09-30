package data

import (
	"context"
	"errors"
	"testing"
)

func openMem(t *testing.T) *SQLite {
	t.Helper()
	eng, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return eng
}

func TestExecQueryRoundTrip(t *testing.T) {
	ctx := context.Background()
	eng := openMem(t)

	if _, err := eng.ExecCtx(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := eng.ExecCtx(ctx, `INSERT INTO t (name) VALUES (?)`, "alice"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := eng.QueryCtx(ctx, `SELECT id, name FROM t`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 || rows[0]["name"] != "alice" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestValuesAreInertParameters(t *testing.T) {
	ctx := context.Background()
	eng := openMem(t)
	if _, err := eng.ExecCtx(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	// A classic injection payload passed as a bound value must be stored literally,
	// not executed.
	payload := "x'); DROP TABLE t;--"
	if _, err := eng.ExecCtx(ctx, `INSERT INTO t (name) VALUES (?)`, payload); err != nil {
		t.Fatal(err)
	}
	rows, err := eng.QueryCtx(ctx, `SELECT name FROM t WHERE name = ?`, payload)
	if err != nil {
		t.Fatalf("table should still exist: %v", err)
	}
	if len(rows) != 1 || rows[0]["name"] != payload {
		t.Fatalf("payload not stored inertly: %+v", rows)
	}
}

func TestQueryRowNoRows(t *testing.T) {
	ctx := context.Background()
	eng := openMem(t)
	if _, err := eng.ExecCtx(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_, err := eng.QueryRowCtx(ctx, `SELECT id FROM t WHERE id = ?`, 999)
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
}

func TestTransactRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	eng := openMem(t)
	if _, err := eng.ExecCtx(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, n INTEGER)`); err != nil {
		t.Fatal(err)
	}

	// Rollback path.
	wantErr := errors.New("boom")
	err := eng.Transact(ctx, func(q Querier) error {
		if _, e := q.ExecCtx(ctx, `INSERT INTO t (n) VALUES (?)`, 1); e != nil {
			return e
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want boom, got %v", err)
	}
	rows, _ := eng.QueryCtx(ctx, `SELECT count(*) AS c FROM t`)
	if rows[0]["c"].(int64) != 0 {
		t.Fatalf("rollback failed, count=%v", rows[0]["c"])
	}

	// Commit path.
	if err := eng.Transact(ctx, func(q Querier) error {
		_, e := q.ExecCtx(ctx, `INSERT INTO t (n) VALUES (?)`, 2)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	rows, _ = eng.QueryCtx(ctx, `SELECT count(*) AS c FROM t`)
	if rows[0]["c"].(int64) != 1 {
		t.Fatalf("commit failed, count=%v", rows[0]["c"])
	}
}
