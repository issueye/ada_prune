// Package anthropic adapts the native Anthropic Messages API.
//
// Like the OpenAI adapter it leans on the gateway's own codec: the wire format is
// a standard one, so the only work here is the endpoint, the auth headers and the
// vendor's quota vocabulary.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/codec"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/logging"
	"github.com/learningdog1/ada_proxy/internal/provider"
)

func init() {
	provider.Register(provider.KindAnthropic, "Anthropic", New)
}

// Defaults for the adapter configuration.
const (
	// DefaultPath is the Messages endpoint.
	DefaultPath = "/v1/messages"
	// DefaultVersion is the API version this adapter speaks.
	DefaultVersion = "2023-06-01"
	// DefaultMaxTokens is sent when a request omits it, which the protocol
	// requires.
	DefaultMaxTokens = 4096
	// defaultTimeout bounds a request that has no provider timeout.
	defaultTimeout = 300 * time.Second
)

// Config is the adapter_config payload.
type Config struct {
	// APIBase overrides the provider BaseURL when that is empty.
	APIBase string `json:"apiBase"`
	// Path is the Messages endpoint path.
	Path string `json:"path"`
	// Version is the anthropic-version header.
	Version string `json:"version"`
	// MaxTokens is the fallback when the request omits max_tokens.
	MaxTokens int `json:"maxTokens"`
	// Beta lists anthropic-beta features to opt into.
	Beta string `json:"beta"`
}

func parseConfig(p gateway.ProviderConfig) (Config, error) {
	c := Config{
		Path:      DefaultPath,
		Version:   DefaultVersion,
		MaxTokens: DefaultMaxTokens,
	}
	if len(p.AdapterConfig) > 0 {
		raw, err := json.Marshal(p.AdapterConfig)
		if err != nil {
			return c, fmt.Errorf("adapter config is not serialisable: %w", err)
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("adapter config has an invalid field: %w", err)
		}
	}
	if strings.TrimSpace(p.BaseURL) != "" {
		c.APIBase = strings.TrimSpace(p.BaseURL)
	}
	if c.Path == "" {
		c.Path = DefaultPath
	}
	if c.Version == "" {
		c.Version = DefaultVersion
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = DefaultMaxTokens
	}
	return c, nil
}

func validateConfig(p gateway.ProviderConfig) error {
	c, err := parseConfig(p)
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.APIBase) == "" {
		return fmt.Errorf("apiBase or the provider base URL is required")
	}
	u, err := url.Parse(c.APIBase)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("apiBase must be an http(s) URL with a host")
	}
	if !strings.HasPrefix(c.Path, "/") {
		return fmt.Errorf("path must start with /")
	}
	return nil
}

func configSchema() []gateway.ConfigField {
	return []gateway.ConfigField{
		{
			Key: "apiBase", Label: "API 根地址", Type: "string",
			Description: "留空则使用供应商 BaseURL",
		},
		{
			Key: "path", Label: "Messages 路径", Type: "string", Default: DefaultPath,
		},
		{
			Key: "version", Label: "API 版本", Type: "string", Default: DefaultVersion,
			Description: "写入 anthropic-version 头",
		},
		{
			Key: "maxTokens", Label: "默认 max_tokens", Type: "int", Default: DefaultMaxTokens,
			Description: "请求未给 max_tokens 时使用；协议要求该字段必填",
		},
		{
			Key: "beta", Label: "Beta 特性", Type: "string",
			Description: "可选，写入 anthropic-beta 头",
		},
	}
}

// Adapter implements gateway.Adapter for the Anthropic Messages API.
type Adapter struct {
	http *http.Client
	log  *logging.Logger
}

// New builds the adapter.
func New(d provider.Deps) (gateway.Adapter, error) {
	httpClient := d.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	log := d.Log
	if log == nil {
		log = logging.Discard()
	}
	return &Adapter{http: httpClient, log: log}, nil
}

// Kind implements gateway.Adapter.
func (a *Adapter) Kind() gateway.Kind { return provider.KindAnthropic }

// Capabilities implements gateway.Adapter.
func (a *Adapter) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{
		Stream:          true,
		Tools:           true,
		Vision:          true,
		Quota:           true,
		ProtocolVersion: DefaultVersion,
	}
}

// ValidateConfig implements gateway.Adapter.
func (a *Adapter) ValidateConfig(p gateway.ProviderConfig) error { return validateConfig(p) }

// Schema implements gateway.ConfigSchemaProvider.
func (a *Adapter) Schema() []gateway.ConfigField { return configSchema() }

