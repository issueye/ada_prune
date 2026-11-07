package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

// streamChunks is the canonical stream every round-trip test starts from.
func streamChunks() []*ir.StreamChunk {
	return []*ir.StreamChunk{
		{Kind: ir.ChunkStart, ID: "resp_abc", Model: "test-model"},
		{Kind: ir.ChunkText, Text: "Hel"},
		{Kind: ir.ChunkText, Text: "lo"},
		{Kind: ir.ChunkToolCall, ToolCallID: "call_1", ToolName: "shell_output", ArgsComplete: `{"command":"ls"}`},
		{Kind: ir.ChunkUsage, Usage: &ir.Usage{PromptTokens: 10, CompletionTokens: 5, CachedTokens: 3}},
		{Kind: ir.ChunkFinish, FinishReason: ir.FinishToolCalls},
		{Kind: ir.ChunkDone},
	}
}

type collected struct {
	text      string
	toolIDs   []string
	toolNames []string
	toolArgs  string
	usage     *ir.Usage
	finish    string
	sawStart  bool
	sawDone   bool
}

func collect(t *testing.T, dec StreamDecoder) collected {
	t.Helper()
	var out collected
	for i := 0; i < 200; i++ {
		chunk, err := dec.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		switch chunk.Kind {
		case ir.ChunkStart:
			out.sawStart = true
		case ir.ChunkText:
			out.text += chunk.Text
		case ir.ChunkToolCall:
			if chunk.ToolCallID != "" {
				out.toolIDs = append(out.toolIDs, chunk.ToolCallID)
			}
			if chunk.ToolName != "" {
				out.toolNames = append(out.toolNames, chunk.ToolName)
			}
			out.toolArgs += firstNonEmpty(chunk.ArgsDelta, chunk.ArgsComplete)
		case ir.ChunkUsage:
			out.usage = chunk.Usage
		case ir.ChunkFinish:
			out.finish = chunk.FinishReason
			if chunk.Usage != nil {
				out.usage = chunk.Usage
			}
		case ir.ChunkDone:
			out.sawDone = true
		}
	}
	t.Fatal("stream never terminated")
	return out
}

// TestStreamRoundTripMatrix encodes the canonical stream with each protocol and
// decodes it back with the same protocol.
func TestStreamRoundTripMatrix(t *testing.T) {
	for _, protocol := range testProtocols() {
		protocol := protocol
		t.Run(string(protocol), func(t *testing.T) {
			c := MustGet(protocol)
			var buf bytes.Buffer
			enc := c.NewStreamEncoder(&buf)
			for _, chunk := range streamChunks() {
				if err := enc.Write(chunk); err != nil {
					t.Fatalf("encode: %v", err)
				}
			}
			if err := enc.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}

			got := collect(t, c.NewStreamDecoder(bytes.NewReader(buf.Bytes())))

			if !got.sawStart {
				t.Error("no start chunk")
			}
			if got.text != "Hello" {
				t.Errorf("text = %q, want Hello", got.text)
			}
			if len(got.toolIDs) == 0 || got.toolIDs[0] != "call_1" {
				t.Errorf("tool ids = %v", got.toolIDs)
			}
			if len(got.toolNames) == 0 || got.toolNames[0] != "shell_output" {
				t.Errorf("tool names = %v", got.toolNames)
			}
			if !strings.Contains(got.toolArgs, `"command"`) {
				t.Errorf("tool arguments = %q", got.toolArgs)
			}
			if got.finish != ir.FinishToolCalls {
				t.Errorf("finish = %q, want tool_calls", got.finish)
			}
			if got.usage == nil {
				t.Fatal("no usage was reported")
			}
			if got.usage.PromptTokens != 10 || got.usage.CompletionTokens != 5 {
				t.Errorf("usage = %+v", got.usage)
			}
		})
	}
}

