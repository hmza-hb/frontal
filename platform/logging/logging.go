// Package logging builds the platform's structured logger and carries run and
// request identity through context.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Level parses a log level name.
func Level(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Format selects the handler shape. Anything other than "json" produces human
// readable console output, which is what a local run wants. The level is set
// separately by New.
func Format(format string, w io.Writer) slog.Handler {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if strings.EqualFold(strings.TrimSpace(format), "json") {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// New returns a logger writing to w.
func New(level, format string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	h := Format(format, w)
	// Re-wrap with the resolved level: Format only decides the shape.
	return slog.New(&levelHandler{Handler: h, level: Level(level)})
}

// levelHandler applies a level to an existing handler without knowing its
// concrete type.
type levelHandler struct {
	slog.Handler
	level slog.Level
}

func (h *levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level && h.Handler.Enabled(ctx, l)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}

// Discard returns a logger that throws everything away, for tests.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type runKey struct{}
type requestKey struct{}

// WithRun returns a context carrying the pipeline run ID. Every log line and
// metric emitted downstream carries it, which is what makes a run's output
// greppable after the fact.
func WithRun(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runKey{}, runID)
}

// RunID returns the run ID in ctx, or "" if none.
func RunID(ctx context.Context) string {
	id, _ := ctx.Value(runKey{}).(string)
	return id
}

// WithRequest returns a context carrying the HTTP request ID.
func WithRequest(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestKey{}, id)
}

// RequestID returns the request ID in ctx, or "" if none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestKey{}).(string)
	return id
}

// FromContext returns a logger annotated with whatever identity ctx carries.
// Pass the result to every log call inside a request or a run.
func FromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	attrs := make([]any, 0, 4)
	if id := RunID(ctx); id != "" {
		attrs = append(attrs, "run_id", id)
	}
	if id := RequestID(ctx); id != "" {
		attrs = append(attrs, "request_id", id)
	}
	if len(attrs) == 0 {
		return base
	}
	return base.With(attrs...)
}
