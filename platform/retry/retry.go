// Package retry provides the one retry policy the platform uses, with jitter
// so that N workers recovering from a 429 do not synchronise into a new burst.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Policy describes an exponential backoff schedule.
type Policy struct {
	MaxAttempts int           // total attempts, including the first
	Base        time.Duration // delay before the second attempt
	Max         time.Duration // ceiling for a single delay
	Multiplier  float64       // growth factor per attempt
	Jitter      float64       // 0..1 fraction of the delay that is randomised
}

// Default is a production-safe policy: four attempts, ~250ms base, 30s ceiling.
func Default() Policy {
	return Policy{MaxAttempts: 4, Base: 250 * time.Millisecond, Max: 30 * time.Second, Multiplier: 2, Jitter: 0.5}
}

// NoRetry runs the operation exactly once.
func NoRetry() Policy {
	return Policy{MaxAttempts: 1}
}

// Validate reports whether the policy can be executed.
func (p Policy) Validate() error {
	switch {
	case p.MaxAttempts < 1:
		return fmt.Errorf("retry: MaxAttempts must be >= 1, got %d", p.MaxAttempts)
	case p.Base < 0:
		return fmt.Errorf("retry: Base must be >= 0, got %s", p.Base)
	case p.Max < p.Base:
		return fmt.Errorf("retry: Max (%s) must be >= Base (%s)", p.Max, p.Base)
	case p.Multiplier < 1:
		return fmt.Errorf("retry: Multiplier must be >= 1, got %v", p.Multiplier)
	case p.Jitter < 0 || p.Jitter > 1:
		return fmt.Errorf("retry: Jitter must be in [0,1], got %v", p.Jitter)
	}
	return nil
}

// Delay returns the wait before attempt n+1 (n is zero-based). It is exported
// so callers can log or test the schedule without running it.
func (p Policy) Delay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := float64(p.Base) * math.Pow(p.Multiplier, float64(attempt))
	if p.Max > 0 && d > float64(p.Max) {
		d = float64(p.Max)
	}
	if p.Jitter > 0 {
		// Randomise downward only: never exceed the computed schedule, so the
		// Max ceiling stays an actual ceiling.
		d -= d * p.Jitter * rand.Float64()
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// Do runs fn until it succeeds, until the policy is exhausted, or until the
// context is cancelled. isRetryable classifies the error; returning false stops
// immediately and returns the error unchanged.
//
// Attempt numbers passed to fn are 1-based.
func Do(ctx context.Context, p Policy, isRetryable func(error) bool, fn func(ctx context.Context, attempt int) error) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if isRetryable == nil {
		isRetryable = func(error) bool { return true }
	}

	var last error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if last != nil {
				return errors.Join(last, err)
			}
			return err
		}

		last = fn(ctx, attempt)
		if last == nil {
			return nil
		}
		if !isRetryable(last) {
			return last
		}
		if attempt == p.MaxAttempts {
			break
		}

		timer := time.NewTimer(p.Delay(attempt - 1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(last, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("after %d attempts: %w", p.MaxAttempts, last)
}

// DoValue is Do for an operation that produces a value. The value is only
// written on success, so a failed retry cannot leak a partial result.
func DoValue[T any](ctx context.Context, p Policy, isRetryable func(error) bool, fn func(ctx context.Context, attempt int) (T, error)) (T, error) {
	var out T
	err := Do(ctx, p, isRetryable, func(ctx context.Context, attempt int) error {
		v, err := fn(ctx, attempt)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}
