package openai

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
	"github.com/learningdog1/ada_prune/internal/gateway/codec"
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

func invokeFor(baseURL string, path ir.Protocol) *gateway.InvokeRequest {
	return &gateway.InvokeRequest{
		Provider: gateway.ProviderConfig{
			ID:      1,
			Name:    "OpenAI",
			Kind:    provider.KindOpenAICompatible,
			BaseURL: baseURL,
		},
		Credential: gateway.Credential{ID: 2, Name: "key", Secret: "sk-test-0123456789"},
		Model: gateway.ModelRef{
			ID:               3,
			PublicName:       "gpt-test",
			UpstreamName:     "gpt-test",
			UpstreamProtocol: path,
			SupportsStream:   true,
			SupportsTools:    true,
		},
		Request: &ir.ChatRequest{
			Model:    "gpt-test",
			Stream:   true,
			Messages: []ir.Message{{Role: ir.RoleUser, Blocks: []ir.Block{ir.TextBlock("hi")}}},
		},
	}
}

func TestKindAndCapabilities(t *testing.T) {
	a := newAdapter(t)
	if a.Kind() != provider.KindOpenAICompatible {
		t.Fatalf("kind = %q", a.Kind())
	}
	caps := a.Capabilities()
	if !caps.Stream || !caps.Tools || !caps.Vision || !caps.ModelDiscovery {
		t.Fatalf("capabilities = %+v", caps)
	}
}

func TestDoSendsChatRequest(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	resp, err := a.Do(context.Background(), invokeFor(srv.URL, ir.ProtoOpenAIChat))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotPath != DefaultChatPath {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test-0123456789" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["model"] != "gpt-test" {
		t.Fatalf("model = %v", payload["model"])
	}
	if payload["stream"] != true {
		t.Fatalf("stream = %v", payload["stream"])
	}
}

func TestDoUsesResponsesPathForResponsesModels(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	resp, err := a.Do(context.Background(), invokeFor(srv.URL, ir.ProtoOpenAIResponses))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if gotPath != DefaultResponsesPath {
		t.Fatalf("path = %q, want the responses path", gotPath)
	}
}

func TestDoRejectsUnsupportedProtocol(t *testing.T) {
	a := newAdapter(t)
	in := invokeFor("http://example.invalid", ir.ProtoAnthropic)
	if _, err := a.Do(context.Background(), in); err == nil {
		t.Fatal("accepted an anthropic upstream protocol")
	}
}

func TestBaseURLWithV1SuffixDoesNotDouble(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	resp, err := a.Do(context.Background(), invokeFor(srv.URL+"/v1", ir.ProtoOpenAIChat))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	// A base that already ends in /v1 must not produce /v1/v1/…
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions without a doubled prefix", gotPath)
	}
}

func TestDecodeChatAndResponses(t *testing.T) {
	a := newAdapter(t)

	chat := []byte(`{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	resp, err := a.DecodeResponse(chat, invokeFor("http://x", ir.ProtoOpenAIChat))
	if err != nil {
		t.Fatalf("chat decode: %v", err)
	}
	if resp.Text() != "hi" {
		t.Fatalf("text = %q", resp.Text())
	}

	responses := []byte(`{"id":"r1","object":"response","status":"completed","model":"m","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hey"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
	resp, err = a.DecodeResponse(responses, invokeFor("http://x", ir.ProtoOpenAIResponses))
	if err != nil {
		t.Fatalf("responses decode: %v", err)
	}
	if resp.Text() != "hey" {
		t.Fatalf("text = %q", resp.Text())
	}
}

func TestDecodeStream(t *testing.T) {
	a := newAdapter(t)
	body := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"he"}}]}`,
		"",
		`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"y"},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	dec := a.DecodeStream(strings.NewReader(body), invokeFor("http://x", ir.ProtoOpenAIChat))
	var text string
	for {
		chunk, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if chunk.Kind == ir.ChunkText {
			text += chunk.Text
		}
	}
	if text != "hey" {
		t.Fatalf("text = %q", text)
	}
}

