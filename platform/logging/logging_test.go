package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewHonoursFormat(t *testing.T) {
	var buf bytes.Buffer
	New("info", "json", &buf).Info("hello", "k", "v")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if got["msg"] != "hello" || got["k"] != "v" {
		t.Errorf("unexpected log line: %v", got)
	}
}

func TestNewHonoursLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New("warn", "json", &buf)
	l.Info("suppressed")
	if buf.Len() != 0 {
		t.Errorf("info was emitted at warn level: %s", buf.String())
	}
	l.Warn("kept")
	if buf.Len() == 0 {
		t.Error("warn was suppressed at warn level")
	}
}

func TestLevelParsing(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"info": slog.LevelInfo, "": slog.LevelInfo, "nonsense": slog.LevelInfo,
		"warn": slog.LevelWarn, "warning": slog.LevelWarn,
		"error": slog.LevelError,
	}
	for in, want := range cases {
		if got := Level(in); got != want {
			t.Errorf("Level(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRunIDPropagatesThroughContext(t *testing.T) {
	ctx := WithRun(context.Background(), "run_123")
	if got := RunID(ctx); got != "run_123" {
		t.Fatalf("RunID = %q", got)
	}
	if got := RunID(context.Background()); got != "" {
		t.Fatalf("RunID on a bare context = %q, want empty", got)
	}
}

func TestFromContextAnnotatesLogger(t *testing.T) {
	var buf bytes.Buffer
	base := New("info", "json", &buf)
	ctx := WithRequest(WithRun(context.Background(), "run_9"), "req_7")

	FromContext(ctx, base).Info("scoped")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["run_id"] != "run_9" || got["request_id"] != "req_7" {
		t.Errorf("context identity missing from log line: %v", got)
	}
}

func TestFromContextWithNoIdentityIsUnchanged(t *testing.T) {
	base := slog.Default()
	if FromContext(context.Background(), base) != base {
		t.Error("FromContext wrapped a logger that had nothing to add")
	}
	if FromContext(context.Background(), nil) == nil {
		t.Error("FromContext returned nil for a nil base logger")
	}
}

func TestDiscardWritesNothing(t *testing.T) {
	Discard().Error("should vanish")
}
