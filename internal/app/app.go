// Package app is the composition root: configuration, storage, the admin
// service, the HTTP surface and the background tasks, wired once and shared by
// both the desktop shell and the headless server.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/learningdog1/ada_prune/internal/admin"
	"github.com/learningdog1/ada_prune/internal/api"
	"github.com/learningdog1/ada_prune/internal/config"
	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/logging"
	"github.com/learningdog1/ada_prune/internal/paths"
	"github.com/learningdog1/ada_prune/internal/provider"
	// Registering the adapters is a blank import: each package adds itself to
	// the registry from init, so wiring stays in one place.
	_ "github.com/learningdog1/ada_prune/internal/provider/anthropic"
	_ "github.com/learningdog1/ada_prune/internal/provider/command_code"
	_ "github.com/learningdog1/ada_prune/internal/provider/openai"
	"github.com/learningdog1/ada_prune/internal/secretbox"
	"github.com/learningdog1/ada_prune/internal/store"
)

// Options configures the app. An empty DataDir resolves through
// PRUNE_DATA_DIR, then "<executable dir>/data".
type Options struct {
	DataDir string
	// AdminToken overrides PRUNE_ADMIN_TOKEN when non-empty.
	AdminToken string
}

// App owns everything the process needs while it runs.
type App struct {
	Cfg       *config.Store
	Store     *store.Store
	Log       *logging.Logger
	Admin     *admin.Service
	Server    *api.Server
	Registry  *provider.Registry
	Router    *gateway.Router
	Relay     *gateway.Relay
	StartedAt int64
	Dirs      paths.Dirs

	recorder    *recorder
	adminToken  string
	keepAlive   time.Duration
	maxInflight int64

	httpServer *http.Server
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	closeOnce  sync.Once
}

// New opens configuration and storage, wires the services and prepares the HTTP
// server. It does not listen yet: the caller owns the listener so port
// selection and error reporting stay in one place.
func New(opts Options) (*App, error) {
	dirs := paths.Resolve(opts.DataDir)
	if err := os.MkdirAll(dirs.Data, 0o755); err != nil {
		return nil, fmt.Errorf("app: create data directory: %w", err)
	}

	cfg, err := config.Open(dirs.Config)
	if err != nil {
		return nil, err
	}
	snap := cfg.Get()
	if err := snap.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirs.Logs, 0o755); err != nil {
		return nil, fmt.Errorf("app: create log directory: %w", err)
	}

	logPath := snap.LogFile
	if logPath == "" {
		logPath = dirs.LogFile
	}
	log := logging.New(logPath, logging.ParseLevel(snap.LogLevel))

	box, err := secretbox.Open(dirs.SecretKey)
	if err != nil {
		log.Close()
		return nil, err
	}
	st := store.Open(dirs.DB, box)
	if err := st.Migrate(); err != nil {
		st.Close()
		log.Close()
		return nil, err
	}

	startedAt := time.Now().UnixMilli()

	// One shared HTTP client for every adapter. Connection pooling is what lets
	// the gateway sustain many long-lived streams without exhausting descriptors.
	sharedHTTP := &http.Client{Timeout: 0}
	registry := provider.Build(provider.Deps{
		Log:   log,
		HTTP:  sharedHTTP,
		State: stateStore{st: st},
		Clock: time.Now,
	})

	router := gateway.NewRouter(catalog{st: st}, registry)
	rec := newRecorder(st, log)
	relay := gateway.NewRelay(router, rec, log, gateway.Limits{
		MaxBodyBytes:  snap.MaxBodyMB << 20,
		StreamIdle:    time.Duration(snap.StreamIdleMs) * time.Millisecond,
		NonStreamIdle: time.Duration(snap.NonStreamIdleMs) * time.Millisecond,
	})
	relay.SetDefaultModel(snap.DefaultModel)
	// The watchdogs are runtime-tunable from the settings page: every call
	// reads the current snapshot, so a saved value applies to the next request
	// without a restart. (The request-body cap in api.Server stays a startup
	// value; this source governs the relay's own reads.)
	relay.SetLimitsSource(func() gateway.Limits {
		snap := cfg.Get()
		return gateway.Limits{
			MaxBodyBytes:  snap.MaxBodyMB << 20,
			StreamIdle:    time.Duration(snap.StreamIdleMs) * time.Millisecond,
			NonStreamIdle: time.Duration(snap.NonStreamIdleMs) * time.Millisecond,
		}
	})

	a := &App{
		Cfg:         cfg,
		Store:       st,
		Log:         log,
		Registry:    registry,
		Router:      router,
		Relay:       relay,
		recorder:    rec,
		StartedAt:   startedAt,
		Dirs:        dirs,
		adminToken:  firstNonEmpty(opts.AdminToken, os.Getenv("PRUNE_ADMIN_TOKEN")),
		keepAlive:   time.Duration(snap.KeepAliveTimeoutMs) * time.Millisecond,
		maxInflight: snap.MaxInflight,
	}

	// The admin service reports in-flight requests, and the server needs the
	// admin service. The closure breaks the cycle without a nil receiver.
	var srv *api.Server
	a.Admin = admin.New(admin.Deps{
		Cfg:       cfg,
		Store:     st,
		Log:       log,
		Registry:  registry,
		Dirs:      dirs,
		StartedAt: startedAt,
		Inflight: func() int64 {
			if srv == nil {
				return 0
			}
			return srv.Inflight()
		},
	})
	srv = api.New(api.Options{
		Cfg:          cfg,
		Admin:        a.Admin,
		Store:        st,
		Relay:        relay,
		Log:          log,
		AdminToken:   a.adminToken,
		MaxInflight:  a.maxInflight,
		MaxBodyBytes: snap.MaxBodyMB << 20,
		KeepAlive:    a.keepAlive,
		StartedAt:    startedAt,
	})
	a.Server = srv

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.startBackground(ctx)
	return a, nil
}

