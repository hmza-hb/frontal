package ratelimit

import (
	"context"
	"testing"
	"time"
)

// fakeClock lets tests advance time without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeLimiter(rate float64, burst int) (*Limiter, *fakeClock, *[]time.Duration) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	var slept []time.Duration
	l := New(rate, burst)
	l.now = c.now
	l.last = c.now() // re-base the bucket on the injected clock
	l.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		c.advance(d)
		return nil
	}
	return l, c, &slept
}

func TestBurstIsAvailableImmediately(t *testing.T) {
	l, _, slept := newFakeLimiter(10, 3)
	for range 3 {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	if len(*slept) != 0 {
		t.Fatalf("burst of 3 should not sleep, slept %v", *slept)
	}
}

func TestWaitSleepsOnceBucketIsEmpty(t *testing.T) {
	l, _, slept := newFakeLimiter(10, 1) // 1 token per 100ms
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 {
		t.Fatalf("expected exactly one sleep, got %v", *slept)
	}
	if d := (*slept)[0]; d < 90*time.Millisecond || d > 110*time.Millisecond {
		t.Fatalf("sleeped %s, want ~100ms at 10/s", d)
	}
}

func TestRefillOverTime(t *testing.T) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	l := New(10, 1)
	l.now = c.now
	l.last = c.now()
	// Drain the bucket.
	c.advance(time.Second) // 10 tokens accrue, capped at the burst of 1
	if delay, _ := l.reserve(); delay != 0 {
		t.Fatalf("first request after idle waited %s, want no wait", delay)
	}
	// The bucket is empty again, so the next caller must be told to wait.
	if delay, _ := l.reserve(); delay <= 0 {
		t.Fatalf("second immediate request got delay %s, want a positive wait", delay)
	}
}

func TestUnlimitedLimiterNeverBlocks(t *testing.T) {
	l := New(0, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx); !isCancelled(err) {
		t.Fatalf("Wait with a cancelled context returned %v", err)
	}
}

func TestWaitHonoursContextCancellation(t *testing.T) {
	// A real limiter: the fake sleep hook ignores ctx, so it cannot be used to
	// prove that Wait actually gives up on cancellation.
	l := New(0.001, 1) // one token per ~17 minutes
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("Wait blocked through context cancellation")
	}
}

func TestKeyedCreatesOneLimiterPerKey(t *testing.T) {
	k := NewKeyed(5, 1, 8)
	a := k.Limiter("host-a")
	b := k.Limiter("host-b")
	if a == b {
		t.Fatal("different keys shared a limiter")
	}
	if k.Limiter("host-a") != a {
		t.Fatal("same key returned a different limiter")
	}
	if k.Len() != 2 {
		t.Fatalf("Len = %d, want 2", k.Len())
	}
}

func TestKeyedEvictsWhenFull(t *testing.T) {
	k := NewKeyed(5, 1, 4)
	for i := range 10 {
		k.Limiter(string(rune('a' + i)))
	}
	if k.Len() > 4 {
		t.Fatalf("Len = %d, want <= 4", k.Len())
	}
}

func TestKeyedDropsIdleEntries(t *testing.T) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	k := NewKeyed(5, 1, 8)
	k.now = c.now
	k.Limiter("stale")
	c.advance(2 * time.Hour)
	k.Limiter("fresh")
	if k.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after idle eviction", k.Len())
	}
}

func isCancelled(err error) bool { return err == context.Canceled }
