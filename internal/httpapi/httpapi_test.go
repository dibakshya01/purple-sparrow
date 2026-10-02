package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/apierr"
	"github.com/dibakshya01/purple-sparrow/internal/config"
	"github.com/dibakshya01/purple-sparrow/internal/reqid"
)

func testServer() *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Defaults(), logger, Deps{})
}

func TestHealthz(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want ok", body["status"])
	}
}

func TestReadyzReflectsReadiness(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want 503", rec.Code)
	}

	s.SetReady(true)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", rec.Code)
	}
}

func TestNotFoundReturnsEnvelopeWithRequestID(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/definitely-not-a-route", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec.Header().Get(reqid.Header) == "" {
		t.Error("X-Request-Id header should be set on every response")
	}
	var env apierr.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("404 body is not an envelope: %v", err)
	}
	if env.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", env.Error.Code)
	}
}

func TestMethodNotAllowedReturnsEnvelope(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	var env apierr.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("405 body is not an envelope: %v", err)
	}
	if env.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", env.Error.Code)
	}
}

func TestSecurityHeadersAndRequestIDOn2xx(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if rec.Header().Get(reqid.Header) == "" {
		t.Error("X-Request-Id should be present on 2xx responses too")
	}
}

func TestPanicIsRecoveredAsEnvelope(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Compose the same middleware chain around a handler that panics.
	panicky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	})
	h := reqid.Middleware(recoverer(logger)(panicky))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env apierr.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("panic body is not an envelope: %v", err)
	}
	if env.Error.Code != "internal" {
		t.Errorf("code = %q, want internal", env.Error.Code)
	}
	if rec.Body.String() != "" && contains(rec.Body.String(), "kaboom") {
		t.Fatal("panic value leaked into response body")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
