// Package admin is the control plane's business layer.
//
// It knows nothing about HTTP: internal/api adapts it, and the desktop UI calls
// it directly. Every method takes a context.Context and returns either a value
// or an *Error carrying the status the transport should use.
package admin

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/learningdog1/ada_proxy/internal/config"
	"github.com/learningdog1/ada_proxy/internal/logging"
	"github.com/learningdog1/ada_proxy/internal/paths"
	"github.com/learningdog1/ada_proxy/internal/provider"
	"github.com/learningdog1/ada_proxy/internal/store"
	"github.com/learningdog1/ada_proxy/internal/version"
)

// Error carries an HTTP status so the transport adapter can answer correctly.
type Error struct {
	Status  int
	Message string
	// Field names the offending form field, when there is one.
	Field string
}

func (e *Error) Error() string { return e.Message }

// Errorf builds an *Error.
func Errorf(status int, format string, args ...any) *Error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

// FieldError builds an *Error bound to a form field.
func FieldError(status int, field, format string, args ...any) *Error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...), Field: field}
}

// StatusOf maps an error to the HTTP status the adapter should use.
func StatusOf(err error) int {
	if e, ok := err.(*Error); ok && e.Status != 0 {
		return e.Status
	}
	return 500
}

// Deps wires the service to the rest of the process.
type Deps struct {
	Cfg       *config.Store
	Store     *store.Store
	Log       *logging.Logger
	Registry  *provider.Registry
	Dirs      paths.Dirs
	StartedAt int64
	Inflight  func() int64
}

// Service is the management layer.
type Service struct {
	d Deps
}

// New returns a management service.
func New(d Deps) *Service { return &Service{d: d} }

// Deps exposes the injected dependencies for adapters that need them.
func (s *Service) Deps() Deps { return s.d }

// Overview is the dashboard payload.
type Overview struct {
	Version     string             `json:"version"`
	StartedAt   int64              `json:"startedAt"`
	UptimeSec   int64              `json:"uptimeSec"`
	Host        string             `json:"host"`
	Port        int                `json:"port"`
	Degraded    bool               `json:"degraded"`
	Inflight    int64              `json:"inflight"`
	DataDir     string             `json:"dataDir"`
	Providers   CountPair          `json:"providers"`
	Credentials CredentialCounts   `json:"credentials"`
	Models      CountPair          `json:"models"`
	ClientKeys  CountPair          `json:"clientKeys"`
	Today       store.UsageSummary `json:"today"`
	Last24h     store.Last24Hours  `json:"last24h"`
}

// CountPair is a total/enabled pair used by several cards.
type CountPair struct {
	Total   int64 `json:"total"`
	Enabled int64 `json:"enabled"`
}

// CredentialCounts breaks credentials down by routing state.
type CredentialCounts struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Exhausted int64 `json:"exhausted"`
}

// Overview assembles the dashboard payload.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	today, err := s.d.Store.TodayStats(dayStart.UnixMilli())
	if err != nil {
		return Overview{}, Errorf(500, "cannot aggregate today's usage")
	}
	chart, err := s.d.Store.Last24Hours(now.UnixMilli(), 24)
	if err != nil {
		return Overview{}, Errorf(500, "cannot aggregate the last 24 hours")
	}
	providers, err := s.d.Store.ListProviders()
	if err != nil {
		return Overview{}, Errorf(500, "cannot list providers")
	}
	models, err := s.d.Store.ListModels(store.ModelFilter{})
	if err != nil {
		return Overview{}, Errorf(500, "cannot list models")
	}
	keys, err := s.d.Store.ListClientKeys()
	if err != nil {
		return Overview{}, Errorf(500, "cannot list client keys")
	}
	credentials, err := s.d.Store.ListCredentials(0)
	if err != nil {
		return Overview{}, Errorf(500, "cannot list credentials")
	}

	out := Overview{
		Version:   version.String(),
		StartedAt: s.d.StartedAt,
		Host:      s.d.Cfg.Get().Host,
		Port:      s.d.Cfg.Get().Port,
		Degraded:  s.d.Store.Degraded(),
		DataDir:   s.d.Dirs.Data,
		Today:     today,
		Last24h:   chart,
	}
	if s.d.StartedAt > 0 {
		out.UptimeSec = (now.UnixMilli() - s.d.StartedAt) / 1000
	}
	if s.d.Inflight != nil {
		out.Inflight = s.d.Inflight()
	}
	for _, p := range providers {
		out.Providers.Total++
		if p.Enabled {
			out.Providers.Enabled++
		}
	}
	for _, m := range models {
		out.Models.Total++
		if m.Enabled {
			out.Models.Enabled++
		}
	}
	for _, k := range keys {
		out.ClientKeys.Total++
		if k.Enabled {
			out.ClientKeys.Enabled++
		}
	}
	for _, c := range credentials {
		out.Credentials.Total++
		if c.Status == store.CredentialActive && c.Enabled {
			out.Credentials.Active++
		}
		if c.Status == store.CredentialExhausted {
			out.Credentials.Exhausted++
		}
	}
	return out, nil
}

