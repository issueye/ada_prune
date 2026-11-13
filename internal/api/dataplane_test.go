package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/learningdog1/ada_prune/internal/app"
	"github.com/learningdog1/ada_prune/internal/store"
)

// upstream is a fake Command Code upstream that streams NDJSON.
type upstream struct {
	*httptest.Server

	mu sync.Mutex
	// rejectSecrets makes the given bearer secrets answer 402.
	rejectSecrets map[string]bool
	// truncate makes streams end after the text deltas, without a finish:
	// the upstream died mid-answer.
	truncate bool
	// calls records the secrets that were used.
	calls []string
}

// setTruncate flips the truncation mode for the streams served after it.
func (u *upstream) setTruncate(on bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.truncate = on
}

func (u *upstream) truncating() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.truncate
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{rejectSecrets: map[string]bool{}}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/alpha/generate") {
			secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			u.mu.Lock()
			u.calls = append(u.calls, secret)
			reject := u.rejectSecrets[secret]
			u.mu.Unlock()
			if reject {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"error":{"message":"no credit","code":"quota_exceeded"}}`)
				return
			}

			var envelope struct {
				Params struct {
					Stream bool `json:"stream"`
				} `json:"params"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &envelope)

			w.Header().Set("Content-Type", "application/x-ndjson")
			flusher, _ := w.(http.Flusher)
			lines := []string{
				`{"type":"text-delta","text":"hello "}`,
				`{"type":"text-delta","text":"stub"}`,
			}
			if !u.truncating() {
				lines = append(lines, `{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":5,"outputTokens":2,"totalTokens":7}}`)
			}
			for _, line := range lines {
				_, _ = io.WriteString(w, line+"\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
			return
		}
		// Fingerprint and lifecycle reports are fire-and-forget.
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) reject(secret string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rejectSecrets[secret] = true
}

func (u *upstream) callSecrets() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.calls...)
}

// socket is a ready-to-use gateway with one provider, credential, model and key.
type socket struct {
	server     *httptest.Server
	app        *app.App
	key        string
	model      string
	providerID int64
}

func newSocket(t *testing.T, upstreamURL string) socket {
	t.Helper()
	a, srv := newServer(t, app.Options{})

	status, provider := adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name":    "Command Code",
		"kind":    "command_code",
		"baseUrl": upstreamURL,
	})
	if status != http.StatusCreated {
		t.Fatalf("create provider: %d %v", status, provider)
	}
	providerID := int64(provider["id"].(float64))

	if status, body := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": providerID,
		"name":       "main",
		"secret":     "user_0123456789abcdef0123456789abcdef",
	}); status != http.StatusCreated {
		t.Fatalf("create credential: %d %v", status, body)
	}

	status, model := adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "Stub Model",
		"publicName":       "stub-model",
		"upstreamName":     "stub/upstream",
		"upstreamProtocol": "vendor_native",
		"supportsVision":   true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: %d %v", status, model)
	}

	status, key := adminCall(t, srv, http.MethodPost, "/admin/api/client-keys", map[string]any{
		"name": "test-key",
	})
	if status != http.StatusCreated {
		t.Fatalf("create key: %d %v", status, key)
	}

	return socket{
		server:     srv,
		app:        a,
		providerID: providerID,
		key:        key["token"].(string),
		model:      "stub-model",
	}
}

func postJSON(t *testing.T, s socket, path string, body map[string]any) (*http.Response, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, s.server.URL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.key)
	resp, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, out
}

func TestChatCompletionNonStreaming(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	var decoded struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if decoded.Object != "chat.completion" {
		t.Fatalf("object = %q", decoded.Object)
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Message.Content != "hello stub" {
		t.Fatalf("choices = %+v", decoded.Choices)
	}
	if decoded.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish = %q", decoded.Choices[0].FinishReason)
	}
	if decoded.Usage.PromptTokens != 5 || decoded.Usage.CompletionTokens != 2 {
		t.Fatalf("usage = %+v", decoded.Usage)
	}

	// The upstream must have received the vendor envelope, addressed by the
	// upstream model name.
	if secrets := up.callSecrets(); len(secrets) != 1 || secrets[0] != "user_0123456789abcdef0123456789abcdef" {
		t.Fatalf("upstream secrets = %v", secrets)
	}
}

