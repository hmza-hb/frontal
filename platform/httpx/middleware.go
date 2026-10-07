package httpx

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// guardedWriter makes a ResponseWriter safe to use after its request deadline
// has passed. Without this, a handler that ignores its context produces a
// half-written body with a 200 status, which is the worst possible outcome for
// a client.
type guardedWriter struct {
	mu        sync.Mutex
	ctx       context.Context
	w         http.ResponseWriter
	committed bool
	aborted   bool
}

func newGuardedWriter(ctx context.Context, w http.ResponseWriter) *guardedWriter {
	return &guardedWriter{ctx: ctx, w: w}
}

// abortIfExpired latches the writer shut once the request context is done, so a
// handler that ignored its deadline cannot append a partial body to a response
// that already told the client "200 OK".
func (g *guardedWriter) abortIfExpired() bool {
	if g.aborted {
		return true
	}
	if err := g.ctx.Err(); err != nil {
		g.aborted = true
		return true
	}
	return false
}

func (g *guardedWriter) Header() http.Header { return g.w.Header() }

func (g *guardedWriter) WriteHeader(status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.committed || g.abortIfExpired() {
		return
	}
	g.committed = true
	g.w.WriteHeader(status)
}

func (g *guardedWriter) Write(b []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.abortIfExpired() {
		return 0, context.DeadlineExceeded
	}
	if !g.committed {
		g.committed = true
	}
	return g.w.Write(b)
}

func (g *guardedWriter) Flush() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f, ok := g.w.(http.Flusher); ok && !g.abortIfExpired() {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer for http.ResponseController.
func (g *guardedWriter) Unwrap() http.ResponseWriter { return g.w }

func (g *guardedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := g.w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("httpx: ResponseWriter does not support hijacking")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return h.Hijack()
}

var _ http.ResponseWriter = (*guardedWriter)(nil)

// Recover converts a panic into a 500 and a single log line. It must be the
// outermost middleware.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p) // deliberate client disconnect, not a bug
					}
					log.ErrorContext(r.Context(), "panic in handler",
						"panic", p, "path", r.URL.Path, "method", r.Method)
					Error(w, Internal("an internal error occurred"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID propagates or mints a request ID and puts it on the context.
func RequestID(header string) func(http.Handler) http.Handler {
	if header == "" {
		header = "X-Request-Id"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(header)
			if id == "" || len(id) > 128 {
				id = newRequestID()
			}
			w.Header().Set(header, id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type requestIDKey struct{}

// FromRequest returns the request ID assigned by the RequestID middleware.
func FromRequest(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

var idCounter atomic.Uint64

func newRequestID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	n := idCounter.Add(1)
	buf := make([]byte, 0, 16)
	for range 12 {
		buf = append(buf, alphabet[n&31])
		n >>= 5
		n += n * 0x9e37 // cheap avalanche so adjacent counters differ widely
	}
	return string(buf)
}

// LogRequests emits one structured line per request at info level, including
// the request ID so a user-reported failure maps to a log line directly.
func LogRequests(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.InfoContext(r.Context(), "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.written,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", FromRequest(r.Context()),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	written     int64
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.written += int64(n)
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var _ http.ResponseWriter = (*statusRecorder)(nil)

// ProbeOptions configures the standard probe endpoints.
type ProbeOptions struct {
	// Version is reported by /version. "dev" is used when empty.
	Version string
	// StartedAt seeds the uptime report. Omitted when zero.
	StartedAt time.Time
	// Ready decides /readyz. Nil means "ready as soon as the process is up",
	// which is correct for a service with no external dependency.
	Ready func(ctx context.Context) error
	// Metrics is mounted at /metrics when non-nil.
	Metrics http.Handler
}

// Probes returns the handler for /healthz, /readyz, /version and /metrics.
// Probe endpoints are deliberately un-authenticated and uncached: a probe that
// can fail is worse than no probe at all.
func Probes(opt ProbeOptions) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		NoCache(w)
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		NoCache(w)
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		NoCache(w)
		if opt.Ready != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := opt.Ready(ctx); err != nil {
				JSON(w, http.StatusServiceUnavailable,
					map[string]string{"status": "not_ready", "reason": err.Error()})
				return
			}
		}
		JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		NoCache(w)
		version := opt.Version
		if version == "" {
			version = "dev"
		}
		body := map[string]string{"version": version}
		if !opt.StartedAt.IsZero() {
			body["uptime_seconds"] = strconv.Itoa(int(time.Since(opt.StartedAt).Seconds()))
		}
		JSON(w, http.StatusOK, body)
	})
	if opt.Metrics != nil {
		mux.Handle("/metrics", opt.Metrics)
	}
	return mux
}

// NotFoundHandler is the shared 404 for unmatched routes.
func NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	Error(w, NotFound("no route for "+r.Method+" "+r.URL.Path))
}

// MethodNotAllowedHandler is the shared 405.
func MethodNotAllowedHandler(allowed string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowed)
		Error(w, &APIError{Status: http.StatusMethodNotAllowed, Code: CodeBadRequest,
			Message: "method " + r.Method + " is not allowed here"})
	}
}

// Handler adapts a function to http.HandlerFunc with a uniform signature.
func Handler(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return http.HandlerFunc(fn)
}
