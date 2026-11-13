// Package config loads data/config.json, writes it back, and applies the
// environment overrides the deployment relies on.
//
// Behaviour:
//   - first run writes a default config.json so operators have something to edit;
//   - environment variables override the file and are never written back;
//   - keys pinned to an environment variable ignore writes and are reported;
//   - unknown keys in the file are preserved.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// Snapshot is one complete set of configuration values. It is immutable: a
// saved change replaces the whole snapshot under a lock, so readers never see a
// half-applied update.
//
// The JSON names are the file's contract. Field order is the write order.
type Snapshot struct {
	// Port and Host are the listener. Changing them needs a restart.
	Port int    `json:"port"`
	Host string `json:"host"`

	// LogFile, LogLevel and LogRetentionDays control the line log and the
	// retention of the requests table.
	LogFile          string `json:"logFile"`
	LogLevel         string `json:"logLevel"`
	LogRetentionDays int    `json:"logRetentionDays"`

	// DefaultModel is used when a request carries no model. Empty means the
	// request must name a model.
	DefaultModel string `json:"defaultModel"`
	// AllowAnonymous lets data-plane calls through without a client key. It is
	// meant for loopback debugging only.
	AllowAnonymous bool `json:"allowAnonymous"`

	// MaxBodyMB bounds the request body read from a client.
	MaxBodyMB int64 `json:"maxBodyMB"`
	// StreamIdleMs and NonStreamIdleMs are the upstream idle watchdogs. The
	// clock resets on every event because a model may legitimately think for a
	// long time between tokens.
	StreamIdleMs    int64 `json:"streamIdleMs"`
	NonStreamIdleMs int64 `json:"nonStreamIdleMs"`
	// MaxInflight caps concurrent data-plane calls. 0 means unlimited.
	MaxInflight int64 `json:"maxInflight"`
	// KeepAliveTimeoutMs is the HTTP keep-alive window. Reverse proxies must
	// idle shorter than this.
	KeepAliveTimeoutMs int64 `json:"keepAliveTimeoutMs"`

	// Background refresh intervals. Each is honoured only by adapters that
	// implement the corresponding optional capability.
	ModelRefreshIntervalMs  int64 `json:"modelRefreshIntervalMs"`
	AccountUsageRefreshMs   int64 `json:"accountUsageRefreshMs"`
	DriftCheckIntervalMs    int64 `json:"driftCheckIntervalMs"`
	IdleWindowSweepInterval int64 `json:"idleWindowSweepIntervalMs"`
}

// Defaults returns the values written on first run.
func Defaults() Snapshot {
	return Snapshot{
		Port:                    3050,
		Host:                    "127.0.0.1",
		LogFile:                 "",
		LogLevel:                "info",
		LogRetentionDays:        30,
		DefaultModel:            "",
		AllowAnonymous:          false,
		MaxBodyMB:               100,
		StreamIdleMs:            30_000,
		NonStreamIdleMs:         90_000,
		MaxInflight:             0,
		KeepAliveTimeoutMs:      65_000,
		ModelRefreshIntervalMs:  300_000,
		AccountUsageRefreshMs:   300_000,
		DriftCheckIntervalMs:    86_400_000,
		IdleWindowSweepInterval: 60_000,
	}
}

// envKeys maps configuration keys to the environment variables that pin them.
// The order is the documented order, used when reporting pinned keys.
var envKeys = []struct{ Key, Env string }{
	{"port", "PRUNE_PORT"},
	{"host", "PRUNE_HOST"},
	{"logFile", "PRUNE_LOG_FILE"},
	{"logLevel", "PRUNE_LOG_LEVEL"},
	{"logRetentionDays", "PRUNE_LOG_RETENTION_DAYS"},
	{"defaultModel", "PRUNE_DEFAULT_MODEL"},
	{"allowAnonymous", "PRUNE_ALLOW_ANONYMOUS"},
	{"maxBodyMB", "PRUNE_MAX_BODY_MB"},
	{"streamIdleMs", "PRUNE_STREAM_IDLE_MS"},
	{"nonStreamIdleMs", "PRUNE_NONSTREAM_IDLE_MS"},
	{"maxInflight", "PRUNE_MAX_INFLIGHT"},
	{"keepAliveTimeoutMs", "PRUNE_KEEPALIVE_TIMEOUT_MS"},
	{"modelRefreshIntervalMs", "PRUNE_MODEL_REFRESH_MS"},
	{"accountUsageRefreshMs", "PRUNE_ACCOUNT_USAGE_REFRESH_MS"},
	{"driftCheckIntervalMs", "PRUNE_DRIFT_CHECK_MS"},
}

