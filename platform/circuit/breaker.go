// Package circuit provides a minimal, dependency-free circuit breaker used to
// keep a dead enrichment provider from consuming the whole run's time budget.
package circuit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is the breaker's current mode.
type State int

const (
	// StateClosed passes calls through.
	StateClosed State = iota
	// StateOpen rejects calls immediately.
	StateOpen
	// StateHalfOpen lets a limited number of probe calls through.
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrOpen is returned when the breaker is rejecting calls.
var ErrOpen = errors.New("circuit: breaker is open")

// Config tunes the breaker.
type Config struct {
	// FailureThreshold is the consecutive failure count that trips the breaker.
	FailureThreshold int
	// SuccessThreshold is the consecutive success count in half-open that
	// closes it again.
	SuccessThreshold int
	// OpenTimeout is how long the breaker stays open before probing.
	OpenTimeout time.Duration
	// HalfOpenMaxCalls caps concurrent probes in half-open.
	HalfOpenMaxCalls int
}

// DefaultConfig is a reasonable starting point for a network dependency.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		OpenTimeout:      30 * time.Second,
		HalfOpenMaxCalls: 1,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = d.FailureThreshold
	}
	if c.SuccessThreshold <= 0 {
		c.SuccessThreshold = d.SuccessThreshold
	}
	if c.OpenTimeout <= 0 {
		c.OpenTimeout = d.OpenTimeout
	}
	if c.HalfOpenMaxCalls <= 0 {
		c.HalfOpenMaxCalls = d.HalfOpenMaxCalls
	}
	return c
}

// Breaker is a circuit breaker. It is safe for concurrent use.
type Breaker struct {
	mu        sync.Mutex
	cfg       Config
	state     State
	failures  int
	successes int
	probes    int
	openedAt  time.Time
	now       func() time.Time
}

// New returns a closed breaker.
func New(cfg Config) *Breaker {
	return &Breaker{cfg: cfg.withDefaults(), state: StateClosed, now: time.Now}
}

// State reports the current state, applying the open -> half-open transition
// if the open timeout has elapsed.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked()
	return b.state
}

// Do runs fn through the breaker. The error is classified by isFailure, which
// lets callers exclude context cancellation and 4xx "you're doing it wrong"
// responses from tripping the breaker.
func (b *Breaker) Do(ctx context.Context, isFailure func(error) bool, fn func(ctx context.Context) error) error {
	done, err := b.Allow()
	if err != nil {
		return err
	}
	callErr := fn(ctx)
	done(callErr == nil || !isFailure(callErr))
	return callErr
}

// Allow reserves a call. The returned function must be invoked exactly once
// with whether the call succeeded. This is the low-level API for callers that
// already have a result to classify.
func (b *Breaker) Allow() (func(success bool), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeHalfOpenLocked()

	switch b.state {
	case StateOpen:
		return nil, fmt.Errorf("%w (retry in %s)", ErrOpen, b.cfg.OpenTimeout-b.now().Sub(b.openedAt))
	case StateHalfOpen:
		if b.probes >= b.cfg.HalfOpenMaxCalls {
			return nil, fmt.Errorf("%w: half-open probe already in flight", ErrOpen)
		}
		b.probes++
	}

	startedIn := b.state
	return func(success bool) {
		b.mu.Lock()
		defer b.mu.Unlock()

		if startedIn == StateHalfOpen && b.state == StateHalfOpen {
			b.probes--
		}
		if success {
			b.successes++
			b.failures = 0
			if b.state == StateHalfOpen && b.successes >= b.cfg.SuccessThreshold {
				b.state = StateClosed
				b.successes = 0
			}
			return
		}

		b.successes = 0
		b.failures++
		if b.state == StateHalfOpen || b.failures >= b.cfg.FailureThreshold {
			b.tripLocked()
		}
	}, nil
}

func (b *Breaker) maybeHalfOpenLocked() {
	if b.state == StateOpen && b.now().Sub(b.openedAt) >= b.cfg.OpenTimeout {
		b.state = StateHalfOpen
		b.successes = 0
		b.probes = 0
	}
}

func (b *Breaker) tripLocked() {
	b.state = StateOpen
	b.openedAt = b.now()
	b.successes = 0
	b.probes = 0
}

// Reset forces the breaker closed. Used by operators and tests.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateClosed
	b.failures = 0
	b.successes = 0
	b.probes = 0
}
