package store

import "encoding/json"

// Credential statuses.
const (
	// CredentialActive is routable.
	CredentialActive = "active"
	// CredentialDisabled was switched off by an operator.
	CredentialDisabled = "disabled"
	// CredentialExhausted ran out of credit upstream and needs a manual reset.
	CredentialExhausted = "exhausted"
)

// Endpoint target types.
const (
	// TargetModel routes to a single model.
	TargetModel = "model"
	// TargetRouteModel routes through a route model.
	TargetRouteModel = "route_model"
)

// Route strategies.
const (
	// StrategyPriority tries members in priority order.
	StrategyPriority = "priority"
	// StrategyWeighted picks by weight.
	StrategyWeighted = "weighted"
)

// Provider is one upstream AI service.
//
// AdapterConfig is opaque to the gateway: the adapter defines its schema and
// validates it. ExtraHeaders is a JSON object of headers added to every
// upstream call.
type Provider struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	BaseURL       string            `json:"baseUrl"`
	Enabled       bool              `json:"enabled"`
	Weight        int               `json:"weight"`
	TimeoutMs     int               `json:"timeoutMs"`
	ProxyURL      string            `json:"proxyUrl"`
	ExtraHeaders  map[string]string `json:"extraHeaders"`
	AdapterConfig map[string]any    `json:"adapterConfig"`
	Remark        string            `json:"remark"`
	CreatedAt     int64             `json:"createdAt"`
	UpdatedAt     int64             `json:"updatedAt"`

	// Derived counts filled in by list/detail queries.
	CredentialCount       int64 `json:"credentialCount"`
	ActiveCredentialCount int64 `json:"activeCredentialCount"`
	ModelCount            int64 `json:"modelCount"`
	EnabledModelCount     int64 `json:"enabledModelCount"`
}

// Credential is one upstream key under a provider. Secret is only ever
// populated on the read path that needs it (routing); API responses carry
// SecretMask instead.
type Credential struct {
	ID           int64  `json:"id"`
	ProviderID   int64  `json:"providerId"`
	ProviderName string `json:"providerName,omitempty"`
	ProviderKind string `json:"providerKind,omitempty"`
	Name         string `json:"name"`
	// Secret is the decrypted key. It is never serialised.
	Secret string `json:"-"`
	// SecretMask is the display form, e.g. "user_abcd…wxyz".
	SecretMask string `json:"secret"`
	Enabled    bool   `json:"enabled"`
	Priority   int    `json:"priority"`
	Status     string `json:"status"`
	ExpiresAt  *int64 `json:"expiresAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
	Remark     string `json:"remark"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	// Pinned reports whether this credential is the routable pin.
	Pinned bool `json:"pinned"`
}

// Model is one callable model under a provider.
type Model struct {
	ID               int64   `json:"id"`
	ProviderID       int64   `json:"providerId"`
	ProviderName     string  `json:"providerName,omitempty"`
	ProviderKind     string  `json:"providerKind,omitempty"`
	DisplayName      string  `json:"displayName"`
	PublicName       string  `json:"publicName"`
	UpstreamName     string  `json:"upstreamName"`
	UpstreamProtocol string  `json:"upstreamProtocol"`
	InputPrice       float64 `json:"inputPrice"`
	OutputPrice      float64 `json:"outputPrice"`
	CachedInputPrice float64 `json:"cachedInputPrice"`
	ContextWindow    int     `json:"contextWindow"`
	MaxTokens        int     `json:"maxTokens"`
	SupportsStream   bool    `json:"supportsStream"`
	SupportsTools    bool    `json:"supportsTools"`
	SupportsVision   bool    `json:"supportsVision"`
	Enabled          bool    `json:"enabled"`
	Sort             int     `json:"sort"`
	Remark           string  `json:"remark"`
	CreatedAt        int64   `json:"createdAt"`
	UpdatedAt        int64   `json:"updatedAt"`
}

// RouteModel aggregates several models under one public name.
type RouteModel struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	Strategy  string             `json:"strategy"`
	Enabled   bool               `json:"enabled"`
	Remark    string             `json:"remark"`
	CreatedAt int64              `json:"createdAt"`
	UpdatedAt int64              `json:"updatedAt"`
	Members   []RouteModelMember `json:"members"`
}