// SaveResult reports what Save did with the requested patch.
type SaveResult struct {
	// RestartRequired names host/port changes that only take effect on restart.
	RestartRequired []string `json:"restartRequired,omitempty"`
	// Ignored names keys pinned to an environment variable whose value differed.
	Ignored []string `json:"ignored,omitempty"`
	// Saved reports the effective values of the keys that were written.
	Saved map[string]any `json:"saved,omitempty"`
}

// Store owns the configuration file and the effective snapshot.
type Store struct {
	mu        sync.RWMutex
	path      string
	file      map[string]any
	effective Snapshot
	pinned    []string
}

// Open loads (or creates) config.json at path and applies environment
// overrides. The parent directory is created when missing.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("config: create directory: %w", err)
	}
	s := &Store{path: path}
	file, err := s.read()
	if err != nil {
		return nil, err
	}
	if len(file) == 0 {
		file = snapshotMap(Defaults())
		if err := s.write(file); err != nil {
			return nil, err
		}
	}
	s.file = file
	s.effective = applyEnv(mergeFile(Defaults(), file))
	s.pinned = pinnedKeys()
	return s, nil
}

// Get returns the effective snapshot. The value is a copy and safe to keep.
func (s *Store) Get() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.effective
}

// Path returns the configuration file path.
func (s *Store) Path() string { return s.path }

// PinnedKeys returns the configuration keys the environment overrides.
func (s *Store) PinnedKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.pinned...)
}

// FileView returns the defaults merged with the file layer, without environment
// overrides. Unknown file keys are included. The result is a deep copy.
func (s *Store) FileView() map[string]any {
	s.mu.RLock()
	file := cloneMap(s.file)
	s.mu.RUnlock()
	return mergeMaps(snapshotMap(Defaults()), file)
}

// Save merges patch into the file and reloads the effective snapshot.
//
// Read-only and secret keys are dropped, keys pinned by the environment are
// dropped and reported, and host/port changes are reported as needing a
// restart. The file is re-read first so an operator editing it by hand is not
// silently overwritten.
func (s *Store) Save(patch map[string]any) (SaveResult, error) {
	var result SaveResult

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.read()
	if err != nil {
		return result, err
	}
	if len(file) == 0 {
		file = snapshotMap(Defaults())
	}

	writable := writableKeys()
	pinned := map[string]bool{}
	for _, k := range s.pinned {
		pinned[k] = true
	}
	cleaned := make(map[string]any, len(patch))
	for k, v := range patch {
		if !writable[k] {
			continue
		}
		if pinned[k] && fmt.Sprint(v) != fmt.Sprint(valueOf(s.effective, k)) {
			result.Ignored = append(result.Ignored, k)
			continue
		}
		cleaned[k] = v
	}

	// These values are read once when the process starts (the listener, the
	// request-body cap and the in-flight cap), so a saved change only takes
	// effect after a restart.
	for _, k := range []string{"host", "port", "maxBodyMB", "maxInflight", "keepAliveTimeoutMs"} {
		if v, ok := cleaned[k]; ok && fmt.Sprint(v) != fmt.Sprint(valueOf(s.effective, k)) {
			result.RestartRequired = append(result.RestartRequired, k)
		}
	}

	for k, v := range cleaned {
		file[k] = v
	}
	if err := s.write(file); err != nil {
		return result, err
	}

	s.file = file
	s.effective = applyEnv(mergeFile(Defaults(), file))
	s.pinned = pinnedKeys()

	result.Saved = make(map[string]any, len(cleaned))
	for k := range cleaned {
		result.Saved[k] = valueOf(s.effective, k)
	}
	return result, nil
}

func (s *Store) read() (map[string]any, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read: %w", err)
	}
	file := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &file); err != nil {
			// A broken file must not stop the proxy: start from defaults and
			// report it, so the operator can fix the file by hand.
			fmt.Fprintf(os.Stderr, "[config] cannot parse %s: %v\n", s.path, err)
			return map[string]any{}, nil
		}
	}
	return file, nil
}

// write serializes the merged defaults first (in Snapshot field order), then
// any unknown keys in sorted order, and replaces the file atomically.
func (s *Store) write(file map[string]any) error {
	merged := mergeMaps(snapshotMap(Defaults()), file)

	var b strings.Builder
	b.WriteString("{\n")
	first := true
	emit := func(k string, v any) error {
		encoded, err := json.MarshalIndent(v, "  ", "  ")
		if err != nil {
			return err
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		b.WriteString("  ")
		if kb, err := json.Marshal(k); err == nil {
			b.Write(kb)
		}
		b.WriteString(": ")
		b.Write(encoded)
		return nil
	}

	t := reflect.TypeOf(Snapshot{})
	seen := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		k := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		v, ok := merged[k]
		if !ok {
			continue
		}
		seen[k] = true
		if err := emit(k, v); err != nil {
			return err
		}
	}
	rest := make([]string, 0, len(merged))
	for k := range merged {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		if err := emit(k, merged[k]); err != nil {
			return err
		}
	}
	b.WriteString("\n}\n")

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("config: replace: %w", err)
	}
	return nil
}

