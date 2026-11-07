// Package httpkit holds the small HTTP helpers every handler shares: bounded
// JSON reads, JSON writes, and an SSE writer that flushes every event.
package httpkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// DefaultMaxBodyMB bounds a request body when the caller has no opinion.
const DefaultMaxBodyMB int64 = 100

// BodyTooLarge reports that a body exceeded the configured limit.
type BodyTooLarge struct {
	Limit int64
}

func (e *BodyTooLarge) Error() string {
	return fmt.Sprintf("request body exceeds %d bytes", e.Limit)
}

// ReadJSON reads a bounded body. It returns *BodyTooLarge when the limit is hit
// so the caller can answer 413 instead of 400.
func ReadJSON(r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyMB << 20
	}
	// Read one byte past the limit to tell "exactly at the limit" from "over".
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, &BodyTooLarge{Limit: maxBytes}
	}
	return raw, nil
}

// DecodeJSON is ReadJSON plus unmarshalling.
func DecodeJSON(r *http.Request, maxBytes int64, v any) error {
	raw, err := ReadJSON(r, maxBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// SendJSON writes v with the given status and a JSON content type.
func SendJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// APIError is the OpenAI-shaped error body. Anthropic callers get their own
// shape from the codec layer; this one covers the control plane and the
// OpenAI-family data plane.
func APIError(message, kind string) map[string]any {
	return map[string]any{"error": map[string]any{"message": message, "type": kind}}
}

// SendError writes an OpenAI-shaped error body.
func SendError(w http.ResponseWriter, status int, message, kind string) {
	SendJSON(w, status, APIError(message, kind))
}

// WriteSSEHeaders sets the headers a streaming response needs. Every caller
// must then flush after each event.
func WriteSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
}

// SSEWriter writes server-sent events and flushes after each one.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	started bool
}

// NewSSEWriter returns a writer over w. Flushing is a no-op when the response
// writer does not support it (only in tests).
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	flusher, _ := w.(http.Flusher)
	return &SSEWriter{w: w, flusher: flusher}
}

// Started reports whether a byte has been written, which decides whether an
// error may still change the HTTP status.
func (s *SSEWriter) Started() bool { return s.started }

// Start writes the SSE headers and flushes them, so the client sees the stream
// open before the first token arrives.
func (s *SSEWriter) Start() {
	if s.started {
		return
	}
	WriteSSEHeaders(s.w)
	s.w.WriteHeader(http.StatusOK)
	s.flush()
	s.started = true
}

// Event writes one "data: <payload>" event.
func (s *SSEWriter) Event(payload string) error {
	s.Start()
	_, err := fmt.Fprintf(s.w, "data: %s\n\n", payload)
	s.flush()
	return err
}

// Named writes one "event: <name>" + "data: <payload>" event.
func (s *SSEWriter) Named(event, payload string) error {
	s.Start()
	var err error
	if event != "" {
		_, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload)
	} else {
		_, err = fmt.Fprintf(s.w, "data: %s\n\n", payload)
	}
	s.flush()
	return err
}

// Raw writes bytes verbatim (used for the "[DONE]" sentinel).
func (s *SSEWriter) Raw(text string) error {
	s.Start()
	_, err := io.WriteString(s.w, text)
	s.flush()
	return err
}

// Close flushes anything pending. It does not write a terminator: the codec
// decides which sentinel the protocol needs.
func (s *SSEWriter) Close() error {
	s.flush()
	return nil
}

func (s *SSEWriter) flush() {
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// ErrClientGone is a convenience for handlers that want to stop quietly.
var ErrClientGone = errors.New("client disconnected")
