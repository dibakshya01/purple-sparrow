// Package reqid provides request-id generation, context propagation, and an
// HTTP middleware. It has no dependencies on other internal packages so both the
// error layer and the HTTP layer can use it without import cycles.
package reqid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// Header is the canonical request-id header name.
const Header = "X-Request-Id"

type ctxKey struct{}

// New returns a random 16-byte hex request id.
func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read never fails on supported platforms; fall back to a constant
		// marker rather than panicking in a request path.
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// WithContext returns a copy of ctx carrying the request id.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request id, or "" if none is set.
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// maxInboundLen bounds an accepted inbound request id.
const maxInboundLen = 64

// safe reports whether an inbound id is safe to echo and log: non-empty, bounded,
// and limited to an unambiguous character set. This prevents log-injection and
// header-smuggling via an attacker-controlled X-Request-Id; anything else is
// replaced with a freshly minted id.
func safe(id string) bool {
	if id == "" || len(id) > maxInboundLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// Middleware ensures every request has an id: it reuses a *validated* inbound
// value from the header when safe, otherwise generates one, stores it in the
// context, and echoes it back on the response.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if !safe(id) {
			id = New()
		}
		w.Header().Set(Header, id)
		ctx := WithContext(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