// Status is the runtime state payload.
type Status struct {
	Version      string   `json:"version"`
	StartedAt    int64    `json:"startedAt"`
	UptimeSec    int64    `json:"uptimeSec"`
	Degraded     bool     `json:"degraded"`
	DataDir      string   `json:"dataDir"`
	DatabasePath string   `json:"databasePath"`
	LogFile      string   `json:"logFile"`
	PinnedKeys   []string `json:"envPinned"`
	GoRoutines   int      `json:"goroutines"`
}

// Status reports process state for the about page and for scripts.
func (s *Service) Status(ctx context.Context) (Status, error) {
	now := time.Now()
	out := Status{
		Version:      version.String(),
		StartedAt:    s.d.StartedAt,
		Degraded:     s.d.Store.Degraded(),
		DataDir:      s.d.Dirs.Data,
		DatabasePath: s.d.Dirs.DB,
		LogFile:      s.d.Log.Path(),
		PinnedKeys:   s.d.Cfg.PinnedKeys(),
		GoRoutines:   runtime.NumGoroutine(),
	}
	if s.d.StartedAt > 0 {
		out.UptimeSec = (now.UnixMilli() - s.d.StartedAt) / 1000
	}
	return out, nil
}

// Adapters describes every built adapter: kind, capabilities and the config
// schema the "new provider" wizard renders.
func (s *Service) Adapters(ctx context.Context) ([]provider.Descriptor, error) {
	if s.d.Registry == nil {
		return []provider.Descriptor{}, nil
	}
	return s.d.Registry.Descriptors(), nil
}

// ExportBundle is a portable configuration snapshot. It never contains a
// plaintext secret: credentials carry their mask, so an import cannot restore
// keys and must ask for them again.
type ExportBundle struct {
	Version     string             `json:"version"`
	ExportedAt  int64              `json:"exportedAt"`
	Settings    map[string]any     `json:"settings"`
	Providers   []store.Provider   `json:"providers"`
	Credentials []ExportCredential `json:"credentials"`
	Models      []store.Model      `json:"models"`
	ClientKeys  []ExportClientKey  `json:"clientKeys"`
	Endpoints   []store.Endpoint   `json:"endpoints"`
}

// ExportCredential is a credential without its secret.
type ExportCredential struct {
	ProviderName string `json:"providerName"`
	Name         string `json:"name"`
	Mask         string `json:"mask"`
	Enabled      bool   `json:"enabled"`
	Priority     int    `json:"priority"`
	Status       string `json:"status"`
	Remark       string `json:"remark"`
}

// ExportClientKey is a client key without its hash.
type ExportClientKey struct {
	Name          string   `json:"name"`
	KeyPrefix     string   `json:"keyPrefix"`
	Enabled       bool     `json:"enabled"`
	AllowedModels []string `json:"allowedModels"`
	AllowedIPs    string   `json:"allowedIps"`
	QuotaTokens   int64    `json:"quotaTokens"`
	Remark        string   `json:"remark"`
}

// Export returns the portable snapshot described above.
func (s *Service) Export(ctx context.Context) (ExportBundle, error) {
	providers, err := s.d.Store.ListProviders()
	if err != nil {
		return ExportBundle{}, err
	}
	credentials, err := s.d.Store.ListCredentials(0)
	if err != nil {
		return ExportBundle{}, err
	}
	models, err := s.d.Store.ListModels(store.ModelFilter{})
	if err != nil {
		return ExportBundle{}, err
	}
	keys, err := s.d.Store.ListClientKeys()
	if err != nil {
		return ExportBundle{}, err
	}
	endpoints, err := s.d.Store.ListEndpoints()
	if err != nil {
		return ExportBundle{}, err
	}

	out := ExportBundle{
		Version:    version.String(),
		ExportedAt: time.Now().UnixMilli(),
		Settings:   s.d.Cfg.FileView(),
		Providers:  providers,
		Models:     models,
		Endpoints:  endpoints,
	}
	for _, c := range credentials {
		out.Credentials = append(out.Credentials, ExportCredential{
			ProviderName: c.ProviderName,
			Name:         c.Name,
			Mask:         c.SecretMask,
			Enabled:      c.Enabled,
			Priority:     c.Priority,
			Status:       c.Status,
			Remark:       c.Remark,
		})
	}
	for _, k := range keys {
		out.ClientKeys = append(out.ClientKeys, ExportClientKey{
			Name:          k.Name,
			KeyPrefix:     k.KeyPrefix,
			Enabled:       k.Enabled,
			AllowedModels: k.AllowedModels,
			AllowedIPs:    k.AllowedIPs,
			QuotaTokens:   k.QuotaTokens,
			Remark:        k.Remark,
		})
	}
	return out, nil
}

