// Package ratelimit provides token-bucket limiters.
//
// The crawler needs two different shapes of limit: a global ceiling on
// outbound request rate, and a per-host politeness delay. Keyed covers both —
// the "global" case is simply a key that never varies.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter is a token bucket. It is safe for concurrent use.
type Limiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

// New returns a limiter allowing rate events per second with the given burst.
// A non-positive rate means "no limit", which Wait treats as always ready.
func New(rate float64, burst int) *Limiter {
	if burst < 1 {
		burst = 1
	}
	l := &Limiter{
		rate:   rate,
		burst:  float64(burst),
		tokens: float64(burst),
		now:    time.Now,
		sleep:  sleepCtx,
	}
	l.last = l.now()
	return l
}

// Wait blocks until a token is available or ctx is done.
func (l *Limiter) Wait(ctx context.Context) error {
	if l.rate <= 0 {
		return ctx.Err()
	}
	for {
		delay, ok := l.reserve()
		if !ok {
			return ctx.Err()
		}
		if delay <= 0 {
			return nil
		}
		if err := l.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

// reserve consumes a token if one is available. It returns the duration the
// caller must wait before the token becomes available; ok is false if the
// limiter is unlimited.
func (l *Limiter) reserve() (delay time.Duration, ok bool) {
	if l.rate <= 0 {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	// A non-positive elapsed time (including a clock that moved backwards) is
	// treated as "no time passed" and re-bases last, so an injected or stepped
	// clock cannot strand the bucket below one token forever.
	if elapsed := now.Sub(l.last); elapsed > 0 {
		l.tokens += elapsed.Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	} else {
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return 0, true
	}
	missing := 1 - l.tokens
	return time.Duration(missing / l.rate * float64(time.Second)), true
}

// Tokens reports the current token count. Test and diagnostic use.
func (l *Limiter) Tokens() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tokens
}

// Keyed holds one Limiter per key and evicts idle entries so a long-running
// crawl over many hosts cannot leak memory.
type Keyed struct {
	mu       sync.Mutex
	limiters map[string]*keyedEntry
	rate     float64
	burst    int
	maxKeys  int
	idleTTL  time.Duration
	now      func() time.Time
}

type keyedEntry struct {
	limiter *Limiter
	lastUse time.Time
}

// DefaultMaxKeys bounds the per-key limiter table.
const DefaultMaxKeys = 4096

// NewKeyed returns a table of limiters with the given default rate and burst.
// maxKeys <= 0 selects DefaultMaxKeys; exceeding it evicts the least recently
// used entry rather than growing without bound.
func NewKeyed(rate float64, burst, maxKeys int) *Keyed {
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}
	return &Keyed{
		limiters: make(map[string]*keyedEntry),
		rate:     rate,
		burst:    burst,
		maxKeys:  maxKeys,
		idleTTL:  10 * time.Minute,
		now:      time.Now,
	}
}

// Wait blocks until the limiter for key allows another event.
func (k *Keyed) Wait(ctx context.Context, key string) error {
	return k.Limiter(key).Wait(ctx)
}

// Limiter returns the limiter for key, creating it if needed.
func (k *Keyed) Limiter(key string) *Limiter {
	k.mu.Lock()
	defer k.mu.Unlock()

	if e, ok := k.limiters[key]; ok {
		e.lastUse = k.now()
		return e.limiter
	}
	k.evictLocked()
	l := New(k.rate, k.burst)
	k.limiters[key] = &keyedEntry{limiter: l, lastUse: k.now()}
	return l
}

// evictLocked drops idle entries, then the oldest ones, until there is room.
func (k *Keyed) evictLocked() {
	now := k.now()
	for key, e := range k.limiters {
		if now.Sub(e.lastUse) > k.idleTTL {
			delete(k.limiters, key)
		}
	}
	for len(k.limiters) >= k.maxKeys {
		var oldestKey string
		var oldest time.Time
		for key, e := range k.limiters {
			if oldestKey == "" || e.lastUse.Before(oldest) {
				oldestKey, oldest = key, e.lastUse
			}
		}
		if oldestKey == "" {
			return
		}
		delete(k.limiters, oldestKey)
	}
}

// Len reports the number of tracked keys. Test and diagnostic use.
func (k *Keyed) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.limiters)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
