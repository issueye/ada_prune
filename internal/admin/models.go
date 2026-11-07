package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/store"
)

// ModelView is one model with its routability.
type ModelView struct {
	store.Model
	Routable       bool   `json:"routable"`
	RoutableReason string `json:"routableReason,omitempty"`
}

// ModelInput is the create/update payload.
type ModelInput struct {
	ProviderID       int64   `json:"providerId"`
	DisplayName      string  `json:"displayName"`
	PublicName       string  `json:"publicName"`
	UpstreamName     string  `json:"upstreamName"`
	UpstreamProtocol string  `json:"upstreamProtocol"`
	InputPrice       float64 `json:"inputPrice"`
	OutputPrice      float64 `json:"outputPrice"`
	CachedInputPrice float64 `json:"cachedInputPrice"`
	ContextWindow    int     `json:"contextWindow"`
	MaxTokens        int     `json:"maxTokens"`
	SupportsStream   *bool   `json:"supportsStream"`
	SupportsTools    *bool   `json:"supportsTools"`
	SupportsVision   *bool   `json:"supportsVision"`
	Enabled          *bool   `json:"enabled"`
	Sort             int     `json:"sort"`
	Remark           string  `json:"remark"`
}

// ModelFilterInput carries the list filters from the query string.
type ModelFilterInput struct {
	ProviderID int64
	Protocol   string
	Query      string
	Enabled    *bool
}

// ListModels returns models with routability computed against their provider.
func (s *Service) ListModels(ctx context.Context, in ModelFilterInput) ([]ModelView, error) {
	rows, err := s.d.Store.ListModels(store.ModelFilter{
		ProviderID: in.ProviderID,
		Enabled:    in.Enabled,
		Protocol:   in.Protocol,
		Query:      in.Query,
	})
	if err != nil {
		return nil, err
	}
	providers, err := s.providerIndex()
	if err != nil {
		return nil, err
	}
	out := make([]ModelView, 0, len(rows))
	for _, m := range rows {
		view := ModelView{Model: m}
		if p, ok := providers[m.ProviderID]; ok {
			view.Routable, view.RoutableReason = routableModel(m, p)
		} else {
			view.RoutableReason = "provider is missing"
		}
		out = append(out, view)
	}
	return out, nil
}

// GetModel returns one model.
func (s *Service) GetModel(ctx context.Context, id int64) (ModelView, error) {
	m, err := s.d.Store.GetModel(id)
	if err != nil {
		return ModelView{}, translateStoreErr(err, "model")
	}
	p, err := s.d.Store.GetProvider(m.ProviderID)
	if err != nil {
		return ModelView{}, translateStoreErr(err, "provider")
	}
	view := ModelView{Model: *m}
	view.Routable, view.RoutableReason = routableModel(*m, *p)
	return view, nil
}

// CreateModel inserts a model.
func (s *Service) CreateModel(ctx context.Context, in ModelInput) (ModelView, error) {
	row, err := s.modelFromInput(in, nil)
	if err != nil {
		return ModelView{}, err
	}
	id, err := s.d.Store.CreateModel(row)
	if err != nil {
		return ModelView{}, translateStoreErr(err, "model")
	}
	return s.GetModel(ctx, id)
}

// UpdateModel replaces the mutable fields.
func (s *Service) UpdateModel(ctx context.Context, id int64, in ModelInput) (ModelView, error) {
	existing, err := s.d.Store.GetModel(id)
	if err != nil {
		return ModelView{}, translateStoreErr(err, "model")
	}
	row, err := s.modelFromInput(in, existing)
	if err != nil {
		return ModelView{}, err
	}
	row.ID = id
	if err := s.d.Store.UpdateModel(row); err != nil {
		return ModelView{}, translateStoreErr(err, "model")
	}
	return s.GetModel(ctx, id)
}

// modelFromInput validates the payload and merges it with existing values.
func (s *Service) modelFromInput(in ModelInput, existing *store.Model) (store.Model, error) {
	providerID := in.ProviderID
	if providerID == 0 && existing != nil {
		providerID = existing.ProviderID
	}
	if providerID <= 0 {
		return store.Model{}, FieldError(400, "providerId", "providerId is required")
	}
	provider, err := s.d.Store.GetProvider(providerID)
	if err != nil {
		return store.Model{}, translateStoreErr(err, "provider")
	}

	displayName, err := validateName("displayName", firstNonEmpty(in.DisplayName, in.UpstreamName), true)
	if err != nil {
		return store.Model{}, err
	}
	upstreamName := strings.TrimSpace(firstNonEmpty(in.UpstreamName, existingUpstreamName(existing)))
	if upstreamName == "" {
		return store.Model{}, FieldError(400, "upstreamName", "upstreamName is required")
	}

	rawProtocol := firstNonEmpty(in.UpstreamProtocol, existingProtocol(existing))
	protocol, err := parseProtocol("upstreamProtocol", rawProtocol)
	if err != nil {
		return store.Model{}, err
	}

	publicName := strings.TrimSpace(in.PublicName)
	if publicName == "" {
		publicName = defaultPublicName(provider.Name, upstreamName)
	}
	if len([]rune(publicName)) > 191 {
		return store.Model{}, FieldError(400, "publicName", "publicName is too long")
	}

	row := store.Model{
		ProviderID:       providerID,
		DisplayName:      displayName,
		PublicName:       publicName,
		UpstreamName:     upstreamName,
		UpstreamProtocol: string(protocol),
		InputPrice:       in.InputPrice,
		OutputPrice:      in.OutputPrice,
		CachedInputPrice: in.CachedInputPrice,
		ContextWindow:    in.ContextWindow,
		MaxTokens:        in.MaxTokens,
		SupportsStream:   true,
		SupportsTools:    true,
		SupportsVision:   false,
		Enabled:          true,
		Sort:             in.Sort,
		Remark:           strings.TrimSpace(in.Remark),
	}
	if existing != nil {
		row.SupportsStream = existing.SupportsStream
		row.SupportsTools = existing.SupportsTools
		row.SupportsVision = existing.SupportsVision
		row.Enabled = existing.Enabled
	}
	if in.SupportsStream != nil {
		row.SupportsStream = *in.SupportsStream
	}
	if in.SupportsTools != nil {
		row.SupportsTools = *in.SupportsTools
	}
	if in.SupportsVision != nil {
		row.SupportsVision = *in.SupportsVision
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}

	if err := validatePrices(in); err != nil {
		return store.Model{}, err
	}
	if row.ContextWindow < 0 || row.MaxTokens < 0 {
		return store.Model{}, FieldError(400, "contextWindow", "window and max tokens must not be negative")
	}
	return row, nil
}

