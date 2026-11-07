// Package gateway is the protocol-neutral core of the data plane.
//
// It defines the adapter contract every upstream implements, the canonical IR
// (in subpackage ir) every protocol encodes to, and the routing/relay code that
// stitches them together.
//
// Nothing in the adapter contract knows about any specific vendor: an adapter
// receives a ProviderConfig, a Credential, a ModelRef and an ir.ChatRequest, and
// returns an *http.Response. Request shaping, device identities, quota semantics
// and error vocabulary all live behind that boundary.
package gateway

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

// Kind identifies an adapter implementation. It is the value stored in
// providers.kind and the key of the registry.
//
// The gateway defines only the type, not the values: concrete kinds belong to the
// adapter packages, so this package never learns a vendor's name.
type Kind string

// String returns the kind as a plain string.
func (k Kind) String() string { return string(k) }

// AccessMode classifies how an adapter reaches its upstream. It is a neutral
// concept: which mode a concrete adapter uses is declared by the adapter
// package, never inferred here.
type AccessMode string

const (
	// AccessAPI is a direct vendor API reached with a key.
	AccessAPI AccessMode = "api"
	// AccessReverseProxy relays through a reversed client or service.
	AccessReverseProxy AccessMode = "reverse_proxy"
)

// Capabilities are what an adapter can do. The control plane uses them to decide
// which panels to show, the data plane to reject unsupported requests before
// spending an upstream call.
type Capabilities struct {
	// Stream: the upstream can stream.
	Stream bool `json:"stream"`
	// Tools: the upstream understands tool definitions and calls.
	Tools bool `json:"tools"`
	// Vision: the upstream accepts images.
	Vision bool `json:"vision"`
	// ModelDiscovery: DiscoverModels is implemented.
	ModelDiscovery bool `json:"modelDiscovery"`
	// AccountUsage: AccountUsage is implemented.
	AccountUsage bool `json:"accountUsage"`
	// Fingerprint: the adapter derives a device identity per credential.
	Fingerprint bool `json:"fingerprint"`
	// SessionAffinity: requests from one credential share a session id.
	SessionAffinity bool `json:"sessionAffinity"`
	// Quota: the adapter classifies quota failures.
	Quota bool `json:"quota"`
	// ProtocolVersion is the upstream wire version the adapter implements.
	ProtocolVersion string `json:"protocolVersion,omitempty"`
}

// ProviderConfig is the durable provider row, handed to the adapter read-only.
type ProviderConfig struct {
	ID           int64
	Name         string
	Kind         Kind
	BaseURL      string
	TimeoutMs    int
	ProxyURL     string
	ExtraHeaders map[string]string
	// AdapterConfig is the adapter's own JSON blob. The gateway never inspects
	// it beyond JSON validity.
	AdapterConfig map[string]any
}

// Credential is one upstream key, already decrypted. It exists only in memory
// for the duration of a call.
type Credential struct {
	ID       int64
	Name     string
	Secret   string
	Priority int
}

// ModelRef is the model the request resolved to.
type ModelRef struct {
	ID               int64
	PublicName       string
	DisplayName      string
	UpstreamName     string
	UpstreamProtocol ir.Protocol
	MaxTokens        int
	SupportsStream   bool
	SupportsTools    bool
	SupportsVision   bool
	// Prices are per million tokens, in the operator's currency.
	InputPrice       float64
	OutputPrice      float64
	CachedInputPrice float64
}

// SessionHints are the caller's session clues, passed through untouched.
type SessionHints struct {
	// SessionID is an explicit id from the client's headers.
	SessionID string
	// PromptCacheKey is OpenAI's prompt_cache_key, used as a session fallback.
	PromptCacheKey string
	ClientKeyID    int64
	RequestID      string
}

// InvokeRequest is everything one upstream call needs.
type InvokeRequest struct {
	Provider   ProviderConfig
	Credential Credential
	Model      ModelRef
	Request    *ir.ChatRequest
	Session    SessionHints
	// OnWire, when set, receives the exact upstream request for the test bench.
	// It must never be wired to the application log: bodies carry user content.
	OnWire func(path string, body []byte, headers http.Header)
}

