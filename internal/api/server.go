// Package api is the HTTP surface: /v1 for clients, /admin/api for management
// tools, /health for probes.
//
// It only parses, authorises and serialises. Every decision belongs to
// internal/admin (control plane) or internal/gateway (data plane), so the
// desktop UI can call the same services without going through HTTP.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/api/adminhttp"
	"github.com/learningdog1/ada_prune/internal/config"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
	"github.com/learningdog1/ada_prune/internal/httpkit"
	"github.com/learningdog1/ada_prune/internal/logging"
	"github.com/learningdog1/ada_prune/internal/ratelimit"
	"github.com/learningdog1/ada_prune/internal/store"
)

// Options configures the server.
type Options struct {
	Cfg          *config.Store
	Admin        *admin.Service
	Store        *store.Store
	Relay        *gateway.Relay
	Log          *logging.Logger
	AdminToken   string
	MaxInflight  int64
	MaxBodyBytes int64
	KeepAlive    time.Duration
	StartedAt    int64
}

// Server routes requests. Its zero value is not usable; call New.
type Server struct {
	o         Options
	adminHTTP http.Handler
	inflight  atomic.Int64
	// endpoints limits custom endpoint traffic per client address.
	endpoints *ratelimit.Limiter
}

// New builds the server.
func New(o Options) *Server {
	return &Server{
		o:         o,
		adminHTTP: adminhttp.NewHTTPHandler(o.Admin, o.AdminToken),
		endpoints: ratelimit.New(time.Minute),
	}
}

// EndpointLimiter exposes the endpoint rate limiter for the periodic sweep.
func (s *Server) EndpointLimiter() *ratelimit.Limiter { return s.endpoints }

// Inflight returns the number of requests currently admitted.
func (s *Server) Inflight() int64 { return s.inflight.Load() }

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := requestID(r)
		w.Header().Set("X-Request-ID", requestID)

		defer func() {
			if rec := recover(); rec != nil {
				s.o.Log.Error("Unhandled request panic", map[string]any{
					"path": r.URL.Path, "panic": fmt.Sprint(rec), "requestId": requestID,
				})
				httpkit.SendError(w, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError), "internal_error")
			}
		}()

		s.serve(w, r)
	})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	isAdmin := strings.HasPrefix(path, "/admin")

	// CORS: /v1 stays open for any client; /admin keeps its same-origin policy
	// and the optional token.
	if !isAdmin {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// In-flight admission; health and admin stay reachable when busy.
	isLiveness := path == "/health" || isAdmin
	if !isLiveness && s.o.MaxInflight > 0 {
		if s.inflight.Load() >= s.o.MaxInflight {
			s.o.Log.Warn("In-flight limit reached, rejecting request", map[string]any{
				"maxInflight": s.o.MaxInflight, "inflight": s.inflight.Load(), "path": path,
			})
			w.Header().Set("Retry-After", "5")
			httpkit.SendError(w, http.StatusServiceUnavailable, "too many concurrent requests", "server_busy")
			return
		}
		s.inflight.Add(1)
		defer s.inflight.Add(-1)
	}

	switch {
	case strings.HasPrefix(path, "/admin/api/"):
		s.adminHTTP.ServeHTTP(w, r)

	case path == "/health":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("OK"))

	case (path == "/v1/models" || path == "/models") && r.Method == http.MethodGet:
		s.models(w, r)

	case isDataPlaneRequest(r):
		s.serveDataPlane(w, r)

	default:
		httpkit.SendError(w, http.StatusNotFound, "not found", "not_found")
	}
}

// isDataPlaneRequest reports whether the path may be served by the relay: one of
// the built-in endpoints, or a custom endpoint registered in the store.
func isDataPlaneRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if _, ok := builtinRoutes[r.URL.Path]; ok {
		return true
	}
	return true // custom endpoints are resolved by the store inside serveDataPlane
}

// models answers GET /v1/models with the models this key may use.
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	key, failure := s.authenticate(r, true)
	if failure != nil {
		writeClientError(w, ir.ProtoOpenAIChat, failure)
		return
	}
	if s.o.Store == nil {
		httpkit.SendJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
		return
	}

	enabled := true
	// Contract §3.4: only routable models are listed. An enabled model under a
	// disabled provider (or one without an active credential) cannot serve, so
	// advertising it would mislead the client.
	rows, err := s.o.Admin.ListModels(r.Context(), admin.ModelFilterInput{Enabled: &enabled})
	if err != nil {
		httpkit.SendError(w, http.StatusInternalServerError, "cannot list models", "internal_error")
		return
	}

	allowed := map[string]bool{}
	if key != nil {
		for _, name := range key.AllowedModels {
			allowed[name] = true
		}
	}
	data := make([]map[string]any, 0, len(rows))
	byID := make(map[int64]admin.ModelView, len(rows))
	for _, view := range rows {
		byID[view.ID] = view
		if !view.Routable {
			continue
		}
		m := view.Model
		if len(allowed) > 0 && !allowed[m.PublicName] {
			continue
		}
		data = append(data, map[string]any{
			"id":             m.PublicName,
			"object":         "model",
			"created":        m.CreatedAt / 1000,
			"owned_by":       m.ProviderKind,
			"context_window": m.ContextWindow,
			"capabilities": map[string]any{
				"stream": m.SupportsStream,
				"tools":  m.SupportsTools,
				"vision": m.SupportsVision,
			},
		})
	}

	// A route model is also a callable id, so it belongs in the catalogue. Its
	// metadata is conservative: only members that can serve right now count,
	// and capabilities are intersected / the context window is the smallest,
	// because failover may land on any of them.
	routes, err := s.o.Admin.ListRouteModels(r.Context())
	if err != nil {
		httpkit.SendError(w, http.StatusInternalServerError, "cannot list models", "internal_error")
		return
	}
	for _, route := range routes {
		if !route.Enabled {
			continue
		}
		if len(allowed) > 0 && !allowed[route.Name] {
			continue
		}
		var live []admin.ModelView
		for _, member := range route.Members {
			if !member.Enabled {
				continue
			}
			if view, ok := byID[member.ModelID]; ok && view.Routable {
				live = append(live, view)
			}
		}
		if len(live) == 0 {
			continue
		}
		stream, tools, vision := true, true, true
		var contextWindow int
		for i, view := range live {
			m := view.Model
			if i == 0 || (m.ContextWindow > 0 && (contextWindow == 0 || m.ContextWindow < contextWindow)) {
				contextWindow = m.ContextWindow
			}
			stream = stream && m.SupportsStream
			tools = tools && m.SupportsTools
			vision = vision && m.SupportsVision
		}
		data = append(data, map[string]any{
			"id":             route.Name,
			"object":         "model",
			"created":        route.CreatedAt / 1000,
			"owned_by":       "route_model",
			"context_window": contextWindow,
			"capabilities": map[string]any{
				"stream": stream,
				"tools":  tools,
				"vision": vision,
			},
		})
	}
	httpkit.SendJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// requestID echoes the caller's id when present, else mints one.
func requestID(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Request-ID")); v != "" {
		return v
	}
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
