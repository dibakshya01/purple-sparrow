// Package records implements policy-enforced CRUD over user tables. Every read is
// row-filtered by policy; every write is checked (WITH CHECK) by policy; results
// are normalized to canonical Go types by the catalog's column types so SQLite
// and Postgres return identical shapes.
package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/idgen"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

const (
	defaultLimit = 50
	maxLimit     = 1000
)

// Sentinel errors mapped to envelope codes by the HTTP layer.
var (
	ErrPolicyDenied   = errors.New("policy denied")
	ErrRecordNotFound = errors.New("record not found")
	ErrUnknownColumn  = errors.New("unknown column")
	ErrReadOnlyColumn = errors.New("read-only column")
	ErrInvalidFilter  = errors.New("invalid filter")
	ErrNoValues       = errors.New("no values provided")
)

var readOnlyColumns = map[string]bool{"id": true, "created_at": true}

// Service performs policy-enforced record operations.
type Service struct {
	eng data.Engine
	cat *catalog.Service
	enf *policy.Enforcer
}

// New returns a records Service.
func New(eng data.Engine, cat *catalog.Service, enf *policy.Enforcer) *Service {
	return &Service{eng: eng, cat: cat, enf: enf}
}

// Filter is a parsed query filter (col op value).
type Filter struct {
	Column string
	Op     string
	Value  any
}

// OrderBy is a parsed sort term.
type OrderBy struct {
	Column string
	Desc   bool
}

// QueryOpts holds parsed list parameters (validated in Query).
type QueryOpts struct {
	Select  []string
	Filters []Filter
	Order   []OrderBy
	Limit   int
	Offset  int
}

var filterOps = map[string]string{
	"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=", "like": "LIKE",
}

// tableContext bundles a table's column metadata.
type tableContext struct {
	names []string
	types map[string]string
}

func (s *Service) tableCtx(ctx context.Context, table string) (*tableContext, error) {
	cols, err := s.cat.Columns(ctx, table)
	if err != nil {
		return nil, err // catalog.ErrTableNotFound flows through
	}
	tc := &tableContext{types: make(map[string]string, len(cols))}
	for _, c := range cols {
		tc.names = append(tc.names, c.Name)
		tc.types[c.Name] = c.Type
	}
	return tc, nil
}

func (tc *tableContext) has(col string) bool { return tc.types[col] != "" }

// Query lists rows the principal may read, applying policy filter AND user filters.
func (s *Service) Query(ctx context.Context, p principal.Principal, table string, opts QueryOpts) ([]data.Row, error) {
	tc, err := s.tableCtx(ctx, table)
	if err != nil {
		return nil, err
	}
	frag, pArgs, allowed, err := s.enf.Filter(ctx, p, table, policy.ActionSelect, tc.names)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrPolicyDenied
	}

	d := s.eng.Dialect()

	// SELECT columns.
	selCols := opts.Select
	if len(selCols) == 0 {
		selCols = tc.names
	}
	var quotedSel []string
	for _, c := range selCols {
		if !tc.has(c) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownColumn, c)
		}
		quotedSel = append(quotedSel, d.QuoteIdent(c))
	}

	// WHERE = policy AND user filters (never OR — user filters cannot widen policy).
	var where []string
	var args []any
	if frag != "" {
		where = append(where, frag)
		args = append(args, pArgs...)
	}
	for _, f := range opts.Filters {
		if !tc.has(f.Column) {
			return nil, fmt.Errorf("%w: unknown column %s", ErrInvalidFilter, f.Column)
		}
		if f.Op == "in" {
			list, ok := f.Value.([]any)
			if !ok || len(list) == 0 {
				return nil, fmt.Errorf("%w: in expects a non-empty list", ErrInvalidFilter)
			}
			ph := strings.TrimSuffix(strings.Repeat("?, ", len(list)), ", ")
			where = append(where, d.QuoteIdent(f.Column)+" IN ("+ph+")")
			args = append(args, list...)
			continue
		}
		sqlOp, ok := filterOps[f.Op]
		if !ok {
			return nil, fmt.Errorf("%w: unknown operator %q", ErrInvalidFilter, f.Op)
		}
		where = append(where, d.QuoteIdent(f.Column)+" "+sqlOp+" ?")
		args = append(args, coerce(f.Value, tc.types[f.Column]))
	}

	// ORDER BY, with id as a deterministic tiebreaker.
	order := opts.Order
	var orderParts []string
	hasID := false
	for _, o := range order {
		if !tc.has(o.Column) {
			return nil, fmt.Errorf("%w: unknown order column %s", ErrInvalidFilter, o.Column)
		}
		dir := "ASC"
		if o.Desc {
			dir = "DESC"
		}
		orderParts = append(orderParts, d.QuoteIdent(o.Column)+" "+dir)
		if o.Column == "id" {
			hasID = true
		}
	}
	if !hasID {
		orderParts = append(orderParts, d.QuoteIdent("id")+" ASC")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(quotedSel, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(d.QuoteIdent(table))
	if len(where) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(where, " AND "))
	}
	sb.WriteString(" ORDER BY ")
	sb.WriteString(strings.Join(orderParts, ", "))
	sb.WriteString(" LIMIT ? OFFSET ?")
	args = append(args, limit, offset)

	rows, err := s.eng.QueryCtx(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []data.Row{} // always a JSON array, never null, even when empty
	}
	for i := range rows {
		normalizeRow(rows[i], tc.types)
	}
	return rows, nil
}

