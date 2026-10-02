package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dibakshya01/purple-sparrow/internal/data/ident"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// ErrNoRows is returned by QueryRowCtx when no row matches.
var ErrNoRows = errors.New("data: no rows in result set")

// --- Dialect --------------------------------------------------------------

type sqliteDialect struct{}

func (sqliteDialect) Name() string             { return "sqlite" }
func (sqliteDialect) Placeholder(_ int) string { return "?" }
func (sqliteDialect) LikeOperator() string     { return "LIKE" } // ASCII case-insensitive in SQLite
func (sqliteDialect) LockClause() string       { return "" }     // single-writer tx serializes; no FOR UPDATE

func (sqliteDialect) SQLType(logical string) (string, bool) { return ident.SQLiteType(logical) }

// QuoteIdent double-quotes an identifier and escapes embedded quotes. Callers
// must still validate identifiers against a whitelist first.
func (sqliteDialect) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// --- Engine ---------------------------------------------------------------

// SQLite is the modernc.org/sqlite adapter implementing Engine.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) a SQLite database at path. Use ":memory:"
// for tests. WAL + a busy timeout make concurrent access robust.
func OpenSQLite(path string) (*SQLite, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite has a single writer; serializing connections avoids "database is
	// locked" under concurrent writes. Adequate for the solo tier; revisited for
	// scale (that tier uses Postgres anyway).
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	// The database file is the trust root (password hashes, the RSA signing key,
	// API-key hashes). Restrict it to the owner. :memory: has no file. The
	// -wal/-shm sidecars are protected by the 0700 data dir.
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
			_ = db.Close()
			return nil, fmt.Errorf("securing database file: %w", err)
		}
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Dialect() Dialect               { return sqliteDialect{} }
func (s *SQLite) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQLite) Close() error                   { return s.db.Close() }

func (s *SQLite) ExecCtx(ctx context.Context, q string, args ...any) (int64, error) {
	return execCtx(ctx, s.db, q, args...)
}
func (s *SQLite) QueryCtx(ctx context.Context, q string, args ...any) ([]Row, error) {
	return queryCtx(ctx, s.db, q, args...)
}
func (s *SQLite) QueryRowCtx(ctx context.Context, q string, args ...any) (Row, error) {
	return queryRowCtx(ctx, s.db, q, args...)
}

// Transact runs fn in a transaction. It commits on success and rolls back on
// error or panic (re-panicking after rollback).
func (s *SQLite) Transact(ctx context.Context, fn func(Querier) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err = fn(&txQuerier{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// txQuerier adapts *sql.Tx to Querier.
type txQuerier struct{ tx *sql.Tx }

func (t *txQuerier) ExecCtx(ctx context.Context, q string, args ...any) (int64, error) {
	return execCtx(ctx, t.tx, q, args...)
}
func (t *txQuerier) QueryCtx(ctx context.Context, q string, args ...any) ([]Row, error) {
	return queryCtx(ctx, t.tx, q, args...)
}
func (t *txQuerier) QueryRowCtx(ctx context.Context, q string, args ...any) (Row, error) {
	return queryRowCtx(ctx, t.tx, q, args...)
}

// dbExecQuery is the subset of *sql.DB / *sql.Tx used by the shared helpers.
type dbExecQuery interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func execCtx(ctx context.Context, e dbExecQuery, q string, args ...any) (int64, error) {
	res, err := e.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil // some statements don't report; not an error for callers
	}
	return n, nil
}

func queryCtx(ctx context.Context, e dbExecQuery, q string, args ...any) ([]Row, error) {
	rows, err := e.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func queryRowCtx(ctx context.Context, e dbExecQuery, q string, args ...any) (Row, error) {
	out, err := queryCtx(ctx, e, q, args...)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNoRows
	}
	return out[0], nil
}

// scanRows materializes rows into []Row, normalizing []byte to string so JSON
// responses render text rather than base64.
func scanRows(rows *sql.Rows) ([]Row, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			row[c] = canonical(cells[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// canonical normalizes driver-specific scan types to a small canonical set so both
// engines return identical Go types: all signed/unsigned integers -> int64,
// float32 -> float64, []byte -> string. (pgx may return int32 for INTEGER where
// modernc returns int64; without this a boolean-as-INTEGER would misread.)
func canonical(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case int64:
		return x
	case int32:
		return int64(x)
	case int16:
		return int64(x)
	case int8:
		return int64(x)
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		return float64(x)
	default:
		return v
	}
}

// compile-time assertion that SQLite satisfies the Engine port.
var _ Engine = (*SQLite)(nil)
