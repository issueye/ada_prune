package codec

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

func testProtocols() []ir.Protocol {
	return []ir.Protocol{ir.ProtoOpenAIChat, ir.ProtoOpenAIResponses, ir.ProtoAnthropic}
}

// baseRequest is the canonical request every conversion test starts from.
func baseRequest() *ir.ChatRequest {
	temp := 0.2
	maxTokens := 512
	return &ir.ChatRequest{
		Model: "test-model",
		System: []ir.Block{
			ir.TextBlock("You are concise."),
		},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hello there")}},
			{
				Role: ir.RoleAssistant,
				Blocks: []ir.Block{
					ir.TextBlock("calling a tool"),
					{Type: ir.BlockThinking, Thinking: &ir.ThinkingBlock{Text: "need the shell"}},
				},
				ToolCalls: []ir.ToolCall{{ID: "call_1", Name: "shell_output", Arguments: `{"command":"ls"}`}},
			},
			{Role: ir.RoleTool, ToolCallID: "call_1", Name: "shell_output",
				Blocks: []ir.Block{{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ToolUseID: "call_1",
					Content:   []ir.Block{ir.TextBlock("file.txt")},
				}}}},
			{Role: ir.RoleUser, Blocks: []ir.Block{
				ir.TextBlock("and this image"),
				{Type: ir.BlockImage, Image: &ir.ImageBlock{URL: "https://example.com/a.png"}},
			}},
		},
		Tools: []ir.ToolDef{{
			Name:        "shell_output",
			Description: "run a command",
			Parameters:  map[string]any{"type": "object"},
		}},
		ToolChoice:  &ir.ToolChoice{Mode: ir.ToolChoiceAuto},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
		Stream:      true,
		Stop:        []string{"END"},
	}
}

// snapshot is the lossy-tolerant view compared across conversions. It records
// exactly the fields a gateway must not drop.
type snapshot struct {
	Model      string
	System     string
	Roles      []string
	Text       []string
	Thinking   string
	ToolCalls  []string
	ToolResult []string
	Tools      []string
	Images     int
	MaxTokens  int
	Stream     bool
}

func snapshotOf(req *ir.ChatRequest) snapshot {
	out := snapshot{
		Model:    req.Model,
		System:   req.SystemText(),
		Thinking: "",
		Stream:   req.Stream,
	}
	if req.MaxTokens != nil {
		out.MaxTokens = *req.MaxTokens
	}
	for _, m := range req.Messages {
		out.Roles = append(out.Roles, m.Role)
		// A tool turn's payload lives in ToolResult below; the plain text view is
		// protocol-specific (chat uses text blocks, the canonical form uses
		// tool_result blocks), so it is deliberately normalised away.
		text := m.Text()
		if m.Role == ir.RoleTool {
			text = ""
		}
		out.Text = append(out.Text, text)
		for _, b := range m.Blocks {
			switch b.Type {
			case ir.BlockThinking:
				if b.Thinking != nil {
					out.Thinking += b.Thinking.Text
				}
			case ir.BlockImage:
				out.Images++
			}
		}
		for _, tc := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, tc.ID+"|"+tc.Name+"|"+tc.Arguments)
		}
		if m.Role == ir.RoleTool {
			out.ToolResult = append(out.ToolResult, m.ToolCallID+"|"+toolResultText(m.Blocks))
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, t.Name)
	}
	return out
}

// roundTrip encodes a canonical request with from, decodes it back, and encodes
// the result with to. It returns the IR as `to` sees it.
func roundTrip(t *testing.T, from, to ir.Protocol, req *ir.ChatRequest) *ir.ChatRequest {
	t.Helper()
	fromCodec, err := Get(from)
	if err != nil {
		t.Fatalf("codec %s: %v", from, err)
	}
	toCodec, err := Get(to)
	if err != nil {
		t.Fatalf("codec %s: %v", to, err)
	}

	firstBody, err := fromCodec.EncodeRequest(req)
	if err != nil {
		t.Fatalf("%s.EncodeRequest: %v", from, err)
	}
	decoded, err := fromCodec.DecodeRequest(firstBody)
	if err != nil {
		t.Fatalf("%s.DecodeRequest: %v\nbody: %s", from, err, firstBody)
	}
	secondBody, err := toCodec.EncodeRequest(decoded)
	if err != nil {
		t.Fatalf("%s.EncodeRequest: %v", to, err)
	}
	again, err := toCodec.DecodeRequest(secondBody)
	if err != nil {
		t.Fatalf("%s.DecodeRequest: %v\nbody: %s", to, err, secondBody)
	}
	return again
}

