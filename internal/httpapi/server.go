// Package httpapi builds the HTTP surface: router, baseline middleware, and the
// M0 health/metadata endpoints. Product routes (data, auth, ...) are mounted by
// later milestones through this same server.
package httpapi

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/reqid"
	"github.com/dibakshya01/purple-sparrow/internal/web"
)

// Server owns the HTTP handler and its request-scoped dependencies.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	ready  atomic.Bool
	router http.Handler
}

// New constructs a Server and builds its router.
func New(cfg config.Config, logger *slog.Logger) *Server {
	s := &Server{cfg: cfg, logger: logger}
	s.router = s.buildRouter()
	return s
}

// Handler returns the composed HTTP handler.
func (s *Server) Handler() http.Handler { return s.router }

// SetReady flips the readiness flag reported by /readyz.
func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()

	// Order matters. request-id is outermost so every inner layer and the error
	// envelope can reference it. recovery sits directly inside it so a panic in
	// ANY later middleware or handler still yields an envelope carrying the
	// request id. access-log wraps the handler to capture its status.
	// security-headers is innermost so headers are set on the ResponseWriter
	// before the handler (or a recovered panic response) writes.
	r.Use(reqid.Middleware)
	r.Use(recoverer(s.logger))
	r.Use(accessLog(s.logger))
	r.Use(securityHeaders)

	// Consistent envelope for framework-level 404/405 instead of chi defaults.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		apierr.Write(w, req, apierr.NotFound(""))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		apierr.Write(w, req, apierr.MethodNotAllowed(""))
	})

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/v1", s.handleServiceInfo)

	// Dashboard placeholder at exactly "/" only. Every other unmatched path falls
	// through to NotFound above and returns the error envelope — the file server
	// must never shadow API routes with a plain-text 404.
	r.Get("/", web.Index().ServeHTTP)

	return r
}
