// Package logging writes the proxy's line log.
//
// The format is stable and greppable, because operators and tests match on it:
//
//	[2026-11-07T10:21:36.123Z] [info] Provider probed {"kind":"openai_compatible","ms":231}
package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level is a log severity.
type Level int

const (
	// LevelDebug is verbose internal detail.
	LevelDebug Level = iota
	// LevelInfo is the default: lifecycle and per-request summaries.
	LevelInfo
	// LevelWarn is a recoverable problem.
	LevelWarn
	// LevelError is a failed operation.
	LevelError
)

// ParseLevel maps "debug"/"info"/"warn"/"error" to a Level. Unknown values
// fall back to info.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// Logger writes lines to stdout and, when configured, to a file.
//
// A nil *Logger is valid and drops everything, so callers never have to guard.
type Logger struct {
	mu    sync.Mutex
	file  *os.File
	path  string
	level Level
}

// New returns a logger writing at level to stdout and, when path is not empty,
// appending to that file.
func New(path string, level Level) *Logger {
	l := &Logger{level: level}
	l.SetFile(path)
	return l
}

// levelOff is above every real level, so Discard drops everything.
const levelOff Level = 1 << 7

// Discard returns a logger that writes nothing. It is useful in tests.
func Discard() *Logger { return &Logger{level: levelOff} }

// SetFile redirects the file sink. An empty path disables it. Errors are
// reported to stderr only: a broken log file must never stop the proxy.
func (l *Logger) SetFile(path string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	l.path = path
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[logging] cannot create log directory: %v\n", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[logging] cannot open log file: %v\n", err)
		return
	}
	l.file = f
}

// SetLevel changes the threshold.
func (l *Logger) SetLevel(level Level) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Path returns the log file path, or "" when file logging is off.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, fields map[string]any) { l.log(LevelDebug, msg, fields) }

// Info logs at info level.
func (l *Logger) Info(msg string, fields map[string]any) { l.log(LevelInfo, msg, fields) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, fields map[string]any) { l.log(LevelWarn, msg, fields) }

// Error logs at error level.
func (l *Logger) Error(msg string, fields map[string]any) { l.log(LevelError, msg, fields) }

// Close releases the file sink.
func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

func (l *Logger) log(level Level, msg string, fields map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.level {
		return
	}

	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	b.WriteString("] [")
	b.WriteString(level.String())
	b.WriteString("] ")
	b.WriteString(msg)
	if len(fields) > 0 {
		if raw, err := json.Marshal(fields); err == nil {
			b.WriteByte(' ')
			b.Write(raw)
		}
	}
	b.WriteByte('\n')
	line := b.String()

	_, _ = os.Stdout.WriteString(line)
	if l.file != nil {
		_, _ = l.file.WriteString(line)
	}
}
