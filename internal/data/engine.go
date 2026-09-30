// Package data defines the storage engine port and its SQLite adapter. The port
// (Engine/Querier/Dialect) is deliberately small and dialect-abstracted so the
// Postgres adapter (M5) drops in without changing callers (ADR-0002).
package data

import "context"

// Row is a single result row keyed by column name. Values are normalized Go
// types (string, int64, float64, bool, nil, []byte) — see the adapter's scan.
type Row = map[string]any

// Querier is the read/write surface shared by the engine and a transaction, so
// services can take either.
type Querier interface {
	ExecCtx(ctx context.Context, query string, args ...any) (rowsAffected int64, err error)
	QueryCtx(ctx context.Context, query string, args ...any) ([]Row, error)
	QueryRowCtx(ctx context.Context, query string, args ...any) (Row, error)
}

// Engine is a database connection pool.
type Engine interface {
	Querier
	// Dialect exposes identifier quoting and placeholder style.
	Dialect() Dialect
	// Transact runs fn in a transaction, committing on nil error and rolling
	// back otherwise (including on panic).
	Transact(ctx context.Context, fn func(Querier) error) error
	// Ping verifies connectivity.
	Ping(ctx context.Context) error
	Close() error
}

// Dialect abstracts the SQL differences between engines.
type Dialect interface {
	// QuoteIdent returns a safely-quoted identifier. Callers MUST still validate
	// identifiers against a whitelist first; quoting is defense-in-depth.
	QuoteIdent(name string) string
	// Placeholder returns the bind placeholder for the 1-based arg position
	// (SQLite: "?", Postgres: "$1").
	Placeholder(n int) string
	// Name identifies the dialect ("sqlite", "postgres").
	Name() string
}
