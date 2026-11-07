package ir

import "testing"

func TestParseProtocol(t *testing.T) {
	for _, p := range All() {
		got, err := ParseProtocol(string(p))
		if err != nil {
			t.Fatalf("ParseProtocol(%q): %v", p, err)
		}
		if got != p {
			t.Fatalf("ParseProtocol(%q) = %q", p, got)
		}
	}
	if _, err := ParseProtocol("vendor_native"); err == nil {
		t.Fatal("vendor_native must not parse as a downstream protocol")
	}
	if _, err := ParseProtocol(""); err == nil {
		t.Fatal("empty protocol parsed")
	}
}

func TestProtocolValid(t *testing.T) {
	valid := []Protocol{ProtoOpenAIChat, ProtoOpenAIResponses, ProtoAnthropic}
	for _, p := range valid {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	invalid := []Protocol{ProtoVendorNative, Protocol("openai"), Protocol("")}
	for _, p := range invalid {
		if p.Valid() {
			t.Errorf("%q should be invalid downstream", p)
		}
	}
}

func TestNormalizeFinish(t *testing.T) {
	cases := map[string]string{
		"":                              "",
		"stop":                          FinishStop,
		"end_turn":                      FinishStop,
		"length":                        FinishLength,
		"max_tokens":                    FinishLength,
		"max_output_tokens":             FinishLength,
		"model_context_window_exceeded": FinishLength,
		"tool_calls":                    FinishToolCalls,
		"tool-calls":                    FinishToolCalls,
		"tool_use":                      FinishToolCalls,
		"function_call":                 FinishToolCalls,
		"content_filter":                FinishContentFilter,
		"refusal":                       FinishContentFilter,
		"error":                         FinishError,
		"upstream_error":                FinishUpstreamError,
		// Unknown reasons are passed through rather than folded into "stop":
		// a client that sees a strange reason can still tell the answer may be
		// incomplete.
		"pause_turn": "pause_turn",
	}
	for in, want := range cases {
		if got := NormalizeFinish(in); got != want {
			t.Errorf("NormalizeFinish(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsageNormalize(t *testing.T) {
	u := Usage{PromptTokens: 10, CompletionTokens: 5}
	u.Normalize()
	if u.TotalTokens != 15 {
		t.Fatalf("TotalTokens = %d, want 15", u.TotalTokens)
	}

	explicit := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 99}
	explicit.Normalize()
	if explicit.TotalTokens != 99 {
		t.Fatalf("explicit TotalTokens was overwritten: %d", explicit.TotalTokens)
	}
}

func TestSystemText(t *testing.T) {
	req := ChatRequest{System: []Block{
		TextBlock("first"),
		TextBlock("second"),
		{Type: BlockImage},
		TextBlock("third"),
	}}
	if got, want := req.SystemText(), "first\nsecond\nthird"; got != want {
		t.Fatalf("SystemText = %q, want %q", got, want)
	}
}

func TestWarnAppends(t *testing.T) {
	var req ChatRequest
	req.Warn("a")
	req.Warn("b")
	if len(req.Warnings) != 2 || req.Warnings[0] != "a" || req.Warnings[1] != "b" {
		t.Fatalf("warnings = %v", req.Warnings)
	}
}

func TestMessageText(t *testing.T) {
	m := Message{Blocks: []Block{TextBlock("hello "), TextBlock("world"), {Type: BlockImage}}}
	if got := m.Text(); got != "hello world" {
		t.Fatalf("Message.Text = %q", got)
	}
}

func TestChatResponseText(t *testing.T) {
	r := ChatResponse{Blocks: []Block{TextBlock("a"), {Type: BlockThinking}, TextBlock("b")}}
	if got := r.Text(); got != "ab" {
		t.Fatalf("ChatResponse.Text = %q", got)
	}
}
