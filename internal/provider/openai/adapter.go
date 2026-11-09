// Package openai adapts any OpenAI-shaped upstream: the official API, Azure
// front doors, and the long tail of compatible gateways.
//
// It reuses the gateway's own OpenAI codecs, which is the point of the IR design:
// a "standard" vendor needs no request translation at all, only a base URL, an
// auth header and a path.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/codec"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/logging"
	"github.com/learningdog1/ada_prune/internal/provider"
)

func init() {
	provider.Register(provider.KindOpenAICompatible, "OpenAI 兼容", New)
}

// Defaults for the adapter configuration.
const (
	// DefaultChatPath is where chat completions live on a conforming upstream.
	DefaultChatPath = "/v1/chat/completions"
	// DefaultResponsesPath is where the Responses API lives.
	DefaultResponsesPath = "/v1/responses"
	// DefaultModelsPath is the model catalogue endpoint.
	DefaultModelsPath = "/v1/models"
	// defaultTimeout is used when the provider sets none. Standard upstreams
	// answer or fail well within this.
	defaultTimeout = 120 * time.Second
)

// Config is the adapter_config payload.
type Config struct {
	// APIBase overrides the provider BaseURL when that is empty.
	APIBase string `json:"apiBase"`
	// ChatPath is the chat-completions path, relative to the base.
	ChatPath string `json:"chatPath"`
	// ResponsesPath is the Responses API path.
	ResponsesPath string `json:"responsesPath"`
	// ModelsPath is the model listing path.
	ModelsPath string `json:"modelsPath"`
	// AuthHeader is the header carrying the key. Empty means "Authorization:
	// Bearer", which is the common case.
	AuthHeader string `json:"authHeader"`
	// AuthPrefix is prepended to the key, e.g. "Bearer " or "".
	AuthPrefix string `json:"authPrefix"`
	// Organization is sent as OpenAI-Organization when set.
	Organization string `json:"organization"`
}

func parseConfig(p gateway.ProviderConfig) (Config, error) {
	c := Config{
		ChatPath:      DefaultChatPath,
		ResponsesPath: DefaultResponsesPath,
		ModelsPath:    DefaultModelsPath,
		AuthHeader:    "Authorization",
		AuthPrefix:    "Bearer ",
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
	if c.ChatPath == "" {
		c.ChatPath = DefaultChatPath
	}
	if c.ResponsesPath == "" {
		c.ResponsesPath = DefaultResponsesPath
	}
	if c.ModelsPath == "" {
		c.ModelsPath = DefaultModelsPath
	}
	if c.AuthHeader == "" {
		c.AuthHeader = "Authorization"
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
	for name, path := range map[string]string{
		"chatPath": c.ChatPath, "responsesPath": c.ResponsesPath, "modelsPath": c.ModelsPath,
	} {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s must start with /", name)
		}
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
			Key: "chatPath", Label: "Chat 路径", Type: "string", Default: DefaultChatPath,
			Description: "OpenAI Chat Completions 的路径",
		},
		{
			Key: "responsesPath", Label: "Responses 路径", Type: "string", Default: DefaultResponsesPath,
			Description: "OpenAI Responses 的路径",
		},
		{
			Key: "modelsPath", Label: "模型列表路径", Type: "string", Default: DefaultModelsPath,
			Description: "用于探活与模型发现",
		},
		{
			Key: "authHeader", Label: "鉴权头", Type: "string", Default: "Authorization",
			Description: "留空即 Authorization；Azure 可填 api-key",
		},
		{
			Key: "authPrefix", Label: "鉴权前缀", Type: "string", Default: "Bearer ",
			Description: "密钥前的字符串，例如 \"Bearer \"；api-key 头通常留空",
		},
		{
			Key: "organization", Label: "组织 ID", Type: "string",
			Description: "可选的 OpenAI-Organization 头",
		},
	}
}

