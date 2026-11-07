package api

import (
	"net"
	"net/http"
	"strings"

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

// serveDataPlane authenticates the caller, resolves the endpoint and hands the
// call to the relay.
func (s *Server) serveDataPlane(w http.ResponseWriter, r *http.Request) {
	protocol, authRequired, ok := s.resolveEndpoint(r)
	if !ok {
		writeNotFound(w)
		return
	}

	key, failure := s.authenticate(r, authRequired)
	if failure != nil {
		writeClientError(w, protocol, failure)
		return
	}

	body, err := httpkit.ReadJSON(r, s.o.MaxBodyBytes)
	if err != nil {
		var tooLarge *httpkit.BodyTooLarge
		if ok := asTooLarge(err, &tooLarge); ok {
			writeClientError(w, protocol, &gateway.ClientError{
				Status:  http.StatusRequestEntityTooLarge,
				Type:    "invalid_request_error",
				Message: "request body is too large",
			})
			return
		}
		writeClientError(w, protocol, gateway.InvalidRequest("", "cannot read request body"))
		return
	}

	// The model is checked before the relay spends an upstream call.
	if failure := s.checkModelPermission(r, key, body); failure != nil {
		writeClientError(w, protocol, failure)
		return
	}

	s.o.Relay.Call(r.Context(), gateway.CallRequest{
		Inbound:     protocol,
		Body:        body,
		Endpoint:    r.URL.Path,
		ClientKeyID: keyID(key),
		Session:     sessionHints(r),
		Writer:      w,
	})
}

// resolveEndpoint returns the inbound protocol and whether a key is required.
func (s *Server) resolveEndpoint(r *http.Request) (ir.Protocol, bool, bool) {
	if protocol, ok := builtinRoutes[r.URL.Path]; ok {
		return protocol, true, true
	}
	if s.o.Store == nil {
		return "", false, false
	}
	endpoint, err := s.o.Store.GetEndpointByPath(r.URL.Path)
	if err != nil || !endpoint.Enabled {
		return "", false, false
	}
	return ir.Protocol(endpoint.InboundProtocol), endpoint.AuthRequired, true
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

// checkModelPermission enforces the key's model allowlist.
func (s *Server) checkModelPermission(r *http.Request, key *store.ClientKey, body []byte) *gateway.ClientError {
	if key == nil || len(key.AllowedModels) == 0 {
		return nil
	}
	model := peekModel(body)
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
