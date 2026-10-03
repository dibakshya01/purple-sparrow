package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/events"
	"github.com/dibakshya01/purple-sparrow/internal/principal"
)

// handleRealtime streams row-change events to the client over Server-Sent Events.
// Each event is authorized per-subscriber via the policy engine, so a client only
// receives changes for rows it could read with GET. Optional ?table= narrows to
// one table.
func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apierr.Write(w, r, apierr.Internal("streaming is not supported by this server"))
		return
	}
	p := principal.FromContext(r.Context())
	tableFilter := r.URL.Query().Get("table")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	w.WriteHeader(http.StatusOK)

	ch, cancel := s.deps.Bus.Subscribe()
	defer cancel()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	colsCache := map[string][]string{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e, open := <-ch:
			if !open {
				return
			}
			if tableFilter != "" && e.Table != tableFilter {
				continue
			}
			if !s.eventVisible(ctx, p, e, colsCache) {
				continue
			}
			payload, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// eventVisible reports whether principal p may receive event e, by evaluating the
// table's SELECT policies against the event's row. Admin always; a delete without
// a captured row is delivered only to admin (we cannot verify row visibility, so
// we fail closed and never leak that a row existed).
func (s *Server) eventVisible(ctx context.Context, p principal.Principal, e events.Event, colsCache map[string][]string) bool {
	if p.IsAdmin() {
		return true
	}
	if e.Row == nil {
		return false
	}
	cols, ok := colsCache[e.Table]
	if !ok {
		cs, err := s.deps.Catalog.Columns(ctx, e.Table)
		if err != nil {
			colsCache[e.Table] = nil
			return false
		}
		for _, c := range cs {
			cols = append(cols, c.Name)
		}
		colsCache[e.Table] = cols
	}
	vis, err := s.deps.Enforcer.Visible(ctx, s.deps.Engine, p, e.Table, cols, e.Row)
	return err == nil && vis
}