func TestAllProtocolsRegistered(t *testing.T) {
	got := Registered()
	// Registered() documents a sorted result, so compare against sorted order.
	want := []ir.Protocol{ir.ProtoAnthropic, ir.ProtoOpenAIChat, ir.ProtoOpenAIResponses}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered = %v, want %v", got, want)
	}
	if _, err := Get(ir.Protocol("nope")); err == nil {
		t.Fatal("Get accepted an unknown protocol")
	}
	if _, err := Get(ir.ProtoVendorNative); err == nil {
		t.Fatal("vendor_native must not have a downstream codec")
	}
}

// TestRequestConversionMatrix walks all nine from/to combinations.
func TestRequestConversionMatrix(t *testing.T) {
	base := baseRequest()
	want := snapshotOf(base)

	for _, from := range testProtocols() {
		for _, to := range testProtocols() {
			from, to := from, to
			t.Run(string(from)+"__to__"+string(to), func(t *testing.T) {
				got := snapshotOf(roundTrip(t, from, to, base))

				if got.Model != want.Model {
					t.Errorf("model = %q, want %q", got.Model, want.Model)
				}
				if got.System != want.System {
					t.Errorf("system = %q, want %q", got.System, want.System)
				}
				if !reflect.DeepEqual(got.Roles, want.Roles) {
					t.Errorf("roles = %v, want %v", got.Roles, want.Roles)
				}
				if !reflect.DeepEqual(got.Text, want.Text) {
					t.Errorf("text = %v, want %v", got.Text, want.Text)
				}
				if got.Thinking != want.Thinking {
					t.Errorf("thinking = %q, want %q", got.Thinking, want.Thinking)
				}
				if !reflect.DeepEqual(got.ToolCalls, want.ToolCalls) {
					t.Errorf("tool calls = %v, want %v", got.ToolCalls, want.ToolCalls)
				}
				if !reflect.DeepEqual(got.ToolResult, want.ToolResult) {
					t.Errorf("tool results = %v, want %v", got.ToolResult, want.ToolResult)
				}
				if !reflect.DeepEqual(got.Tools, want.Tools) {
					t.Errorf("tools = %v, want %v", got.Tools, want.Tools)
				}
				if got.Images != want.Images {
					t.Errorf("images = %d, want %d", got.Images, want.Images)
				}
				if got.MaxTokens != want.MaxTokens {
					t.Errorf("max tokens = %d, want %d", got.MaxTokens, want.MaxTokens)
				}
				if got.Stream != want.Stream {
					t.Errorf("stream = %v, want %v", got.Stream, want.Stream)
				}
			})
		}
	}
}