// Get returns a single row by id (policy-filtered), or ErrRecordNotFound.
func (s *Service) Get(ctx context.Context, p principal.Principal, table, id string) (data.Row, error) {
	rows, err := s.Query(ctx, p, table, QueryOpts{
		Filters: []Filter{{Column: "id", Op: "eq", Value: id}},
		Limit:   1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrRecordNotFound
	}
	return rows[0], nil
}

// Insert inserts a single row.
func (s *Service) Insert(ctx context.Context, p principal.Principal, table string, values map[string]any) (data.Row, error) {
	out, err := s.InsertMany(ctx, p, table, []map[string]any{values})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// InsertMany inserts rows all-or-nothing; each row is policy-checked.
func (s *Service) InsertMany(ctx context.Context, p principal.Principal, table string, inputs []map[string]any) ([]data.Row, error) {
	if len(inputs) == 0 {
		return nil, ErrNoValues
	}
	tc, err := s.tableCtx(ctx, table)
	if err != nil {
		return nil, err
	}
	d := s.eng.Dialect()

	type prepared struct {
		row  map[string]any
		cols []string
		args []any
	}
	prep := make([]prepared, 0, len(inputs))
	for _, in := range inputs {
		row := map[string]any{
			"id":         idgen.NewUUID(),
			"created_at": idgen.NowRFC3339(),
		}
		for k, v := range in {
			k = strings.ToLower(k)
			if readOnlyColumns[k] {
				return nil, fmt.Errorf("%w: %s is managed by the system", ErrReadOnlyColumn, k)
			}
			if !tc.has(k) {
				return nil, fmt.Errorf("%w: %s", ErrUnknownColumn, k)
			}
			row[k] = coerce(v, tc.types[k])
		}
		ok, cErr := s.enf.Check(ctx, p, table, policy.ActionInsert, tc.names, row)
		if cErr != nil {
			return nil, cErr
		}
		if !ok {
			return nil, ErrPolicyDenied
		}
		var cols []string
		var args []any
		for _, c := range tc.names {
			if val, present := row[c]; present {
				cols = append(cols, c)
				args = append(args, val)
			}
		}
		prep = append(prep, prepared{row: row, cols: cols, args: args})
	}

	err = s.eng.Transact(ctx, func(q data.Querier) error {
		for _, pr := range prep {
			var quoted []string
			for _, c := range pr.cols {
				quoted = append(quoted, d.QuoteIdent(c))
			}
			ph := strings.TrimSuffix(strings.Repeat("?, ", len(pr.cols)), ", ")
			sql := "INSERT INTO " + d.QuoteIdent(table) + " (" + strings.Join(quoted, ", ") + ") VALUES (" + ph + ")"
			if _, e := q.ExecCtx(ctx, sql, pr.args...); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]data.Row, 0, len(prep))
	for _, pr := range prep {
		normalizeRow(pr.row, tc.types)
		out = append(out, pr.row)
	}
	return out, nil
}

// Update patches a row by id. It requires the policy USING filter to target the
// row AND the policy CHECK to accept the post-update values (blocks e.g. changing
// owner_id to another user's id).
func (s *Service) Update(ctx context.Context, p principal.Principal, table, id string, patch map[string]any) (data.Row, error) {
	if len(patch) == 0 {
		return nil, ErrNoValues
	}
	tc, err := s.tableCtx(ctx, table)
	if err != nil {
		return nil, err
	}
	d := s.eng.Dialect()

	usingFrag, usingArgs, allowed, err := s.enf.Filter(ctx, p, table, policy.ActionUpdate, tc.names)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrPolicyDenied
	}

	// Validate and coerce the patch BEFORE touching the row, so a malformed patch
	// is a clean input error regardless of whether the row exists (no existence leak).
	coerced := make(map[string]any, len(patch))
	var setCols []string
	var setArgs []any
	for k, v := range patch {
		k = strings.ToLower(k)
		if readOnlyColumns[k] {
			return nil, fmt.Errorf("%w: %s is managed by the system", ErrReadOnlyColumn, k)
		}
		if !tc.has(k) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownColumn, k)
		}
		cv := coerce(v, tc.types[k])
		coerced[k] = cv
		setCols = append(setCols, d.QuoteIdent(k)+" = ?")
		setArgs = append(setArgs, cv)
	}

	// Fetch the existing row within the policy's USING scope.
	selArgs := []any{id}
	selWhere := d.QuoteIdent("id") + " = ?"
	if usingFrag != "" {
		selWhere += " AND " + usingFrag
		selArgs = append(selArgs, usingArgs...)
	}
	existing, err := s.eng.QueryRowCtx(ctx, "SELECT * FROM "+d.QuoteIdent(table)+" WHERE "+selWhere, selArgs...)
	if errors.Is(err, data.ErrNoRows) {
		return nil, ErrRecordNotFound
	}
	if err != nil {
		return nil, err
	}

	// Merge the coerced patch onto the existing row for the CHECK.
	merged := make(map[string]any, len(existing))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range coerced {
		merged[k] = v
	}

	ok, err := s.enf.Check(ctx, p, table, policy.ActionUpdate, tc.names, merged)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrPolicyDenied
	}

	// UPDATE constrained again by id + USING so a concurrent change can't slip out.
	upWhere := d.QuoteIdent("id") + " = ?"
	upArgs := append(append([]any{}, setArgs...), id)
	if usingFrag != "" {
		upWhere += " AND " + usingFrag
		upArgs = append(upArgs, usingArgs...)
	}
	sql := "UPDATE " + d.QuoteIdent(table) + " SET " + strings.Join(setCols, ", ") + " WHERE " + upWhere
	n, err := s.eng.ExecCtx(ctx, sql, upArgs...)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrRecordNotFound
	}

	normalizeRow(merged, tc.types)
	return merged, nil
}

