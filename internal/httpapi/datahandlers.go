package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/orange-crow/internal/apierr"
	"github.com/dibakshya01/orange-crow/internal/catalog"
	"github.com/dibakshya01/orange-crow/internal/policy"
	"github.com/dibakshya01/orange-crow/internal/principal"
	"github.com/dibakshya01/orange-crow/internal/records"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// --- Tables (admin-only) --------------------------------------------------

func (s *Server) handleCreateTable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Name    string           `json:"name"`
		Columns []catalog.Column `json:"columns"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tbl, err := s.deps.Catalog.CreateTable(r.Context(), in.Name, in.Columns)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, tbl)
}

func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	names, err := s.deps.Catalog.ListTables(r.Context())
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"tables": names})
}

func (s *Server) handleDescribeTable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	table := chi.URLParam(r, "table")
	tbl, err := s.deps.Catalog.GetTable(r.Context(), table)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	pols, err := s.deps.Policy.List(r.Context(), table)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"name": tbl.Name, "columns": tbl.Columns, "created_at": tbl.CreatedAt, "policies": pols,
	})
}

func (s *Server) handleDropTable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.deps.Catalog.DropTable(r.Context(), chi.URLParam(r, "table")); err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Records (policy-enforced) --------------------------------------------

func (s *Server) handleInsert(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	table := chi.URLParam(r, "table")
	raw, ok := readBody(w, r)
	if !ok {
		return
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var many []map[string]any
		if json.Unmarshal(raw, &many) != nil {
			apierr.Write(w, r, badJSON())
			return
		}
		rows, err := s.deps.Records.InsertMany(r.Context(), p, table, many)
		if err != nil {
			apierr.Write(w, r, mapDomainError(err))
			return
		}
		writeJSON(w, r, http.StatusCreated, map[string]any{"records": rows})
		return
	}
	var one map[string]any
	if json.Unmarshal(raw, &one) != nil {
		apierr.Write(w, r, badJSON())
		return
	}
	row, err := s.deps.Records.Insert(r.Context(), p, table, one)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, row)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	opts, err := parseQueryOpts(r.URL.Query())
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	rows, err := s.deps.Records.Query(r.Context(), p, chi.URLParam(r, "table"), opts)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"records": rows})
}

func (s *Server) handleGetRecord(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	row, err := s.deps.Records.Get(r.Context(), p, chi.URLParam(r, "table"), chi.URLParam(r, "id"))
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, row)
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	var patch map[string]any
	if !decodeJSON(w, r, &patch) {
		return
	}
	row, err := s.deps.Records.Update(r.Context(), p, chi.URLParam(r, "table"), chi.URLParam(r, "id"), patch)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, row)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if err := s.deps.Records.Delete(r.Context(), p, chi.URLParam(r, "table"), chi.URLParam(r, "id")); err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Policies (admin-only) ------------------------------------------------

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in policy.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tbl, err := s.deps.Catalog.GetTable(r.Context(), in.Table)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	cols := make([]string, 0, len(tbl.Columns))
	for _, c := range tbl.Columns {
		cols = append(cols, c.Name)
	}
	pol, err := s.deps.Policy.Create(r.Context(), in, cols)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, pol)
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	pols, err := s.deps.Policy.List(r.Context(), chi.URLParam(r, "table"))
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"policies": pols})
}

// --- /meta (admin-only) ---------------------------------------------------

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	snap, err := s.deps.Meta.Build(r.Context())
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, snap)
}

// --- helpers --------------------------------------------------------------

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if principal.FromContext(r.Context()).IsAdmin() {
		return true
	}
	apierr.Write(w, r, apierr.New(http.StatusForbidden, "admin_required",
		"This operation requires project_admin.",
		"Present a valid admin API key as `Authorization: Bearer <key>`.",
		"/docs/auth#admin"))
	return false
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		apierr.Write(w, r, apierr.BadRequest("Could not read request body.", "Send a JSON body within the size limit."))
		return nil, false
	}
	return raw, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, ok := readBody(w, r)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		apierr.Write(w, r, badJSON())
		return false
	}
	return true
}

func badJSON() *apierr.Error {
	return apierr.BadRequest("The request body is not valid JSON for this endpoint.",
		"Send a JSON body matching the endpoint's schema (see GET /meta and the docs).")
}

// parseQueryOpts converts URL query params to records.QueryOpts. Reserved keys:
// select, order, limit, offset. Every other key is a filter col=op.value.
func parseQueryOpts(v url.Values) (records.QueryOpts, error) {
	var opts records.QueryOpts

	opts.Select = append(opts.Select, splitCSV(v.Get("select"))...)
	for _, term := range splitCSV(v.Get("order")) {
		col, dir, _ := strings.Cut(term, ".")
		opts.Order = append(opts.Order, records.OrderBy{Column: col, Desc: strings.EqualFold(dir, "desc")})
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return opts, filterErr("limit must be an integer")
		}
		opts.Limit = n
	}
	if s := v.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return opts, filterErr("offset must be an integer")
		}
		opts.Offset = n
	}

	reserved := map[string]bool{"select": true, "order": true, "limit": true, "offset": true}
	for key, vals := range v {
		if reserved[key] {
			continue
		}
		for _, raw := range vals {
			op, val, found := strings.Cut(raw, ".")
			if !found {
				return opts, filterErr("filter %q must be op.value (e.g. eq.5)", key)
			}
			if op == "in" {
				var list []any
				for _, item := range splitCSV(strings.Trim(val, "()")) {
					list = append(list, item)
				}
				opts.Filters = append(opts.Filters, records.Filter{Column: key, Op: "in", Value: list})
				continue
			}
			opts.Filters = append(opts.Filters, records.Filter{Column: key, Op: op, Value: val})
		}
	}
	return opts, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func filterErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", records.ErrInvalidFilter, fmt.Sprintf(format, args...))
}
