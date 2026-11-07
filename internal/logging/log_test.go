package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":   LevelDebug,
		"INFO":    LevelInfo,
		"warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
		"":        LevelInfo,
		"bogus":   LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLevelString(t *testing.T) {
	cases := map[Level]string{
		LevelDebug: "debug",
		LevelInfo:  "info",
		LevelWarn:  "warn",
		LevelError: "error",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", level, got, want)
		}
	}
}

func TestFileSinkAndLevelFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "test.log")
	l := New(path, LevelWarn)
	defer l.Close()

	l.Info("dropped", nil)
	l.Warn("kept", map[string]any{"k": "v"})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "dropped") {
		t.Fatalf("info line was written at warn level: %q", text)
	}
	if !strings.Contains(text, "[warn] kept") {
		t.Fatalf("warn line missing: %q", text)
	}
	if !strings.Contains(text, `{"k":"v"}`) {
		t.Fatalf("fields missing: %q", text)
	}
}

func TestNilLoggerIsSafe(t *testing.T) {
	var l *Logger
	l.Info("x", nil)
	l.SetFile("")
	l.SetLevel(LevelDebug)
	if got := l.Path(); got != "" {
		t.Fatalf("Path on nil logger = %q", got)
	}
	l.Close()
}

func TestDiscardWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quiet.log")
	l := Discard()
	l.SetFile(path)
	l.Error("nope", nil)
	l.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("discard logger wrote %q", raw)
	}
}

func TestSetFileToEmptyDisablesSink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toggle.log")
	l := New(path, LevelInfo)
	l.Info("first", nil)
	l.SetFile("")
	l.Info("second", nil)
	l.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "first") {
		t.Fatalf("first line missing: %q", raw)
	}
	if strings.Contains(string(raw), "second") {
		t.Fatalf("line was written after the sink was disabled: %q", raw)
	}
}
