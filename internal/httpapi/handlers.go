package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/dibakshya01/orange-crow/internal/apierr"
	"github.com/dibakshya01/orange-crow/internal/buildinfo"
)

// writeJSON writes v as JSON with the given status. On encode failure it falls
// back to the error envelope.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		apierr.Write(w, r, apierr.Internal("").WithInternal(err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		apierr.Write(w, r, apierr.ServiceUnavailable("The service is starting and not yet ready."))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleServiceInfo(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	writeJSON(w, r, http.StatusOK, map[string]any{
		"name":        info.Name,
		"version":     info.Version,
		"commit":      info.Commit,
		"description": "Agent-native backend platform",
		"tier":        string(s.cfg.EffectiveTier()),
		"docs":        "/docs",
	})
}