// Adapter implements gateway.Adapter for OpenAI-shaped upstreams.
type Adapter struct {
	http  *http.Client
	log   *logging.Logger
	clock func() time.Time
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
func (a *Adapter) Kind() gateway.Kind { return provider.KindOpenAICompatible }

// client returns the HTTP client for a provider, honouring its proxy setting.
func (a *Adapter) client(p gateway.ProviderConfig) *http.Client {
	return provider.HTTPClientFor(a.http, p.ProxyURL)
}

// Capabilities implements gateway.Adapter.
func (a *Adapter) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{
		Stream:         true,
		Tools:          true,
		Vision:         true,
		ModelDiscovery: true,
		Quota:          true,
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
	protocol, err := upstreamProtocol(in.Model.UpstreamProtocol)
	if err != nil {
		return nil, err
	}
	wire, err := codec.Get(protocol)
	if err != nil {
		return nil, err
	}

	body, err := wire.EncodeRequest(in.Request)
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}

	path := cfg.ChatPath
	if protocol == ir.ProtoOpenAIResponses {
		path = cfg.ResponsesPath
	}
	target := joinURL(cfg.APIBase, path)
	if in.OnWire != nil {
		in.OnWire(path, body, headersFor(cfg, in))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	applyHeaders(req, cfg, in)
	return a.client(in.Provider).Do(req)
}

// DecodeResponse implements gateway.Adapter.
func (a *Adapter) DecodeResponse(body []byte, in *gateway.InvokeRequest) (*ir.ChatResponse, error) {
	wire, err := codecFor(in)
	if err != nil {
		return nil, err
	}
	return wire.DecodeResponse(body)
}

// DecodeStream implements gateway.Adapter.
func (a *Adapter) DecodeStream(r io.Reader, in *gateway.InvokeRequest) gateway.StreamDecoder {
	wire, err := codecFor(in)
	if err != nil {
		return errorDecoder{err: err}
	}
	return wire.NewStreamDecoder(r)
}

// Probe implements gateway.Prober using the model list.
func (a *Adapter) Probe(ctx context.Context, in gateway.ProbeRequest) (gateway.ProbeResult, error) {
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	started := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(cfg.APIBase, cfg.ModelsPath), nil)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	applyAuth(req, cfg, in.Credential.Secret)

	resp, err := a.client(in.Provider).Do(req)
	if err != nil {
		return gateway.ProbeResult{OK: false, DurationMs: time.Since(started).Milliseconds(),
			Detail: truncate(err.Error(), 200)}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	result := gateway.ProbeResult{
		Status:     resp.StatusCode,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Detail = truncate(string(raw), 200)
		return result, nil
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		// A 2xx that is not JSON still proves reachability.
		result.OK = true
		result.Detail = truncate(string(raw), 120)
		return result, nil
	}
	result.OK = true
	result.Models = len(payload.Data)
	result.Detail = fmt.Sprintf("%d models", len(payload.Data))
	return result, nil
}

// DiscoverModels implements gateway.ModelDiscovery.
func (a *Adapter) DiscoverModels(ctx context.Context, in gateway.ProbeRequest) ([]gateway.DiscoveredModel, error) {
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(cfg.APIBase, cfg.ModelsPath), nil)
	if err != nil {
		return nil, err
	}
	applyAuth(req, cfg, in.Credential.Secret)

	resp, err := a.client(in.Provider).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai: model list returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("openai: decode model list: %w", err)
	}
	out := make([]gateway.DiscoveredModel, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		meta := map[string]string{}
		if m.OwnedBy != "" {
			meta["ownedBy"] = m.OwnedBy
		}
		out = append(out, gateway.DiscoveredModel{UpstreamName: m.ID, DisplayName: m.ID, Meta: meta})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("openai: model list was empty")
	}
	return out, nil
}

// Classify implements gateway.QuotaPolicy. A standard upstream has no credit
// concept, so only transient throttling is recognised.
func (a *Adapter) Classify(status int, body []byte) gateway.QuotaVerdict {
	switch status {
	case http.StatusTooManyRequests:
		return gateway.QuotaVerdict{Kind: gateway.QuotaRateLimit, Message: snippet(body)}
	case http.StatusPaymentRequired:
		// Some compatible gateways do report billing trouble this way.
		return gateway.QuotaVerdict{Kind: gateway.QuotaCredit, Message: snippet(body)}
	default:
		return gateway.QuotaVerdict{Kind: gateway.QuotaNone, Message: snippet(body)}
	}
}

// RotateOn implements gateway.QuotaPolicy. Rotation only pays off when the
// account truly cannot serve, which a transient throttle does not prove.
func (a *Adapter) RotateOn(kind gateway.QuotaKind) bool {
	return kind == gateway.QuotaCredit
}

// ---- helpers ----

// upstreamProtocol decides which wire format to speak.
func upstreamProtocol(p ir.Protocol) (ir.Protocol, error) {
	switch p {
	case ir.ProtoOpenAIChat, "":
		return ir.ProtoOpenAIChat, nil
	case ir.ProtoOpenAIResponses:
		return ir.ProtoOpenAIResponses, nil
	default:
		return "", fmt.Errorf("openai: upstream protocol %q is not supported by this adapter", p)
	}
}

func codecFor(in *gateway.InvokeRequest) (codec.Codec, error) {
	if in == nil {
		return nil, gateway.ErrUnsupported
	}
	protocol, err := upstreamProtocol(in.Model.UpstreamProtocol)
	if err != nil {
		return nil, err
	}
	return codec.Get(protocol)
}

func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return path
	}
	// A base that already ends in /v1 plus a /v1 path would double up.
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
	applyAuthHeader(h, cfg, in.Credential.Secret)
	return h
}

func applyHeaders(req *http.Request, cfg Config, in *gateway.InvokeRequest) {
	for k, v := range in.Provider.ExtraHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	applyAuthHeader(req.Header, cfg, in.Credential.Secret)
}

func applyAuthHeader(h http.Header, cfg Config, secret string) {
	if secret == "" {
		return
	}
	h.Set(cfg.AuthHeader, cfg.AuthPrefix+secret)
	if cfg.Organization != "" {
		h.Set("OpenAI-Organization", cfg.Organization)
	}
}

func applyAuth(req *http.Request, cfg Config, secret string) {
	applyAuthHeader(req.Header, cfg, secret)
}

func snippet(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(trimmed), &parsed) == nil {
		if parsed.Error.Message != "" {
			return truncate(parsed.Error.Message, 300)
		}
		if parsed.Message != "" {
			return truncate(parsed.Message, 300)
		}
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

// errorDecoder turns a setup failure into a decode error.
type errorDecoder struct{ err error }

func (d errorDecoder) Next() (*ir.StreamChunk, error) { return nil, d.err }
