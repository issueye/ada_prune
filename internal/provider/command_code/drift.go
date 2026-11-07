package command_code

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/learningdog1/ada_proxy/internal/gateway"
)

// DriftStatus reports whether the CLI version this adapter implements is still
// the published one. Drift is a warning to re-read the upstream package, not a
// reason to change the version we claim: claiming a shape we do not speak is
// itself a suspicious signal.
type DriftStatus struct {
	LastCheckedAt   *int64  `json:"lastCheckedAt"`
	LatestVersion   *string `json:"latestVersion"`
	ProtocolVersion string  `json:"protocolVersion"`
	Changed         bool    `json:"changed"`
	LastError       *string `json:"lastError"`
}

// driftCache holds the last check so the status page does not hit the network.
type driftCache struct {
	mu     sync.Mutex
	status DriftStatus
}

// driftURL is the published package metadata.
const driftURL = "https://registry.npmjs.org/command-code/latest"

// CheckDrift queries the package registry once and caches the result.
func (a *Adapter) CheckDrift(ctx context.Context, p gateway.ProviderConfig) (DriftStatus, error) {
	cfg, err := parseConfig(p)
	if err != nil {
		cfg = Config{ProtocolVersion: ProtocolVersion}
	}
	now := a.clock().UnixMilli()

	status := DriftStatus{
		LastCheckedAt:   &now,
		ProtocolVersion: cfg.ProtocolVersion,
	}

	latest, fetchErr := fetchLatestVersion(ctx, a.httpClient(cfg, p))
	if fetchErr != nil {
		msg := truncate(fetchErr.Error(), 200)
		status.LastError = &msg
		a.storeDrift(status)
		return status, fetchErr
	}
	status.LatestVersion = &latest
	status.Changed = latest != cfg.ProtocolVersion
	a.storeDrift(status)

	if status.Changed {
		return status, fmt.Errorf("command_code: CLI version drift: implemented %s, published %s",
			cfg.ProtocolVersion, latest)
	}
	return status, nil
}

// Drift returns the cached status.
func (a *Adapter) Drift() DriftStatus {
	a.drift.mu.Lock()
	defer a.drift.mu.Unlock()
	status := a.drift.status
	if status.ProtocolVersion == "" {
		status.ProtocolVersion = ProtocolVersion
	}
	return status
}

func (a *Adapter) storeDrift(status DriftStatus) {
	a.drift.mu.Lock()
	a.drift.status = status
	a.drift.mu.Unlock()
}

func fetchLatestVersion(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, driftURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("command_code: registry returned HTTP %d", resp.StatusCode)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return "", fmt.Errorf("command_code: decode registry answer: %w", err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("command_code: registry returned no version")
	}
	return pkg.Version, nil
}

// driftCheckInterval throttles automatic checks.
const driftCheckInterval = 24 * time.Hour
