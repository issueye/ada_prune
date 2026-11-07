package command_code

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
)

func decodeAll(t *testing.T, body string) ([]*ir.StreamChunk, error) {
	t.Helper()
	dec := newStreamDecoder(strings.NewReader(body), sampleInvoke())
	var out []*ir.StreamChunk
	for {
		chunk, err := dec.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, chunk)
	}
}

func TestDecodeStreamTextResponse(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"text-delta","text":"Hel"}`,
		`{"type":"text-delta","delta":"lo"}`,
		`{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":11,"outputTokens":2,"totalTokens":13}}`,
		"",
	}, "\n")

	chunks, err := decodeAll(t, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	var text string
	var finish string
	var usage *ir.Usage
	for _, c := range chunks {
		switch c.Kind {
		case ir.ChunkStart:
			if c.Model == "" {
				t.Fatal("start chunk has no model")
			}
		case ir.ChunkText:
			text += c.Text
		case ir.ChunkUsage:
			usage = c.Usage
		case ir.ChunkFinish:
			finish = c.FinishReason
		}
	}
	if text != "Hello" {
		t.Fatalf("text = %q, want Hello", text)
	}
	if finish != ir.FinishStop {
		t.Fatalf("finish = %q, want stop", finish)
	}
	if usage == nil || usage.PromptTokens != 11 || usage.CompletionTokens != 2 || usage.TotalTokens != 13 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestDecodeStreamToolCallCarriesWholeInput(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"tool-call","toolCallId":"call_1","toolName":"shell_output","input":{"command":"ls"}}`,
		`{"type":"finish","finishReason":"tool-calls"}`,
	}, "\n")
	chunks, err := decodeAll(t, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var call *ir.StreamChunk
	for _, c := range chunks {
		if c.Kind == ir.ChunkToolCall {
			call = c
		}
	}
	if call == nil {
		t.Fatal("no tool call chunk")
	}
	if call.ToolCallID != "call_1" || call.ToolName != "shell_output" {
		t.Fatalf("tool call = %+v", call)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.ArgsComplete), &args); err != nil {
		t.Fatalf("arguments are not JSON: %q", call.ArgsComplete)
	}
	if args["command"] != "ls" {
		t.Fatalf("arguments = %v", args)
	}
}

func TestDecodeStreamReasoning(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"reasoning-delta","text":"thinking..."}`,
		`{"type":"text-delta","text":"answer"}`,
		`{"type":"finish","finishReason":"stop"}`,
	}, "\n")
	chunks, err := decodeAll(t, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawThinking bool
	for _, c := range chunks {
		if c.Kind == ir.ChunkThinking && c.Text == "thinking..." {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Fatal("reasoning-delta was not mapped to a thinking chunk")
	}
}

func TestDecodeStreamWithoutFinishIsTruncation(t *testing.T) {
	_, err := decodeAll(t, `{"type":"text-delta","text":"partial"}`)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestDecodeStreamFinishWithoutReasonIsTruncation(t *testing.T) {
	_, err := decodeAll(t, `{"type":"finish"}`)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestDecodeStreamFinishReasonOtherIsNoSignal(t *testing.T) {
	_, err := decodeAll(t, `{"type":"finish","finishReason":"other"}`)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated for an \"other\" reason", err)
	}
}

func TestDecodeStreamNetworkFailureIsTruncation(t *testing.T) {
	// A provider-reported connection failure means the answer is incomplete, so
	// the decoder refuses to present it as a finished stream.
	_, err := decodeAll(t, `{"type":"finish","rawFinishReason":"network-error"}`)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestDecodeStreamErrorEvent(t *testing.T) {
	body := `{"type":"error","error":{"message":"<429> slow down","code":"rate","statusCode":429}}`
	_, err := decodeAll(t, body)
	var streamErr *StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("err = %T %v, want *StreamError", err, err)
	}
	if streamErr.Status != 429 {
		t.Fatalf("status = %d, want 429", streamErr.Status)
	}
	if streamErr.Code != "rate" {
		t.Fatalf("code = %q", streamErr.Code)
	}
}

func TestDecodeStreamZeroOutputZeroesUsage(t *testing.T) {
	body := `{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":900,"outputTokens":0}}`
	chunks, err := decodeAll(t, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range chunks {
		if c.Kind != ir.ChunkUsage || c.Usage == nil {
			continue
		}
		if c.Usage.PromptTokens != 0 || c.Usage.CompletionTokens != 0 {
			t.Fatalf("usage = %+v, want zeroed when no output was produced", c.Usage)
		}
	}
}

func TestAggregateNonStreaming(t *testing.T) {
	body := strings.Join([]string{
		`{"type":"text-delta","text":"Hello "}`,
		`{"type":"text-delta","text":"world"}`,
		`{"type":"tool-call","toolCallId":"c1","toolName":"read_file","input":{"path":"a.txt"}}`,
		`{"type":"finish","finishReason":"tool-calls","totalUsage":{"inputTokens":5,"outputTokens":7}}`,
	}, "\n")

	resp, err := aggregate([]byte(body), sampleInvoke())
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if resp.Text() != "Hello world" {
		t.Fatalf("text = %q", resp.Text())
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.FinishReason != ir.FinishToolCalls {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
	if resp.Usage.CompletionTokens != 7 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestAggregateRejectsTruncatedBody(t *testing.T) {
	_, err := aggregate([]byte(`{"type":"text-delta","text":"partial"}`), sampleInvoke())
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestParseEventSkipsNoise(t *testing.T) {
	for _, line := range []string{"", "   ", "[DONE]", ": comment", "not json", `{"text":"no type"}`} {
		if ev := ParseEvent([]byte(line)); ev != nil {
			t.Errorf("ParseEvent(%q) = %+v, want nil", line, ev)
		}
	}
	if ev := ParseEvent([]byte(`{"type":"text-delta","text":"x"}`)); ev == nil || ev.Text != "x" {
		t.Fatalf("valid event was not parsed: %+v", ev)
	}
}

func TestEventFinishReasonPrecedence(t *testing.T) {
	raw := "max_tokens"
	other := "other"
	stop := "stop"

	if reason, ok := EventFinishReason(&Event{RawFinishReason: &raw, FinishReason: &stop}); !ok || reason != "max_tokens" {
		t.Fatalf("raw reason should win: %q %v", reason, ok)
	}
	if _, ok := EventFinishReason(&Event{FinishReason: &other}); ok {
		t.Fatal("\"other\" without a raw reason must count as no signal")
	}
	if reason, ok := EventFinishReason(&Event{FinishReason: &stop}); !ok || reason != "stop" {
		t.Fatalf("fallback reason = %q %v", reason, ok)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{
		"tool-calls":    ir.FinishToolCalls,
		"tool_use":      ir.FinishToolCalls,
		"max_tokens":    ir.FinishLength,
		"end_turn":      ir.FinishStop,
		"refusal":       ir.FinishContentFilter,
		"network-error": ir.FinishUpstreamError,
		"weird":         "weird",
	}
	for in, want := range cases {
		if got := MapFinishReason(in); got != want {
			t.Errorf("MapFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecoderSurvivesLargeLines(t *testing.T) {
	big := strings.Repeat("x", 300_000)
	body := `{"type":"text-delta","text":"` + big + `"}` + "\n" + `{"type":"finish","finishReason":"stop"}`
	chunks, err := decodeAll(t, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var text string
	for _, c := range chunks {
		if c.Kind == ir.ChunkText {
			text += c.Text
		}
	}
	if len(text) != len(big) {
		t.Fatalf("text length = %d, want %d", len(text), len(big))
	}
}

func TestStreamErrorIsNotSwallowed(t *testing.T) {
	dec := newStreamDecoder(strings.NewReader(`{"type":"error","message":"boom"}`), sampleInvoke())
	// The first call opens the stream; the error surfaces on the next one.
	if chunk, err := dec.Next(); err != nil || chunk.Kind != ir.ChunkStart {
		t.Fatalf("first chunk = %+v, err = %v", chunk, err)
	}
	_, err := dec.Next()
	var streamErr *StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("err = %v, want *StreamError", err)
	}
	if streamErr.Status != 502 {
		t.Fatalf("status = %d, want the 502 default", streamErr.Status)
	}
	_ = gateway.QuotaNone
}