// KeepAlive returns the keep-alive timeout the server uses; reverse proxies must
// idle shorter than this.
func (a *App) KeepAlive() time.Duration { return a.keepAlive }

// Listen binds exactly host:port.
func (a *App) Listen(host string, port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
}

// ListenScanning tries port, port+1, ... port+19 and returns the first free one.
// The desktop shell uses it so another service on the default port does not
// break startup.
func (a *App) ListenScanning(host string, port int) (net.Listener, int, error) {
	var lastErr error
	for i := 0; i < 20; i++ {
		p := port + i
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err == nil {
			return ln, p, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("app: ports %d..%d are all in use: %w", port, port+19, lastErr)
}

// Serve runs the HTTP server on ln until it is shut down.
func (a *App) Serve(ln net.Listener) error {
	a.httpServer = &http.Server{
		Handler:           a.Server.Handler(),
		IdleTimeout:       a.keepAlive,
		ReadHeaderTimeout: a.keepAlive + time.Second,
		// No write timeout: SSE responses are long lived by design.
		WriteTimeout: 0,
	}
	a.Log.Info("AdaPrune started", map[string]any{
		"url":              "http://" + ln.Addr().String(),
		"dataDir":          a.Dirs.Data,
		"database":         a.Dirs.DB,
		"degraded":         a.Store.Degraded(),
		"keepAliveTimeout": a.keepAlive.String(),
	})
	if a.adminToken == "" {
		a.Log.Info("Admin API is open (loopback only). Set PRUNE_ADMIN_TOKEN to require a bearer token.", nil)
	}
	if !isLoopbackHost(a.Cfg.Get().Host) {
		a.Log.Warn("Non-loopback host: the admin API is reachable from the network", map[string]any{
			"host": a.Cfg.Get().Host,
			"hint": "only expose this on a trusted network, and set PRUNE_ADMIN_TOKEN",
		})
	}
	if a.Store.Degraded() {
		a.Log.Warn("Storage is degraded: nothing will persist after this process exits", nil)
	}

	err := a.httpServer.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the HTTP server.
func (a *App) Shutdown(ctx context.Context) error {
	if a.httpServer == nil {
		return nil
	}
	return a.httpServer.Shutdown(ctx)
}

// Close releases the store and the logger. It is idempotent.
func (a *App) Close() {
	a.closeOnce.Do(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.wg.Wait()
		a.Store.Close()
		a.Log.Close()
	})
}

// startBackground runs the periodic jobs. Each one exits on ctx cancellation and
// Close waits for them.
func (a *App) startBackground(ctx context.Context) {
	// The request recorder persists finished calls off the hot path.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.recorder.Run(ctx)
	}()

	// Rate windows expire on their own; sweeping keeps the map from growing and
	// lets a recovered credential be chosen again.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		interval := time.Duration(a.Cfg.Get().IdleWindowSweepInterval) * time.Millisecond
		if interval <= 0 {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if freed := a.Router.Windows().Sweep(); freed > 0 {
					a.Log.Info("Rate windows recovered", map[string]any{"credentials": freed})
				}
				// Custom endpoint buckets are per client address; expired ones
				// would otherwise accumulate for the life of the process.
				a.Server.EndpointLimiter().Sweep()
			}
		}
	}()

	// Request log retention: once at startup, then daily.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		cleanup := func() {
			days := a.Cfg.Get().LogRetentionDays
			if n := a.Store.CleanupRequests(days); n > 0 {
				a.Log.Info("Request log cleanup", map[string]any{"deleted": n, "retentionDays": days})
			}
		}
		cleanup()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}
