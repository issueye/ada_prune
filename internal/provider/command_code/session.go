package command_code

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsUUID reports whether a value may be sent as the envelope's threadId. The CLI
// omits the field entirely when the session id is not a UUID.
func IsUUID(v string) bool { return uuidRe.MatchString(v) }

// Sessions keeps one upstream session per credential so a key looks like one CLI
// install across requests. A caller-supplied session id always wins.
type Sessions struct {
	mu     sync.Mutex
	byKey  map[string]sessionEntry
	ttl    time.Duration
	jitter time.Duration
	rand   io.Reader
	clock  func() time.Time
}

type sessionEntry struct {
	id      string
	expires time.Time
}

// NewSessions returns a session store. ttl <= 0 falls back to the documented
// default; jitter of one hour keeps a fleet from rotating in lockstep.
func NewSessions(ttl time.Duration, rnd io.Reader, clock func() time.Time) *Sessions {
	if ttl <= 0 {
		ttl = DefaultSessionTTLHours * time.Hour
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	if clock == nil {
		clock = time.Now
	}
	return &Sessions{
		byKey:  make(map[string]sessionEntry),
		ttl:    ttl,
		jitter: time.Hour,
		rand:   rnd,
		clock:  clock,
	}
}

// ID resolves the session for one request: an explicit client session when it
// sent one, else its prompt cache key, else the credential's session, created or
// renewed on demand.
func (s *Sessions) ID(explicit, promptCacheKey, secret string) string {
	if v := strings.TrimSpace(explicit); len(v) >= 8 {
		return v
	}
	if v := strings.TrimSpace(promptCacheKey); len(v) >= 8 {
		return v
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if entry, ok := s.byKey[secret]; ok && now.Before(entry.expires) {
		return entry.id
	}
	id := newUUID(s.rand)
	s.byKey[secret] = sessionEntry{id: id, expires: now.Add(s.ttl + time.Duration(s.randomJitter()))}
	return id
}

// Sweep drops expired sessions and returns how many it removed.
func (s *Sessions) Sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, entry := range s.byKey {
		if !now.Before(entry.expires) {
			delete(s.byKey, key)
			removed++
		}
	}
	return removed
}

// Len returns the number of live sessions.
func (s *Sessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byKey)
}

func (s *Sessions) randomJitter() int64 {
	if s.jitter <= 0 {
		return 0
	}
	var buf [8]byte
	if _, err := io.ReadFull(s.rand, buf[:]); err != nil {
		return 0
	}
	var v uint64
	for _, b := range buf {
		v = v<<8 | uint64(b)
	}
	return int64(v % uint64(s.jitter))
}

func newUUID(r io.Reader) string {
	if r == nil {
		r = rand.Reader
	}
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		_, _ = rand.Read(b[:])
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}
