package command_code

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/provider"
)

func sampleInvoke() *gateway.InvokeRequest {
	return &gateway.InvokeRequest{
		Provider: gateway.ProviderConfig{
			ID:   1,
			Name: "Command Code 主号",
			Kind: provider.KindCommandCode,
			AdapterConfig: map[string]any{
				"cliMode":      "agent",
				"projectDir":   DefaultProjectDir,
				"apiBase":      DefaultAPIBase,
				"sessionTtl":   12,
				"modelCatalog": "hybrid",
			},
		},
		Credential: gateway.Credential{ID: 7, Name: "主号", Secret: fixedSecret},
		Model: gateway.ModelRef{
			ID:               10,
			PublicName:       "deepseek/deepseek-v4-flash",
			UpstreamName:     "deepseek/deepseek-v4-flash",
			UpstreamProtocol: ir.ProtoVendorNative,
			MaxTokens:        64000,
		},
		Session: gateway.SessionHints{PromptCacheKey: ""},
		Request: sampleRequest(),
	}
}

func sampleRequest() *ir.ChatRequest {
	temp := 0.3
	maxTokens := 4096
	return &ir.ChatRequest{
		System: []ir.Block{ir.TextBlock("You are a careful assistant.")},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hello")}},
			{
				Role:      ir.RoleAssistant,
				Blocks:    []ir.Block{ir.TextBlock("checking")},
				ToolCalls: []ir.ToolCall{{ID: "call_1", Name: "bash_output", Arguments: `{"id":"x"}`}},
			},
			{Role: ir.RoleTool, ToolCallID: "call_1", Name: "bash_output",
				Blocks: []ir.Block{{Type: ir.BlockToolResult, Text: "done"}}},
		},
		Tools: []ir.ToolDef{{
			Name:        "read_multiple_files",
			Description: "read files",
			Parameters:  map[string]any{"type": "object"},
		}},
		ToolChoice:  &ir.ToolChoice{Mode: ir.ToolChoiceAuto},
		Temperature: &temp,
		MaxTokens:   &maxTokens,
		Stream:      true,
		Metadata:    map[string]any{"reasoning_effort": "high"},
	}
}

func buildSample(t *testing.T) []byte {
	t.Helper()
	in := sampleInvoke()
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	env := BuildRequest(sampleRequest(), in, BuildOptions{
		Config:   cfg,
		ThreadID: "0f0e0d0c-0b0a-4908-a706-050403020100",
		Now:      func() time.Time { return time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC) },
	})
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func TestEnvelopeGolden(t *testing.T) {
	checkGolden(t, "envelope.json", buildSample(t))
}

func TestEnvelopeKeyOrder(t *testing.T) {
	raw := string(buildSample(t))
	// Field order is part of the wire contract, so assert the sequence rather
	// than trusting the struct declaration alone.
	order := []string{`"config"`, `"memory"`, `"taste"`, `"skills"`, `"permissionMode"`, `"threadId"`, `"mode"`, `"params"`}
	last := -1
	for _, key := range order {
		idx := strings.Index(raw, key)
		if idx < 0 {
			t.Fatalf("envelope is missing %s:\n%s", key, raw)
		}
		if idx < last {
			t.Fatalf("%s is out of order in:\n%s", key, raw)
		}
		last = idx
	}
}

