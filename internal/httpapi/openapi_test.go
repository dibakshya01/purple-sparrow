package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/agent/advisor"
	agentdocs "github.com/dibakshya01/purple-sparrow/internal/agent/docs"
	"github.com/dibakshya01/purple-sparrow/internal/agent/memory"
	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/blob"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/data"
	"github.com/dibakshya01/purple-sparrow/internal/data/migrate"
	"github.com/dibakshya01/purple-sparrow/internal/events"
	"github.com/dibakshya01/purple-sparrow/internal/functions"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
	"github.com/dibakshya01/purple-sparrow/internal/storage"
	"github.com/dibakshya01/purple-sparrow/openapi"
)

// fullServer wires every service so all routes are mounted.
func fullServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	eng, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := migrate.Run(ctx, eng); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(eng)
	pol := policy.NewService(eng)
	enf := policy.NewEnforcer(eng)
	rec := records.New(eng, cat, enf)
	bus := events.NewHub()
	rec.SetPublisher(bus)
	authSvc, err := auth.NewService(ctx, eng)
	if err != nil {
		t.Fatal(err)
	}
	store, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stg, err := storage.New(ctx, eng, store)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := functions.NewWazero(ctx, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close(ctx) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Defaults(), logger, Deps{
		Catalog: cat, Records: rec, Policy: pol, Meta: meta.New(eng, cat, pol),
		Auth: authSvc, Docs: agentdocs.New(), Memory: memory.New(eng),
		Advisor: advisor.New(cat, pol), Storage: stg,
		Functions: functions.New(eng, store, runner),
		Bus:       bus, Enforcer: enf, Engine: eng,
	})
}

var specPathRe = regexp.MustCompile(`(?m)^  (/[^:\n]+):`)

// specPaths returns the set of path templates declared in the OpenAPI document.
func specPaths(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, m := range specPathRe.FindAllStringSubmatch(string(openapi.Spec), -1) {
		out[strings.TrimSpace(m[1])] = true
	}
	if len(out) < 20 {
		t.Fatalf("spec parsed only %d paths — regex or spec is wrong", len(out))
	}
	return out
}

// TestOpenAPIContractParity walks every route the server registers and asserts it
// is documented in openapi.yaml — so the contract can never silently drift from
// the code (ADR-0004). A new endpoint without a spec entry fails this test.
func TestOpenAPIContractParity(t *testing.T) {
	spec := specPaths(t)
	s := fullServer(t)
	mux, ok := s.Handler().(chi.Routes)
	if !ok {
		t.Fatal("handler is not a chi.Routes")
	}
	var missing []string
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch method {
		case http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace:
			return nil
		}
		if route == "/" { // the dashboard document, not an API endpoint
			return nil
		}
		p := normalizeRoute(route)
		if !spec[p] {
			missing = append(missing, method+" "+route+" (normalized "+p+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("routes not documented in openapi.yaml:\n  %s", strings.Join(missing, "\n  "))
	}
}

// normalizeRoute maps a chi route template to its OpenAPI path template: a
// trailing wildcard `/*` becomes the `{key}` path parameter used in the spec.
func normalizeRoute(route string) string {
	if strings.HasSuffix(route, "/*") {
		return strings.TrimSuffix(route, "/*") + "/{key}"
	}
	return route
}

// TestOpenAPICoversEveryFeatureArea is a cheap guard that the spec didn't regress
// to a stub: a representative endpoint from each area must be present.
func TestOpenAPICoversEveryFeatureArea(t *testing.T) {
	spec := specPaths(t)
	for _, p := range []string{
		"/v1/tables", "/v1/tables/{table}/records", "/v1/policies", "/meta",
		"/v1/auth/login", "/v1/storage/buckets", "/v1/storage/{bucket}/{key}",
		"/v1/functions", "/v1/fn/{slug}", "/v1/realtime", "/v1/memory/{namespace}/{key}",
		"/openapi.yaml",
	} {
		if !spec[p] {
			t.Errorf("OpenAPI spec is missing %s", p)
		}
	}
}