// RouteModelMember is one model in a route model.
type RouteModelMember struct {
	ID           int64  `json:"id"`
	RouteModelID int64  `json:"routeModelId"`
	ModelID      int64  `json:"modelId"`
	Priority     int    `json:"priority"`
	Weight       int    `json:"weight"`
	Enabled      bool   `json:"enabled"`
	DisplayName  string `json:"displayName,omitempty"`
	PublicName   string `json:"publicName,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	ProviderKind string `json:"providerKind,omitempty"`
}

// ClientKey is a key handed to a downstream tool. Only the hash is stored.
type ClientKey struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	KeyPrefix     string   `json:"keyPrefix"`
	Enabled       bool     `json:"enabled"`
	AllowedModels []string `json:"allowedModels"`
	AllowedIPs    string   `json:"allowedIps"`
	QuotaTokens   int64    `json:"quotaTokens"`
	QuotaUsed     int64    `json:"quotaUsed"`
	ExpiresAt     *int64   `json:"expiresAt"`
	LastUsedAt    *int64   `json:"lastUsedAt"`
	Remark        string   `json:"remark"`
	CreatedAt     int64    `json:"createdAt"`
	UpdatedAt     int64    `json:"updatedAt"`
}

// Endpoint is a data-plane entry point.
type Endpoint struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	InboundProtocol string `json:"inboundProtocol"`
	TargetType      string `json:"targetType"`
	TargetID        int64  `json:"targetId"`
	AuthRequired    bool   `json:"authRequired"`
	RatePerMin      int    `json:"ratePerMin"`
	TimeoutMs       int    `json:"timeoutMs"`
	Enabled         bool   `json:"enabled"`
	Remark          string `json:"remark"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	// Builtin marks the three read-only endpoints the gateway always serves.
	Builtin bool `json:"builtin"`
}

// RequestRecord is one data-plane call, as written to the requests table.
type RequestRecord struct {
	TS                 int64
	Endpoint           string
	InboundProtocol    string
	ProviderID         *int64
	ProviderKind       string
	Model              string
	ClientKeyID        *int64
	CredentialID       *int64
	StatusCode         int64
	Stream             bool
	DurationMs         int64
	TTFTMs             int64
	InputTokens        int64
	OutputTokens       int64
	CachedTokens       int64
	Cost               float64
	FinishReason       string
	ErrorType          string
	ClientDisconnected bool
}

// RequestRow is one request log row as the API returns it.
type RequestRow struct {
	ID                 int64   `json:"id"`
	TS                 int64   `json:"ts"`
	Endpoint           string  `json:"endpoint"`
	InboundProtocol    string  `json:"inboundProtocol"`
	ProviderID         *int64  `json:"providerId"`
	ProviderKind       string  `json:"providerKind"`
	ProviderName       *string `json:"providerName,omitempty"`
	Model              string  `json:"model"`
	ClientKeyID        *int64  `json:"clientKeyId"`
	ClientKeyName      *string `json:"clientKeyName,omitempty"`
	CredentialID       *int64  `json:"credentialId"`
	StatusCode         int64   `json:"statusCode"`
	Stream             bool    `json:"stream"`
	DurationMs         int64   `json:"durationMs"`
	TTFTMs             int64   `json:"ttftMs"`
	InputTokens        int64   `json:"inputTokens"`
	OutputTokens       int64   `json:"outputTokens"`
	CachedTokens       int64   `json:"cachedTokens"`
	Cost               float64 `json:"cost"`
	FinishReason       string  `json:"finishReason"`
	ErrorType          string  `json:"errorType"`
	ClientDisconnected bool    `json:"clientDisconnected"`
}

// UsageRow is one aggregated usage bucket.
type UsageRow struct {
	Day          string  `json:"day,omitempty"`
	ProviderID   int64   `json:"providerId,omitempty"`
	ProviderName string  `json:"providerName,omitempty"`
	Model        string  `json:"model,omitempty"`
	ClientKeyID  int64   `json:"clientKeyId,omitempty"`
	KeyName      string  `json:"keyName,omitempty"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CachedTokens int64   `json:"cachedTokens"`
	Cost         float64 `json:"cost"`
}

// UsageSummary is the aggregate card: totals plus success rate.
type UsageSummary struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	SuccessRate  float64 `json:"successRate"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CachedTokens int64   `json:"cachedTokens"`
	Cost         float64 `json:"cost"`
}

// HourBucket is one point of the rolling 24 hour chart.
type HourBucket struct {
	TS           int64   `json:"ts"`
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	Cost         float64 `json:"cost"`
}

// Last24Hours is the rolling chart payload.
type Last24Hours struct {
	Points []HourBucket `json:"points"`
	Peak   int64        `json:"peak"`
	Total  int64        `json:"total"`
}

// JSONList marshals a string slice, used by the client key whitelist column.
func JSONList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// ParseJSONList unmarshals a whitelist column, tolerating empty values.
func ParseJSONList(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
