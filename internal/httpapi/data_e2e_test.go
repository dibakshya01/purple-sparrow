package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
)

const testAdminKey = "ps_sk_test-key"

func dataServer(t *testing.T) *Server {
	t.Helper()
	eng, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := migrate.Run(context.Background(), eng); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(eng)
	pol := policy.NewService(eng)
	enf := policy.NewEnforcer(eng)
	authSvc, err := auth.NewService(context.Background(), eng)
	if err != nil {
		t.Fatal(err)
	}
	if err := authSvc.SeedAdminKey(context.Background(), testAdminKey); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Defaults(), logger, Deps{
		Catalog: cat, Records: records.New(eng, cat, enf), Policy: pol,
		Meta: meta.New(eng, cat, pol), Auth: authSvc,
	})
}

func do(t *testing.T, s *Server, method, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad json (%d): %s", rec.Code, rec.Body.String())
	}
	return m
}

func TestE2E_TableRecordsPolicyMeta(t *testing.T) {
	s := dataServer(t)

	// Create table — admin required.
	if rec := do(t, s, "POST", "/v1/tables", "", `{"name":"todos","columns":[{"name":"title","type":"text"},{"name":"done","type":"boolean"}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("anon create table: want 403, got %d", rec.Code)
	}
	if rec := do(t, s, "POST", "/v1/tables", testAdminKey, `{"name":"todos","columns":[{"name":"title","type":"text"},{"name":"done","type":"boolean"}]}`); rec.Code != http.StatusCreated {
		t.Fatalf("admin create table: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Insert as admin (bypass), with a boolean.
	rec := do(t, s, "POST", "/v1/tables/todos/records", testAdminKey, `{"title":"ship it","done":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("insert: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decode(t, rec)
	if created["done"] != true {
		t.Fatalf("boolean should round-trip as JSON true, got %#v", created["done"])
	}

	// Anon read denied (deny-by-default).
	rec = do(t, s, "GET", "/v1/tables/todos/records", "", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anon read without policy: want 403, got %d", rec.Code)
	}
	if decode(t, rec)["error"].(map[string]any)["code"] != "policy_denied" {
		t.Fatalf("expected policy_denied envelope, got %s", rec.Body.String())
	}

	// Grant anon read-all.
	if rec := do(t, s, "POST", "/v1/policies", testAdminKey, `{"table":"todos","action":"select","roles":["anon"],"using":"true"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create policy: want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, s, "GET", "/v1/tables/todos/records", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("anon read with policy: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	recs := decode(t, rec)["records"].([]any)
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}

	// /meta admin-gated.
	if rec := do(t, s, "GET", "/meta", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anon /meta: want 403, got %d", rec.Code)
	}
	rec = do(t, s, "GET", "/meta", testAdminKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin /meta: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if tables := decode(t, rec)["tables"].([]any); len(tables) != 1 {
		t.Fatalf("meta should list 1 table, got %d", len(tables))
	}

	// Bad credential -> 401.
	if rec := do(t, s, "GET", "/meta", "wrong-key", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad key: want 401, got %d", rec.Code)
	}
}
