package gateway

import (
	"sync"
	"time"
)

// Windows tracks credentials that are temporarily unusable because an upstream
// rate window is exhausted.
//
// This is deliberately in memory: a rate window resets on its own, so persisting
// it would only risk blocking a credential that has already recovered. A durable
// problem (no credit) is different and is stored on the credential instead.
type Windows struct {
	mu    sync.Mutex
	until map[int64]map[string]int64
	now   func() time.Time
}

// WindowBlock describes one blocked window of one credential.
type WindowBlock struct {
	// Window names the upstream window (fiveHour, weekly, usage).
	Window string `json:"window"`
	// Until is the reset time in Unix milliseconds.
	Until int64 `json:"until"`
}

// NewWindows returns an empty tracker.
func NewWindows() *Windows {
	return &Windows{until: map[int64]map[string]int64{}, now: time.Now}
}

// Blocked reports whether any window of the credential is still exhausted, and
// the latest reset time when it is.
func (w *Windows) Blocked(credentialID int64) (bool, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries := w.until[credentialID]
	if len(entries) == 0 {
		return false, 0
	}
	now := w.now().UnixMilli()
	var latest int64
	for name, until := range entries {
		if until <= now {
			delete(entries, name)
			continue
		}
		if until > latest {
			latest = until
		}
	}
	if len(entries) == 0 {
		delete(w.until, credentialID)
		return false, 0
	}
	return true, latest
}

// Mark records a window block. A later block that expires sooner never shortens
// one already recorded: recovery is never guessed earlier than known.
func (w *Windows) Mark(credentialID int64, window string, until int64) {
	if credentialID <= 0 {
		return
	}
	if until <= w.now().UnixMilli() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.until[credentialID] == nil {
		w.until[credentialID] = map[string]int64{}
	}
	if existing, ok := w.until[credentialID][window]; !ok || until > existing {
		w.until[credentialID][window] = until
	}
}

// Blocks returns every active block for the status page.
func (w *Windows) Blocks() map[int64][]WindowBlock {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[int64][]WindowBlock, len(w.until))
	for id, entries := range w.until {
		for name, until := range entries {
			out[id] = append(out[id], WindowBlock{Window: name, Until: until})
		}
	}
	return out
}

// Sweep drops expired entries and returns how many credentials went free.
func (w *Windows) Sweep() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now().UnixMilli()
	freed := 0
	for id, entries := range w.until {
		for name, until := range entries {
			if until <= now {
				delete(entries, name)
			}
		}
		if len(entries) == 0 {
			delete(w.until, id)
			freed++
		}
	}
	return freed
}

// Len returns the number of credentials with at least one block.
func (w *Windows) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.until)
}
