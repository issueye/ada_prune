package command_code

import (
	"testing"
	"time"
)

func TestSessionIsStablePerCredential(t *testing.T) {
	s := NewSessions(12*time.Hour, nil, nil)
	first := s.ID("", "", "secret-a")
	second := s.ID("", "", "secret-a")
	if first != second {
		t.Fatalf("session changed between calls: %s vs %s", first, second)
	}
	if !IsUUID(first) {
		t.Fatalf("session id %q is not a UUID, so threadId would be dropped", first)
	}
	if other := s.ID("", "", "secret-b"); other == first {
		t.Fatal("two credentials shared a session")
	}
}

func TestSessionPrefersClientHints(t *testing.T) {
	s := NewSessions(time.Hour, nil, nil)

	if got := s.ID("client-session-1", "", "secret"); got != "client-session-1" {
		t.Fatalf("explicit session = %q", got)
	}
	if got := s.ID("", "prompt-cache-key-1", "secret"); got != "prompt-cache-key-1" {
		t.Fatalf("cache key session = %q", got)
	}
	// Too short to be trusted: fall back to the derived session.
	if got := s.ID("short", "tiny", "secret"); got == "short" || got == "tiny" {
		t.Fatalf("short hints were accepted: %q", got)
	}
}

func TestSessionExpires(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := NewSessions(time.Hour, nil, clock)

	first := s.ID("", "", "secret")
	now = now.Add(30 * time.Minute)
	if got := s.ID("", "", "secret"); got != first {
		t.Fatal("session rotated before its TTL")
	}

	now = now.Add(3 * time.Hour)
	second := s.ID("", "", "secret")
	if second == first {
		t.Fatal("session did not rotate after its TTL")
	}
}

func TestSessionSweep(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := NewSessions(time.Hour, nil, clock)

	s.ID("", "", "secret")
	if s.Len() != 1 {
		t.Fatalf("len = %d", s.Len())
	}
	now = now.Add(4 * time.Hour)
	if removed := s.Sweep(now); removed != 1 {
		t.Fatalf("swept = %d, want 1", removed)
	}
	if s.Len() != 0 {
		t.Fatalf("len after sweep = %d", s.Len())
	}
}

func TestIsUUID(t *testing.T) {
	cases := map[string]bool{
		"0f0e0d0c-0b0a-4908-a706-050403020100": true,
		"not-a-uuid":                           false,
		"":                                     false,
		"0f0e0d0c0b0a4908a706050403020100":     false,
	}
	for in, want := range cases {
		if got := IsUUID(in); got != want {
			t.Errorf("IsUUID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewUUIDShape(t *testing.T) {
	for i := 0; i < 20; i++ {
		id := newUUID(nil)
		if !IsUUID(id) {
			t.Fatalf("newUUID produced %q", id)
		}
		// Version 4 and RFC 4122 variant.
		if id[14] != '4' {
			t.Fatalf("version nibble = %c, want 4 (%s)", id[14], id)
		}
		switch id[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Fatalf("variant nibble = %c, want 8..b (%s)", id[19], id)
		}
	}
}
