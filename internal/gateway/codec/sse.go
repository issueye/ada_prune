package codec

import (
	"bufio"
	"io"
	"strings"
)

// sseEvent is one parsed server-sent event.
type sseEvent struct {
	// Event is the event name, empty for unnamed events.
	Event string
	// Data is the concatenated data payload.
	Data string
}

// isDone reports whether the payload is the stream terminator.
func (e *sseEvent) isDone() bool { return strings.TrimSpace(e.Data) == "[DONE]" }

// sseReader turns an SSE body into events. It tolerates comment lines, blank
// lines inside an event, and CRLF endings.
type sseReader struct {
	sc *bufio.Scanner
}

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	// Tool arguments and base64 images can produce long single lines.
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	return &sseReader{sc: sc}
}

// Next returns the next event, or io.EOF at the end of the body.
func (r *sseReader) Next() (*sseEvent, error) {
	var (
		event string
		data  strings.Builder
		seen  bool
	)
	for r.sc.Scan() {
		line := strings.TrimRight(r.sc.Text(), "\r")
		if line == "" {
			if !seen {
				continue
			}
			return &sseEvent{Event: event, Data: data.String()}, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			// A bare line is the field name with an empty value.
			field, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
			seen = true
		case "data":
			if seen && data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			seen = true
		}
	}
	if err := r.sc.Err(); err != nil {
		return nil, err
	}
	if seen {
		// The body ended without a trailing blank line: yield the last event.
		return &sseEvent{Event: event, Data: data.String()}, nil
	}
	return nil, io.EOF
}

// sseWriter writes server-sent events and flushes nothing on its own: the HTTP
// layer owns flushing so the codec stays transport-agnostic.
type sseWriter struct {
	w io.Writer
}

func newSSEWriter(w io.Writer) *sseWriter { return &sseWriter{w: w} }

// Event writes one event, with a name when the protocol uses named events.
func (w *sseWriter) Event(name, data string) error {
	var b strings.Builder
	if name != "" {
		b.WriteString("event: ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	b.WriteString("data: ")
	b.WriteString(data)
	b.WriteString("\n\n")
	_, err := io.WriteString(w.w, b.String())
	return err
}

// Data writes an unnamed event.
func (w *sseWriter) Data(data string) error { return w.Event("", data) }

// flush lets a stream encoder hand control back to the HTTP layer, which calls
// Flush on the response writer. The codec never does I/O beyond writing.
type flusher interface{ Flush() }