// validatePrices rejects negative prices: they would make usage cost negative.
func validatePrices(in ModelInput) error {
	if in.InputPrice < 0 || in.OutputPrice < 0 || in.CachedInputPrice < 0 {
		return FieldError(400, "inputPrice", "prices must not be negative")
	}
	return nil
}

// defaultPublicName is the display id a client sends: the provider's name
// namespaced with the upstream model, as the contract specifies. Provider
// names are unique, so the ids cannot collide across providers; routing is an
// exact match on the whole string, never a split on the slash.
func defaultPublicName(providerName, upstreamName string) string {
	return providerName + "/" + upstreamName
}

// SetModelEnabled toggles a model.
func (s *Service) SetModelEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.d.Store.SetModelEnabled(id, enabled); err != nil {
		return translateStoreErr(err, "model")
	}
	return nil
}

// DeleteModel removes a model unless a route model references it.
func (s *Service) DeleteModel(ctx context.Context, id int64) error {
	if err := s.d.Store.DeleteModel(id); err != nil {
		return translateStoreErr(err, "model")
	}
	return nil
}

// ModelImportRow is one row of a bulk import.
type ModelImportRow struct {
	UpstreamName     string   `json:"upstreamName"`
	DisplayName      string   `json:"displayName"`
	PublicName       string   `json:"publicName"`
	UpstreamProtocol string   `json:"upstreamProtocol"`
	InputPrice       *float64 `json:"inputPrice"`
	OutputPrice      *float64 `json:"outputPrice"`
	ContextWindow    int      `json:"contextWindow"`
	MaxTokens        int      `json:"maxTokens"`
	SupportsVision   *bool    `json:"supportsVision"`
}

// ModelImportResult reports a bulk import.
type ModelImportResult struct {
	Created []ModelView `json:"created"`
	Skipped []string    `json:"skipped"`
	Failed  []string    `json:"failed"`
}

// ImportModels bulk-creates models from a discovery result.
func (s *Service) ImportModels(ctx context.Context, providerID int64, rows []ModelImportRow) (ModelImportResult, error) {
	if _, err := s.d.Store.GetProvider(providerID); err != nil {
		return ModelImportResult{}, translateStoreErr(err, "provider")
	}
	existing, err := s.d.Store.ListModels(store.ModelFilter{ProviderID: providerID})
	if err != nil {
		return ModelImportResult{}, err
	}
	known := make(map[string]bool, len(existing))
	for _, m := range existing {
		known[m.UpstreamName] = true
	}

	var result ModelImportResult
	for _, row := range rows {
		name := strings.TrimSpace(row.UpstreamName)
		if name == "" {
			result.Failed = append(result.Failed, "(empty upstream name)")
			continue
		}
		if known[name] {
			result.Skipped = append(result.Skipped, name)
			continue
		}

		in := ModelInput{
			ProviderID:   providerID,
			DisplayName:  row.DisplayName,
			PublicName:   row.PublicName,
			UpstreamName: name,
			// Discovery never knows the protocol; the caller states it. The
			// fallback is the provider's native vendor protocol.
			UpstreamProtocol: firstNonEmpty(row.UpstreamProtocol, string(ir.ProtoVendorNative)),
			ContextWindow:    row.ContextWindow,
			MaxTokens:        row.MaxTokens,
			SupportsVision:   row.SupportsVision,
		}
		if row.InputPrice != nil {
			in.InputPrice = *row.InputPrice
		}
		if row.OutputPrice != nil {
			in.OutputPrice = *row.OutputPrice
		}

		created, createErr := s.CreateModel(ctx, in)
		if createErr != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", name, createErr))
			continue
		}
		known[name] = true
		result.Created = append(result.Created, created)
	}
	return result, nil
}

// providerIndex loads providers keyed by id.
func (s *Service) providerIndex() (map[int64]store.Provider, error) {
	rows, err := s.d.Store.ListProviders()
	if err != nil {
		return nil, err
	}
	out := make(map[int64]store.Provider, len(rows))
	for _, p := range rows {
		out[p.ID] = p
	}
	return out, nil
}

func existingUpstreamName(existing *store.Model) string {
	if existing == nil {
		return ""
	}
	return existing.UpstreamName
}

func existingProtocol(existing *store.Model) string {
	if existing == nil {
		return ""
	}
	return existing.UpstreamProtocol
}
