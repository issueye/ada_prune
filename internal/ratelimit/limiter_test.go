package ratelimit

import (
	"testing"
	"time"
)

// clock is a manually advanced time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestAllowEnforcesLimitWithinWindow(t *testing.T) {
	clk := &clock{t: time.Unix(0, 0)}
	l := New(time.Minute)
	l.now = clk.now

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a", 3); !ok {
			t.Fatalf("request %d denied inside the limit", i+1)
		}
	}
	ok, retry := l.Allow("a", 3)
	if ok {
		t.Fatal("fourth request should be denied")
	}
	if retry != 60 {
		t.Fatalf("retry = %d, want 60", retry)
	}
}

func TestAllowResetsAfterWindow(t *testing.T) {
	clk := &clock{t: time.Unix(0, 0)}
	l := New(time.Minute)
	l.now = clk.now

	if ok, _ := l.Allow("a", 1); !ok {
		t.Fatal("first request denied")
	}
	if ok, _ := l.Allow("a", 1); ok {
		t.Fatal("second request should be denied")
	}

	clk.advance(time.Minute)
	if ok, _ := l.Allow("a", 1); !ok {
		t.Fatal("request after the window was denied")
	}
}

func TestAllowZeroMeansUnlimited(t *testing.T) {
	l := New(time.Minute)
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("a", 0); !ok {
			t.Fatalf("request %d denied with limit 0", i+1)
		}
	}
}

func TestAllowKeysAreIndependent(t *testing.T) {
	clk := &clock{t: time.Unix(0, 0)}
	l := New(time.Minute)
	l.now = clk.now

	if ok, _ := l.Allow("a", 1); !ok {
		t.Fatal("a denied")
	}
	if ok, _ := l.Allow("b", 1); !ok {
		t.Fatal("b was counted against a")
	}
}

func TestSweepDropsExpiredWindows(t *testing.T) {
	clk := &clock{t: time.Unix(0, 0)}
	l := New(time.Minute)
	l.now = clk.now

	_, _ = l.Allow("a", 1)
	_, _ = l.Allow("b", 1)
	clk.advance(time.Minute)
	if freed := l.Sweep(); freed != 2 {
		t.Fatalf("freed = %d, want 2", freed)
	}
	if len(l.entries) != 0 {
		t.Fatalf("entries = %d after sweep", len(l.entries))
	}
}
