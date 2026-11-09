package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/logging"
	"github.com/learningdog1/ada_prune/internal/provider"
)

func newAdapter(t *testing.T) *Adapter {
	t.Helper()
	built, err := New(provider.Deps{Log: logging.Discard()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built.(*Adapter)
}

func invokeFor(baseURL string) *gateway.InvokeRequest {
	return &gateway.InvokeRequest{
		Provider: gateway.ProviderConfig{
			ID:      1,
			Name:    "Anthropic",
			Kind:    provider.KindAnthropic,
			BaseURL: baseURL,
		},
		Credential: gateway.Credential{ID: 2, Name: "key", Secret: "sk-ant-test"},
		Model: gateway.ModelRef{
			ID:               3,
			PublicName:       "claude-test",
			UpstreamName:     "claude-test",
			UpstreamProtocol: ir.ProtoAnthropic,
			SupportsStream:   true,
		},
		Request: &ir.ChatRequest{
			Model:    "claude-test",
			Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
		},
	}
}

func TestKindAndCapabilities(t *testing.T) {
	a := newAdapter(t)
	if a.Kind() != provider.KindAnthropic {
		t.Fatalf("kind = %q", a.Kind())
	}
	caps := a.Capabilities()
	if !caps.Stream || !caps.Tools || !caps.Vision {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.ProtocolVersion != DefaultVersion {
		t.Fatalf("protocol version = %q", caps.ProtocolVersion)
	}
}

func TestDoSendsMessagesRequest(t *testing.T) {
	var gotPath, gotKey, gotVersion, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = io.WriteString(w, `{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	resp, err := a.Do(context.Background(), invokeFor(srv.URL))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotPath != DefaultPath {
		t.Fatalf("path = %q", gotPath)
	}
	if gotKey != "sk-ant-test" {
		t.Fatalf("x-api-key = %q", gotKey)
	}
	if gotVersion != DefaultVersion {
		t.Fatalf("anthropic-version = %q", gotVersion)
	}

	var payload struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Model != "claude-test" {
		t.Fatalf("model = %q", payload.Model)
	}
	if payload.MaxTokens != DefaultMaxTokens {
		t.Fatalf("max_tokens = %d, want the adapter fallback %d", payload.MaxTokens, DefaultMaxTokens)
	}
}

func TestDoHonoursRequestMaxTokens(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			MaxTokens int `json:"max_tokens"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &payload)
		got = payload.MaxTokens
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	in := invokeFor(srv.URL)
	limit := 128
	in.Request.MaxTokens = &limit

	a := newAdapter(t)
	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if got != 128 {
		t.Fatalf("max_tokens = %d, want 128", got)
	}
}

func TestDoUsesModelMaxTokensWhenRequestOmitsIt(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			MaxTokens int `json:"max_tokens"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &payload)
		got = payload.MaxTokens
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	in := invokeFor(srv.URL)
	in.Model.MaxTokens = 2048

	a := newAdapter(t)
	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if got != 2048 {
		t.Fatalf("max_tokens = %d, want the model's 2048", got)
	}
}

func TestDecodeStreamParsesNamedEvents(t *testing.T) {
	body := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m1","model":"claude-test","usage":{"input_tokens":3,"output_tokens":0}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hey"}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	a := newAdapter(t)
	dec := a.DecodeStream(strings.NewReader(body), invokeFor("http://x"))
	var text, finish string
	var usage *ir.Usage
	for {
		chunk, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		switch chunk.Kind {
		case ir.ChunkText:
			text += chunk.Text
		case ir.ChunkFinish:
			finish = chunk.FinishReason
			usage = chunk.Usage
		}
	}
	if text != "hey" || finish != ir.FinishStop {
		t.Fatalf("text = %q finish = %q", text, finish)
	}
	if usage == nil || usage.PromptTokens != 3 || usage.CompletionTokens != 2 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestProbeAcceptsBadRequest(t *testing.T) {
	// A 400 proves the key was accepted and the endpoint understood the call; the
	// probe request itself is intentionally minimal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"model required"}}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	result, err := a.Probe(context.Background(), gateway.ProbeRequest{
		Provider:   gateway.ProviderConfig{BaseURL: srv.URL},
		Credential: gateway.Credential{Secret: "sk-ant"},
		Model:      "claude-test",
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.OK {
		t.Fatalf("probe = %+v, want OK on a 400", result)
	}
}

func TestProbeReportsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	result, err := a.Probe(context.Background(), gateway.ProbeRequest{
		Provider:   gateway.ProviderConfig{BaseURL: srv.URL},
		Credential: gateway.Credential{Secret: "sk-ant"},
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.OK {
		t.Fatalf("probe = %+v, want failure", result)
	}
}

func TestClassifyOverloaded(t *testing.T) {
	a := newAdapter(t)
	cases := map[int]gateway.QuotaKind{
		http.StatusTooManyRequests: gateway.QuotaRateLimit,
		529:                        gateway.QuotaRateLimit,
		http.StatusPaymentRequired: gateway.QuotaCredit,
		http.StatusBadRequest:      gateway.QuotaNone,
	}
	for status, want := range cases {
		if got := a.Classify(status, nil); got.Kind != want {
			t.Errorf("classify(%d) = %q, want %q", status, got.Kind, want)
		}
	}
	if a.RotateOn(gateway.QuotaRateLimit) {
		t.Fatal("an overload must not rotate")
	}
}

func TestConfigValidation(t *testing.T) {
	a := newAdapter(t)

	if err := a.ValidateConfig(gateway.ProviderConfig{BaseURL: "https://api.anthropic.com"}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := a.ValidateConfig(gateway.ProviderConfig{}); err == nil {
		t.Fatal("accepted a provider without a base URL")
	}
	if err := a.ValidateConfig(gateway.ProviderConfig{
		BaseURL:       "https://api.anthropic.com",
		AdapterConfig: map[string]any{"path": "messages"},
	}); err == nil {
		t.Fatal("accepted a path without a leading slash")
	}
}

func TestSchemaCoversDefaults(t *testing.T) {
	a := newAdapter(t)
	schema := a.Schema()
	if len(schema) == 0 {
		t.Fatal("schema is empty")
	}
	keys := map[string]bool{}
	for _, f := range schema {
		keys[f.Key] = true
	}
	for _, want := range []string{"apiBase", "path", "version", "maxTokens"} {
		if !keys[want] {
			t.Errorf("schema is missing %q", want)
		}
	}
}

// TestDoUsesProviderProxy pins that a provider's network proxy is actually
// used for the Messages endpoint too.
func TestDoUsesProviderProxy(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, `{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer proxy.Close()

	a := newAdapter(t)
	in := invokeFor("http://upstream.invalid")
	in.Provider.ProxyURL = proxy.URL
	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("the request bypassed the configured proxy")
	}
}