// TestStreamWireShapes asserts the protocol-specific framing clients depend on.
func TestStreamWireShapes(t *testing.T) {
	cases := map[ir.Protocol]struct {
		mustContain []string
	}{
		ir.ProtoOpenAIChat: {
			mustContain: []string{"data: [DONE]", `"object":"chat.completion.chunk"`, `"finish_reason":"tool_calls"`},
		},
		ir.ProtoAnthropic: {
			mustContain: []string{
				"event: message_start",
				"event: content_block_delta",
				"event: message_delta",
				"event: message_stop",
				`"type":"text_delta"`,
				`"stop_reason":"tool_use"`,
			},
		},
		ir.ProtoOpenAIResponses: {
			mustContain: []string{
				"event: response.created",
				"event: response.output_text.delta",
				"event: response.completed",
				`"sequence_number":1`,
			},
		},
	}

	for protocol, want := range cases {
		c := MustGet(protocol)
		var buf bytes.Buffer
		enc := c.NewStreamEncoder(&buf)
		for _, chunk := range streamChunks() {
			if err := enc.Write(chunk); err != nil {
				t.Fatalf("%s encode: %v", protocol, err)
			}
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("%s close: %v", protocol, err)
		}
		body := buf.String()
		for _, needle := range want.mustContain {
			if !strings.Contains(body, needle) {
				t.Errorf("%s stream is missing %q\nbody:\n%s", protocol, needle, body)
			}
		}
	}
}

func TestStreamEncodersAreIdempotentOnClose(t *testing.T) {
	for _, protocol := range testProtocols() {
		c := MustGet(protocol)
		var buf bytes.Buffer
		enc := c.NewStreamEncoder(&buf)
		for _, chunk := range streamChunks() {
			if err := enc.Write(chunk); err != nil {
				t.Fatalf("%s: %v", protocol, err)
			}
		}
		if err := enc.Close(); err != nil {
			t.Fatalf("%s close: %v", protocol, err)
		}
		first := buf.String()
		if err := enc.Close(); err != nil {
			t.Fatalf("%s second close: %v", protocol, err)
		}
		if buf.String() != first {
			t.Errorf("%s wrote more bytes on a second Close", protocol)
		}
	}
}

func TestChatStreamDecoderHandlesReasoningAndUsageOnlyChunks(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	got := collect(t, MustGet(ir.ProtoOpenAIChat).NewStreamDecoder(strings.NewReader(body)))
	if got.text != "answer" {
		t.Fatalf("text = %q", got.text)
	}
	if got.finish != ir.FinishStop {
		t.Fatalf("finish = %q", got.finish)
	}
	if got.usage == nil || got.usage.PromptTokens != 7 {
		t.Fatalf("usage = %+v", got.usage)
	}
	if !got.sawDone {
		t.Fatal("missing terminator")
	}
}

func TestAnthropicStreamTruncationIsAnError(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m1","model":"m","usage":{"input_tokens":5,"output_tokens":0}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		"",
	}, "\n")
	_, err := collectErr(MustGet(ir.ProtoAnthropic).NewStreamDecoder(strings.NewReader(body)))
	if err == nil {
		t.Fatal("a stream without message_stop must be an error")
	}
}

func TestResponsesStreamTruncationIsAnError(t *testing.T) {
	body := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","response":{"id":"r1","model":"m"}}`,
		"",
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		"",
	}, "\n")
	_, err := collectErr(MustGet(ir.ProtoOpenAIResponses).NewStreamDecoder(strings.NewReader(body)))
	if err == nil {
		t.Fatal("a stream without response.completed must be an error")
	}
}

// collectErr drains a decoder and returns the first error.
func collectErr(dec StreamDecoder) (int, error) {
	chunks := 0
	for i := 0; i < 100; i++ {
		_, err := dec.Next()
		if err == io.EOF {
			return chunks, nil
		}
		if err != nil {
			return chunks, err
		}
		chunks++
	}
	return chunks, errors.New("stream never terminated")
}

func TestSSEReaderParsesFields(t *testing.T) {
	body := ": comment\n\nevent: ping\ndata: one\ndata: two\n\nid: 5\ndata: three\n\n"
	r := newSSEReader(strings.NewReader(body))

	first, err := r.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if first.Event != "ping" || first.Data != "one\ntwo" {
		t.Fatalf("first = %+v", first)
	}

	second, err := r.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if second.Event != "" || second.Data != "three" {
		t.Fatalf("second = %+v", second)
	}

	if _, err := r.Next(); err != io.EOF {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestChatRequestEncodesStreamUsageOption(t *testing.T) {
	body, err := MustGet(ir.ProtoOpenAIChat).EncodeRequest(&ir.ChatRequest{
		Model:    "m",
		Stream:   true,
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var raw struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !raw.StreamOptions.IncludeUsage {
		t.Fatal("a streaming request must ask for usage, or the gateway cannot bill it")
	}
}
