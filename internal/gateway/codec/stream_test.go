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

// Regression: some OpenAI-compatible gateways fold the finish reason into the
// usage frame instead of sending it separately. The decoder must not drop it.
func TestChatStreamDecoderHandlesUsageAndFinishInOneFrame(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"finish_reason":"stop","delta":{}}],"usage":{"prompt_tokens":35,"completion_tokens":13,"total_tokens":48}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	got := collect(t, MustGet(ir.ProtoOpenAIChat).NewStreamDecoder(strings.NewReader(body)))
	if got.text != "answer" {
		t.Fatalf("text = %q", got.text)
	}
	if got.finish != ir.FinishStop {
		t.Fatalf("finish = %q; want stop (the frame shared with usage must not be dropped)", got.finish)
	}
	if got.usage == nil || got.usage.PromptTokens != 35 || got.usage.CompletionTokens != 13 {
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

// reencodeChatStream decodes an upstream openai_chat SSE body and re-encodes it
// with the same protocol, returning the client-visible wire the relay would
// produce.
func reencodeChatStream(t *testing.T, upstream string) string {
	t.Helper()
	c := MustGet(ir.ProtoOpenAIChat)
	dec := c.NewStreamDecoder(strings.NewReader(upstream))
	var buf bytes.Buffer
	enc := c.NewStreamEncoder(&buf)
	for {
		chunk, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if err := enc.Write(chunk); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.String()
}

// chatWireToolCall is one delta.tool_calls entry as clients see it on the
// openai_chat wire.
type chatWireToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatWireToolDeltas collects every delta.tool_calls entry of an openai_chat
// wire dump, in order.
func chatWireToolDeltas(t *testing.T, wire string) []chatWireToolCall {
	t.Helper()
	var out []chatWireToolCall
	for _, line := range strings.Split(wire, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					ToolCalls []chatWireToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatalf("wire frame is not valid JSON: %v\n%s", err, line)
		}
		if len(frame.Choices) > 0 {
			out = append(out, frame.Choices[0].Delta.ToolCalls...)
		}
	}
	return out
}

// TestChatToolCallFragmentsShareOneWireIndex replays the fragmented tool-call
// stream real OpenAI-compatible upstreams send — one call, many argument
// fragments — and asserts the client-visible wire keeps a single tool_calls
// index, so the fragments reassemble into valid JSON instead of each becoming
// a malformed standalone call.
func TestChatToolCallFragmentsShareOneWireIndex(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_files","arguments":""}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\".\"}"}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	deltas := chatWireToolDeltas(t, reencodeChatStream(t, upstream))
	if len(deltas) == 0 {
		t.Fatal("no tool call deltas on the wire")
	}
	ids := map[string]bool{}
	args := ""
	for _, d := range deltas {
		if d.Index != 0 {
			t.Errorf("fragment got tool_calls index %d, want the shared index 0", d.Index)
		}
		if d.ID != "" {
			ids[d.ID] = true
		}
		args += d.Function.Arguments
	}
	if len(ids) != 1 {
		t.Errorf("call ids on the wire: %v, want exactly one", ids)
	}
	if args != `{"path":"."}` {
		t.Errorf("reassembled arguments = %q, want {\"path\":\".\"}", args)
	}
	if !json.Valid([]byte(args)) {
		t.Errorf("reassembled arguments %q are not valid JSON", args)
	}
}

// TestChatStreamParallelToolCallsKeepDistinctIndexes feeds two interleaved
// tool calls and asserts their fragments land on distinct wire indexes with
// arguments kept apart.
func TestChatStreamParallelToolCallsKeepDistinctIndexes(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_files","arguments":""}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a\"}"}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"path\":\"b\"}"}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	deltas := chatWireToolDeltas(t, reencodeChatStream(t, upstream))
	argsByIndex := map[int]string{}
	idByIndex := map[int]string{}
	for _, d := range deltas {
		if d.ID != "" {
			if prev, seen := idByIndex[d.Index]; seen {
				t.Errorf("index %d carried ids %q and %q", d.Index, prev, d.ID)
			}
			idByIndex[d.Index] = d.ID
		}
		argsByIndex[d.Index] += d.Function.Arguments
	}
	if len(idByIndex) != 2 {
		t.Fatalf("wire carries calls for indexes %v, want two distinct calls", idByIndex)
	}
	if got := argsByIndex[0]; got != `{"path":"a"}` {
		t.Errorf("index 0 arguments = %q, want {\"path\":\"a\"}", got)
	}
	if got := argsByIndex[1]; got != `{"path":"b"}` {
		t.Errorf("index 1 arguments = %q, want {\"path\":\"b\"}", got)
	}
}

