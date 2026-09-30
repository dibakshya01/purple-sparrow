package reqid

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareGeneratesIDWhenAbsent(t *testing.T) {
	var seen string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if seen == "" {
		t.Fatal("expected a generated request id in context")
	}
	if rec.Header().Get(Header) != seen {
		t.Error("response header should echo the context request id")
	}
}

func TestMiddlewareReusesSafeInboundID(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(Header, "trace-abc_123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get(Header) != "trace-abc_123" {
		t.Errorf("safe inbound id should be reused, got %q", rec.Header().Get(Header))
	}
}

func TestMiddlewareRejectsUnsafeInboundID(t *testing.T) {
	cases := []string{
		"has space",
		"inject\nnewline",
		"semi;colon",
		strings.Repeat("x", 65), // too long
	}
	for _, bad := range cases {
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(Header, bad)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		got := rec.Header().Get(Header)
		if got == bad {
			t.Errorf("unsafe inbound id %q should have been replaced", bad)
		}
		if !safe(got) {
			t.Errorf("replacement id %q is itself unsafe", got)
		}
	}
}
