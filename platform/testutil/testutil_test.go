package testutil

import (
	"testing"
	"time"
)

func TestClockAdvances(t *testing.T) {
	c := NewClock()
	start := c.Now()
	c.Advance(90 * time.Second)
	if got := c.Now().Sub(start); got != 90*time.Second {
		t.Fatalf("clock advanced %s, want 90s", got)
	}
}

func TestClockSleepAdvances(t *testing.T) {
	c := NewClock()
	start := c.Now()
	if err := c.Sleep(time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := c.Now().Sub(start); got != time.Minute {
		t.Fatalf("Sleep advanced %s, want 1m", got)
	}
}

func TestFreePortIsBindable(t *testing.T) {
	if p := FreePort(t); p <= 0 || p > 65535 {
		t.Fatalf("FreePort returned %d", p)
	}
}

func TestSanitiseProducesIdentifierSafeName(t *testing.T) {
	got := sanitise("TestSomething/With-Chars 12")
	for _, r := range got {
		valid := r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !valid {
			t.Fatalf("sanitise left %q in %q", r, got)
		}
	}
}
