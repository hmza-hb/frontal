package circuit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestBreaker(t *testing.T, cfg Config) (*Breaker, *time.Time) {
	t.Helper()
	b := New(cfg)
	now := time.Unix(1_700_000_000, 0)
	b.now = func() time.Time { return now }
	return b, &now
}

func TestClosedUntilThresholdThenOpen(t *testing.T) {
	b, _ := newTestBreaker(t, Config{FailureThreshold: 3, SuccessThreshold: 1, OpenTimeout: time.Minute})
	boom := errors.New("boom")
	for i := range 2 {
		if err := b.Do(context.Background(), isErr, func(context.Context) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("attempt %d: got %v", i, err)
		}
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state after 2/3 failures = %s, want closed", got)
	}
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return boom })
	if got := b.State(); got != StateOpen {
		t.Fatalf("state after 3/3 failures = %s, want open", got)
	}
}

func TestOpenRejectsWithoutCalling(t *testing.T) {
	b, _ := newTestBreaker(t, Config{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: time.Minute})
	boom := errors.New("boom")
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return boom })

	called := false
	err := b.Do(context.Background(), isErr, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("got %v, want ErrOpen", err)
	}
	if called {
		t.Fatal("the wrapped function ran while the breaker was open")
	}
}

func TestHalfOpenProbeSucceedsAndCloses(t *testing.T) {
	b, now := newTestBreaker(t, Config{FailureThreshold: 1, SuccessThreshold: 2, OpenTimeout: time.Minute})
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return errors.New("boom") })

	*now = now.Add(2 * time.Minute)
	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("state after open timeout = %s, want half-open", got)
	}
	if err := b.Do(context.Background(), isErr, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("state after 1/2 successes = %s, want half-open", got)
	}
	if err := b.Do(context.Background(), isErr, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state after 2/2 successes = %s, want closed", got)
	}
}

func TestHalfOpenProbeFailureReopens(t *testing.T) {
	b, now := newTestBreaker(t, Config{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: time.Minute})
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return errors.New("boom") })
	*now = now.Add(2 * time.Minute)
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return errors.New("boom") })
	if got := b.State(); got != StateOpen {
		t.Fatalf("state after a failed probe = %s, want open", got)
	}
}

func TestNonFailuresDoNotTripTheBreaker(t *testing.T) {
	b, _ := newTestBreaker(t, Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: time.Minute})
	clientErr := errors.New("400 bad request")
	for range 10 {
		_ = b.Do(context.Background(), func(err error) bool { return !errors.Is(err, clientErr) },
			func(context.Context) error { return clientErr })
	}
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed: classified errors must not trip it", got)
	}
}

func TestSuccessResetsFailureStreak(t *testing.T) {
	b, _ := newTestBreaker(t, Config{FailureThreshold: 3, SuccessThreshold: 1, OpenTimeout: time.Minute})
	for range 2 {
		_ = b.Do(context.Background(), isErr, func(context.Context) error { return errors.New("boom") })
	}
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return nil })
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return errors.New("boom") })
	if got := b.State(); got != StateClosed {
		t.Fatalf("state = %s, want closed: a success must reset the streak", got)
	}
}

func TestResetClosesTheBreaker(t *testing.T) {
	b, _ := newTestBreaker(t, Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	_ = b.Do(context.Background(), isErr, func(context.Context) error { return errors.New("boom") })
	b.Reset()
	if got := b.State(); got != StateClosed {
		t.Fatalf("state after Reset = %s, want closed", got)
	}
}

func TestStateString(t *testing.T) {
	for _, tc := range []struct {
		s    State
		want string
	}{{StateClosed, "closed"}, {StateOpen, "open"}, {StateHalfOpen, "half-open"}, {State(99), "unknown"}} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func isErr(err error) bool { return err != nil }