func TestEnvelopeAlwaysStreams(t *testing.T) {
	var decoded Request
	if err := json.Unmarshal(buildSample(t), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Params.Stream {
		t.Fatal("params.stream must be true: the upstream has no non-streaming mode")
	}
}

func TestEnvelopeToolNameRewrites(t *testing.T) {
	var decoded Request
	if err := json.Unmarshal(buildSample(t), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(decoded.Params.Tools) != 1 || decoded.Params.Tools[0].Name != "read_file" {
		t.Fatalf("tool definition was not renamed: %+v", decoded.Params.Tools)
	}

	var sawToolCall bool
	for _, m := range decoded.Params.Messages {
		for _, b := range m.Content {
			if b.Type == "tool-call" {
				sawToolCall = true
				if b.ToolName != "shell_output" {
					t.Fatalf("tool call name = %q, want shell_output", b.ToolName)
				}
			}
		}
	}
	if !sawToolCall {
		t.Fatal("no tool-call block was emitted")
	}
}

func TestEnvelopeEmptySystemPlaceholder(t *testing.T) {
	in := sampleInvoke()
	cfg, _ := parseConfig(in.Provider)

	env := BuildRequest(&ir.ChatRequest{
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
	}, in, BuildOptions{Config: cfg})
	if len(env.Params.System) != 1 || env.Params.System[0].Text != " " {
		t.Fatalf("placeholder system = %+v, want a single space", env.Params.System)
	}

	cfg.EmptySystemPlaceholder = false
	env = BuildRequest(&ir.ChatRequest{
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
	}, in, BuildOptions{Config: cfg})
	if len(env.Params.System) != 0 {
		t.Fatalf("system = %+v, want none when the placeholder is off", env.Params.System)
	}
}

func TestEnvelopeCacheBreakpoint(t *testing.T) {
	in := sampleInvoke()
	in.Session.PromptCacheKey = "cache-key-abcdefgh"
	cfg, _ := parseConfig(in.Provider)

	env := BuildRequest(&ir.ChatRequest{
		System:   []ir.Block{ir.TextBlock("first"), ir.TextBlock("second")},
		Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
	}, in, BuildOptions{Config: cfg})

	if len(env.Params.System) != 2 {
		t.Fatalf("system blocks = %d", len(env.Params.System))
	}
	// Only the last block carries the marker.
	if len(env.Params.System[0].CacheControl) != 0 {
		t.Fatal("the first system block got a cache marker")
	}
	if string(env.Params.System[1].CacheControl) != `{"type":"ephemeral"}` {
		t.Fatalf("last system cache_control = %s", env.Params.System[1].CacheControl)
	}
	// Non-final blocks gain a newline so the breakpoints survive concatenation.
	if env.Params.System[0].Text != "first\n" {
		t.Fatalf("first system text = %q, want a trailing newline", env.Params.System[0].Text)
	}
}

func TestEnvelopeMaxTokens(t *testing.T) {
	in := sampleInvoke()
	cfg, _ := parseConfig(in.Provider)

	if env := BuildRequest(&ir.ChatRequest{}, in, BuildOptions{Config: cfg}); env.Params.MaxTokens != 64000 {
		t.Fatalf("default max_tokens = %d, want 64000", env.Params.MaxTokens)
	}

	huge := 500_000
	if env := BuildRequest(&ir.ChatRequest{MaxTokens: &huge}, in, BuildOptions{Config: cfg}); env.Params.MaxTokens != 200_000 {
		t.Fatalf("clamped max_tokens = %d, want 200000", env.Params.MaxTokens)
	}

	in.Model.MaxTokens = 1024
	if env := BuildRequest(&ir.ChatRequest{}, in, BuildOptions{Config: cfg}); env.Params.MaxTokens != 1024 {
		t.Fatalf("model max_tokens fallback = %d, want 1024", env.Params.MaxTokens)
	}
}

func TestEnvelopeThreadIDOnlyForUUIDs(t *testing.T) {
	in := sampleInvoke()
	cfg, _ := parseConfig(in.Provider)

	withUUID := BuildRequest(&ir.ChatRequest{}, in, BuildOptions{
		Config: cfg, ThreadID: "0f0e0d0c-0b0a-4908-a706-050403020100",
	})
	if withUUID.ThreadID == "" {
		t.Fatal("a UUID session id must be written as threadId")
	}

	withOther := BuildRequest(&ir.ChatRequest{}, in, BuildOptions{Config: cfg, ThreadID: "not-a-uuid"})
	if withOther.ThreadID != "" {
		t.Fatalf("non-UUID threadId = %q, want empty", withOther.ThreadID)
	}
}

func TestEnvelopeToolChoiceModes(t *testing.T) {
	in := sampleInvoke()
	cfg, _ := parseConfig(in.Provider)

	env := BuildRequest(&ir.ChatRequest{
		ToolChoice: &ir.ToolChoice{Mode: ir.ToolChoiceTool, Name: "read_multiple_files"},
	}, in, BuildOptions{Config: cfg})
	choice, ok := env.Params.ToolChoice.(*ToolChoice)
	if !ok {
		t.Fatalf("tool_choice = %T", env.Params.ToolChoice)
	}
	if choice.Type != "tool" || choice.Name != "read_file" {
		t.Fatalf("tool_choice = %+v", choice)
	}
}

func TestEnvelopeUserToolResultIsHoisted(t *testing.T) {
	in := sampleInvoke()
	cfg, _ := parseConfig(in.Provider)

	// Anthropic-shaped input: a tool result nested in a user turn.
	env := BuildRequest(&ir.ChatRequest{
		Messages: []ir.Message{{
			Role: ir.RoleUser,
			Blocks: []ir.Block{
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ToolUseID: "call_9",
					Content:   []ir.Block{ir.TextBlock("result text")},
				}},
			},
		}},
	}, in, BuildOptions{Config: cfg})

	if len(env.Params.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 tool turn", len(env.Params.Messages))
	}
	msg := env.Params.Messages[0]
	if msg.Role != "tool" {
		t.Fatalf("role = %q, want tool", msg.Role)
	}
	if msg.Content[0].ToolCallID != "call_9" || msg.Content[0].Output.Value != "result text" {
		t.Fatalf("tool block = %+v", msg.Content[0])
	}
}