// mergeFile overlays the file layer on base, ignoring unknown keys but keeping
// the typed result.
func mergeFile(base Snapshot, file map[string]any) Snapshot {
	merged := mergeMaps(snapshotMap(base), file)
	out := base
	raw, err := json.Marshal(merged)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// applyEnv reapplies the environment overrides.
func applyEnv(s Snapshot) Snapshot {
	setStr := func(env string, dst *string) {
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	setInt := func(env string, dst *int) {
		if v := os.Getenv(env); v != "" {
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				*dst = n
			}
		}
	}
	setInt64 := func(env string, dst *int64) {
		if v := os.Getenv(env); v != "" {
			var n int64
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				*dst = n
			}
		}
	}
	setBool := func(env string, dst *bool) {
		if v := os.Getenv(env); v != "" {
			switch strings.ToLower(v) {
			case "1", "true", "yes", "on":
				*dst = true
			default:
				*dst = false
			}
		}
	}

	setInt("PRUNE_PORT", &s.Port)
	setStr("PRUNE_HOST", &s.Host)
	setStr("PRUNE_LOG_FILE", &s.LogFile)
	setStr("PRUNE_LOG_LEVEL", &s.LogLevel)
	setInt("PRUNE_LOG_RETENTION_DAYS", &s.LogRetentionDays)
	setStr("PRUNE_DEFAULT_MODEL", &s.DefaultModel)
	setBool("PRUNE_ALLOW_ANONYMOUS", &s.AllowAnonymous)
	setInt64("PRUNE_MAX_BODY_MB", &s.MaxBodyMB)
	setInt64("PRUNE_STREAM_IDLE_MS", &s.StreamIdleMs)
	setInt64("PRUNE_NONSTREAM_IDLE_MS", &s.NonStreamIdleMs)
	setInt64("PRUNE_MAX_INFLIGHT", &s.MaxInflight)
	setInt64("PRUNE_KEEPALIVE_TIMEOUT_MS", &s.KeepAliveTimeoutMs)
	setInt64("PRUNE_MODEL_REFRESH_MS", &s.ModelRefreshIntervalMs)
	setInt64("PRUNE_ACCOUNT_USAGE_REFRESH_MS", &s.AccountUsageRefreshMs)
	setInt64("PRUNE_DRIFT_CHECK_MS", &s.DriftCheckIntervalMs)
	return s
}

func pinnedKeys() []string {
	out := []string{}
	for _, e := range envKeys {
		if os.Getenv(e.Env) != "" {
			out = append(out, e.Key)
		}
	}
	return out
}

// Validate reports configuration that cannot work.
func (s Snapshot) Validate() error {
	switch {
	case s.Port < 1 || s.Port > 65535:
		return fmt.Errorf("config: port %d is out of range 1..65535", s.Port)
	case strings.TrimSpace(s.Host) == "":
		return fmt.Errorf("config: host is empty")
	case s.MaxBodyMB <= 0:
		return fmt.Errorf("config: maxBodyMB must be positive")
	case s.StreamIdleMs <= 0:
		return fmt.Errorf("config: streamIdleMs must be positive")
	case s.NonStreamIdleMs <= 0:
		return fmt.Errorf("config: nonStreamIdleMs must be positive")
	case s.MaxInflight < 0:
		return fmt.Errorf("config: maxInflight must not be negative")
	case s.KeepAliveTimeoutMs <= 0:
		return fmt.Errorf("config: keepAliveTimeoutMs must be positive")
	case s.LogRetentionDays < 1:
		return fmt.Errorf("config: logRetentionDays must be at least 1")
	}
	return nil
}

func writableKeys() map[string]bool {
	out := make(map[string]bool)
	t := reflect.TypeOf(Snapshot{})
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

func snapshotMap(s Snapshot) map[string]any {
	raw, _ := json.Marshal(s)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func mergeMaps(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopy(v)
	}
	return out
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = deepCopy(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = deepCopy(vv)
		}
		return out
	default:
		return v
	}
}

// valueOf reads a field of Snapshot by its JSON name.
func valueOf(s Snapshot, key string) any {
	return snapshotMap(s)[key]
}