// StreamDecoder yields canonical increments. io.EOF ends a complete stream;
// any other error means the stream failed.
type StreamDecoder interface {
	Next() (*ir.StreamChunk, error)
}

// Adapter implements one upstream vendor's wire protocol.
//
// Implementations must be safe for concurrent use and hold no per-request state
// in the struct: everything request-scoped arrives in InvokeRequest.
type Adapter interface {
	// Kind reports the adapter identity, matching providers.kind.
	Kind() Kind
	// Capabilities reports what the adapter supports.
	Capabilities() Capabilities
	// ValidateConfig checks providers.adapter_config and returns a readable
	// message naming the offending field.
	ValidateConfig(cfg ProviderConfig) error
	// Do encodes the request and sends it. A non-2xx response is returned as a
	// response, not an error: the caller classifies it (quota, auth, transient).
	Do(ctx context.Context, in *InvokeRequest) (*http.Response, error)
	// DecodeResponse parses a complete (non-streaming) upstream body.
	DecodeResponse(body []byte, in *InvokeRequest) (*ir.ChatResponse, error)
	// DecodeStream parses a streaming upstream body.
	DecodeStream(r io.Reader, in *InvokeRequest) StreamDecoder
}

// ---- optional capabilities ----
//
// They are optional on purpose: an adapter that cannot list models should not
// have to write an empty method. The control plane probes with a type assertion.

// ProbeRequest is one connectivity check.
type ProbeRequest struct {
	Provider   ProviderConfig
	Credential Credential
	// Model optionally names a model to exercise instead of a metadata call.
	Model   string
	Timeout time.Duration
}

// ProbeResult is the outcome of a connectivity check.
type ProbeResult struct {
	// OK is true when the upstream answered acceptably.
	OK bool `json:"ok"`
	// Status is the HTTP status observed, 0 for a transport failure.
	Status int `json:"status"`
	// DurationMs is the round trip.
	DurationMs int64 `json:"durationMs"`
	// Detail is a truncated, operator-facing excerpt of the upstream answer.
	Detail string `json:"detail,omitempty"`
	// Models is the model count when the probe doubles as discovery.
	Models int `json:"models,omitempty"`
}

// Prober is implemented by adapters that can test connectivity.
type Prober interface {
	Probe(ctx context.Context, in ProbeRequest) (ProbeResult, error)
}

// DiscoveredModel is one entry of an upstream model catalogue.
type DiscoveredModel struct {
	// UpstreamName is the name the upstream expects in requests.
	UpstreamName string `json:"upstreamName"`
	// DisplayName is a human label when the upstream provides one.
	DisplayName string `json:"displayName,omitempty"`
	// Meta carries vendor extras such as a subscription tier.
	Meta map[string]string `json:"meta,omitempty"`
}

// ModelDiscovery is implemented by adapters that can enumerate models.
type ModelDiscovery interface {
	DiscoverModels(ctx context.Context, in ProbeRequest) ([]DiscoveredModel, error)
}

// AccountUsageProvider is implemented by adapters whose upstream exposes
// per-credential quota or billing information. The returned value is
// adapter-defined and rendered as-is by the control plane.
type AccountUsageProvider interface {
	AccountUsage(ctx context.Context, in ProbeRequest) (any, error)
}

// StatusProvider is implemented by adapters with runtime state worth showing.
type StatusProvider interface {
	Status(ctx context.Context, in ProviderConfig) any
}

// Sweeper is implemented by adapters with in-memory state that expires.
type Sweeper interface {
	// Sweep drops expired state and returns the credential ids it forgot, so the
	// caller can drop derived caches too.
	Sweep(now time.Time) []int64
}

// ConfigField describes one adapter_config key for the dynamic form.
type ConfigField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // string | int | bool | select | textarea
	Default     any      `json:"default"`
	Options     []string `json:"options,omitempty"`
	Required    bool     `json:"required"`
	Secret      bool     `json:"secret"`
	Description string   `json:"description,omitempty"`
}

// ConfigSchemaProvider is implemented by adapters that expose configurable
// options. The control plane renders ConfigField values as a form.
type ConfigSchemaProvider interface {
	Schema() []ConfigField
}

