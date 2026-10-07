package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func TestDoSucceedsOnFirstAttempt(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Default(), nil, func(context.Context, int) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned %v", err)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}

func TestDoRetriesUntilSuccess(t *testing.T) {
	p := Policy{MaxAttempts: 5, Base: time.Millisecond, Max: 2 * time.Millisecond, Multiplier: 2}
	calls := 0
	err := Do(context.Background(), p, nil, func(context.Context, int) error {
		calls++
		if calls < 3 {
			return errBoom
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do returned %v", err)
	}
	if calls != 3 {
		t.Fatalf("fn called %d times, want 3", calls)
	}
}

func TestDoStopsOnNonRetryable(t *testing.T) {
	var fatal = errors.New("fatal")
	calls := 0
	err := Do(context.Background(), Policy{MaxAttempts: 5, Base: time.Millisecond, Max: time.Millisecond, Multiplier: 1},
		func(err error) bool { return !errors.Is(err, fatal) },
		func(context.Context, int) error {
			calls++
			return fatal
		})
	if !errors.Is(err, fatal) {
		t.Fatalf("Do returned %v, want the original error", err)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times, want 1", calls)
	}
}

func TestDoExhaustsAttempts(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Policy{MaxAttempts: 3, Base: time.Millisecond, Max: time.Millisecond, Multiplier: 1},
		nil, func(context.Context, int) error {
			calls++
			return errBoom
		})
	if calls != 3 {
		t.Fatalf("fn called %d times, want 3", calls)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("wrapped error lost its cause: %v", err)
	}
}

func TestDoRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := Do(ctx, Policy{MaxAttempts: 10, Base: 50 * time.Millisecond, Max: 50 * time.Millisecond, Multiplier: 1},
		nil, func(context.Context, int) error {
			calls++
			cancel()
			return errBoom
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do returned %v, want a context error", err)
	}
	if calls != 1 {
		t.Fatalf("fn called %d times after cancellation, want 1", calls)
	}
}

func TestDoRejectsInvalidPolicy(t *testing.T) {
	err := Do(context.Background(), Policy{MaxAttempts: 0}, nil, func(context.Context, int) error { return nil })
	if err == nil {
		t.Fatal("Do accepted a policy with MaxAttempts=0")
	}
}

func TestDelayNeverExceedsMax(t *testing.T) {
	p := Policy{MaxAttempts: 10, Base: 100 * time.Millisecond, Max: 250 * time.Millisecond, Multiplier: 10, Jitter: 0}
	for i := range 8 {
		if d := p.Delay(i); d > 250*time.Millisecond {
			t.Fatalf("Delay(%d) = %s exceeds Max", i, d)
		}
	}
}

func TestDoValueDiscardsPartialResults(t *testing.T) {
	p := Policy{MaxAttempts: 3, Base: time.Millisecond, Max: time.Millisecond, Multiplier: 1}
	v, err := DoValue(context.Background(), p, nil, func(context.Context, int) (string, error) {
		return "wrong", errBoom
	})
	if err == nil {
		t.Fatal("DoValue returned no error")
	}
	if v != "" {
		t.Fatalf("DoValue returned %q on failure, want the zero value", v)
	}
}

func TestDoValueReturnsSuccess(t *testing.T) {
	v, err := DoValue(context.Background(), Default(), nil, func(context.Context, int) (int, error) { return 42, nil })
	if err != nil || v != 42 {
		t.Fatalf("DoValue = %d, %v", v, err)
	}
}
