package admin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/store"
)

// ProviderView is one provider as the control plane returns it.
type ProviderView struct {
	store.Provider
	// Routable is true when the provider can serve traffic right now.
	Routable       bool   `json:"routable"`
	RoutableReason string `json:"routableReason,omitempty"`
	// AccessMode mirrors the adapter's access classification (api /
	// reverse_proxy). It is derived from the kind, never stored or editable.
	AccessMode gateway.AccessMode `json:"accessMode"`
	// AdapterCapabilities mirrors the registry entry, so the UI can decide which
	// panels to render without a second call.
	AdapterCapabilities *gateway.Capabilities `json:"adapterCapabilities,omitempty"`
	// Status is the adapter's runtime state when it implements StatusProvider.
	Status any `json:"status,omitempty"`
}

// ProviderInput is the create/update payload.
type ProviderInput struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	BaseURL       string            `json:"baseUrl"`
	Enabled       *bool             `json:"enabled"`
	Weight        *int              `json:"weight"`
	TimeoutMs     *int              `json:"timeoutMs"`
	ProxyURL      string            `json:"proxyUrl"`
	ExtraHeaders  map[string]string `json:"extraHeaders"`
	AdapterConfig map[string]any    `json:"adapterConfig"`
	Remark        string            `json:"remark"`
}

// ListProviders returns every provider with routability and adapter metadata.
func (s *Service) ListProviders(ctx context.Context) ([]ProviderView, error) {
	rows, err := s.d.Store.ListProviders()
	if err != nil {
		return nil, err
	}
	out := make([]ProviderView, 0, len(rows))
	for _, p := range rows {
		out = append(out, s.providerView(p))
	}
	return out, nil
}

// GetProvider returns one provider.
func (s *Service) GetProvider(ctx context.Context, id int64) (ProviderView, error) {
	p, err := s.d.Store.GetProvider(id)
	if err != nil {
		return ProviderView{}, translateStoreErr(err, "provider")
	}
	return s.providerView(*p), nil
}

func (s *Service) providerView(p store.Provider) ProviderView {
	view := ProviderView{Provider: p}
	view.Routable, view.RoutableReason = routableProvider(p)
	view.AccessMode = s.d.Registry.AccessMode(gateway.Kind(p.Kind))
	if s.d.Registry != nil {
		if adapter, ok := s.d.Registry.Get(gateway.Kind(p.Kind)); ok {
			caps := adapter.Capabilities()
			view.AdapterCapabilities = &caps
		}
	}
	return view
}

// CreateProvider validates and inserts a provider.
func (s *Service) CreateProvider(ctx context.Context, in ProviderInput) (ProviderView, error) {
	p, err := s.providerFromInput(in, nil)
	if err != nil {
		return ProviderView{}, err
	}
	id, err := s.d.Store.CreateProvider(p)
	if err != nil {
		return ProviderView{}, translateStoreErr(err, "provider")
	}
	return s.GetProvider(ctx, id)
}

// UpdateProvider replaces the mutable fields. The kind is immutable: changing it
// would leave credentials and models pointing at a different protocol.
func (s *Service) UpdateProvider(ctx context.Context, id int64, in ProviderInput) (ProviderView, error) {
	existing, err := s.d.Store.GetProvider(id)
	if err != nil {
		return ProviderView{}, translateStoreErr(err, "provider")
	}
	if in.Kind != "" && in.Kind != existing.Kind {
		return ProviderView{}, FieldError(400, "kind", "a provider's kind cannot change")
	}
	// Keep the stored kind so validation runs against the right adapter.
	in.Kind = existing.Kind
	p, err := s.providerFromInput(in, existing)
	if err != nil {
		return ProviderView{}, err
	}
	p.ID = id
	if err := s.d.Store.UpdateProvider(p); err != nil {
		return ProviderView{}, translateStoreErr(err, "provider")
	}
	return s.GetProvider(ctx, id)
}

// providerFromInput validates the payload and merges it with existing values.
func (s *Service) providerFromInput(in ProviderInput, existing *store.Provider) (store.Provider, error) {
	name, err := validateName("name", in.Name, true)
	if err != nil {
		return store.Provider{}, err
	}
	kind := strings.TrimSpace(in.Kind)
	if existing != nil {
		kind = existing.Kind
	}
	if kind == "" {
		return store.Provider{}, FieldError(400, "kind", "kind is required")
	}
	adapter, err := s.adapterFor(kind)
	if err != nil {
		return store.Provider{}, err
	}
	baseURL, err := validateBaseURL("baseUrl", in.BaseURL)
	if err != nil {
		return store.Provider{}, err
	}
	proxyURL, err := validateProxyURL("proxyUrl", in.ProxyURL)
	if err != nil {
		return store.Provider{}, err
	}

	p := store.Provider{
		Name:          name,
		Kind:          kind,
		BaseURL:       baseURL,
		Enabled:       true,
		Weight:        1,
		TimeoutMs:     in.TimeoutMsValue(),
		ProxyURL:      proxyURL,
		ExtraHeaders:  in.ExtraHeaders,
		AdapterConfig: in.AdapterConfig,
		Remark:        strings.TrimSpace(in.Remark),
	}
	if p.ExtraHeaders == nil {
		p.ExtraHeaders = map[string]string{}
	}
	if p.AdapterConfig == nil {
		p.AdapterConfig = map[string]any{}
	}
	if existing != nil {
		p.Enabled = existing.Enabled
		p.Weight = existing.Weight
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if in.Weight != nil {
		if *in.Weight < 0 {
			return store.Provider{}, FieldError(400, "weight", "weight must not be negative")
		}
		p.Weight = *in.Weight
	}
	if p.TimeoutMs < 0 {
		return store.Provider{}, FieldError(400, "timeoutMs", "timeoutMs must not be negative")
	}

	// The adapter owns its own schema; ask it rather than guessing.
	if err := adapter.ValidateConfig(providerConfigOf(p)); err != nil {
		return store.Provider{}, FieldError(400, "adapterConfig", "%s", err.Error())
	}
	return p, nil
}

// TimeoutMsValue returns the timeout, defaulting to zero (no hard deadline).
func (in ProviderInput) TimeoutMsValue() int {
	if in.TimeoutMs == nil {
		return 0
	}
	return *in.TimeoutMs
}

// SetProviderEnabled toggles a provider.
func (s *Service) SetProviderEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.d.Store.SetProviderEnabled(id, enabled); err != nil {
		return translateStoreErr(err, "provider")
	}
	return nil
}

