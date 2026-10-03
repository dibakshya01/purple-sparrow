package httpapi

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/functions"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

func (s *Server) mountFunctionRoutes(g chi.Router) {
	// Management (admin).
	g.Post("/v1/functions", s.handleCreateFunction)
	g.Get("/v1/functions", s.handleListFunctions)
	g.Get("/v1/functions/{slug}", s.handleGetFunction)
	g.Put("/v1/functions/{slug}/code", s.handleUploadFunctionCode)
	g.Delete("/v1/functions/{slug}", s.handleDeleteFunction)

	// Invocation (authorized per-function by invoke_roles).
	g.Post("/v1/fn/{slug}", s.handleInvokeFunction)
	g.Get("/v1/fn/{slug}", s.handleInvokeFunction)
}

func (s *Server) handleCreateFunction(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in functions.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	f, err := s.deps.Functions.Create(r.Context(), in)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusCreated, f)
}

func (s *Server) handleListFunctions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	fns, err := s.deps.Functions.List(r.Context())
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"functions": fns})
}

func (s *Server) handleGetFunction(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	f, err := s.deps.Functions.Get(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, f)
}

func (s *Server) handleUploadFunctionCode(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	// 32 MiB module cap mirrors functions.maxWasmBytes; MaxBytesReader stops abuse.
	body := http.MaxBytesReader(w, r.Body, 32<<20)
	f, err := s.deps.Functions.UploadCode(r.Context(), chi.URLParam(r, "slug"), body)
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	writeJSON(w, r, http.StatusOK, f)
}

func (s *Server) handleDeleteFunction(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.deps.Functions.Delete(r.Context(), chi.URLParam(r, "slug")); err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInvokeFunction(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	raw, ok := readBody(w, r)
	if !ok {
		return
	}
	out, err := s.deps.Functions.Invoke(r.Context(), p, chi.URLParam(r, "slug"), raw,
		functions.InvokeMeta{Method: r.Method, Query: r.URL.RawQuery})
	if err != nil {
		apierr.Write(w, r, mapDomainError(err))
		return
	}
	if out.ExitCode != 0 {
		// The function's own code failed. Log its stderr with the request id; return
		// a generic error so a function's internal output never leaks to the caller.
		apierr.Write(w, r, apierr.New(http.StatusBadGateway, "function_error",
			fmt.Sprintf("The function exited with a non-zero status (%d).", out.ExitCode),
			"Check the function's logs (server-side, by request_id) and its code.",
			"/docs/functions").WithInternal(fmt.Errorf("fn exit %d; stderr: %s", out.ExitCode, truncate(out.Stderr, 2048))))
		return
	}
	w.Header().Set("Content-Type", sniffContentType(out.Stdout))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Stdout)
}

func sniffContentType(b []byte) string {
	t := bytes.TrimSpace(b)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		return "application/json; charset=utf-8"
	}
	return "text/plain; charset=utf-8"
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
