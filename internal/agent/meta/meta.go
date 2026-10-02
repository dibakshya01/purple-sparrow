// Package meta implements /meta: a single-call introspection of the whole backend
// shape (tables, columns, policies, row counts) designed for an agent to load
// context in one request. Admin-only (schema disclosure).
package meta

import (
	"context"

	"github.com/dibakshya01/purple-sparrow/internal/buildinfo"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
)

// Service builds the metadata snapshot.
type Service struct {
	eng data.Engine
	cat *catalog.Service
	pol *policy.Service
}

// New returns a meta Service.
func New(eng data.Engine, cat *catalog.Service, pol *policy.Service) *Service {
	return &Service{eng: eng, cat: cat, pol: pol}
}

// TableMeta describes one table.
type TableMeta struct {
	Name     string           `json:"name"`
	Columns  []catalog.Column `json:"columns"`
	Policies []policy.Policy  `json:"policies"`
	RowCount int64            `json:"row_count"`
}

// Snapshot is the whole-backend introspection payload.
type Snapshot struct {
	Engine  string          `json:"engine"`
	Version string          `json:"version"`
	Tables  []TableMeta     `json:"tables"`
	Counts  map[string]int  `json:"counts"`
}

// Build assembles the snapshot.
func (s *Service) Build(ctx context.Context) (*Snapshot, error) {
	names, err := s.cat.ListTables(ctx)
	if err != nil {
		return nil, err
	}
	out := &Snapshot{
		Engine:  s.eng.Dialect().Name(),
		Version: buildinfo.Get().Version,
		Tables:  make([]TableMeta, 0, len(names)),
		Counts:  map[string]int{"tables": len(names)},
	}
	totalPolicies := 0
	for _, name := range names {
		cols, err := s.cat.Columns(ctx, name)
		if err != nil {
			return nil, err
		}
		pols, err := s.pol.List(ctx, name)
		if err != nil {
			return nil, err
		}
		totalPolicies += len(pols)
		out.Tables = append(out.Tables, TableMeta{
			Name:     name,
			Columns:  cols,
			Policies: pols,
			RowCount: s.rowCount(ctx, name),
		})
	}
	out.Counts["policies"] = totalPolicies
	return out, nil
}

func (s *Service) rowCount(ctx context.Context, table string) int64 {
	// table is a validated catalog name; quote defensively.
	row, err := s.eng.QueryRowCtx(ctx, "SELECT count(*) AS c FROM "+s.eng.Dialect().QuoteIdent(table))
	if err != nil {
		return -1
	}
	switch n := row["c"].(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return -1
}