// DeleteProvider removes a provider and its disabled models.
func (s *Service) DeleteProvider(ctx context.Context, id int64) error {
	if err := s.d.Store.DeleteProvider(id); err != nil {
		return translateStoreErr(err, "provider")
	}
	return nil
}

// ProviderStatus asks the adapter for its runtime state.
func (s *Service) ProviderStatus(ctx context.Context, id int64) (any, error) {
	p, err := s.d.Store.GetProvider(id)
	if err != nil {
		return nil, translateStoreErr(err, "provider")
	}
	adapter, err := s.adapterFor(p.Kind)
	if err != nil {
		return nil, err
	}
	provider, ok := adapter.(gateway.StatusProvider)
	if !ok {
		return map[string]any{"supported": false}, nil
	}
	return provider.Status(ctx, providerConfigOf(*p)), nil
}

// ProbeProvider performs a connectivity check with the first usable credential.
func (s *Service) ProbeProvider(ctx context.Context, id int64) (gateway.ProbeResult, error) {
	p, err := s.d.Store.GetProvider(id)
	if err != nil {
		return gateway.ProbeResult{}, translateStoreErr(err, "provider")
	}
	adapter, err := s.adapterFor(p.Kind)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	prober, ok := adapter.(gateway.Prober)
	if !ok {
		return gateway.ProbeResult{}, FieldError(400, "kind",
			"adapter %q does not support connectivity checks", s.kindLabel(p.Kind))
	}

	cred, err := s.firstCredential(p.ID)
	if err != nil {
		return gateway.ProbeResult{}, err
	}

	timeout := 20 * time.Second
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, probeErr := prober.Probe(probeCtx, gateway.ProbeRequest{
		Provider:   providerConfigOf(*p),
		Credential: credentialOf(*cred),
		Timeout:    timeout,
	})
	if probeErr != nil {
		return result, Errorf(502, "probe failed: %v", probeErr)
	}
	if !result.OK {
		return result, nil
	}
	return result, nil
}

// DiscoverModels lists candidate models from the upstream. Nothing is persisted:
// the operator chooses what to import.
type DiscoveredModelView struct {
	gateway.DiscoveredModel
	// Exists is true when this upstream model is already in the model table.
	Exists bool `json:"exists"`
}

// DiscoverModelsResult is the response to discover-models.
type DiscoverModelsResult struct {
	Source string                `json:"source"`
	Rows   []DiscoveredModelView `json:"rows"`
}

// DiscoverModels asks the adapter for its catalogue and marks known models.
func (s *Service) DiscoverModels(ctx context.Context, id int64) (DiscoverModelsResult, error) {
	p, err := s.d.Store.GetProvider(id)
	if err != nil {
		return DiscoverModelsResult{}, translateStoreErr(err, "provider")
	}
	adapter, err := s.adapterFor(p.Kind)
	if err != nil {
		return DiscoverModelsResult{}, err
	}
	discovery, ok := adapter.(gateway.ModelDiscovery)
	if !ok {
		return DiscoverModelsResult{}, FieldError(400, "kind",
			"adapter %q cannot list models", s.kindLabel(p.Kind))
	}
	cred, err := s.firstCredential(p.ID)
	if err != nil {
		return DiscoverModelsResult{}, err
	}

	discoverCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	models, discoverErr := discovery.DiscoverModels(discoverCtx, gateway.ProbeRequest{
		Provider:   providerConfigOf(*p),
		Credential: credentialOf(*cred),
	})
	if discoverErr != nil {
		return DiscoverModelsResult{}, Errorf(502, "model discovery failed: %v", discoverErr)
	}

	existing, err := s.d.Store.ListModels(store.ModelFilter{ProviderID: p.ID})
	if err != nil {
		return DiscoverModelsResult{}, err
	}
	known := make(map[string]bool, len(existing))
	for _, m := range existing {
		known[m.UpstreamName] = true
	}

	rows := make([]DiscoveredModelView, 0, len(models))
	for _, m := range models {
		rows = append(rows, DiscoveredModelView{DiscoveredModel: m, Exists: known[m.UpstreamName]})
	}
	return DiscoverModelsResult{Source: p.Kind, Rows: rows}, nil
}

// firstCredential returns the highest-priority usable credential of a provider.
func (s *Service) firstCredential(providerID int64) (*store.Credential, error) {
	rows, err := s.d.Store.ListRoutableCredentials(providerID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, FieldError(400, "providerId",
			"provider has no active credential; add one before probing")
	}
	return &rows[0], nil
}

// translateStoreErr maps store sentinels onto control-plane errors.
func translateStoreErr(err error, resource string) error {
	if err == nil {
		return nil
	}
	var inUse *store.ErrInUse
	if errors.As(err, &inUse) {
		return &Error{Status: 409, Message: inUse.Reason}
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Errorf(404, "%s not found", resource)
	case errors.Is(err, store.ErrDuplicate):
		return Errorf(409, "%s already exists", resource)
	default:
		return Errorf(500, "%s", err.Error())
	}
}
