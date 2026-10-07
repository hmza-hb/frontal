package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/hmza-hb/lead-intelligence/platform/config"
	"github.com/hmza-hb/lead-intelligence/platform/observe"
)

// Server is the shared HTTP server: bounded timeouts, a middleware stack that
// every service uses, standard probe endpoints, and shutdown that drains
// in-flight requests.
type Server struct {
	cfg     config.HTTPConfig
	log     *slog.Logger
	version string
	ready   func(ctx context.Context) error
	metrics http.Handler

	srv       *http.Server
	ln        net.Listener
	bound     chan struct{}
	startTime time.Time
}

// Option customises a Server.
type Option func(*Server)

// WithVersion sets the version reported by /version.
func WithVersion(v string) Option { return func(s *Server) { s.version = v } }

// WithReadiness sets the /readyz check.
func WithReadiness(fn func(ctx context.Context) error) Option {
	return func(s *Server) { s.ready = fn }
}

// WithMetrics mounts a metrics handler at /metrics.
func WithMetrics(reg *observe.Registry) Option {
	return func(s *Server) {
		if reg != nil {
			s.metrics = reg.Handler()
		}
	}
}

// NewServer returns a server. Routes are registered on the mux passed to Run.
func NewServer(cfg config.HTTPConfig, log *slog.Logger, opts ...Option) *Server {
	s := &Server{cfg: cfg, log: log, version: "dev", bound: make(chan struct{})}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler builds the fully wrapped handler for a route mux. Exported so tests
// can exercise the real middleware stack without binding a port.
func (s *Server) Handler(mux *http.ServeMux) http.Handler {
	probes := Probes(ProbeOptions{Version: s.version, StartedAt: s.startedAt(), Ready: s.ready, Metrics: s.metrics})

	root := http.NewServeMux()
	root.Handle("/healthz", probes)
	root.Handle("/livez", probes)
	root.Handle("/readyz", probes)
	root.Handle("/version", probes)
	if s.metrics != nil {
		root.Handle("/metrics", probes)
	}
	root.Handle("/", Chain(mux,
		SecurityHeaders,
		RequestID(""),
		LogRequests(s.log),
		Deadline(s.cfg.WriteTimeout),
	))
	return Chain(root, Recover(s.log))
}

func (s *Server) startedAt() time.Time { return s.startTime }

// Run binds the address and serves until ctx is cancelled, then drains.
func (s *Server) Run(ctx context.Context, mux *http.ServeMux) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("httpx: listen on %s: %w", s.cfg.Addr, err)
	}
	s.ln = ln
	s.startTime = time.Now()
	close(s.bound) // Addr() is now safe to call from another goroutine
	s.srv = &http.Server{
		Handler:           s.Handler(mux),
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		ErrorLog:          nil,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}

	s.log.InfoContext(ctx, "http server listening",
		"addr", ln.Addr().String(), "version", s.version)

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.log.InfoContext(ctx, "http server shutting down", "grace", s.cfg.ShutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownGrace)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("httpx: graceful shutdown: %w", err)
	}
	return <-errCh
}

// Addr reports the bound address, which is how a test discovers the port when
// the configuration asked for port 0. It blocks until the listener is bound, so
// a caller in another goroutine cannot observe a half-initialised server. It
// returns the configured address if Run was never called.
func (s *Server) Addr() string {
	select {
	case <-s.bound:
		return s.ln.Addr().String()
	case <-time.After(5 * time.Second):
		return s.cfg.Addr
	}
}
