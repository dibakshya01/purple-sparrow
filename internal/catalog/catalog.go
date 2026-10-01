// Package catalog manages user-defined tables: it validates and executes DDL and
// keeps the _oc_* metadata in sync, always inside a transaction so the physical
// table and its catalog rows never disagree.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dibakshya01/orange-crow/internal/data"
	"github.com/dibakshya01/orange-crow/internal/data/ident"
)

// Sentinel errors; the HTTP layer maps these to envelope codes.
var (
	ErrTableExists       = errors.New("table already exists")
	ErrTableNotFound     = errors.New("table not found")
	ErrInvalidIdentifier = errors.New("invalid identifier")
	ErrInvalidType       = errors.New("invalid column type")
	ErrNoColumns         = errors.New("table needs at least one user column")
	ErrReservedColumn    = errors.New("reserved column name")
)

// Reserved columns are managed by the system and cannot be redefined by users.
var reservedColumns = map[string]bool{"id": true, "created_at": true}

// Column is a user-facing column definition.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Unique   bool   `json:"unique"`
	Ordinal  int    `json:"-"`
}

// Table is a user table's metadata.
type Table struct {
	Name      string   `json:"name"`
	Columns   []Column `json:"columns"`
	CreatedAt string   `json:"created_at"`
}

// Service manages the catalog.
type Service struct{ eng data.Engine }

// New returns a catalog Service.
func New(eng data.Engine) *Service { return &Service{eng: eng} }