// AccessModeProvider is implemented by adapters whose access mode is not the
// default AccessAPI. The registry probes it with a type assertion.
type AccessModeProvider interface {
	AccessMode() AccessMode
}

// FingerprintPolicy derives and reports a stable device identity per credential.
// Only adapters whose upstream cares about device identity implement it.
type FingerprintPolicy interface {
	// Derive returns the identity for a credential. It must be deterministic:
	// the same credential and configuration always yield the same device, across
	// restarts.
	Derive(cfg ProviderConfig, cred Credential) Identity
	// Schedule reports how often the identity must be refreshed upstream.
	Schedule() ReportSchedule
	// Report announces the identity. A failed report is retried soon; it must
	// never block a data-plane request.
	Report(ctx context.Context, in FingerprintRequest) ReportResult
}

// Identity is a derived device identity. Shape is adapter-defined.
type Identity struct {
	// Thumbmark is the opaque stable digest the upstream keys on.
	Thumbmark string `json:"thumbmark"`
	// Components are the individual signals, already hashed or normalised.
	Components map[string]any `json:"components"`
}

// ReportSchedule is the refresh cadence for identity reporting.
type ReportSchedule struct {
	// Every is the normal refresh interval.
	Every time.Duration
	// Jitter is added to Every so a fleet does not report in lockstep.
	Jitter time.Duration
	// RetryMin and RetryMax bound the retry backoff after a failed report.
	RetryMin time.Duration
	RetryMax time.Duration
}

// FingerprintRequest is one reporting attempt.
type FingerprintRequest struct {
	Provider   ProviderConfig
	Credential Credential
	Identity   Identity
}

// ReportResult is the outcome of one reporting attempt.
type ReportResult struct {
	TS     int64  `json:"ts"`
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// QuotaKind classifies why an upstream refused to serve.
type QuotaKind string

const (
	// QuotaNone means the failure is unrelated to quota.
	QuotaNone QuotaKind = "none"
	// QuotaCredit means the account is out of credit and must be reloaded or
	// replaced; the caller persists this as an exhausted credential.
	QuotaCredit QuotaKind = "credit"
	// QuotaWindow means a rate window is exhausted until a known reset time; the
	// caller blocks the credential in memory until then.
	QuotaWindow QuotaKind = "window"
	// QuotaRateLimit is a transient throttle. It must not trigger rotation:
	// switching credentials means switching devices, only worth it on exhaustion.
	QuotaRateLimit QuotaKind = "rate_limit"
)

// QuotaVerdict is the classification of one upstream failure.
type QuotaVerdict struct {
	Kind QuotaKind `json:"kind"`
	// Window names the exhausted window, when the upstream says so.
	Window string `json:"window,omitempty"`
	// Until is the reset time in Unix milliseconds, 0 when unknown.
	Until int64 `json:"until,omitempty"`
	// Message is the upstream's own explanation.
	Message string `json:"message,omitempty"`
}

// QuotaPolicy classifies upstream refusals for one adapter.
type QuotaPolicy interface {
	// Classify inspects a status code and body. Returning QuotaNone means the
	// failure should be reported to the client unchanged.
	Classify(status int, body []byte) QuotaVerdict
	// RotateOn reports whether a kind should retry on another credential.
	RotateOn(kind QuotaKind) bool
}

// StateStore is adapter-private persistence. Keys are scoped per provider, so an
// adapter never has to think about namespacing.
type StateStore interface {
	// Get loads a value; found is false when the key is unset.
	Get(providerID int64, key string, v any) (found bool, err error)
	// Set stores a JSON-serialisable value.
	Set(providerID int64, key string, v any) error
	// Delete removes a value.
	Delete(providerID int64, key string) error
}

// Well-known provider_state keys. Adapters may define more.
const (
	StateFingerprint  = "fingerprint"
	StateModelCatalog = "model_catalog"
	StateAccountUsage = "account_usage"
	StateDrift        = "drift"
)

// ErrUnsupported lets an adapter say plainly that it cannot serve a request.
var ErrUnsupported = errUnsupported{}

type errUnsupported struct{}

func (errUnsupported) Error() string { return "gateway: adapter does not support this request" }
