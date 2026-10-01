// Package apierr defines Orange Crow's agent-native error envelope (ADR-0006).
//
// Every non-2xx response across the whole service is written through this
// package so agents always receive a machine-readable, self-correcting error:
// a stable code, a human message, a remediation hint, concrete next actions, and
// a docs reference. Handlers never write bare errors.
package apierr

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dibakshya01/orange-crow/internal/reqid"
)

// Detail is the body of an error response.
type Detail struct {
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation,omitempty"`
	NextActions []string `json:"next_actions,omitempty"`
	DocRef      string   `json:"doc_ref,omitempty"`
	RequestID   string   `json:"request_id,omitempty"`
}

// Envelope is the top-level JSON shape: {"error": {...}}.
type Envelope struct {
	Error Detail `json:"error"`
}

// Error is a typed API error carrying an HTTP status. It implements error so it
// can flow through normal Go error handling and be unwrapped from wrapped errors.
type Error struct {
	Status      int
	Code        string
	Message     string
	Remediation string
	NextActions []string
	DocRef      string
	// Internal is an optional underlying cause; it is logged but NEVER sent to
	// the client, so internal details cannot leak through the envelope.
	Internal error
}

func (e *Error) Error() string {
	if e.Internal != nil {
		return e.Code + ": " + e.Message + ": " + e.Internal.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Internal }

// New constructs an Error. next may be nil.
func New(status int, code, message, remediation, docRef string, next ...string) *Error {
	return &Error{
		Status:      status,
		Code:        code,
		Message:     message,
		Remediation: remediation,
		DocRef:      docRef,
		NextActions: next,
	}
}

// WithInternal attaches an underlying cause (logged, never serialized).
func (e *Error) WithInternal(err error) *Error {
	e.Internal = err
	return e
}

// Write serializes err as the envelope with the correct status. Any error is
// accepted: a *Error is used as-is; anything else becomes a generic 500 so no
// raw error text ever reaches the client. The request id is pulled from context.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = Internal("").WithInternal(err)
	}

	if apiErr.Internal != nil {
		slog.ErrorContext(r.Context(), "request failed",
			"code", apiErr.Code,
			"status", apiErr.Status,
			"request_id", reqid.FromContext(r.Context()),
			"error", apiErr.Internal.Error(),
		)
	}

	detail := Detail{
		Code:        apiErr.Code,
		Message:     apiErr.Message,
		Remediation: apiErr.Remediation,
		NextActions: apiErr.NextActions,
		DocRef:      apiErr.DocRef,
		RequestID:   reqid.FromContext(r.Context()),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(apiErr.Status)
	_ = json.NewEncoder(w).Encode(Envelope{Error: detail})
}

// --- Common constructors -------------------------------------------------

// NotFound is returned when a route or resource does not exist.
func NotFound(message string) *Error {
	if message == "" {
		message = "The requested resource was not found."
	}
	return New(http.StatusNotFound, "not_found", message,
		"Check the path and method. Call GET /v1 to list available routes, or GET /meta once data exists.",
		"/docs/errors#not_found")
}

// MethodNotAllowed is returned when the route exists but the method does not.
func MethodNotAllowed(message string) *Error {
	if message == "" {
		message = "That method is not allowed on this route."
	}
	return New(http.StatusMethodNotAllowed, "method_not_allowed", message,
		"Use one of the methods listed in the Allow header for this route.",
		"/docs/errors#method_not_allowed")
}

// BadRequest is returned for malformed or invalid input.
func BadRequest(message, remediation string) *Error {
	if message == "" {
		message = "The request was invalid."
	}
	return New(http.StatusBadRequest, "bad_request", message, remediation,
		"/docs/errors#bad_request")
}

// Internal is returned for unexpected server errors; the message is deliberately
// generic and never includes internal detail.
func Internal(message string) *Error {
	if message == "" {
		message = "An unexpected internal error occurred."
	}
	return New(http.StatusInternalServerError, "internal", message,
		"This is a server-side error. Retry; if it persists, check server logs using the request_id.",
		"/docs/errors#internal")
}

// ServiceUnavailable is returned when the server is not ready to serve.
func ServiceUnavailable(message string) *Error {
	if message == "" {
		message = "The service is not ready."
	}
	return New(http.StatusServiceUnavailable, "unavailable", message,
		"Wait and retry. Check GET /readyz for readiness state.",
		"/docs/errors#unavailable")
}
