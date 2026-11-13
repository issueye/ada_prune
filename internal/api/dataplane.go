package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/httpkit"
	"github.com/learningdog1/ada_prune/internal/store"
)

// builtinRoutes maps the three fixed paths to their inbound protocol.
var builtinRoutes = map[string]ir.Protocol{
	"/v1/chat/completions": ir.ProtoOpenAIChat,
	"/v1/responses":        ir.ProtoOpenAIResponses,
	"/v1/messages":         ir.ProtoAnthropic,
}

// dataPlaneEndpoint is the resolved entry point for one request.
type dataPlaneEndpoint struct {
	Protocol     ir.Protocol
	Path         string
	AuthRequired bool
	// TargetType and TargetID pin a custom endpoint to one model or route
	// model. Builtin endpoints leave both empty and route by the request's
	// model field.
	TargetType string
	TargetID   int64
	// Timeout bounds one upstream call; zero means the provider setting.
	Timeout time.Duration
	// RatePerMin caps requests per client address per minute; zero is unlimited.
	RatePerMin int
}

// serveDataPlane authenticates the caller, resolves the endpoint and hands the
// call to the relay.
func (s *Server) serveDataPlane(w http.ResponseWriter, r *http.Request) {
	endpoint, ok := s.resolveEndpoint(r)
	if !ok {
		writeNotFound(w)
		return
	}

	// Rate limiting runs before authentication: an unauthenticated flood should
	// not reach the key lookup either.
	if endpoint.RatePerMin > 0 {
		bucket := endpoint.Path + "|" + clientAddress(r)
		if allowed, retry := s.endpoints.Allow(bucket, endpoint.RatePerMin); !allowed {
			writeClientError(w, endpoint.Protocol, &gateway.ClientError{
				Status:     http.StatusTooManyRequests,
				Type:       "rate_limit_error",
				Message:    "endpoint rate limit exceeded",
				Code:       "rate_limit_exceeded",
				RetryAfter: retry,
			})
			return
		}
	}

	key, failure := s.authenticate(r, endpoint.AuthRequired)
	if failure != nil {
		writeClientError(w, endpoint.Protocol, failure)
		return
	}

	// A custom endpoint pins its target; the body's model field is only a
	// fallback for the builtin endpoints.
	modelOverride := ""
	if endpoint.TargetType != "" {
		model, failure := s.endpointTarget(endpoint)
		if failure != nil {
			writeClientError(w, endpoint.Protocol, failure)
			return
		}
		modelOverride = model
	}

	body, err := httpkit.ReadJSON(r, s.o.MaxBodyBytes)
	if err != nil {
		var tooLarge *httpkit.BodyTooLarge
		if ok := asTooLarge(err, &tooLarge); ok {
			writeClientError(w, endpoint.Protocol, &gateway.ClientError{
				Status:  http.StatusRequestEntityTooLarge,
				Type:    "invalid_request_error",
				Message: "request body is too large",
			})
			return
		}
		writeClientError(w, endpoint.Protocol, gateway.InvalidRequest("", "cannot read request body"))
		return
	}

	// The effective model is checked before the relay spends an upstream call.
	// For a pinned endpoint that is the target, not whatever the body carries.
	effectiveModel := modelOverride
	if effectiveModel == "" {
		effectiveModel = peekModel(body)
	}
	if failure := s.checkModelPermission(key, effectiveModel); failure != nil {
		writeClientError(w, endpoint.Protocol, failure)
		return
	}

	s.o.Relay.Call(r.Context(), gateway.CallRequest{
		Inbound:       endpoint.Protocol,
		Body:          body,
		Endpoint:      r.URL.Path,
		ClientKeyID:   keyID(key),
		Session:       sessionHints(r),
		ModelOverride: modelOverride,
		Timeout:       endpoint.Timeout,
		Writer:        w,
	})
}

// resolveEndpoint returns the entry point that serves the request path. Builtin
// paths win over custom ones; custom endpoints match by longest segment prefix.
func (s *Server) resolveEndpoint(r *http.Request) (dataPlaneEndpoint, bool) {
	if protocol, ok := builtinRoutes[r.URL.Path]; ok {
		return dataPlaneEndpoint{Protocol: protocol, Path: r.URL.Path, AuthRequired: true}, true
	}
	if s.o.Store == nil {
		return dataPlaneEndpoint{}, false
	}
	endpoint, err := s.o.Store.MatchEndpointByPath(r.URL.Path)
	if err != nil || !endpoint.Enabled {
		return dataPlaneEndpoint{}, false
	}
	return dataPlaneEndpoint{
		Protocol:     ir.Protocol(endpoint.InboundProtocol),
		Path:         endpoint.Path,
		AuthRequired: endpoint.AuthRequired,
		TargetType:   endpoint.TargetType,
		TargetID:     endpoint.TargetID,
		Timeout:      time.Duration(endpoint.TimeoutMs) * time.Millisecond,
		RatePerMin:   endpoint.RatePerMin,
	}, true
}

