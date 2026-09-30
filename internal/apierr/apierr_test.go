package apierr

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dibakshya01/purple-sparrow/internal/reqid"
)

func TestWriteProducesEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/missing", nil)
	req = req.WithContext(reqid.WithContext(req.Context(), "req-123"))

	Write(rec, req, NotFound(""))

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body is not valid envelope JSON: %v", err)
	}
	if env.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", env.Error.Code)
	}
	if env.Error.Remediation == "" {
		t.Error("remediation should be populated for agent self-correction")
	}
	if env.Error.RequestID != "req-123" {
		t.Errorf("request_id = %q, want req-123", env.Error.RequestID)
	}
}

func TestWriteNeverLeaksInternalCause(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/boom", nil)

	secret := "postgres://user:hunter2@db.internal/prod"
	Write(rec, req, Internal("").WithInternal(errors.New(secret)))

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("internal cause leaked into client response body")
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatal("internal secret leaked into client response body")
	}
}

func TestWriteWrapsUnknownErrorsAs500(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)

	Write(rec, req, errors.New("some raw non-API error"))

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "some raw non-API error") {
		t.Fatal("raw error text leaked to client")
	}
}
