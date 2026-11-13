// Package ratelimit is a small in-memory fixed-window limiter.
//
// It is deliberately per process: the gateway is a single binary and the limits
// protect one instance, not a cluster. Keys are opaque strings so callers can
// scope a limit by endpoint, client address or anything else.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// cleanupThreshold bounds the entry map: once it grows past this size, expired
// windows are swept on the next call.
const cleanupThreshold = 1024

// Limiter counts requests per key inside a fixed window.
type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	entries map[string]*entry
}

type entry struct {
	start time.Time
	count int
}

// New returns a limiter with the given window (default one minute).
func New(window time.Duration) *Limiter {
	if window <= 0 {
		window = time.Minute
	}
	return &Limiter{
		window:  window,
		now:     time.Now,
		entries: make(map[string]*entry),
	}
}

// Allow reports whether key may make another request. A limit of zero or less
// means unlimited. When the request is denied it also returns the whole seconds
// until the window resets, for Retry-After.
func (l *Limiter) Allow(key string, limit int) (bool, int) {
	if limit <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if len(l.entries) >= cleanupThreshold {
		for k, e := range l.entries {
			if now.Sub(e.start) >= l.window {
				delete(l.entries, k)
			}
		}
	}

	e := l.entries[key]
	if e == nil || now.Sub(e.start) >= l.window {
		e = &entry{start: now}
		l.entries[key] = e
	}
	if e.count >= limit {
		retry := int(math.Ceil((l.window - now.Sub(e.start)).Seconds()))
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}
	e.count++
	return true, 0
}

// Sweep drops every expired window. The app calls it from its periodic job so a
// long-lived process does not keep entries for clients that went away.
func (l *Limiter) Sweep() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	freed := 0
	for k, e := range l.entries {
		if now.Sub(e.start) >= l.window {
			delete(l.entries, k)
			freed++
		}
	}
	return freed
}