// endpointTarget resolves the public id a custom endpoint pins. A target that
// vanished after the endpoint was saved is a server-side misconfiguration, not
// a client error.
func (s *Server) endpointTarget(endpoint dataPlaneEndpoint) (string, *gateway.ClientError) {
	unavailable := &gateway.ClientError{
		Status:     http.StatusServiceUnavailable,
		Type:       "overloaded_error",
		Message:    "endpoint target is not available",
		RetryAfter: 30,
	}
	switch endpoint.TargetType {
	case store.TargetModel:
		model, err := s.o.Store.GetModel(endpoint.TargetID)
		if err != nil || model.PublicName == "" {
			return "", unavailable
		}
		return model.PublicName, nil
	case store.TargetRouteModel:
		route, err := s.o.Store.GetRouteModel(endpoint.TargetID)
		if err != nil || route.Name == "" {
			return "", unavailable
		}
		return route.Name, nil
	default:
		return "", unavailable
	}
}

// authenticate verifies the client key and its network restrictions.
func (s *Server) authenticate(r *http.Request, required bool) (*store.ClientKey, *gateway.ClientError) {
	token := bearerToken(r)
	if token == "" && !required {
		return nil, nil
	}
	if token == "" {
		return nil, &gateway.ClientError{
			Status:  http.StatusUnauthorized,
			Type:    "authentication_error",
			Message: "missing API key; send Authorization: Bearer sk-prune-… or x-api-key",
		}
	}

	key, err := s.o.Store.VerifyClientKey(token)
	if err != nil {
		if !required && s.o.Cfg.Get().AllowAnonymous {
			return nil, nil
		}
		return nil, &gateway.ClientError{
			Status:  http.StatusUnauthorized,
			Type:    "authentication_error",
			Message: "invalid, disabled or expired API key",
		}
	}

	if !ipAllowed(r, key.AllowedIPs) {
		return nil, &gateway.ClientError{
			Status:  http.StatusForbidden,
			Type:    "permission_error",
			Message: "this API key is not allowed from the request's address",
		}
	}
	if key.QuotaTokens > 0 && key.QuotaUsed >= key.QuotaTokens {
		return nil, &gateway.ClientError{
			Status:     http.StatusTooManyRequests,
			Type:       "rate_limit_error",
			Message:    "api key quota exhausted",
			Code:       "quota_exhausted",
			RetryAfter: 60,
		}
	}
	return key, nil
}

// checkModelPermission enforces the key's model allowlist against the model the
// request will actually be routed to.
func (s *Server) checkModelPermission(key *store.ClientKey, model string) *gateway.ClientError {
	if key == nil || len(key.AllowedModels) == 0 {
		return nil
	}
	if model == "" {
		// No model in the body: the relay falls back to the configured default and
		// the allowlist cannot be enforced here. Refusing is safer than guessing.
		return gateway.InvalidRequest("model", "model is required")
	}
	for _, allowed := range key.AllowedModels {
		if allowed == model {
			return nil
		}
	}
	return &gateway.ClientError{
		Status:  http.StatusForbidden,
		Type:    "permission_error",
		Message: "this API key may not use model " + model,
		Code:    "model_not_allowed",
	}
}

// sessionHints collects the client's session clues.
func sessionHints(r *http.Request) gateway.SessionHints {
	hints := gateway.SessionHints{
		RequestID:      r.Header.Get("X-Request-ID"),
		PromptCacheKey: r.Header.Get("x-prompt-cache-key"),
	}
	for _, name := range []string{"x-session-id", "x-claude-code-session-id", "session_id"} {
		if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
			hints.SessionID = v
			break
		}
	}
	return hints
}

// bearerToken reads the key from either supported header.
func bearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if v := strings.TrimSpace(r.Header.Get("x-api-key")); v != "" {
		return v
	}
	return ""
}

// ipAllowed matches the request address against an allowlist of IPs and CIDRs.
func ipAllowed(r *http.Request, allowlist string) bool {
	allowlist = strings.TrimSpace(allowlist)
	if allowlist == "" {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, entry := range strings.Split(allowlist, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
	}
	return false
}

// clientAddress is the peer address used for per-endpoint rate limiting.
//
// Forwarding headers are deliberately ignored: anything a client can write must
// not become a rate-limit bucket key, or the limit is trivially bypassed.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// peekModel reads just the model field, without decoding the whole body.
func peekModel(body []byte) string {
	var probe struct {
		Model string `json:"model"`
	}
	if err := jsonUnmarshal(body, &probe); err != nil {
		return ""
	}
	return probe.Model
}

func keyID(key *store.ClientKey) int64 {
	if key == nil {
		return 0
	}
	return key.ID
}

// writeClientError renders a data-plane failure in the caller's protocol.
func writeClientError(w http.ResponseWriter, protocol ir.Protocol, failure *gateway.ClientError) {
	if failure.RetryAfter > 0 {
		w.Header().Set("Retry-After", itoa(failure.RetryAfter))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(failure.Status)
	_, _ = w.Write(gateway.EncodeClientError(protocol, failure))
}

func writeNotFound(w http.ResponseWriter) {
	httpkit.SendError(w, http.StatusNotFound, "not found", "not_found")
}