func TestProbeReadsModelList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, DefaultModelsPath) {
			t.Errorf("probe path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	result, err := a.Probe(context.Background(), gateway.ProbeRequest{
		Provider:   gateway.ProviderConfig{BaseURL: srv.URL},
		Credential: gateway.Credential{Secret: "sk-test"},
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !result.OK || result.Models != 2 {
		t.Fatalf("probe = %+v", result)
	}
}

func TestProbeReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	result, err := a.Probe(context.Background(), gateway.ProbeRequest{
		Provider:   gateway.ProviderConfig{BaseURL: srv.URL},
		Credential: gateway.Credential{Secret: "sk-test"},
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.OK {
		t.Fatalf("probe = %+v, want failure", result)
	}
	if !strings.Contains(result.Detail, "bad key") {
		t.Fatalf("detail = %q", result.Detail)
	}
}

func TestDiscoverModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-a","owned_by":"openai"},{"id":"gpt-b"}]}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	models, err := a.DiscoverModels(context.Background(), gateway.ProbeRequest{
		Provider:   gateway.ProviderConfig{BaseURL: srv.URL},
		Credential: gateway.Credential{Secret: "sk-test"},
	})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 || models[0].UpstreamName != "gpt-a" {
		t.Fatalf("models = %+v", models)
	}
}

func TestConfigValidation(t *testing.T) {
	a := newAdapter(t)

	ok := gateway.ProviderConfig{BaseURL: "https://api.example.com"}
	if err := a.ValidateConfig(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	noBase := gateway.ProviderConfig{}
	if err := a.ValidateConfig(noBase); err == nil {
		t.Fatal("accepted a provider without a base URL")
	}

	badPath := gateway.ProviderConfig{
		BaseURL:       "https://api.example.com",
		AdapterConfig: map[string]any{"chatPath": "chat"},
	}
	if err := a.ValidateConfig(badPath); err == nil {
		t.Fatal("accepted a path without a leading slash")
	}
}

func TestCustomAuthHeader(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("api-key")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	a := newAdapter(t)
	in := invokeFor(srv.URL, ir.ProtoOpenAIChat)
	in.Provider.AdapterConfig = map[string]any{"authHeader": "api-key", "authPrefix": ""}
	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if gotKey != "sk-test-0123456789" {
		t.Fatalf("api-key header = %q", gotKey)
	}
}

func TestQuotaClassification(t *testing.T) {
	a := newAdapter(t)
	if got := a.Classify(http.StatusTooManyRequests, nil); got.Kind != gateway.QuotaRateLimit {
		t.Fatalf("429 -> %q", got.Kind)
	}
	if got := a.Classify(http.StatusPaymentRequired, nil); got.Kind != gateway.QuotaCredit {
		t.Fatalf("402 -> %q", got.Kind)
	}
	if got := a.Classify(http.StatusBadRequest, nil); got.Kind != gateway.QuotaNone {
		t.Fatalf("400 -> %q", got.Kind)
	}
	if a.RotateOn(gateway.QuotaRateLimit) {
		t.Fatal("a transient throttle must not rotate")
	}
	if !a.RotateOn(gateway.QuotaCredit) {
		t.Fatal("credit exhaustion must rotate")
	}
}

// TestCrossProtocolConversion proves the whole point: an Anthropic-style request
// served by an OpenAI-shaped upstream.
func TestCrossProtocolConversion(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"converted\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	// An Anthropic-shaped request, canonicalised by the Anthropic codec, then sent
	// to an OpenAI upstream by this adapter.
	anthropicBody := `{"model":"gpt-test","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	canonical, err := anthropicDecode(anthropicBody)
	if err != nil {
		t.Fatalf("decode anthropic: %v", err)
	}

	a := newAdapter(t)
	in := invokeFor(srv.URL, ir.ProtoOpenAIChat)
	in.Request = canonical

	resp, err := a.Do(context.Background(), in)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if gotPath != DefaultChatPath {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"model":"gpt-test"`) {
		t.Fatalf("upstream body = %s", gotBody)
	}

	dec := a.DecodeStream(resp.Body, in)
	var text string
	for {
		chunk, err := dec.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if chunk.Kind == ir.ChunkText {
			text += chunk.Text
		}
	}
	if text != "converted" {
		t.Fatalf("text = %q", text)
	}
}

// anthropicDecode parses an Anthropic-shaped body into the canonical form.
func anthropicDecode(body string) (*ir.ChatRequest, error) {
	return codec.MustGet(ir.ProtoAnthropic).DecodeRequest([]byte(body))
}

// TestDoUsesProviderProxy pins that a provider's network proxy is actually
// used: the request must reach the proxy, not the upstream address.
func TestDoUsesProviderProxy(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer proxy.Close()

	a := newAdapter(t)
	in := invokeFor("http://upstream.invalid", ir.ProtoOpenAIChat)
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