func TestChatCompletionStreaming(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}

	text := string(body)
	if !strings.Contains(text, "data: [DONE]") {
		t.Fatalf("stream has no terminator:\n%s", text)
	}
	var assembled string
	var streamUsage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			streamUsage.PromptTokens = chunk.Usage.PromptTokens
			streamUsage.CompletionTokens = chunk.Usage.CompletionTokens
		}
		for _, c := range chunk.Choices {
			assembled += c.Delta.Content
		}
	}
	if assembled != "hello stub" {
		t.Fatalf("assembled = %q\nstream:\n%s", assembled, text)
	}
	// The stream must carry token accounting: clients derive their speed and
	// context indicators from this frame, and a missing one reads as zero usage.
	if streamUsage.PromptTokens != 5 || streamUsage.CompletionTokens != 2 {
		t.Fatalf("stream usage = %+v, want 5/2\nstream:\n%s", streamUsage, text)
	}

	// A complete stream must carry no in-band error: the decoder's io.EOF is
	// the end-of-stream signal, and treating it as a failure once made every
	// stream end with an injected 502 event.
	if strings.Contains(text, `"error"`) {
		t.Fatalf("complete stream carried an in-band error:\n%s", text)
	}

	// The record agrees with the wire: 200, the finish reason, no error type.
	rec := waitRecord(t, s, "/v1/chat/completions")
	if rec.StatusCode != http.StatusOK || rec.ErrorType != "" || rec.FinishReason != "stop" {
		t.Fatalf("record = status %d, error %q, finish %q; want 200, no error, stop",
			rec.StatusCode, rec.ErrorType, rec.FinishReason)
	}
}

// TestChatCompletionStreamTruncatedStillFails pins the red line the io.EOF fix
// must not soften: an upstream that dies mid-answer is a 502 record and an
// in-band error, never a fabricated completion.
func TestChatCompletionStreamTruncatedStillFails(t *testing.T) {
	up := newUpstream(t)
	up.setTruncate(true)
	s := newSocket(t, up.URL)

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	// Headers are already sent, so the failure travels inside the stream — and
	// without the success terminator: [DONE] would claim the answer is complete.
	text := string(body)
	if !strings.Contains(text, `"error"`) {
		t.Fatalf("truncated stream carried no in-band error:\n%s", body)
	}
	if strings.Contains(text, "[DONE]") {
		t.Fatalf("truncated stream still claimed completion with [DONE]:\n%s", body)
	}

	rec := waitRecord(t, s, "/v1/chat/completions")
	if rec.StatusCode != http.StatusBadGateway || rec.ErrorType != "upstream_stream_error" {
		t.Fatalf("record = status %d, error %q; want 502 upstream_stream_error",
			rec.StatusCode, rec.ErrorType)
	}
	if rec.FinishReason != "" {
		t.Fatalf("finish reason = %q, want none for a truncated stream", rec.FinishReason)
	}
}

