package data

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/data/ident"

	_ "github.com/jackc/pgx/v5/stdlib" // Postgres driver registered as "pgx"
)

// --- Dialect --------------------------------------------------------------

type postgresDialect struct{}

func (postgresDialect) Name() string                        { return "postgres" }
func (postgresDialect) Placeholder(n int) string            { return "$" + strconv.Itoa(n) }
func (postgresDialect) LikeOperator() string                 { return "ILIKE" } // case-insensitive, matching SQLite LIKE
func (postgresDialect) LockClause() string                   { return " FOR UPDATE" }
func (postgresDialect) SQLType(logical string) (string, bool) { return ident.PostgresType(logical) }

func (postgresDialect) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// --- Engine ---------------------------------------------------------------

// Postgres is the pgx (database/sql) adapter implementing Engine for the
// startup/enterprise tiers. The rest of the codebase emits "?" placeholders; this
// adapter rewrites them to $N before dispatch.
type Postgres struct {
	db *sql.DB
}

// OpenPostgres opens a connection pool to the given DSN.
func OpenPostgres(dsn string) (*Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Postgres{db: db}, nil
}

func (p *Postgres) Dialect() Dialect              { return postgresDialect{} }
func (p *Postgres) Ping(ctx context.Context) error { return p.db.PingContext(ctx) }
func (p *Postgres) Close() error                  { return p.db.Close() }

func (p *Postgres) ExecCtx(ctx context.Context, q string, args ...any) (int64, error) {
	rq, err := checkedRewrite(q, len(args))
	if err != nil {
		return 0, err
	}
	return execCtx(ctx, p.db, rq, args...)
}
func (p *Postgres) QueryCtx(ctx context.Context, q string, args ...any) ([]Row, error) {
	rq, err := checkedRewrite(q, len(args))
	if err != nil {
		return nil, err
	}
	return queryCtx(ctx, p.db, rq, args...)
}
func (p *Postgres) QueryRowCtx(ctx context.Context, q string, args ...any) (Row, error) {
	rq, err := checkedRewrite(q, len(args))
	if err != nil {
		return nil, err
	}
	return queryRowCtx(ctx, p.db, rq, args...)
}

func (p *Postgres) Transact(ctx context.Context, fn func(Querier) error) (err error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	if err = fn(&pgTxQuerier{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type pgTxQuerier struct{ tx *sql.Tx }

func (t *pgTxQuerier) ExecCtx(ctx context.Context, q string, args ...any) (int64, error) {
	rq, err := checkedRewrite(q, len(args))
	if err != nil {
		return 0, err
	}
	return execCtx(ctx, t.tx, rq, args...)
}
func (t *pgTxQuerier) QueryCtx(ctx context.Context, q string, args ...any) ([]Row, error) {
	rq, err := checkedRewrite(q, len(args))
	if err != nil {
		return nil, err
	}
	return queryCtx(ctx, t.tx, rq, args...)
}
func (t *pgTxQuerier) QueryRowCtx(ctx context.Context, q string, args ...any) (Row, error) {
	rq, err := checkedRewrite(q, len(args))
	if err != nil {
		return nil, err
	}
	return queryRowCtx(ctx, t.tx, rq, args...)
}

// checkedRewrite rewrites "?" placeholders to $N and FAILS LOUDLY if the produced
// count does not equal the number of args. This turns the informal "generated SQL
// contains no literal ?" invariant into an enforced one: a stray "?" (a jsonb
// operator, a literal, a migration comment) would mis-number placeholders — the
// mismatch is caught here instead of corrupting a query.
func checkedRewrite(q string, argc int) (string, error) {
	rq, n := rewritePlaceholders(q)
	if n != argc {
		return "", fmt.Errorf("placeholder/arg mismatch: query has %d placeholders but %d args were provided", n, argc)
	}
	return rq, nil
}

// rewritePlaceholders converts "?" placeholders to Postgres's positional $1,$2,…
// form, returning the rewritten query and the number of placeholders produced.
func rewritePlaceholders(q string) (string, int) {
	if !strings.Contains(q, "?") {
		return q, 0
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String(), n
}

// compile-time assertion that Postgres satisfies the Engine port.
var _ Engine = (*Postgres)(nil)
