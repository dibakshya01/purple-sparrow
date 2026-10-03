// Package httpapi builds the HTTP surface: router, baseline middleware, the M0
// health/metadata endpoints, and (when data services are wired) the M1 tables,
// records, policies, and /meta endpoints.
package httpapi

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/agent/advisor"
	agentdocs "github.com/dibakshya01/purple-sparrow/internal/agent/docs"
	"github.com/dibakshya01/purple-sparrow/internal/agent/memory"
	"github.com/dibakshya01/purple-sparrow/internal/agent/meta"
	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/auth"
	"github.com/dibakshya01/purple-sparrow/internal/catalog"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/functions"
	"github.com/dibakshya01/purple-sparrow/internal/policy"
	"github.com/dibakshya01/purple-sparrow/internal/records"
	"github.com/dibakshya01/purple-sparrow/internal/reqid"
	"github.com/dibakshya01/purple-sparrow/internal/storage"
	"github.com/dibakshya01/purple-sparrow/internal/web"
)

// Deps are the service dependencies for the data plane. When Catalog is nil the
// data routes are not mounted (M0-only server, e.g. in tests).
type Deps struct {
	Catalog   *catalog.Service
	Records   *records.Service
	Policy    *policy.Service
	Meta      *meta.Service
	Auth      *auth.Service
	Docs      *agentdocs.Service
	Memory    *memory.Service
	Advisor   *advisor.Service
	Storage   *storage.Service
	Functions *functions.Service
}

// Server owns the HTTP handler and its request-scoped dependencies.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	deps   Deps
	ready  atomic.Bool
	router http.Handler
}

// New constructs a Server and builds its router.
func New(cfg config.Config, logger *slog.Logger, deps Deps) *Server {
	s := &Server{cfg: cfg, logger: logger, deps: deps}
	s.router = s.buildRouter()
	return s
}

// Handler returns the composed HTTP handler.
func (s *Server) Handler() http.Handler { return s.router }

// SetReady flips the readiness flag reported by /readyz.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()

	// request-id outermost; recovery directly inside so any later panic still
	// yields an envelope with the id; access-log wraps the handler; security
	// headers innermost.
	r.Use(reqid.Middleware)
	r.Use(recoverer(s.logger))
	r.Use(accessLog(s.logger))
	r.Use(securityHeaders)

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		apierr.Write(w, req, apierr.NotFound(""))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		apierr.Write(w, req, apierr.MethodNotAllowed(""))
	})

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/v1", s.handleServiceInfo)

	if s.deps.Auth != nil {
		// JWKS is public (no principal needed) so third parties can verify tokens.
		r.Get("/.well-known/jwks.json", s.handleJWKS)
	}
	if s.deps.Docs != nil {
		// Docs are public, read-only, embedded content.
		r.Get("/docs", s.handleDocsIndex)
		r.Get("/docs/{slug}", s.handleDocGet)
	}

	if s.deps.Catalog != nil {
		r.Group(func(g chi.Router) {
			g.Use(principalMiddleware(s.deps.Auth))
			s.mountDataRoutes(g)
			s.mountAuthRoutes(g)
			s.mountAgentRoutes(g)
			if s.deps.Storage != nil {
				s.mountStorageRoutes(g)
			}
			if s.deps.Functions != nil {
				s.mountFunctionRoutes(g)
			}
		})
	}

	// Dashboard placeholder at exactly "/" only; other unmatched paths -> envelope.
	r.Get("/", web.Index().ServeHTTP)

	return r
}

func (s *Server) mountDataRoutes(g chi.Router) {
	g.Post("/v1/tables", s.handleCreateTable)
	g.Get("/v1/tables", s.handleListTables)
	g.Get("/v1/tables/{table}", s.handleDescribeTable)
	g.Delete("/v1/tables/{table}", s.handleDropTable)

	g.Post("/v1/tables/{table}/records", s.handleInsert)
	g.Get("/v1/tables/{table}/records", s.handleQuery)
	g.Get("/v1/tables/{table}/records/{id}", s.handleGetRecord)
	g.Patch("/v1/tables/{table}/records/{id}", s.handleUpdateRecord)
	g.Delete("/v1/tables/{table}/records/{id}", s.handleDeleteRecord)

	g.Post("/v1/policies", s.handleCreatePolicy)
	g.Get("/v1/tables/{table}/policies", s.handleListPolicies)

	g.Get("/meta", s.handleMeta)
}
