package app

import (
	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/store"
)

// catalog adapts the store to the gateway's narrow view.
//
// The gateway never imports the store: it asks this adapter for routable
// candidates and credentials, which keeps the data-plane core testable with a map.
type catalog struct {
	st *store.Store
}

// Resolve returns the candidate chain for a public name.
//
// A plain model resolves to itself; a route model resolves to its enabled
// members in priority order, skipping members whose provider cannot serve.
func (c catalog) Resolve(name string) ([]gateway.Candidate, error) {
	if m, err := c.st.GetModelByPublicName(name); err == nil {
		provider, providerErr := c.st.GetProvider(m.ProviderID)
		if providerErr != nil || !provider.Enabled || !m.Enabled {
			return nil, gateway.ErrModelNotFound
		}
		return []gateway.Candidate{{
			Model:    modelRef(*m),
			Provider: providerConfig(*provider),
		}}, nil
	}

	route, err := c.routeModelByName(name)
	if err != nil {
		return nil, gateway.ErrModelNotFound
	}
	members, err := c.st.ResolveRouteModelCandidates(route.ID)
	if err != nil {
		return nil, gateway.ErrModelNotFound
	}
	out := make([]gateway.Candidate, 0, len(members))
	for _, m := range members {
		provider, providerErr := c.st.GetProvider(m.ProviderID)
		if providerErr != nil || !provider.Enabled {
			continue
		}
		out = append(out, gateway.Candidate{
			Model:    modelRef(m),
			Provider: providerConfig(*provider),
		})
	}
	if len(out) == 0 {
		return nil, gateway.ErrRouteModelNotFound
	}
	return out, nil
}

// routeModelByName finds an enabled route model.
func (c catalog) routeModelByName(name string) (*store.RouteModel, error) {
	routes, err := c.st.ListRouteModels()
	if err != nil {
		return nil, err
	}
	for i := range routes {
		if routes[i].Name == name && routes[i].Enabled {
			return &routes[i], nil
		}
	}
	return nil, gateway.ErrRouteModelNotFound
}

// Credentials returns a provider's usable credentials, best first.
func (c catalog) Credentials(providerID int64) ([]gateway.Credential, error) {
	rows, err := c.st.ListRoutableCredentials(providerID)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.Credential, 0, len(rows))
	for _, row := range rows {
		out = append(out, gateway.Credential{
			ID:       row.ID,
			Name:     row.Name,
			Secret:   row.Secret,
			Priority: row.Priority,
		})
	}
	return out, nil
}

// MarkExhausted persists a credit exhaustion.
func (c catalog) MarkExhausted(credentialID int64) error {
	return c.st.SetCredentialStatus(credentialID, store.CredentialExhausted)
}

// Touch records use of a credential.
func (c catalog) Touch(credentialID int64) {
	c.st.TouchCredential(credentialID)
}

// PinnedCredential returns the routing pin.
func (c catalog) PinnedCredential() int64 {
	pinned, err := c.st.PinnedCredentialID()
	if err != nil {
		return 0
	}
	return pinned
}

// providerConfig projects a stored provider onto the gateway's view.
func providerConfig(p store.Provider) gateway.ProviderConfig {
	return gateway.ProviderConfig{
		ID:            p.ID,
		Name:          p.Name,
		Kind:          gateway.Kind(p.Kind),
		BaseURL:       p.BaseURL,
		TimeoutMs:     p.TimeoutMs,
		ProxyURL:      p.ProxyURL,
		ExtraHeaders:  p.ExtraHeaders,
		AdapterConfig: p.AdapterConfig,
	}
}

// modelRef projects a stored model onto the gateway's view.
func modelRef(m store.Model) gateway.ModelRef {
	return gateway.ModelRef{
		ID:               m.ID,
		PublicName:       m.PublicName,
		DisplayName:      m.DisplayName,
		UpstreamName:     m.UpstreamName,
		UpstreamProtocol: ir.Protocol(m.UpstreamProtocol),
		MaxTokens:        m.MaxTokens,
		SupportsStream:   m.SupportsStream,
		SupportsTools:    m.SupportsTools,
		SupportsVision:   m.SupportsVision,
		InputPrice:       m.InputPrice,
		OutputPrice:      m.OutputPrice,
		CachedInputPrice: m.CachedInputPrice,
	}
}
