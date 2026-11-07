package admin

import (
	"context"
	"strings"

	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/store"
)

// builtinEndpoints are always served and cannot be edited or deleted.
func builtinEndpoints() []store.Endpoint {
	make := func(path, protocol, name string) store.Endpoint {
		return store.Endpoint{
			ID:              0,
			Name:            name,
			Path:            path,
			InboundProtocol: protocol,
			TargetType:      "",
			AuthRequired:    true,
			Enabled:         true,
			Builtin:         true,
		}
	}
	return []store.Endpoint{
		make("/v1/chat/completions", string(ir.ProtoOpenAIChat), "OpenAI Chat"),
		make("/v1/responses", string(ir.ProtoOpenAIResponses), "OpenAI Responses"),
		make("/v1/messages", string(ir.ProtoAnthropic), "Anthropic Messages"),
	}
}

// EndpointInput is the create/update payload.
type EndpointInput struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	InboundProtocol string `json:"inboundProtocol"`
	TargetType      string `json:"targetType"`
	TargetID        int64  `json:"targetId"`
	AuthRequired    *bool  `json:"authRequired"`
	RatePerMin      int    `json:"ratePerMin"`
	TimeoutMs       int    `json:"timeoutMs"`
	Enabled         *bool  `json:"enabled"`
	Remark          string `json:"remark"`
}

// ListEndpoints returns the builtin endpoints followed by the custom ones.
func (s *Service) ListEndpoints(ctx context.Context) ([]store.Endpoint, error) {
	custom, err := s.d.Store.ListEndpoints()
	if err != nil {
		return nil, err
	}
	out := builtinEndpoints()
	out = append(out, custom...)
	return out, nil
}

// CreateEndpoint validates and stores a custom endpoint.
func (s *Service) CreateEndpoint(ctx context.Context, in EndpointInput) (store.Endpoint, error) {
	row, err := s.endpointFromInput(in, nil)
	if err != nil {
		return store.Endpoint{}, err
	}
	id, err := s.d.Store.CreateEndpoint(row)
	if err != nil {
		return store.Endpoint{}, translateStoreErr(err, "endpoint")
	}
	return s.getEndpoint(id)
}

// UpdateEndpoint replaces the mutable fields.
func (s *Service) UpdateEndpoint(ctx context.Context, id int64, in EndpointInput) (store.Endpoint, error) {
	existing, err := s.d.Store.GetEndpoint(id)
	if err != nil {
		return store.Endpoint{}, translateStoreErr(err, "endpoint")
	}
	row, err := s.endpointFromInput(in, existing)
	if err != nil {
		return store.Endpoint{}, err
	}
	row.ID = id
	if err := s.d.Store.UpdateEndpoint(row); err != nil {
		return store.Endpoint{}, translateStoreErr(err, "endpoint")
	}
	return s.getEndpoint(id)
}

// endpointFromInput validates the payload.
func (s *Service) endpointFromInput(in EndpointInput, existing *store.Endpoint) (store.Endpoint, error) {
	name, err := validateName("name", in.Name, true)
	if err != nil {
		return store.Endpoint{}, err
	}

	path := strings.TrimSpace(in.Path)
	if existing != nil && path == "" {
		path = existing.Path
	}
	if err := validateEndpointPath(path); err != nil {
		return store.Endpoint{}, err
	}
	for _, builtin := range builtinEndpoints() {
		if builtin.Path == path {
			return store.Endpoint{}, FieldError(400, "path",
				"%s is a builtin endpoint and cannot be redefined", path)
		}
	}

	protocol, err := parseProtocol("inboundProtocol", firstNonEmpty(in.InboundProtocol, existingProtocolString(existing)))
	if err != nil {
		return store.Endpoint{}, err
	}

	targetType := strings.TrimSpace(in.TargetType)
	if existing != nil && targetType == "" {
		targetType = existing.TargetType
	}
	switch targetType {
	case store.TargetModel, store.TargetRouteModel:
	case "":
		return store.Endpoint{}, FieldError(400, "targetType", "targetType is required")
	default:
		return store.Endpoint{}, FieldError(400, "targetType", "targetType must be model or route_model")
	}

	targetID := in.TargetID
	if targetID == 0 && existing != nil {
		targetID = existing.TargetID
	}
	if targetID <= 0 {
		return store.Endpoint{}, FieldError(400, "targetId", "targetId is required")
	}
	if err := s.ensureTargetExists(targetType, targetID); err != nil {
		return store.Endpoint{}, err
	}

	row := store.Endpoint{
		Name:            name,
		Path:            path,
		InboundProtocol: string(protocol),
		TargetType:      targetType,
		TargetID:        targetID,
		AuthRequired:    true,
		RatePerMin:      in.RatePerMin,
		TimeoutMs:       in.TimeoutMs,
		Enabled:         true,
		Remark:          strings.TrimSpace(in.Remark),
	}
	if existing != nil {
		row.AuthRequired = existing.AuthRequired
		row.Enabled = existing.Enabled
	}
	if in.AuthRequired != nil {
		row.AuthRequired = *in.AuthRequired
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if row.RatePerMin < 0 || row.TimeoutMs < 0 {
		return store.Endpoint{}, FieldError(400, "ratePerMin", "rate and timeout must not be negative")
	}
	return row, nil
}

// validateEndpointPath requires a rooted, absolute style path.
func validateEndpointPath(path string) error {
	if path == "" {
		return FieldError(400, "path", "path is required")
	}
	if !strings.HasPrefix(path, "/") {
		return FieldError(400, "path", "path must start with /")
	}
	if strings.ContainsAny(path, " \t?#") {
		return FieldError(400, "path", "path must not contain spaces, queries or fragments")
	}
	if strings.HasSuffix(path, "/") && path != "/" {
		return FieldError(400, "path", "path must not end with /")
	}
	return nil
}

// ensureTargetExists checks the endpoint's target is real.
func (s *Service) ensureTargetExists(targetType string, targetID int64) error {
	switch targetType {
	case store.TargetModel:
		if _, err := s.d.Store.GetModel(targetID); err != nil {
			return translateStoreErr(err, "model")
		}
	case store.TargetRouteModel:
		if _, err := s.d.Store.GetRouteModel(targetID); err != nil {
			return translateStoreErr(err, "route model")
		}
	}
	return nil
}

// DeleteEndpoint removes a custom endpoint.
func (s *Service) DeleteEndpoint(ctx context.Context, id int64) error {
	if err := s.d.Store.DeleteEndpoint(id); err != nil {
		return translateStoreErr(err, "endpoint")
	}
	return nil
}

// GetEndpoint returns one custom endpoint.
func (s *Service) GetEndpoint(ctx context.Context, id int64) (store.Endpoint, error) {
	return s.getEndpoint(id)
}

func (s *Service) getEndpoint(id int64) (store.Endpoint, error) {
	row, err := s.d.Store.GetEndpoint(id)
	if err != nil {
		return store.Endpoint{}, translateStoreErr(err, "endpoint")
	}
	return *row, nil
}

func existingProtocolString(existing *store.Endpoint) string {
	if existing == nil {
		return ""
	}
	return existing.InboundProtocol
}
