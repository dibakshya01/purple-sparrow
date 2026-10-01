package mcp

import (
	"context"
	"net/http"
)

func (s *Server) add(name, desc string, schema map[string]any, handle func(context.Context, map[string]any) (string, bool, error)) {
	s.tools[name] = toolDef{name: name, description: desc, schema: schema, handle: handle}
	s.order = append(s.order, name)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func (s *Server) get(ctx context.Context, path string) (string, bool, error) {
	body, status, err := s.be.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", false, err
	}
	return body, httpIsError(status), nil
}

func (s *Server) post(ctx context.Context, path string, payload any) (string, bool, error) {
	body, status, err := s.be.call(ctx, http.MethodPost, path, payload)
	if err != nil {
		return "", false, err
	}
	return body, httpIsError(status), nil
}

func (s *Server) registerTools() {
	s.add("fetch_docs",
		"Fetch Orange Crow documentation. Omit slug to list docs; pass a slug (e.g. 'policies', 'errors', 'auth', 'getting-started') to read one.",
		obj(map[string]any{"slug": map[string]any{"type": "string"}}),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			if slug := argStr(a, "slug"); slug != "" {
				return s.get(ctx, "/docs/"+escapePath(slug))
			}
			return s.get(ctx, "/docs")
		})

	s.add("get_meta",
		"Return the whole backend shape in one call: tables, columns, policies, row counts.",
		obj(nil),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			return s.get(ctx, "/meta")
		})

	s.add("list_tables", "List user table names.", obj(nil),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			return s.get(ctx, "/v1/tables")
		})

	s.add("create_table",
		"Create a user table. columns is an array of {name, type, nullable?, unique?}; types: text, integer, real, boolean, timestamp, uuid, json. id and created_at are added automatically.",
		obj(map[string]any{
			"name":    map[string]any{"type": "string"},
			"columns": map[string]any{"type": "array"},
		}, "name", "columns"),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			return s.post(ctx, "/v1/tables", map[string]any{"name": a["name"], "columns": a["columns"]})
		})

	s.add("query_records",
		"Query rows from a table (policy-enforced). Optional limit (default 50).",
		obj(map[string]any{
			"table": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		}, "table"),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			limit := argInt(a, "limit", 50)
			return s.get(ctx, errMsg("/v1/tables/%s/records?limit=%d", escapePath(argStr(a, "table")), limit))
		})

	s.add("insert_record",
		"Insert a record into a table (policy-checked). values is an object of column->value.",
		obj(map[string]any{
			"table":  map[string]any{"type": "string"},
			"values": map[string]any{"type": "object"},
		}, "table", "values"),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			return s.post(ctx, "/v1/tables/"+escapePath(argStr(a, "table"))+"/records", a["values"])
		})

	s.add("create_policy",
		"Create an access policy. action: select|insert|update|delete; roles: subset of anon,authenticated,project_admin; using (rows) and/or check (new values) are expressions.",
		obj(map[string]any{
			"table":  map[string]any{"type": "string"},
			"action": map[string]any{"type": "string"},
			"roles":  map[string]any{"type": "array"},
			"using":  map[string]any{"type": "string"},
			"check":  map[string]any{"type": "string"},
		}, "table", "action", "roles"),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			return s.post(ctx, "/v1/policies", a)
		})

	s.add("run_advisor",
		"Run the configuration advisor; returns findings (e.g. tables without policies) with remediation.",
		obj(nil),
		func(ctx context.Context, a map[string]any) (string, bool, error) {
			return s.get(ctx, "/advisor")
		})
}
