// Package httpx is the HTTP layer every service in the platform shares:
// timeouts, request identity, panic recovery, JSON responses, uniform errors,
// health/readiness probes, metrics and graceful shutdown.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrorBody is the single error shape every endpoint returns. Clients can
// branch on Code and show Message without parsing prose.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries the machine-readable and human-readable parts.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// Canonical error codes. Clients switch on these; the strings are part of the
// API contract.
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeTooLarge     = "payload_too_large"
	CodeRateLimited  = "rate_limited"
	CodeInternal     = "internal_error"
	CodeUnavailable  = "service_unavailable"
	CodeTimeout      = "timeout"
)

// APIError is an error that carries an HTTP status and a stable code. Handlers
// return it; the middleware-free helpers turn it into a response.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]string
	cause   error
}

func (e *APIError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

// Unwrap exposes the cause for errors.Is/As.
func (e *APIError) Unwrap() error { return e.cause }

// WithCause attaches an internal error for logging. The cause is never sent to
// the client.
func (e *APIError) WithCause(err error) *APIError {
	clone := *e
	clone.cause = err
	return &clone
}

// Constructors for the common failures.

func BadRequest(msg string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: msg}
}

func Unauthorized(msg string) *APIError {
	return &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: msg}
}

func Forbidden(msg string) *APIError {
	return &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: msg}
}

func NotFound(msg string) *APIError {
	return &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: msg}
}

func Conflict(msg string) *APIError {
	return &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: msg}
}

func Internal(msg string) *APIError {
	return &APIError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: msg}
}

func Unavailable(msg string) *APIError {
	return &APIError{Status: http.StatusServiceUnavailable, Code: CodeUnavailable, Message: msg}
}

func Timeout(msg string) *APIError {
	return &APIError{Status: http.StatusGatewayTimeout, Code: CodeTimeout, Message: msg}
}

// JSON writes v as the response body with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		// The status line is already written, so the only useful action is to
		// stop; the client will see a truncated body and fail its own decode.
		return
	}
}

// Error writes err as a uniform error body. A non-*APIError becomes a 500 with
// a generic message: internal details never reach a client.
func Error(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = Internal("an internal error occurred").WithCause(err)
	}
	JSON(w, apiErr.Status, ErrorBody{Error: ErrorDetail{
		Code:    apiErr.Code,
		Message: apiErr.Message,
		Details: apiErr.Details,
	}})
}

// DecodeJSON reads a JSON request body with a size limit and rejects unknown
// fields, so a typo in a client payload is an error rather than a silent
// default.
func DecodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	if limit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return BadRequest("request body is too large").WithCause(err)
		}
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return BadRequest(fmt.Sprintf("malformed JSON at byte %d", syn.Offset)).WithCause(err)
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return BadRequest(fmt.Sprintf("field %q expects %s", typeErr.Field, typeErr.Type)).WithCause(err)
		}
		return BadRequest("request body could not be decoded").WithCause(err)
	}
	return nil
}

// NoCacheMiddleware applies NoCache to every response.
func NoCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		NoCache(w)
		next.ServeHTTP(w, r)
	})
}

// NoCache sets headers that keep an API response out of every proxy and cache
// between the client and the operator's infrastructure.
func NoCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// SecurityHeaders sets conservative defaults for an API that a browser may
// touch. The platform's own UI is a separate deployment; this is belt and
// braces.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Deadline wraps a handler with a request deadline. The response writer is
// guarded so a late write after the deadline becomes a no-op instead of a
// corrupt body with a 200 status.
func Deadline(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(newGuardedWriter(ctx, w), r.WithContext(ctx))
		})
	}
}

// Chain applies middleware left to right, so Chain(a, b, h) runs a, then b,
// then h — the same reading order as the declaration.
func Chain(h http.Handler, middleware ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
