package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
)

// rateLimiter is a per-client-IP token bucket (M10). It is dependency-free and
// in-process: fine for a single binary and each node of a horizontally-scaled
// deployment. A background janitor evicts idle buckets so memory stays bounded.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rps     float64
	burst   float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rps, burst int) *rateLimiter {
	l := &rateLimiter{buckets: make(map[string]*bucket), rps: float64(rps), burst: float64(burst)}
	go l.janitor()
	return l
}

func (l *rateLimiter) janitor() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// allow reports whether a request from key may proceed, consuming one token.
func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// rateLimit middleware rejects over-limit clients with 429 and a Retry-After. The
// client is keyed by RemoteAddr IP (never a spoofable forwarded header), so a
// hostile client cannot evade the limit by forging X-Forwarded-For.
func rateLimit(l *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(clientIP(r)) {
				w.Header().Set("Retry-After", "1")
				apierr.Write(w, r, apierr.New(http.StatusTooManyRequests, "rate_limited",
					"Too many requests.",
					"Slow down and retry after a moment. Raise PS_RATE_LIMIT_RPS on the server if this is legitimate traffic.",
					"/docs/errors#rate_limited"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string { return clientIPFromAddr(r.RemoteAddr) }

func clientIPFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