func TestConvertRequestHelper(t *testing.T) {
	base := baseRequest()
	chatBody, err := MustGet(ir.ProtoOpenAIChat).EncodeRequest(base)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	anthropicBody, err := ConvertRequest(ir.ProtoOpenAIChat, ir.ProtoAnthropic, chatBody)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(anthropicBody, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw["max_tokens"] == nil {
		t.Fatal("anthropic body has no max_tokens")
	}
	if raw["system"] != "You are concise." {
		t.Fatalf("system = %v", raw["system"])
	}
}

func TestAnthropicDefaultsMaxTokens(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req, err := MustGet(ir.ProtoAnthropic).DecodeRequest(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.MaxTokens == nil || *req.MaxTokens != anthropicDefaultMaxTokens {
		t.Fatalf("max tokens = %v, want the protocol default", req.MaxTokens)
	}

	out, err := MustGet(ir.ProtoAnthropic).EncodeRequest(&ir.ChatRequest{
		Model:    "m",
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(out, &raw)
	if raw["max_tokens"] != float64(anthropicDefaultMaxTokens) {
		t.Fatalf("encoded max_tokens = %v", raw["max_tokens"])
	}
}

func TestAnthropicClampsTemperature(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"temperature":2.5,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := MustGet(ir.ProtoAnthropic).DecodeRequest(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Temperature == nil || *req.Temperature != 1 {
		t.Fatalf("temperature = %v, want 1", req.Temperature)
	}
	if len(req.Warnings) == 0 {
		t.Fatal("clamping should be recorded as a warning")
	}
}

// baseResponse is the canonical response every conversion test starts from.
func baseResponse() *ir.ChatResponse {
	return &ir.ChatResponse{
		ID:    "resp_test",
		Model: "test-model",
		Blocks: []ir.Block{
			ir.TextBlock("the answer"),
			{Type: ir.BlockThinking, Thinking: &ir.ThinkingBlock{Text: "reasoning", Signature: "sig"}},
		},
		ToolCalls:    []ir.ToolCall{{ID: "call_9", Name: "shell_output", Arguments: `{"command":"pwd"}`}},
		FinishReason: ir.FinishToolCalls,
		Usage: ir.Usage{
			PromptTokens:     100,
			CompletionTokens: 20,
			CachedTokens:     40,
		},
	}
}

// TestResponseConversionMatrix walks the response side of all nine pairs.
func TestResponseConversionMatrix(t *testing.T) {
	base := baseResponse()

	for _, from := range testProtocols() {
		for _, to := range testProtocols() {
			from, to := from, to
			t.Run(string(from)+"__to__"+string(to), func(t *testing.T) {
				fromCodec := MustGet(from)
				toCodec := MustGet(to)

				body, err := fromCodec.EncodeResponse(base)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				decoded, err := fromCodec.DecodeResponse(body)
				if err != nil {
					t.Fatalf("decode: %v\nbody: %s", err, body)
				}
				second, err := toCodec.EncodeResponse(decoded)
				if err != nil {
					t.Fatalf("re-encode: %v", err)
				}
				got, err := toCodec.DecodeResponse(second)
				if err != nil {
					t.Fatalf("re-decode: %v\nbody: %s", err, second)
				}

				if text := got.Text(); text != "the answer" {
					t.Errorf("text = %q", text)
				}
				if thinking := thinkingText(got.Blocks); thinking != "reasoning" {
					t.Errorf("thinking = %q", thinking)
				}
				if len(got.ToolCalls) != 1 {
					t.Fatalf("tool calls = %+v", got.ToolCalls)
				}
				if got.ToolCalls[0].ID != "call_9" || got.ToolCalls[0].Name != "shell_output" {
					t.Errorf("tool call = %+v", got.ToolCalls[0])
				}
				if got.FinishReason != ir.FinishToolCalls {
					t.Errorf("finish = %q", got.FinishReason)
				}
				if got.Usage.CompletionTokens != base.Usage.CompletionTokens {
					t.Errorf("completion tokens = %d", got.Usage.CompletionTokens)
				}
				// The input total must survive the protocol-specific split.
				if got.Usage.PromptTokens != base.Usage.PromptTokens {
					t.Errorf("prompt tokens = %d, want %d", got.Usage.PromptTokens, base.Usage.PromptTokens)
				}
				if got.Usage.CachedTokens != base.Usage.CachedTokens {
					t.Errorf("cached tokens = %d, want %d", got.Usage.CachedTokens, base.Usage.CachedTokens)
				}
			})
		}
	}
}

func TestAnthropicUsageExcludesCachedInput(t *testing.T) {
	// Anthropic's input_tokens is the non-cached share; the total is rebuilt on
	// decode. Getting this wrong makes clients double count the cache.
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"m",
		"content":[{"type":"text","text":"hi"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":30,"output_tokens":5,"cache_read_input_tokens":70,"cache_creation_input_tokens":0}
	}`)
	resp, err := MustGet(ir.ProtoAnthropic).DecodeResponse(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Usage.PromptTokens != 100 {
		t.Fatalf("prompt tokens = %d, want 100 (30 uncached + 70 cached)", resp.Usage.PromptTokens)
	}
	if resp.Usage.CachedTokens != 70 {
		t.Fatalf("cached = %d, want 70", resp.Usage.CachedTokens)
	}

	encoded, err := MustGet(ir.ProtoAnthropic).EncodeResponse(&ir.ChatResponse{
		Model: "m", Blocks: []ir.Block{ir.TextBlock("hi")},
		Usage: ir.Usage{PromptTokens: 100, CachedTokens: 70, CompletionTokens: 5},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var raw struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw.Usage.InputTokens != 30 {
		t.Fatalf("encoded input_tokens = %d, want 30", raw.Usage.InputTokens)
	}
}

func TestResponsesUsageIncludesCachedInput(t *testing.T) {
	// The opposite of Anthropic: Responses reports the total input.
	body := []byte(`{
		"id":"resp_1","object":"response","status":"completed","model":"m",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],
		"usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,
		         "input_tokens_details":{"cached_tokens":70}}
	}`)
	resp, err := MustGet(ir.ProtoOpenAIResponses).DecodeResponse(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Usage.PromptTokens != 100 {
		t.Fatalf("prompt tokens = %d, want 100 (total, cache included)", resp.Usage.PromptTokens)
	}
	if resp.Usage.CachedTokens != 70 {
		t.Fatalf("cached = %d, want 70", resp.Usage.CachedTokens)
	}
}

func TestDecodeRejectsMalformedBodies(t *testing.T) {
	for _, protocol := range testProtocols() {
		if _, err := MustGet(protocol).DecodeRequest([]byte(`{"model":`)); err == nil {
			t.Errorf("%s accepted malformed JSON", protocol)
		}
	}
}

func TestChatToolChoiceForms(t *testing.T) {
	cases := []struct {
		body string
		want ir.ToolChoice
	}{
		{`{"tool_choice":"auto"}`, ir.ToolChoice{Mode: ir.ToolChoiceAuto}},
		{`{"tool_choice":"none"}`, ir.ToolChoice{Mode: ir.ToolChoiceNone}},
		{`{"tool_choice":"required"}`, ir.ToolChoice{Mode: ir.ToolChoiceRequired}},
		{`{"tool_choice":{"type":"function","function":{"name":"f"}}}`, ir.ToolChoice{Mode: ir.ToolChoiceTool, Name: "f"}},
		{`{"tool_choice":{"type":"function","name":"g"}}`, ir.ToolChoice{Mode: ir.ToolChoiceTool, Name: "g"}},
	}
	for _, c := range cases {
		body := []byte(`{"model":"m","messages":[],` + c.body[1:])
		req, err := MustGet(ir.ProtoOpenAIChat).DecodeRequest(body)
		if err != nil {
			t.Fatalf("decode %s: %v", c.body, err)
		}
		if req.ToolChoice == nil || *req.ToolChoice != c.want {
			t.Errorf("%s -> %+v, want %+v", c.body, req.ToolChoice, c.want)
		}
	}
}

func TestResponsesInputForms(t *testing.T) {
	// A bare string input is one user message.
	req, err := MustGet(ir.ProtoOpenAIResponses).DecodeRequest([]byte(`{"model":"m","input":"hello"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Text() != "hello" {
		t.Fatalf("messages = %+v", req.Messages)
	}

	// Item arrays carry tool calls and their outputs.
	body := `{"model":"m","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"run it"}]},
		{"type":"function_call","name":"shell_output","arguments":"{\"command\":\"ls\"}","call_id":"c1"},
		{"type":"function_call_output","call_id":"c1","output":"done"}
	]}`
	req, err = MustGet(ir.ProtoOpenAIResponses).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var calls, results int
	for _, m := range req.Messages {
		calls += len(m.ToolCalls)
		if m.Role == ir.RoleTool {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("calls = %d results = %d messages = %+v", calls, results, req.Messages)
	}
}

func TestAnthropicToolResultTurns(t *testing.T) {
	body := `{
		"model":"m","max_tokens":10,
		"messages":[
			{"role":"user","content":"do it"},
			{"role":"assistant","content":[
				{"type":"text","text":"ok"},
				{"type":"tool_use","id":"tu_1","name":"shell_output","input":{"command":"ls"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"tu_1","content":[{"type":"text","text":"done"}]}
			]}
		]
	}`
	req, err := MustGet(ir.ProtoAnthropic).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var toolTurns int
	for _, m := range req.Messages {
		if m.Role == ir.RoleAssistant && len(m.ToolCalls) == 1 && m.ToolCalls[0].Name == "shell_output" {
			toolTurns++
		}
	}
	if toolTurns != 1 {
		t.Fatalf("assistant tool call missing: %+v", req.Messages)
	}
	if req.Messages[len(req.Messages)-1].Role != ir.RoleTool {
		t.Fatalf("tool result was not lifted into its own turn: %+v", req.Messages)
	}
}

func TestSystemBlockVariants(t *testing.T) {
	// Chat: system role messages.
	chatBody := `{"model":"m","messages":[
		{"role":"system","content":"a"},{"role":"system","content":"b"},
		{"role":"user","content":"hi"}]}`
	req, err := MustGet(ir.ProtoOpenAIChat).DecodeRequest([]byte(chatBody))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := req.SystemText(); got != "a\nb" {
		t.Fatalf("system = %q", got)
	}

	// Anthropic: string or block array.
	anthropicBody := `{"model":"m","max_tokens":10,"system":[{"type":"text","text":"x"}],
		"messages":[{"role":"user","content":"hi"}]}`
	req, err = MustGet(ir.ProtoAnthropic).DecodeRequest([]byte(anthropicBody))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := req.SystemText(); got != "x" {
		t.Fatalf("anthropic system = %q", got)
	}
}

// Regression: a compatible gateway may split one assistant turn across two
// choices — the prose in choices[0], the tool calls in choices[1], and the
// finish reason only on the latter. Decoding choices[0] alone silently drops
// either the prose or the tool call.
func TestChatDecodeMergesSplitChoices(t *testing.T) {
	body := []byte(`{
		"id":"c1","model":"m",
		"choices":[
			{"index":0,"message":{"role":"assistant","content":"intro"},"finish_reason":null},
			{"index":1,"message":{"role":"assistant","content":null,"tool_calls":[
				{"index":0,"id":"call_1","type":"function","function":{"name":"get_time","arguments":"{}"}}
			]},"finish_reason":"tool_calls"}
		],
		"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
	}`)
	resp, err := MustGet(ir.ProtoOpenAIChat).DecodeResponse(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp.Text(); got != "intro" {
		t.Fatalf("text = %q", got)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Name != "get_time" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if resp.FinishReason != ir.FinishToolCalls {
		t.Fatalf("finish = %q, want tool_calls", resp.FinishReason)
	}
}

// Regression: reasoning may travel as reasoning_details instead of the
// DeepSeek-style reasoning_content. Dropping it leaves agents with no thinking.
func TestChatDecodeAcceptsReasoningDetails(t *testing.T) {
	body := []byte(`{"id":"c1","model":"m","choices":[{"index":0,"finish_reason":"stop","message":{
		"role":"assistant","content":"answer",
		"reasoning_details":[
			{"type":"reasoning.summary","summary":"first "},
			{"type":"reasoning.text","text":"second"}
		]}}]}`)
	resp, err := MustGet(ir.ProtoOpenAIChat).DecodeResponse(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := thinkingText(resp.Blocks); got != "first second" {
		t.Fatalf("thinking = %q", got)
	}
	if got := resp.Text(); got != "answer" {
		t.Fatalf("text = %q", got)
	}
}
