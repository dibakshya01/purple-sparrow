package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/agent/memory"
	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// --- Docs (public) --------------------------------------------------------

func (s *Server) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]any{"docs": s.deps.Docs.Slugs()})
}

func (s *Server) handleDocGet(w http.ResponseWriter, r *http.Request) {
	content, err := s.deps.Docs.Get(chi.URLParam(r, "slug"))
	if err != nil {
		apierr.Write(w, r, apierr.NotFound("No such doc. GET /docs lists available slugs."))
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content))
}

// --- Memory (requires an identity) ----------------------------------------

func (s *Server) mountAgentRoutes(g chi.Router) {
	g.Put("/v1/memory/{namespace}/{key}", s.handleMemorySet)
	g.Get("/v1/memory/{namespace}/{key}", s.handleMemoryGet)
	g.Delete("/v1/memory/{namespace}/{key}", s.handleMemoryDelete)
	g.Get("/v1/memory/{namespace}", s.handleMemoryList)
	g.Get("/advisor", s.handleAdvisor)
}

// memorySubject resolves the storage subject. Admin uses a reserved subject;
// authenticated users use their own; anon is refused (memory needs identity).
func (s *Server) memorySubject(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := principal.FromContext(r.Context())
	if p.IsAdmin() {
		return "__admin__", true
	}
	if p.Subject != "" {
		return p.Subject, true
	}
	apierr.Write(w, r, apierr.New(http.StatusForbidden, "identity_required",
		"Agent memory requires an authenticated identity.",
		"Authenticate (a user token or an API key with a subject) before using /v1/memory.",
		"/docs/auth"))
	return "", false
}

func (s *Server) handleMemorySet(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.memorySubject(w, r)
	if !ok {
		return
	}
	var value any
	if !decodeJSON(w, r, &value) {
		return
	}
	if err := s.deps.Memory.Set(r.Context(), subject, chi.URLParam(r, "namespace"), chi.URLParam(r, "key"), value); err != nil {
		apierr.Write(w, r, mapMemoryError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleMemoryGet(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.memorySubject(w, r)
	if !ok {
		return
	}
	entry, err := s.deps.Memory.Get(r.Context(), subject, chi.URLParam(r, "namespace"), chi.URLParam(r, "key"))
	if err != nil {
		apierr.Write(w, r, mapMemoryError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, entry)
}

func (s *Server) handleMemoryList(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.memorySubject(w, r)
	if !ok {
		return
	}
	entries, err := s.deps.Memory.List(r.Context(), subject, chi.URLParam(r, "namespace"))
	if err != nil {
		apierr.Write(w, r, mapMemoryError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	subject, ok := s.memorySubject(w, r)
	if !ok {
		return
	}
	if err := s.deps.Memory.Delete(r.Context(), subject, chi.URLParam(r, "namespace"), chi.URLParam(r, "key")); err != nil {
		apierr.Write(w, r, mapMemoryError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Advisor (admin-only) -------------------------------------------------

func (s *Server) handleAdvisor(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	findings, err := s.deps.Advisor.Analyze(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.Internal("").WithInternal(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"findings": findings})
}

func mapMemoryError(err error) *apierr.Error {
	switch {
	case errors.Is(err, memory.ErrNotFound):
		return apierr.New(http.StatusNotFound, "memory_not_found",
			"No value stored at that namespace/key for your identity.",
			"Set it first with PUT, or list the namespace with GET /v1/memory/{namespace}.",
			"/docs/getting-started")
	case errors.Is(err, memory.ErrInvalidName):
		return apierr.BadRequest("Invalid namespace or key.",
			"Namespace and key must be lowercase identifiers (letters, digits, underscore).")
	default:
		return apierr.Internal("").WithInternal(err)
	}
}