// Do implements gateway.Adapter.
func (a *Adapter) Do(ctx context.Context, in *gateway.InvokeRequest) (*http.Response, error) {
	if in == nil || in.Request == nil {
		return nil, gateway.ErrUnsupported
	}
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return nil, err
	}

	// The protocol requires max_tokens; fill it rather than letting the upstream
	// reject the whole call.
	req := *in.Request
	if req.MaxTokens == nil || *req.MaxTokens <= 0 {
		fallback := cfg.MaxTokens
		if in.Model.MaxTokens > 0 {
			fallback = in.Model.MaxTokens
		}
		req.MaxTokens = &fallback
	}

	body, err := codec.MustGet(ir.ProtoAnthropic).EncodeRequest(&req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode request: %w", err)
	}
	if in.OnWire != nil {
		in.OnWire(cfg.Path, body, headersFor(cfg, in))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		joinURL(cfg.APIBase, cfg.Path), strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	applyHeaders(httpReq, cfg, in)
	return a.http.Do(httpReq)
}

// DecodeResponse implements gateway.Adapter.
func (a *Adapter) DecodeResponse(body []byte, in *gateway.InvokeRequest) (*ir.ChatResponse, error) {
	return codec.MustGet(ir.ProtoAnthropic).DecodeResponse(body)
}

// DecodeStream implements gateway.Adapter.
func (a *Adapter) DecodeStream(r io.Reader, in *gateway.InvokeRequest) gateway.StreamDecoder {
	return codec.MustGet(ir.ProtoAnthropic).NewStreamDecoder(r)
}

// Probe implements gateway.Prober. It sends the smallest legal Messages request,
// because the model list endpoint is not available on every deployment.
func (a *Adapter) Probe(ctx context.Context, in gateway.ProbeRequest) (gateway.ProbeResult, error) {
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	started := time.Now()

	payload := map[string]any{
		"model":      firstNonEmpty(in.Model, "claude-haiku-4-5-20251001"),
		"max_tokens": 1,
		"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		joinURL(cfg.APIBase, cfg.Path), strings.NewReader(string(raw)))
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	applyHeaders(req, cfg, &gateway.InvokeRequest{
		Provider:   in.Provider,
		Credential: in.Credential,
	})

	resp, err := a.http.Do(req)
	if err != nil {
		return gateway.ProbeResult{OK: false, DurationMs: time.Since(started).Milliseconds(),
			Detail: truncate(err.Error(), 200)}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	result := gateway.ProbeResult{Status: resp.StatusCode, DurationMs: time.Since(started).Milliseconds()}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		result.OK = true
		result.Detail = "messages endpoint reachable"
	case resp.StatusCode == http.StatusBadRequest:
		// A 400 means the credential was accepted and the request shape was
		// understood; only the payload was wrong. That is still a valid probe.
		result.OK = true
		result.Detail = truncate(string(body), 200)
	default:
		result.Detail = truncate(string(body), 200)
	}
	return result, nil
}

// Classify implements gateway.QuotaPolicy.
func (a *Adapter) Classify(status int, body []byte) gateway.QuotaVerdict {
	message := snippet(body)
	switch status {
	case http.StatusTooManyRequests:
		return gateway.QuotaVerdict{Kind: gateway.QuotaRateLimit, Message: message}
	case 529, http.StatusServiceUnavailable:
		return gateway.QuotaVerdict{Kind: gateway.QuotaRateLimit, Message: firstNonEmpty(message, "overloaded")}
	case http.StatusPaymentRequired:
		return gateway.QuotaVerdict{Kind: gateway.QuotaCredit, Message: message}
	default:
		return gateway.QuotaVerdict{Kind: gateway.QuotaNone, Message: message}
	}
}

// RotateOn implements gateway.QuotaPolicy. Anthropic has no credit pool exposed
// through this API; a 402 is handled, a throttle is not worth switching devices
// over because there is no device identity here.
func (a *Adapter) RotateOn(kind gateway.QuotaKind) bool { return kind == gateway.QuotaCredit }

// ---- helpers ----

func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return path
	}
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

func headersFor(cfg Config, in *gateway.InvokeRequest) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	for k, v := range in.Provider.ExtraHeaders {
		h.Set(k, v)
	}
	h.Set("x-api-key", in.Credential.Secret)
	h.Set("anthropic-version", cfg.Version)
	if cfg.Beta != "" {
		h.Set("anthropic-beta", cfg.Beta)
	}
	return h
}

func applyHeaders(req *http.Request, cfg Config, in *gateway.InvokeRequest) {
	for k, v := range in.Provider.ExtraHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if in.Credential.Secret != "" {
		req.Header.Set("x-api-key", in.Credential.Secret)
	}
	req.Header.Set("anthropic-version", cfg.Version)
	if cfg.Beta != "" {
		req.Header.Set("anthropic-beta", cfg.Beta)
	}
}

func snippet(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(trimmed), &parsed) == nil && parsed.Error.Message != "" {
		return truncate(parsed.Error.Message, 300)
	}
	return truncate(trimmed, 200)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