// TestChatStreamEncoderEmitsUsageOnClose covers the relay's shape: the relay
// never forwards the decoder's terminal Done chunk, it only closes the encoder.
// A streaming chat client must still receive the usage frame before [DONE],
// otherwise its token accounting (speed and context share) sees nothing.
func TestChatStreamEncoderEmitsUsageOnClose(t *testing.T) {
	c := MustGet(ir.ProtoOpenAIChat)
	var buf bytes.Buffer
	enc := c.NewStreamEncoder(&buf)
	chunks := []*ir.StreamChunk{
		{Kind: ir.ChunkStart, ID: "c1", Model: "m"},
		{Kind: ir.ChunkText, Text: "hi"},
		{Kind: ir.ChunkUsage, Usage: &ir.Usage{PromptTokens: 10, CompletionTokens: 5}},
		{Kind: ir.ChunkFinish, FinishReason: ir.FinishStop},
	}
	for _, chunk := range chunks {
		if err := enc.Write(chunk); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	body := buf.String()
	usageAt := strings.Index(body, `"prompt_tokens":10`)
	doneAt := strings.Index(body, "data: [DONE]")
	if usageAt < 0 {
		t.Fatalf("Close dropped the usage frame\nbody:\n%s", body)
	}
	if doneAt < 0 || usageAt > doneAt {
		t.Fatalf("usage must precede [DONE]\nbody:\n%s", body)
	}

	got := collect(t, c.NewStreamDecoder(strings.NewReader(body)))
	if got.usage == nil || got.usage.PromptTokens != 10 || got.usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v, want 10/5", got.usage)
	}
}

// TestChatStreamCompleteCallsWithoutIDsStaySeparate replays an upstream that
// sends several complete tool calls in one stream but omits the call id and
// reuses index 0 for every call. Each call must land on its own wire index;
// folding them together makes the client concatenate two JSON objects into one
// arguments string, which it then rejects as malformed.
func TestChatStreamCompleteCallsWithoutIDsStaySeparate(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"name":"read","arguments":"{\"file_path\":\"internal/engine/context.go\",\"limit\":80}"}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":"function","function":{"name":"read","arguments":"{\"file_path\":\"internal/plugins/host.go\"}"}}]}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	deltas := chatWireToolDeltas(t, reencodeChatStream(t, upstream))
	argsByIndex := map[int]string{}
	for _, d := range deltas {
		argsByIndex[d.Index] += d.Function.Arguments
	}
	if len(argsByIndex) != 2 {
		t.Fatalf("wire carries %d calls, want 2 distinct calls: %v", len(argsByIndex), argsByIndex)
	}
	for index, args := range argsByIndex {
		if !json.Valid([]byte(args)) {
			t.Errorf("call %d arguments are not valid JSON: %q", index, args)
		}
		if strings.Contains(args, "}{") {
			t.Errorf("call %d arguments hold two objects: %q", index, args)
		}
	}
}

// TestChatStreamArgsCompleteChunksWithoutIDStaySeparate covers the vendor
// adapter path: the upstream delivers a whole call per event and names no id,
// so the encoder must open a new slot per complete call.
func TestChatStreamArgsCompleteChunksWithoutIDStaySeparate(t *testing.T) {
	c := MustGet(ir.ProtoOpenAIChat)
	var buf bytes.Buffer
	enc := c.NewStreamEncoder(&buf)
	chunks := []*ir.StreamChunk{
		{Kind: ir.ChunkStart, ID: "c1", Model: "m"},
		{Kind: ir.ChunkToolCall, ToolName: "read", ArgsComplete: `{"file_path":"internal/engine/context.go","limit":80}`},
		{Kind: ir.ChunkToolCall, ToolName: "read", ArgsComplete: `{"file_path":"internal/plugins/host.go"}`},
		{Kind: ir.ChunkFinish, FinishReason: ir.FinishToolCalls},
	}
	for _, chunk := range chunks {
		if err := enc.Write(chunk); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	deltas := chatWireToolDeltas(t, buf.String())
	argsByIndex := map[int]string{}
	for _, d := range deltas {
		argsByIndex[d.Index] += d.Function.Arguments
	}
	if len(argsByIndex) != 2 {
		t.Fatalf("wire carries %d calls, want 2 distinct calls: %v", len(argsByIndex), argsByIndex)
	}
	for index, args := range argsByIndex {
		if !json.Valid([]byte(args)) {
			t.Errorf("call %d arguments are not valid JSON: %q", index, args)
		}
	}
}
