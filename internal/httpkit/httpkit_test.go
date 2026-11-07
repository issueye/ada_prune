package httpkit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadJSONRejectsOversizedBody(t *testing.T) {
	body := strings.NewReader(`{"a":"` + strings.Repeat("x", 100) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)

	_, err := ReadJSON(req, 16)
	var tooLarge *BodyTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want *BodyTooLarge", err)
	}
	if tooLarge.Limit != 16 {
		t.Fatalf("limit = %d", tooLarge.Limit)
	}
}

func TestReadJSONAcceptsBodyAtTheLimit(t *testing.T) {
	payload := `{"a":1}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	raw, err := ReadJSON(req, int64(len(payload)))
	if err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if string(raw) != payload {
		t.Fatalf("raw = %q", raw)
	}
}

func TestDecodeJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"x"}`))
	var v struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(req, 1024, &v); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if v.Name != "x" {
		t.Fatalf("name = %q", v.Name)
	}

	bad := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`))
	if err := DecodeJSON(bad, 1024, &v); err == nil {
		t.Fatal("malformed JSON decoded without an error")
	}
}

func TestSendJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	SendJSON(rec, http.StatusCreated, map[string]any{"ok": true})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content type = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("body = %v", body)
	}
}

func TestSendErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	SendError(rec, http.StatusUnauthorized, "nope", "auth_error")

	var body struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Message != "nope" || body.Error.Type != "auth_error" {
		t.Fatalf("body = %+v", body)
	}
}

func TestSSEWriterEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	sse := NewSSEWriter(rec)

	if sse.Started() {
		t.Fatal("writer reports started before any write")
	}
	if err := sse.Event(`{"x":1}`); err != nil {
		t.Fatalf("Event: %v", err)
	}
	if !sse.Started() {
		t.Fatal("writer does not report started after a write")
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q", got)
	}
	if body := rec.Body.String(); body != "data: {\"x\":1}\n\n" {
		t.Fatalf("body = %q", body)
	}

	if err := sse.Named("ping", `{}`); err != nil {
		t.Fatalf("Named: %v", err)
	}
	if body := rec.Body.String(); !strings.Contains(body, "event: ping\ndata: {}\n\n") {
		t.Fatalf("named event missing: %q", body)
	}

	if err := sse.Raw("data: [DONE]\n\n"); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if body := rec.Body.String(); !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("raw tail = %q", body)
	}
}

func TestSSEWriterStartIsIdempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	sse := NewSSEWriter(rec)
	sse.Start()
	sse.Start()
	if rec.Body.Len() != 0 {
		t.Fatalf("Start wrote a body: %q", rec.Body.String())
	}
}
