package command_code

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/logging"
	"github.com/learningdog1/ada_proxy/internal/provider"
)

// Adapter implements gateway.Adapter for the Command Code subscription upstream.
type Adapter struct {
	log   *logging.Logger
	http  *http.Client
	state gateway.StateStore
	clock func() time.Time
	rnd   io.Reader

	sessions *Sessions
	catalog  catalogCache
	usage    usageCache
	drift    driftCache

	keys *keyBook

	clients struct {
		mu      sync.Mutex
		byProxy map[string]*http.Client
	}

	reportingMu sync.Mutex
	reporting   map[int64]bool
}

// New builds the adapter.
func New(d provider.Deps) (gateway.Adapter, error) {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	a := &Adapter{
		log:       d.Log,
		http:      d.HTTP,
		state:     d.State,
		clock:     clock,
		sessions:  NewSessions(DefaultSessionTTLHours*time.Hour, d.Rand, clock),
		keys:      &keyBook{byID: map[int64]*keyEntry{}},
		reporting: map[int64]bool{},
	}
	if a.http == nil {
		a.http = a.newClient("", 0)
	}
	a.clients.byProxy = map[string]*http.Client{}
	return a, nil
}

func init() {
	provider.Register(provider.KindCommandCode, "Command Code", New)
}

// Kind implements gateway.Adapter.
func (a *Adapter) Kind() gateway.Kind { return provider.KindCommandCode }

// AccessMode implements gateway.AccessModeProvider: Command Code is reached
// through a reversed client, not a public vendor API.
func (a *Adapter) AccessMode() gateway.AccessMode { return gateway.AccessReverseProxy }

// Capabilities implements gateway.Adapter.
func (a *Adapter) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{
		Stream:          true,
		Tools:           true,
		Vision:          true,
		ModelDiscovery:  true,
		AccountUsage:    true,
		Fingerprint:     true,
		SessionAffinity: true,
		Quota:           true,
		ProtocolVersion: ProtocolVersion,
	}
}

// ValidateConfig implements gateway.Adapter.
func (a *Adapter) ValidateConfig(cfg gateway.ProviderConfig) error {
	return validateConfig(cfg)
}

// Schema implements gateway.ConfigSchemaProvider.
func (a *Adapter) Schema() []gateway.ConfigField { return configSchema() }

// Do implements gateway.Adapter.
func (a *Adapter) Do(ctx context.Context, in *gateway.InvokeRequest) (*http.Response, error) {
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return nil, gateway.ErrUnsupported
	}
	if in.Credential.Secret == "" {
		return nil, fmt.Errorf("command_code: credential has no secret")
	}
	if in.Request == nil {
		return nil, fmt.Errorf("command_code: request is missing")
	}

	sessionID := a.sessions.ID(in.Session.SessionID, in.Session.PromptCacheKey, in.Credential.Secret)
	envelope := BuildRequest(in.Request, in, BuildOptions{
		Config:         cfg,
		ThreadID:       sessionID,
		PromptCacheKey: in.Session.PromptCacheKey,
		Now:            a.clock,
	})

	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("command_code: encode envelope: %w", err)
	}
	if in.OnWire != nil {
		in.OnWire(generatePath, body, generateHeaders(in, cfg, sessionID))
	}

	url := apiBaseOf(cfg, in.Provider) + generatePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("command_code: build request: %w", err)
	}
	for k, values := range generateHeaders(in, cfg, sessionID) {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}

	// Announce the device identity when it is due. This never blocks the call.
	a.maybeReport(in.Provider.ID, in.Credential, cfg)

	return a.httpClient(cfg, in.Provider).Do(req)
}

// DecodeResponse implements gateway.Adapter by folding the NDJSON body into one
// canonical response. The upstream has no non-streaming mode; the gateway
// aggregates.
func (a *Adapter) DecodeResponse(body []byte, in *gateway.InvokeRequest) (*ir.ChatResponse, error) {
	return aggregate(body, in)
}

// DecodeStream implements gateway.Adapter.
func (a *Adapter) DecodeStream(r io.Reader, in *gateway.InvokeRequest) gateway.StreamDecoder {
	return newStreamDecoder(r, in)
}

// Probe implements gateway.Prober: it asks for the model list, which proves both
// reachability and that the credential is valid.
func (a *Adapter) Probe(ctx context.Context, in gateway.ProbeRequest) (gateway.ProbeResult, error) {
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	if in.Credential.Secret == "" {
		return gateway.ProbeResult{OK: false, Detail: "no credential"}, nil
	}
	started := a.clock()
	models, source, err := a.fetchModels(ctx, cfg, in.Provider, in.Credential)
	duration := a.clock().Sub(started).Milliseconds()
	if err != nil {
		return gateway.ProbeResult{OK: false, DurationMs: duration, Detail: truncate(err.Error(), 200)}, nil
	}
	return gateway.ProbeResult{
		OK:         true,
		Status:     http.StatusOK,
		DurationMs: duration,
		Detail:     fmt.Sprintf("%d models from %s", len(models), source),
		Models:     len(models),
	}, nil
}

// Status implements gateway.StatusProvider.
func (a *Adapter) Status(ctx context.Context, p gateway.ProviderConfig) any {
	cfg, err := parseConfig(p)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	a.keys.mu.Lock()
	defer a.keys.mu.Unlock()

	credentials := make([]map[string]any, 0, len(a.keys.byID))
	for id, entry := range a.keys.byID {
		credentials = append(credentials, map[string]any{
			"credentialId":      id,
			"thumbmark":         entry.fingerprint.Thumbmark,
			"cpuModel":          entry.fingerprint.Components.CPUModel,
			"memGiB":            entry.fingerprint.Components.MemGiB,
			"timezone":          entry.fingerprint.Components.Timezone,
			"nextReportAt":      entry.report.NextReportAt,
			"fingerprintReport": entry.report.FingerprintReport,
			"lifecycleReport":   entry.report.LifecycleReport,
		})
	}

	catalog := map[string]any{"source": CatalogBuiltin, "count": len(BuiltinModels)}
	a.catalog.mu.Lock()
	if a.catalog.source != "" {
		catalog = map[string]any{
			"source":    a.catalog.source,
			"count":     len(a.catalog.models),
			"fetchedAt": a.catalog.fetchedAt.UnixMilli(),
		}
	}
	a.catalog.mu.Unlock()

	return map[string]any{
		"protocolVersion": cfg.ProtocolVersion,
		"projectDir":      cfg.ProjectDir,
		"projectSlug":     SlugifyProjectPath(cfg.ProjectDir),
		"sessions":        a.sessions.Len(),
		"credentials":     credentials,
		"modelCatalog":    catalog,
	}
}

// IdentityFor exposes the derived identity for the control plane's status page.
func (a *Adapter) IdentityFor(p gateway.ProviderConfig, cred gateway.Credential) gateway.Identity {
	return a.Derive(p, cred)
}

// CLILoginPath is where the vendor CLI stores its login. The control plane uses
// it for the one-click import; an empty string means there is nothing to import.
func (a *Adapter) CLILoginPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".commandcode", "auth.json")
}