// CreateTable validates and creates a user table plus its catalog rows. An "id"
// (uuid, primary key) and "created_at" (timestamp) column are added automatically.
func (s *Service) CreateTable(ctx context.Context, name string, cols []Column) (*Table, error) {
	if !ident.ValidUser(name) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidIdentifier, name)
	}
	if len(cols) == 0 {
		return nil, ErrNoColumns
	}

	seen := map[string]bool{}
	for i := range cols {
		c := &cols[i]
		c.Name = strings.ToLower(strings.TrimSpace(c.Name))
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
		if reservedColumns[c.Name] {
			return nil, fmt.Errorf("%w: %s", ErrReservedColumn, c.Name)
		}
		if !ident.ValidUser(c.Name) {
			return nil, fmt.Errorf("%w: column %s", ErrInvalidIdentifier, c.Name)
		}
		if !ident.ValidType(c.Type) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidType, c.Type)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("%w: duplicate column %s", ErrInvalidIdentifier, c.Name)
		}
		seen[c.Name] = true
	}

	dialect := s.eng.Dialect()
	full := s.systemColumns()
	full = append(full, cols...)
	for i := range full {
		full[i].Ordinal = i
	}

	ddl, err := buildCreateDDL(dialect, name, full)
	if err != nil {
		return nil, err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)

	err = s.eng.Transact(ctx, func(q data.Querier) error {
		exists, err := tableExistsTx(ctx, q, name)
		if err != nil {
			return err
		}
		if exists {
			return ErrTableExists
		}
		if _, err := q.ExecCtx(ctx, ddl); err != nil {
			return fmt.Errorf("create table ddl: %w", err)
		}
		if _, err := q.ExecCtx(ctx,
			`INSERT INTO _oc_tables (name, created_at) VALUES (?, ?)`, name, createdAt); err != nil {
			return err
		}
		for _, c := range full {
			if _, err := q.ExecCtx(ctx,
				`INSERT INTO _oc_columns (table_name, name, type, nullable, is_unique, ordinal)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				name, c.Name, c.Type, boolToInt(c.Nullable), boolToInt(c.Unique), c.Ordinal); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Table{Name: name, Columns: full, CreatedAt: createdAt}, nil
}

// systemColumns are the auto-managed columns present on every user table.
func (s *Service) systemColumns() []Column {
	return []Column{
		{Name: "id", Type: "uuid", Nullable: false, Unique: true},
		{Name: "created_at", Type: "timestamp", Nullable: false},
	}
}

// ListTables returns the names of all user tables.
func (s *Service) ListTables(ctx context.Context) ([]string, error) {
	rows, err := s.eng.QueryCtx(ctx, `SELECT name FROM _oc_tables ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if n, ok := r["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// GetTable returns a table's metadata (columns), or ErrTableNotFound.
func (s *Service) GetTable(ctx context.Context, name string) (*Table, error) {
	trow, err := s.eng.QueryRowCtx(ctx, `SELECT name, created_at FROM _oc_tables WHERE name = ?`, name)
	if errors.Is(err, data.ErrNoRows) {
		return nil, ErrTableNotFound
	}
	if err != nil {
		return nil, err
	}
	cols, err := s.Columns(ctx, name)
	if err != nil {
		return nil, err
	}
	return &Table{Name: name, Columns: cols, CreatedAt: asString(trow["created_at"])}, nil
}

// Columns returns a table's columns in ordinal order, or ErrTableNotFound if the
// table has no catalog entry.
func (s *Service) Columns(ctx context.Context, name string) ([]Column, error) {
	rows, err := s.eng.QueryCtx(ctx,
		`SELECT name, type, nullable, is_unique, ordinal FROM _oc_columns WHERE table_name = ? ORDER BY ordinal`, name)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrTableNotFound
	}
	out := make([]Column, 0, len(rows))
	for _, r := range rows {
		out = append(out, Column{
			Name:     asString(r["name"]),
			Type:     asString(r["type"]),
			Nullable: asInt(r["nullable"]) != 0,
			Unique:   asInt(r["is_unique"]) != 0,
			Ordinal:  int(asInt(r["ordinal"])),
		})
	}
	return out, nil
}

// DropTable drops a user table and its catalog rows.
func (s *Service) DropTable(ctx context.Context, name string) error {
	if !ident.ValidUser(name) {
		return fmt.Errorf("%w: %s", ErrInvalidIdentifier, name)
	}
	q := s.eng.Dialect().QuoteIdent(name)
	return s.eng.Transact(ctx, func(tx data.Querier) error {
		exists, err := tableExistsTx(ctx, tx, name)
		if err != nil {
			return err
		}
		if !exists {
			return ErrTableNotFound
		}
		if _, err := tx.ExecCtx(ctx, "DROP TABLE "+q); err != nil {
			return err
		}
		// Cascades to _oc_columns and _oc_policies via FK ON DELETE CASCADE.
		_, err = tx.ExecCtx(ctx, `DELETE FROM _oc_tables WHERE name = ?`, name)
		return err
	})
}

// TableExists reports whether a user table exists in the catalog.
func (s *Service) TableExists(ctx context.Context, name string) (bool, error) {
	return tableExistsTx(ctx, s.eng, name)
}

func tableExistsTx(ctx context.Context, q data.Querier, name string) (bool, error) {
	_, err := q.QueryRowCtx(ctx, `SELECT name FROM _oc_tables WHERE name = ?`, name)
	if errors.Is(err, data.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// buildCreateDDL builds a CREATE TABLE statement with quoted identifiers and
// whitelisted, mapped column types. Identifiers are already validated by callers.
func buildCreateDDL(d data.Dialect, table string, cols []Column) (string, error) {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(d.QuoteIdent(table))
	b.WriteString(" (\n")
	for i, c := range cols {
		sqlType, ok := d.SQLType(c.Type)
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrInvalidType, c.Type)
		}
		b.WriteString("  ")
		b.WriteString(d.QuoteIdent(c.Name))
		b.WriteString(" ")
		b.WriteString(sqlType)
		if c.Name == "id" {
			b.WriteString(" PRIMARY KEY")
		} else {
			if !c.Nullable {
				b.WriteString(" NOT NULL")
			}
			if c.Unique {
				b.WriteString(" UNIQUE")
			}
		}
		if i < len(cols)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(")")
	return b.String(), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}
