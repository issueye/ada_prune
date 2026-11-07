package admin

import (
	"context"
	"errors"
	"strings"

	"github.com/learningdog1/ada_prune/internal/store"
)

// RouteModelInput is the create/update payload.
type RouteModelInput struct {
	Name     string             `json:"name"`
	Strategy string             `json:"strategy"`
	Enabled  *bool              `json:"enabled"`
	Remark   string             `json:"remark"`
	Members  []RouteMemberInput `json:"members"`
}

// RouteMemberInput is one member of a route model.
type RouteMemberInput struct {
	ModelID  int64 `json:"modelId"`
	Priority int   `json:"priority"`
	Weight   int   `json:"weight"`
	Enabled  *bool `json:"enabled"`
}

// ListRouteModels returns every route model.
func (s *Service) ListRouteModels(ctx context.Context) ([]store.RouteModel, error) {
	return s.d.Store.ListRouteModels()
}

// GetRouteModel returns one route model.
func (s *Service) GetRouteModel(ctx context.Context, id int64) (store.RouteModel, error) {
	row, err := s.d.Store.GetRouteModel(id)
	if err != nil {
		return store.RouteModel{}, translateStoreErr(err, "route model")
	}
	return *row, nil
}

// CreateRouteModel validates and inserts a route model.
func (s *Service) CreateRouteModel(ctx context.Context, in RouteModelInput) (store.RouteModel, error) {
	row, err := s.routeModelFromInput(in, nil)
	if err != nil {
		return store.RouteModel{}, err
	}
	id, err := s.d.Store.CreateRouteModel(row)
	if err != nil {
		return store.RouteModel{}, translateStoreErr(err, "route model")
	}
	return s.GetRouteModel(ctx, id)
}

// UpdateRouteModel replaces the scalars and the whole member list.
func (s *Service) UpdateRouteModel(ctx context.Context, id int64, in RouteModelInput) (store.RouteModel, error) {
	existing, err := s.d.Store.GetRouteModel(id)
	if err != nil {
		return store.RouteModel{}, translateStoreErr(err, "route model")
	}
	row, err := s.routeModelFromInput(in, existing)
	if err != nil {
		return store.RouteModel{}, err
	}
	row.ID = id
	if err := s.d.Store.UpdateRouteModel(row); err != nil {
		return store.RouteModel{}, translateStoreErr(err, "route model")
	}
	return s.GetRouteModel(ctx, id)
}

// routeModelFromInput validates the payload.
func (s *Service) routeModelFromInput(in RouteModelInput, existing *store.RouteModel) (store.RouteModel, error) {
	name, err := validateName("name", in.Name, true)
	if err != nil {
		return store.RouteModel{}, err
	}
	// The data plane resolves a plain model before a route model, so a route
	// model sharing a model's public name would be unreachable. Reject the
	// collision here instead of shipping a route nobody can call.
	if _, modelErr := s.d.Store.GetModelByPublicName(name); modelErr == nil {
		return store.RouteModel{}, FieldError(409, "name", "name conflicts with an existing model")
	} else if !errors.Is(modelErr, store.ErrNotFound) {
		return store.RouteModel{}, modelErr
	}

	strategy := strings.TrimSpace(in.Strategy)
	if strategy == "" {
		if existing != nil {
			strategy = existing.Strategy
		} else {
			strategy = store.StrategyPriority
		}
	}
	switch strategy {
	case store.StrategyPriority, store.StrategyWeighted:
	default:
		return store.RouteModel{}, FieldError(400, "strategy", "strategy must be priority or weighted")
	}

	members, err := s.routeMembers(in.Members)
	if err != nil {
		return store.RouteModel{}, err
	}
	if len(members) == 0 {
		return store.RouteModel{}, FieldError(400, "members", "a route model needs at least one member")
	}

	row := store.RouteModel{
		Name:     name,
		Strategy: strategy,
		Enabled:  true,
		Remark:   strings.TrimSpace(in.Remark),
		Members:  members,
	}
	if existing != nil {
		row.Enabled = existing.Enabled
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	return row, nil
}

// routeMembers validates each membership row.
func (s *Service) routeMembers(inputs []RouteMemberInput) ([]store.RouteModelMember, error) {
	seen := make(map[int64]bool, len(inputs))
	out := make([]store.RouteModelMember, 0, len(inputs))
	for _, in := range inputs {
		if in.ModelID <= 0 {
			return nil, FieldError(400, "members", "every member needs a modelId")
		}
		if seen[in.ModelID] {
			return nil, FieldError(400, "members", "duplicate member model %d", in.ModelID)
		}
		if _, err := s.d.Store.GetModel(in.ModelID); err != nil {
			return nil, translateStoreErr(err, "model")
		}
		weight := in.Weight
		if weight <= 0 {
			weight = 1
		}
		member := store.RouteModelMember{
			ModelID:  in.ModelID,
			Priority: in.Priority,
			Weight:   weight,
			Enabled:  true,
		}
		if in.Enabled != nil {
			member.Enabled = *in.Enabled
		}
		seen[in.ModelID] = true
		out = append(out, member)
	}
	return out, nil
}

// DeleteRouteModel removes a route model.
func (s *Service) DeleteRouteModel(ctx context.Context, id int64) error {
	if err := s.d.Store.DeleteRouteModel(id); err != nil {
		return translateStoreErr(err, "route model")
	}
	return nil
}
