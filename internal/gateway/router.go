package gateway

import (
	"fmt"
	"sort"
	"sync"
)

// Router answers the one question the data plane asks: given a model name, which
// adapter, provider and credential should serve it?
//
// It owns credential ordering and the in-memory window bookkeeping. Persistence
// and vendor rules stay behind the Catalog and Adapter interfaces.
type Router struct {
	catalog  Catalog
	adapters AdapterResolver
	windows  *Windows

	mu    sync.Mutex
	rrIdx map[string]int
}

// NewRouter wires a router.
func NewRouter(catalog Catalog, adapters AdapterResolver) *Router {
	return &Router{
		catalog:  catalog,
		adapters: adapters,
		windows:  NewWindows(),
		rrIdx:    map[string]int{},
	}
}

// Windows exposes the window tracker for status reporting and sweeps.
func (r *Router) Windows() *Windows { return r.windows }

// Route is one resolved target, ready to invoke.
type Route struct {
	Adapter  Adapter
	Provider ProviderConfig
	Model    ModelRef
	// Credentials are the candidates for this provider, best first, with
	// window-blocked credentials already removed.
	Credentials []Credential
}

// AdapterError means the provider's kind has no built adapter.
type AdapterError struct{ Kind Kind }

func (e *AdapterError) Error() string {
	return fmt.Sprintf("gateway: no adapter registered for kind %q", e.Kind)
}

// Resolve turns a public model name into a route.
func (r *Router) Resolve(name string) (*Route, error) {
	candidates, err := r.catalog.Resolve(name)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrModelNotFound
	}

	// Try candidates in order; the first one with a usable credential wins.
	var lastErr error = ErrModelNotFound
	for _, candidate := range candidates {
		adapter, ok := r.adapters.Get(candidate.Provider.Kind)
		if !ok {
			lastErr = &AdapterError{Kind: candidate.Provider.Kind}
			continue
		}
		credentials := r.credentialsFor(candidate.Provider.ID)
		if len(credentials) == 0 {
			lastErr = ErrNoCredential
			continue
		}
		return &Route{
			Adapter:     adapter,
			Provider:    candidate.Provider,
			Model:       candidate.Model,
			Credentials: credentials,
		}, nil
	}
	return nil, lastErr
}

// ErrNoCredential means every credential of a provider is unavailable.
var ErrNoCredential = fmt.Errorf("gateway: no usable credential for the provider")

// credentialsFor returns the credentials of a provider in routing order:
// the operator's pin first, then priority, then id. Window-blocked credentials
// are dropped, because a blocked one cannot serve anyway.
func (r *Router) credentialsFor(providerID int64) []Credential {
	rows, err := r.catalog.Credentials(providerID)
	if err != nil || len(rows) == 0 {
		return nil
	}
	pinned := r.catalog.PinnedCredential()

	out := make([]Credential, 0, len(rows))
	for _, cred := range rows {
		if blocked, _ := r.windows.Blocked(cred.ID); blocked {
			continue
		}
		out = append(out, cred)
	}
	if pinned > 0 {
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].ID == pinned && out[j].ID != pinned
		})
	}
	return out
}

// MarkCredentialExhausted persists a credit exhaustion and blocks the credential
// for the rest of the call chain.
func (r *Router) MarkCredentialExhausted(credentialID int64) error {
	if r.catalog == nil {
		return nil
	}
	return r.catalog.MarkExhausted(credentialID)
}

// Touch records a credential's use.
func (r *Router) Touch(credentialID int64) {
	if r.catalog != nil {
		r.catalog.Touch(credentialID)
	}
}

// BlockWindow records an exhausted rate window until its reset.
func (r *Router) BlockWindow(credentialID int64, window string, until int64) {
	r.windows.Mark(credentialID, window, until)
}

// CredentialBlocked reports whether a credential is currently windowed.
func (r *Router) CredentialBlocked(credentialID int64) (bool, int64) {
	return r.windows.Blocked(credentialID)
}