// Delete removes a row by id within the policy USING scope.
func (s *Service) Delete(ctx context.Context, p principal.Principal, table, id string) error {
	tc, err := s.tableCtx(ctx, table)
	if err != nil {
		return err
	}
	d := s.eng.Dialect()
	frag, pArgs, allowed, err := s.enf.Filter(ctx, p, table, policy.ActionDelete, tc.names)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrPolicyDenied
	}
	where := d.QuoteIdent("id") + " = ?"
	args := []any{id}
	if frag != "" {
		where += " AND " + frag
		args = append(args, pArgs...)
	}
	n, err := s.eng.ExecCtx(ctx, "DELETE FROM "+d.QuoteIdent(table)+" WHERE "+where, args...)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRecordNotFound
	}
	return nil
}

// coerce converts an incoming JSON value to the canonical storage form for a
// logical column type (bool->0/1, json object/array->string, integer float->int64).
func coerce(v any, logical string) any {
	switch logical {
	case "boolean":
		switch b := v.(type) {
		case bool:
			if b {
				return int64(1)
			}
			return int64(0)
		case float64:
			if b != 0 {
				return int64(1)
			}
			return int64(0)
		}
	case "integer":
		if f, ok := v.(float64); ok {
			return int64(f)
		}
	case "json":
		switch v.(type) {
		case map[string]any, []any:
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
	}
	return v
}

// normalizeRow rewrites a row's values to canonical Go types keyed by column type
// so responses are identical across engines.
func normalizeRow(row data.Row, types map[string]string) {
	for col, v := range row {
		switch types[col] {
		case "boolean":
			row[col] = toInt(v) != 0
		case "integer":
			row[col] = toInt(v)
		case "json":
			if s, ok := v.(string); ok && s != "" {
				var decoded any
				if json.Unmarshal([]byte(s), &decoded) == nil {
					row[col] = decoded
				}
			}
		}
	}
}

func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case bool:
		if n {
			return 1
		}
	}
	return 0
}