// waitRecord polls the store for the newest row of an endpoint; the recorder
// persists calls off the hot path, so the row lands a moment after the answer.
func waitRecord(t *testing.T, s socket, endpoint string) store.RequestRow {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page, err := s.app.Store.ListRequests(store.LogsFilter{Endpoint: endpoint, Limit: 5})
		if err == nil && len(page.Rows) > 0 {
			return page.Rows[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no request recorded for %s", endpoint)
	return store.RequestRow{}
}

func TestAnthropicInboundGetsAnthropicShape(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	resp, body := postJSON(t, s, "/v1/messages", map[string]any{
		"model":      s.model,
		"max_tokens": 64,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	var decoded struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if decoded.Type != "message" || decoded.Role != "assistant" {
		t.Fatalf("shape = %+v", decoded)
	}
	if len(decoded.Content) == 0 || decoded.Content[0].Text != "hello stub" {
		t.Fatalf("content = %+v", decoded.Content)
	}
	if decoded.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q", decoded.StopReason)
	}
}

func TestResponsesInboundGetsResponsesShape(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	resp, body := postJSON(t, s, "/v1/responses", map[string]any{
		"model": s.model,
		"input": "hi",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	var decoded struct {
		Object string `json:"object"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if decoded.Object != "response" {
		t.Fatalf("object = %q", decoded.Object)
	}
	if len(decoded.Output) == 0 || len(decoded.Output[0].Content) == 0 {
		t.Fatalf("output = %+v", decoded.Output)
	}
	if decoded.Output[0].Content[0].Text != "hello stub" {
		t.Fatalf("text = %q", decoded.Output[0].Content[0].Text)
	}
}

func TestUnknownModelIs404InEachProtocol(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	cases := []struct {
		path string
		body map[string]any
	}{
		{"/v1/chat/completions", map[string]any{"model": "nope", "messages": []any{}}},
		{"/v1/responses", map[string]any{"model": "nope", "input": "hi"}},
		{"/v1/messages", map[string]any{"model": "nope", "max_tokens": 8, "messages": []any{}}},
	}
	for _, c := range cases {
		resp, body := postJSON(t, s, c.path, c.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d body = %s", c.path, resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "not available") {
			t.Fatalf("%s body = %s", c.path, body)
		}
	}
}

func TestCreditExhaustionRotatesCredentials(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	// Add a second credential and make the first one refuse with 402.
	const second = "user_fedcba9876543210fedcba9876543210"
	if status, body := adminCall(t, s.server, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": 1,
		"name":       "backup",
		"secret":     second,
	}); status != http.StatusCreated {
		t.Fatalf("create backup credential: %d %v", status, body)
	}
	up.reject("user_0123456789abcdef0123456789abcdef")

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	secrets := up.callSecrets()
	if len(secrets) < 2 {
		t.Fatalf("expected a rotation, upstream saw %v", secrets)
	}
	if secrets[len(secrets)-1] != second {
		t.Fatalf("rotation did not use the backup credential: %v", secrets)
	}

	// The exhausted credential is persisted as such, so it is not tried again.
	status, credentials := adminCall(t, s.server, http.MethodGet, "/admin/api/credentials?providerId=1", nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials: %d", status)
	}
	var exhausted int
	for _, raw := range credentials["rows"].([]any) {
		if raw.(map[string]any)["status"] == "exhausted" {
			exhausted++
		}
	}
	if exhausted != 1 {
		t.Fatalf("exhausted credentials = %d, want 1", exhausted)
	}
}

// TestModelsListingSkipsNonRoutable pins the contract: /v1/models advertises
// only models that can actually serve. A provider without an active credential
// makes its models invisible until one is added.
func TestModelsListingSkipsNonRoutable(t *testing.T) {
	_, srv := newServer(t, app.Options{})

	status, provider := adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name":    "No Credential",
		"kind":    "openai_compatible",
		"baseUrl": "https://example.invalid",
	})
	if status != http.StatusCreated {
		t.Fatalf("create provider: %d %v", status, provider)
	}
	providerID := int64(provider["id"].(float64))

	status, model := adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"upstreamName":     "no-cred/model",
		"upstreamProtocol": "openai_chat",
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: %d %v", status, model)
	}
	const publicName = "No Credential/no-cred/model"

	status, key := adminCall(t, srv, http.MethodPost, "/admin/api/client-keys", map[string]any{"name": "reader"})
	if status != http.StatusCreated {
		t.Fatalf("create key: %d %v", status, key)
	}

	listed := func() []string {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+key["token"].(string))
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET models: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var payload struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d body = %s", resp.StatusCode, raw)
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ids := make([]string, 0, len(payload.Data))
		for _, row := range payload.Data {
			ids = append(ids, row.ID)
		}
		return ids
	}

	if ids := listed(); len(ids) != 0 {
		t.Fatalf("models = %v, want none without a credential", ids)
	}

	if status, cred := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": providerID, "name": "main", "secret": "sk-0123456789abcdef0123456789abcdef",
	}); status != http.StatusCreated {
		t.Fatalf("create credential: %d %v", status, cred)
	}
	ids := listed()
	if len(ids) != 1 || ids[0] != publicName {
		t.Fatalf("models = %v, want [%q] after adding a credential", ids, publicName)
	}
}

func TestModelsListingIsFilteredByKey(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	req, err := http.NewRequest(http.MethodGet, s.server.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	resp, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET models: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "stub-model" {
		t.Fatalf("models = %+v", payload.Data)
	}
}

// TestModelsListingUsesNamespacedID pins the contract: a model created without
// an explicit publicName is listed under {provider}/{upstream}, and a request
// carrying that id routes to the upstream model.
func TestModelsListingUsesNamespacedID(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	status, body := adminCall(t, s.server, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       s.providerID,
		"upstreamName":     "stub/second",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: %d %v", status, body)
	}
	const publicName = "Command Code/stub/second"
	if body["publicName"] != publicName {
		t.Fatalf("publicName = %v, want %q", body["publicName"], publicName)
	}

	req, err := http.NewRequest(http.MethodGet, s.server.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	resp, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET models: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, raw)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, m := range payload.Data {
		if m.ID == publicName {
			found = true
		}
	}
	if !found {
		t.Fatalf("models = %+v, want the namespaced id %q", payload.Data, publicName)
	}

	// The namespaced id is what a client sends back as the model field.
	chatResp, chatBody := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    publicName,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if chatResp.StatusCode != http.StatusOK {
		t.Fatalf("chat with namespaced id: status = %d body = %s", chatResp.StatusCode, chatBody)
	}

	// The short alias serves the same list.
	for _, path := range []string{"/models", "/v1/models"} {
		req, err := http.NewRequest(http.MethodGet, s.server.URL+path, nil)
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		req.Header.Set("Authorization", "Bearer "+s.key)
		resp, err := s.server.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !json.Valid(raw) {
			t.Fatalf("%s: status = %d body = %s", path, resp.StatusCode, raw)
		}
	}
}

// openaiUpstream is a fake OpenAI-compatible upstream.
func openaiUpstream(t *testing.T) (*httptest.Server, *[]string, *bool) {
	t.Helper()
	var bodies []string
	// dropFinish makes the streaming handler end the SSE after the content
	// chunk, without any finish_reason — an upstream that dies mid-answer.
	dropFinish := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(raw, &req)
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = io.WriteString(w, `{"id":"r1","object":"response","status":"completed","model":"gpt-upstream",
				"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"from responses"}]}],
				"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
			return
		}
		if !req.Stream {
			_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","model":"gpt-upstream",
				"choices":[{"index":0,"message":{"role":"assistant","content":"from openai"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
			return
		}
		// Streaming: content, then the finish chunk unless dropped.
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		lines := []string{
			`data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{"content":"from openai"}}]}`,
			`data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		if dropFinish {
			lines = lines[:1]
		}
		lines = append(lines, "data: [DONE]")
		for _, line := range lines {
			_, _ = io.WriteString(w, line+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies, &dropFinish
}

// stallingStreamUpstream sends one vendor-native content frame, flushes it and
// then holds the stream open until the gateway disconnects: the "upstream went
// quiet mid-answer" case the stream watchdog exists for.
func stallingStreamUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `{"type":"text-delta","text":"hello "}`+"\n")
		if flusher != nil {
			flusher.Flush()
		}
		// Hold the connection until the gateway aborts the call.
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stallingBufferedUpstream flushes headers and a partial body, then holds the
// connection open: a non-streaming response that never finishes arriving.
func stallingBufferedUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `{"type":"text-delta","text":"hello "}`+"\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// openaiErrorFinishUpstream streams content and then reports finish_reason
// "error" after a clean wire terminator: a failed generation that must not be
// presented as a completion.
func openaiErrorFinishUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		lines := []string{
			`data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{"content":"half"}}]}`,
			`data: {"id":"c1","object":"chat.completion.chunk","model":"gpt-upstream","choices":[{"index":0,"delta":{},"finish_reason":"error"}]}`,
			`data: [DONE]`,
		}
		for _, line := range lines {
			_, _ = io.WriteString(w, line+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newOpenAISocket builds a gateway whose only provider is OpenAI-compatible.
func newOpenAISocket(t *testing.T, upstreamURL string) socket {
	t.Helper()
	a, srv := newServer(t, app.Options{})

	status, provider := adminCall(t, srv, http.MethodPost, "/admin/api/providers", map[string]any{
		"name":    "OpenAI",
		"kind":    "openai_compatible",
		"baseUrl": upstreamURL,
	})
	if status != http.StatusCreated {
		t.Fatalf("create provider: %d %v", status, provider)
	}
	providerID := int64(provider["id"].(float64))

	if status, body := adminCall(t, srv, http.MethodPost, "/admin/api/credentials", map[string]any{
		"providerId": providerID, "name": "key", "secret": "sk-openai-test",
	}); status != http.StatusCreated {
		t.Fatalf("create credential: %d %v", status, body)
	}

	status, model := adminCall(t, srv, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       providerID,
		"displayName":      "GPT",
		"publicName":       "gpt-cross",
		"upstreamName":     "gpt-upstream",
		"upstreamProtocol": "openai_chat",
	})
	if status != http.StatusCreated {
		t.Fatalf("create model: %d %v", status, model)
	}

	status, key := adminCall(t, srv, http.MethodPost, "/admin/api/client-keys", map[string]any{"name": "k"})
	if status != http.StatusCreated {
		t.Fatalf("create key: %d %v", status, key)
	}

	return socket{app: a, server: srv, key: key["token"].(string), model: "gpt-cross", providerID: providerID}
}

// TestCrossProviderAnthropicInboundToOpenAIUpstream is the point of the whole
// design: an Anthropic-shaped client served by an OpenAI upstream.
func TestCrossProviderAnthropicInboundToOpenAIUpstream(t *testing.T) {
	upstream, bodies, _ := openaiUpstream(t)
	s := newOpenAISocket(t, upstream.URL)

	resp, body := postJSON(t, s, "/v1/messages", map[string]any{
		"model":      s.model,
		"max_tokens": 64,
		"messages":   []map[string]any{{"role": "user", "content": "hello"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	// The upstream must have received a chat-completions body addressed by the
	// upstream model name.
	if len(*bodies) != 1 {
		t.Fatalf("upstream calls = %d", len(*bodies))
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte((*bodies)[0]), &sent); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if sent["model"] != "gpt-upstream" {
		t.Fatalf("upstream model = %v", sent["model"])
	}
	if !strings.Contains((*bodies)[0], `"role":"user"`) {
		t.Fatalf("upstream body = %s", (*bodies)[0])
	}

	// The client must receive an Anthropic message.
	var decoded struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	if decoded.Type != "message" {
		t.Fatalf("type = %q", decoded.Type)
	}
	if len(decoded.Content) == 0 || decoded.Content[0].Text != "from openai" {
		t.Fatalf("content = %+v", decoded.Content)
	}
	if decoded.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q", decoded.StopReason)
	}
}

// TestCrossProviderResponsesInboundToOpenAIUpstream covers the same path for the
// Responses API.
func TestCrossProviderResponsesInboundToOpenAIUpstream(t *testing.T) {
	upstream, _, _ := openaiUpstream(t)
	s := newOpenAISocket(t, upstream.URL)

	// A second model speaks the Responses wire format on the same upstream.
	status, model := adminCall(t, s.server, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       s.providerID,
		"displayName":      "GPT Responses",
		"publicName":       "gpt-responses",
		"upstreamName":     "gpt-upstream-responses",
		"upstreamProtocol": "openai_responses",
	})
	if status != http.StatusCreated {
		t.Fatalf("create responses model: %d %v", status, model)
	}

	resp, body := postJSON(t, s, "/v1/responses", map[string]any{
		"model": "gpt-responses",
		"input": "hello",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var decoded struct {
		Object string `json:"object"`
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Object != "response" {
		t.Fatalf("object = %q", decoded.Object)
	}
	if len(decoded.Output) == 0 || len(decoded.Output[0].Content) == 0 ||
		decoded.Output[0].Content[0].Text != "from responses" {
		t.Fatalf("output = %+v", decoded.Output)
	}
}

// TestOpenAIUpstreamPassesThroughChat stays in the same protocol: no conversion
// should be observable to the client.
func TestOpenAIUpstreamPassesThroughChat(t *testing.T) {
	upstream, _, _ := openaiUpstream(t)
	s := newOpenAISocket(t, upstream.URL)

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content != "from openai" {
		t.Fatalf("choices = %+v", decoded.Choices)
	}
}

// TestOpenAIStreamWithoutFinishStillFails pins the red line on the OpenAI
// codec path: an upstream that streams content and then ends without any
// finish_reason is a truncation — 502 in the record, an in-band error to the
// client — never a clean 200 with [DONE].
func TestOpenAIStreamWithoutFinishStillFails(t *testing.T) {
	upstream, _, drop := openaiUpstream(t)
	s := newOpenAISocket(t, upstream.URL)
	*drop = true

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	// The content chunk went out, then the failure — inside the stream, and
	// without [DONE]: the answer must not look complete.
	text := string(body)
	if !strings.Contains(text, `"error"`) {
		t.Fatalf("finish-less stream carried no in-band error:\n%s", body)
	}
	if strings.Contains(text, "[DONE]") {
		t.Fatalf("finish-less stream still claimed completion with [DONE]:\n%s", body)
	}

	rec := waitRecord(t, s, "/v1/chat/completions")
	if rec.StatusCode != http.StatusBadGateway || rec.ErrorType != "upstream_stream_error" {
		t.Fatalf("record = status %d, error %q; want 502 upstream_stream_error",
			rec.StatusCode, rec.ErrorType)
	}
	if rec.FinishReason != "" {
		t.Fatalf("finish reason = %q, want none", rec.FinishReason)
	}
}

// TestProviderTimeoutCoversResponseBody pins the timeout contract: a provider
// with timeoutMs > 0 must still be able to read the response body. Cancelling
// the request context as soon as Adapter.Do returned made every such call fail
// with "context canceled" before a single byte of the answer was read.
func TestProviderTimeoutCoversResponseBody(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	status, body := adminCall(t, s.server, http.MethodPut,
		fmt.Sprintf("/admin/api/providers/%d", s.providerID), map[string]any{
			"name": "Command Code", "baseUrl": up.URL, "timeoutMs": 5000,
		})
	if status != http.StatusOK {
		t.Fatalf("update provider: %d %v", status, body)
	}

	// Non-streaming: the buffered body must remain readable.
	resp, raw := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("non-stream status = %d body = %s", resp.StatusCode, raw)
	}

	// Streaming: the body keeps arriving after Do has returned.
	resp, raw = postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "data: [DONE]") {
		t.Fatalf("stream status = %d body = %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), `"error"`) {
		t.Fatalf("stream under a provider timeout carried an in-band error:\n%s", raw)
	}
}

// TestStreamIdleTimeoutDoesNotFakeCompletion pins both halves of the idle
// contract: the client gets an in-band error instead of silence, and the stream
// carries no success terminator, because the answer is not complete.
func TestStreamIdleTimeoutDoesNotFakeCompletion(t *testing.T) {
	up := stallingStreamUpstream(t)
	s := newSocket(t, up.URL)
	if _, err := s.app.Cfg.Save(map[string]any{"streamIdleMs": 250}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	text := string(body)
	if !strings.Contains(text, "hello ") {
		t.Fatalf("the content sent before the stall is missing:\n%s", text)
	}
	if !strings.Contains(text, `"error"`) {
		t.Fatalf("idle stream carried no in-band error:\n%s", text)
	}
	if strings.Contains(text, "[DONE]") {
		t.Fatalf("idle stream claimed completion with [DONE]:\n%s", text)
	}

	rec := waitRecord(t, s, "/v1/chat/completions")
	if rec.StatusCode != http.StatusGatewayTimeout || rec.ErrorType != "stream_idle_timeout" {
		t.Fatalf("record = status %d, error %q; want 504 stream_idle_timeout",
			rec.StatusCode, rec.ErrorType)
	}
}

// TestNonStreamIdleTimeout pins the buffered watchdog: an upstream that sends
// headers and then stalls must be cut off, not waited on forever.
func TestNonStreamIdleTimeout(t *testing.T) {
	up := stallingBufferedUpstream(t)
	s := newSocket(t, up.URL)
	if _, err := s.app.Cfg.Save(map[string]any{"nonStreamIdleMs": 100}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d body = %s; want 504", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("timeout response carried no error body: %s", body)
	}

	rec := waitRecord(t, s, "/v1/chat/completions")
	if rec.StatusCode != http.StatusGatewayTimeout || rec.ErrorType != "upstream_read_timeout" {
		t.Fatalf("record = status %d, error %q; want 504 upstream_read_timeout",
			rec.StatusCode, rec.ErrorType)
	}
}

// TestStreamFinishErrorIsUpstreamFailure pins the failure vocabulary: a finish
// reason that names an upstream failure is a 502, not a completed answer.
func TestStreamFinishErrorIsUpstreamFailure(t *testing.T) {
	up := openaiErrorFinishUpstream(t)
	s := newOpenAISocket(t, up.URL)

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	text := string(body)
	if !strings.Contains(text, `"error"`) {
		t.Fatalf("failed finish carried no in-band error:\n%s", text)
	}
	if strings.Contains(text, "[DONE]") {
		t.Fatalf("failed finish claimed completion with [DONE]:\n%s", text)
	}

	rec := waitRecord(t, s, "/v1/chat/completions")
	if rec.StatusCode != http.StatusBadGateway || rec.ErrorType != "upstream_stream_error" {
		t.Fatalf("record = status %d, error %q; want 502 upstream_stream_error",
			rec.StatusCode, rec.ErrorType)
	}
}

func TestRequestIsRecorded(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	if resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    s.model,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("call failed: %d %s", resp.StatusCode, body)
	}

	// Recording is asynchronous: poll the store instead of sleeping blindly.
	deadline := time.Now().Add(2 * time.Second)
	for {
		page, err := s.app.Store.ListRequests(store.LogsFilter{Limit: 10})
		if err != nil {
			t.Fatalf("list requests: %v", err)
		}
		if len(page.Rows) > 0 {
			row := page.Rows[0]
			if row.Model != "stub-model" {
				t.Fatalf("model = %q", row.Model)
			}
			if row.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", row.StatusCode)
			}
			if row.InputTokens != 5 || row.OutputTokens != 2 {
				t.Fatalf("tokens = %d/%d", row.InputTokens, row.OutputTokens)
			}
			if row.ProviderName == nil || *row.ProviderName != "Command Code" {
				t.Fatalf("provider = %v", row.ProviderName)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the call was never recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Usage aggregates are written alongside the log row.
	summary, err := s.app.Store.UsageSummary("", "")
	if err != nil {
		t.Fatalf("usage summary: %v", err)
	}
	if summary.Requests != 1 || summary.InputTokens != 5 {
		t.Fatalf("usage = %+v", summary)
	}
}

// TestRouteModelRoutesByPublicID pins the module's promise: a client sends the
// route model's id as the model field, and the gateway picks the first member
// with a usable credential — no client-side knowledge of the member model ids.
func TestRouteModelRoutesByPublicID(t *testing.T) {
	up := newUpstream(t)
	s := newSocket(t, up.URL)

	status, primary := adminCall(t, s.server, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       s.providerID,
		"upstreamName":     "stub/primary",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusCreated {
		t.Fatalf("create primary model: %d %v", status, primary)
	}
	status, backup := adminCall(t, s.server, http.MethodPost, "/admin/api/models", map[string]any{
		"providerId":       s.providerID,
		"upstreamName":     "stub/backup",
		"upstreamProtocol": "vendor_native",
	})
	if status != http.StatusCreated {
		t.Fatalf("create backup model: %d %v", status, backup)
	}

	status, route := adminCall(t, s.server, http.MethodPost, "/admin/api/route-models", map[string]any{
		"name":     "fast-route",
		"strategy": "priority",
		"members": []map[string]any{
			{"modelId": int64(primary["id"].(float64)), "priority": 2},
			{"modelId": int64(backup["id"].(float64)), "priority": 1},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create route model: %d %v", status, route)
	}

	// The route id is discoverable through /v1/models alongside plain models.
	req, err := http.NewRequest(http.MethodGet, s.server.URL+"/v1/models", nil)
	if err != nil {
		t.Fatalf("request models: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	modelsResp, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET models: %v", err)
	}
	modelsBody, _ := io.ReadAll(modelsResp.Body)
	_ = modelsResp.Body.Close()
	if modelsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET models: status = %d body = %s", modelsResp.StatusCode, modelsBody)
	}
	var catalogue struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(modelsBody, &catalogue); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	foundRoute := false
	for _, entry := range catalogue.Data {
		if entry.ID == "fast-route" {
			foundRoute = true
			if entry.OwnedBy != "route_model" {
				t.Fatalf("route owned_by = %q, want route_model", entry.OwnedBy)
			}
		}
	}
	if !foundRoute {
		t.Fatalf("the route id should be listed: %s", modelsBody)
	}

	resp, body := postJSON(t, s, "/v1/chat/completions", map[string]any{
		"model":    "fast-route",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Message.Content != "hello stub" {
		t.Fatalf("choices = %+v", decoded.Choices)
	}
	if secrets := up.callSecrets(); len(secrets) != 1 {
		t.Fatalf("upstream calls = %v, want one", secrets)
	}
}