// SettingsView is the settings form. Secrets are reported as booleans only.
type SettingsView struct {
	Port                    int      `json:"port"`
	Host                    string   `json:"host"`
	LogFile                 string   `json:"logFile"`
	LogLevel                string   `json:"logLevel"`
	LogRetentionDays        int      `json:"logRetentionDays"`
	DefaultModel            string   `json:"defaultModel"`
	AllowAnonymous          bool     `json:"allowAnonymous"`
	MaxBodyMB               int64    `json:"maxBodyMB"`
	StreamIdleMs            int64    `json:"streamIdleMs"`
	NonStreamIdleMs         int64    `json:"nonStreamIdleMs"`
	MaxInflight             int64    `json:"maxInflight"`
	KeepAliveTimeoutMs      int64    `json:"keepAliveTimeoutMs"`
	ModelRefreshIntervalMs  int64    `json:"modelRefreshIntervalMs"`
	AccountUsageRefreshMs   int64    `json:"accountUsageRefreshMs"`
	DriftCheckIntervalMs    int64    `json:"driftCheckIntervalMs"`
	IdleWindowSweepInterval int64    `json:"idleWindowSweepIntervalMs"`
	EnvPinned               []string `json:"envPinned"`
	DataDir                 string   `json:"dataDir"`
	ConfigPath              string   `json:"configPath"`
}

// Settings returns the effective configuration.
func (s *Service) Settings(ctx context.Context) (SettingsView, error) {
	c := s.d.Cfg.Get()
	return SettingsView{
		Port:                    c.Port,
		Host:                    c.Host,
		LogFile:                 c.LogFile,
		LogLevel:                c.LogLevel,
		LogRetentionDays:        c.LogRetentionDays,
		DefaultModel:            c.DefaultModel,
		AllowAnonymous:          c.AllowAnonymous,
		MaxBodyMB:               c.MaxBodyMB,
		StreamIdleMs:            c.StreamIdleMs,
		NonStreamIdleMs:         c.NonStreamIdleMs,
		MaxInflight:             c.MaxInflight,
		KeepAliveTimeoutMs:      c.KeepAliveTimeoutMs,
		ModelRefreshIntervalMs:  c.ModelRefreshIntervalMs,
		AccountUsageRefreshMs:   c.AccountUsageRefreshMs,
		DriftCheckIntervalMs:    c.DriftCheckIntervalMs,
		IdleWindowSweepInterval: c.IdleWindowSweepInterval,
		EnvPinned:               s.d.Cfg.PinnedKeys(),
		DataDir:                 s.d.Dirs.Data,
		ConfigPath:              s.d.Cfg.Path(),
	}, nil
}

// SaveSettingsResult reports what a settings write changed.
type SaveSettingsResult struct {
	OK              bool           `json:"ok"`
	RestartRequired []string       `json:"restartRequired,omitempty"`
	Ignored         []string       `json:"ignored,omitempty"`
	Saved           map[string]any `json:"saved,omitempty"`
}

// SaveSettings merges a patch into config.json.
func (s *Service) SaveSettings(ctx context.Context, patch map[string]any) (SaveSettingsResult, error) {
	if patch == nil {
		return SaveSettingsResult{}, Errorf(400, "empty settings patch")
	}
	res, err := s.d.Cfg.Save(patch)
	if err != nil {
		return SaveSettingsResult{}, Errorf(500, "cannot save settings: %v", err)
	}
	if level, ok := res.Saved["logLevel"].(string); ok {
		s.d.Log.SetLevel(logging.ParseLevel(level))
	}
	if file, ok := res.Saved["logFile"].(string); ok {
		s.d.Log.SetFile(file)
	}
	return SaveSettingsResult{
		OK:              true,
		RestartRequired: res.RestartRequired,
		Ignored:         res.Ignored,
		Saved:           res.Saved,
	}, nil
}
