package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hmza-hb/lead-intelligence/platform/config"
	"github.com/hmza-hb/lead-intelligence/platform/logging"
	"github.com/hmza-hb/lead-intelligence/platform/observe"
)

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestJSONWritesBody(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := decode[map[string]int](t, rec); got["n"] != 1 {
		t.Errorf("body = %v", got)
	}
}

func TestJSONEscapesHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusOK, map[string]string{"q": "<script>"})
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Errorf("JSON encoder did not escape HTML: %s", rec.Body.String())
	}
}

func TestErrorMapsAPIErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, NotFound("no such lead"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decode[ErrorBody](t, rec)
	if body.Error.Code != CodeNotFound || body.Error.Message != "no such lead" {
		t.Errorf("body = %+v", body.Error)
	}
}

func TestErrorHidesInternalDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, Internal("query failed").WithCause(errors.New("pq: password authentication failed for user postgres")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "password authentication") {
		t.Fatalf("internal detail leaked to the client: %s", rec.Body.String())
	}
}

func TestErrorWrapsPlainErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, errors.New("totally unexpected"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decode[ErrorBody](t, rec)
	if body.Error.Code != CodeInternal {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestErrorUnwrapsNestedAPIErrors(t *testing.T) {
	wrapped := errors.Join(errors.New("layer"), NotFound("gone"))
	rec := httptest.NewRecorder()
	Error(rec, wrapped)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want the wrapped API status", rec.Code)
	}
}

func TestAPIErrorFormatting(t *testing.T) {
	e := BadRequest("nope")
	if !strings.Contains(e.Error(), "nope") {
		t.Errorf("Error() = %q", e.Error())
	}
	cause := errors.New("root")
	if !errors.Is(e.WithCause(cause), cause) {
		t.Error("WithCause did not preserve the cause for errors.Is")
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"limit":5,"surprise":true}`))
	err := DecodeJSON(httptest.NewRecorder(), req, 1<<20, &struct {
		Limit int `json:"limit"`
	}{})
	if err == nil {
		t.Fatal("DecodeJSON accepted an unknown field")
	}
	if !strings.Contains(err.Error(), "surprise") && !strings.Contains(err.Error(), "could not be decoded") {
		t.Errorf("error should name the problem: %v", err)
	}
}

func TestDecodeJSONRejectsMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"limit":`))
	err := DecodeJSON(httptest.NewRecorder(), req, 1<<20, &map[string]any{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("err = %v, want a 400 APIError", err)
	}
}

func TestDecodeJSONEnforcesSizeLimit(t *testing.T) {
	big := strings.Repeat("a", 4096)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+big+`"}`))
	err := DecodeJSON(httptest.NewRecorder(), req, 100, &map[string]any{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an APIError", err)
	}
}

func TestDecodeJSONAcceptsValidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"limit":5}`))
	var dst struct {
		Limit int `json:"limit"`
	}
	if err := DecodeJSON(httptest.NewRecorder(), req, 1<<20, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Limit != 5 {
		t.Errorf("limit = %d", dst.Limit)
	}
}

func TestNoCacheAndSecurityHeaders(t *testing.T) {
	h := Chain(Handler(func(w http.ResponseWriter, _ *http.Request) {}), SecurityHeaders, NoCacheMiddleware)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	want := map[string]string{
		"Cache-Control":           "no-store, max-age=0",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestRequestIDIsMintedAndPropagated(t *testing.T) {
	var seen string
	h := Chain(Handler(func(w http.ResponseWriter, r *http.Request) {
		seen = FromRequest(r.Context())
	}), RequestID("X-Request-Id"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == "" {
		t.Fatal("handler saw no request ID")
	}
	if got := rec.Header().Get("X-Request-Id"); got != seen {
		t.Errorf("header %q != context %q", got, seen)
	}
}

func TestRequestIDHonoursInboundValue(t *testing.T) {
	var seen string
	h := Chain(Handler(func(_ http.ResponseWriter, r *http.Request) { seen = FromRequest(r.Context()) }), RequestID(""))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "caller-supplied-123")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "caller-supplied-123" {
		t.Errorf("request ID = %q, want the caller's value", seen)
	}
}

func TestRequestIDRejectsAbsurdInboundValue(t *testing.T) {
	var seen string
	h := Chain(Handler(func(_ http.ResponseWriter, r *http.Request) { seen = FromRequest(r.Context()) }), RequestID(""))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", strings.Repeat("x", 500))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == strings.Repeat("x", 500) {
		t.Error("an oversized inbound request ID was trusted")
	}
}

func TestRequestIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		id := newRequestID()
		if seen[id] {
			t.Fatalf("duplicate request ID %q", id)
		}
		seen[id] = true
	}
}

func TestRecoverTurnsPanicInto500(t *testing.T) {
	var logBuf strings.Builder
	h := Chain(Handler(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		Recover(logging.New("error", "json", &logBuf)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(logBuf.String(), "boom") {
		t.Errorf("panic was not logged: %s", logBuf.String())
	}
}

func TestRecoverRethrowsErrAbortHandler(t *testing.T) {
	h := Chain(Handler(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }), Recover(logging.Discard()))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("Recover swallowed ErrAbortHandler: %v", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestDeadlineStopsLateWrites(t *testing.T) {
	h := Chain(Handler(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		// The deadline has passed; this must not corrupt the response.
		_, _ = w.Write([]byte("late"))
	}), Deadline(5*time.Millisecond))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), "late") {
		t.Errorf("a write after the deadline reached the client: %s", rec.Body.String())
	}
}

func TestDeadlinePassesContextDown(t *testing.T) {
	var deadlineSet bool
	h := Chain(Handler(func(_ http.ResponseWriter, r *http.Request) {
		_, deadlineSet = r.Context().Deadline()
	}), Deadline(time.Second))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !deadlineSet {
		t.Error("the request context has no deadline")
	}
}

func TestDeadlineOfZeroIsNoop(t *testing.T) {
	h := Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	if got := Chain(h, Deadline(0)); got == nil {
		t.Fatal("Chain returned nil")
	}
	rec := httptest.NewRecorder()
	Chain(h, Deadline(0)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestChainAppliesLeftToRight(t *testing.T) {
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(Handler(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }), mw("a"), mw("b"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Join(order, ",") != "a,b,handler" {
		t.Errorf("order = %v", order)
	}
}

func TestLogRequestsRecordsStatusAndDuration(t *testing.T) {
	var buf strings.Builder
	h := Chain(Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }),
		RequestID(""), LogRequests(logging.New("info", "json", &buf)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !strings.Contains(buf.String(), `"status":418`) {
		t.Errorf("status not logged: %s", buf.String())
	}
}

func TestStatusRecorderCountsBytes(t *testing.T) {
	rec := httptest.NewRecorder()
	Chain(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello")
		_, _ = io.WriteString(w, " world")
	}), LogRequests(slog.New(slog.NewTextHandler(io.Discard, nil)))).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestProbesReportHealth(t *testing.T) {
	reg := observe.New()
	h := Probes(ProbeOptions{
		Version:   "1.2.3",
		StartedAt: time.Now().Add(-time.Minute),
		Metrics:   reg.Handler(),
	})

	for path, want := range map[string]int{"/healthz": 200, "/livez": 200, "/readyz": 200, "/version": 200, "/metrics": 200} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
		if rec.Header().Get("Cache-Control") == "" && path != "/metrics" {
			t.Errorf("%s did not set Cache-Control", path)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if !strings.Contains(rec.Body.String(), "1.2.3") {
		t.Errorf("version missing: %s", rec.Body.String())
	}
}

func TestReadyzFailsWhenNotReady(t *testing.T) {
	h := Probes(ProbeOptions{Ready: func(context.Context) error { return errors.New("database down") }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "database down") {
		t.Errorf("reason not reported: %s", rec.Body.String())
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	NotFoundHandler(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	MethodNotAllowedHandler("GET, POST")(rec, httptest.NewRequest(http.MethodDelete, "/x", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Header().Get("Allow") != "GET, POST" {
		t.Errorf("Allow = %q", rec.Header().Get("Allow"))
	}
}

func TestServerRunAndGracefulShutdown(t *testing.T) {
	cfg := config.Default().HTTP
	cfg.Addr = "127.0.0.1:0"
	cfg.ShutdownGrace = 2 * time.Second
	cfg.WriteTimeout = 5 * time.Second

	srv := NewServer(cfg, logging.Discard(), WithVersion("test"))
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, _ *http.Request) { JSON(w, 200, map[string]string{"ok": "1"}) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, mux) }()

	base := "http://" + srv.Addr()
	waitForServer(t, base+"/healthz")

	resp, err := http.Get(base + "/v1/ping") //nolint:noctx // short-lived test request
	if err != nil {
		t.Fatalf("GET /v1/ping: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Unmatched routes fall through to the probe mux, which 404s.
	resp2, err := http.Get(base + "/nope") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unmatched route = %d, want 404", resp2.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestServerRunReportsBindFailure(t *testing.T) {
	cfg := config.Default().HTTP
	cfg.Addr = "256.256.256.256:99999"
	srv := NewServer(cfg, logging.Discard())
	if err := srv.Run(context.Background(), http.NewServeMux()); err == nil {
		t.Fatal("Run succeeded with an impossible address")
	}
}

func waitForServer(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx // polling a local test server
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s never became reachable", url)
}
