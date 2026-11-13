package command_code

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
)

// Reporting cadence. The upstream expects a credential to announce its device
// shortly after first use and then refresh periodically; a key that never
// reports looks like a stolen credential.
const (
	reportJitterMin      = time.Minute
	reportJitterMax      = 4 * time.Minute
	defaultReportRefresh = 8 * time.Hour
	defaultReportJitter  = 2 * time.Hour
)

// credentialReport is the persisted state of one credential's announcements.
type credentialReport struct {
	NextReportAt      int64                `json:"nextReportAt"`
	FingerprintReport gateway.ReportResult `json:"fingerprintReport"`
	LifecycleReport   gateway.ReportResult `json:"lifecycleReport"`
}

// reportBook is the persisted map, one entry per credential.
type reportBook struct {
	Credentials map[int64]credentialReport `json:"credentials"`
}

// keyBook holds the in-memory view, guarded by wallClockMuNeutral state.
type keyBook struct {
	mu   sync.Mutex
	byID map[int64]*keyEntry
}

type keyEntry struct {
	fingerprint Fingerprint
	report      credentialReport
}

// Derive implements gateway.FingerprintPolicy.
func (a *Adapter) Derive(p gateway.ProviderConfig, cred gateway.Credential) gateway.Identity {
	cfg, err := parseConfig(p)
	if err != nil {
		cfg = Config{FingerprintSalt: "", ProjectDir: DefaultProjectDir}
	}
	return GenerateFingerprint(cred.Secret, cfg.FingerprintSalt, cfg.ProjectDir).asIdentity()
}

// Schedule implements gateway.FingerprintPolicy.
func (a *Adapter) Schedule() gateway.ReportSchedule {
	return gateway.ReportSchedule{
		Every:    defaultReportRefresh,
		Jitter:   defaultReportJitter,
		RetryMin: reportJitterMin,
		RetryMax: reportJitterMax,
	}
}

// entry returns the in-memory key entry, creating it from the persisted book if
// this is the first touch since start.
func (a *Adapter) entry(providerID int64, cred gateway.Credential, cfg Config) *keyEntry {
	a.keys.mu.Lock()
	defer a.keys.mu.Unlock()
	if e, ok := a.keys.byID[cred.ID]; ok {
		return e
	}
	e := &keyEntry{
		fingerprint: GenerateFingerprint(cred.Secret, cfg.FingerprintSalt, cfg.ProjectDir),
	}
	e.report = a.loadReport(providerID, cred.ID)
	a.keys.byID[cred.ID] = e
	return e
}

// loadReport reads one credential's persisted announcements.
func (a *Adapter) loadReport(providerID, credID int64) credentialReport {
	if a.state == nil {
		return credentialReport{}
	}
	var book reportBook
	found, err := a.state.Get(providerID, gateway.StateFingerprint, &book)
	if err != nil || !found || book.Credentials == nil {
		return credentialReport{}
	}
	return book.Credentials[credID]
}

// saveReport writes one credential's announcements back. The lock covers the
// whole read-modify-write: concurrent reports for different credentials would
// otherwise drop each other's entries, making the loser report again on the
// next request.
func (a *Adapter) saveReport(providerID, credID int64, report credentialReport) {
	if a.state == nil {
		return
	}
	a.reportBookMu.Lock()
	defer a.reportBookMu.Unlock()

	var book reportBook
	if found, err := a.state.Get(providerID, gateway.StateFingerprint, &book); err == nil && found && book.Credentials != nil {
		// keep the other entries
	} else {
		book = reportBook{Credentials: map[int64]credentialReport{}}
	}
	if book.Credentials == nil {
		book.Credentials = map[int64]credentialReport{}
	}
	book.Credentials[credID] = report
	_ = a.state.Set(providerID, gateway.StateFingerprint, book)
}

// maybeReport announces the device identity when the schedule says it is due.
// It never blocks the request that triggered it: a failed report is retried in a
// few minutes and the data plane is unaffected either way.
func (a *Adapter) maybeReport(provider gateway.ProviderConfig, cred gateway.Credential, cfg Config) {
	e := a.entry(provider.ID, cred, cfg)
	now := a.clock().UnixMilli()
	if e.report.NextReportAt > now {
		return
	}

	a.reportingMu.Lock()
	if a.reporting[cred.ID] {
		a.reportingMu.Unlock()
		return
	}
	a.reporting[cred.ID] = true
	a.reportingMu.Unlock()

	go func() {
		defer func() {
			a.reportingMu.Lock()
			delete(a.reporting, cred.ID)
			a.reportingMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// The full config, not just the id: Report resolves the API base,
		// proxy, fingerprint salt, project directory and protocol version from
		// it, and a stripped config would report a different device to the
		// default upstream.
		_, _ = a.Report(ctx, gateway.FingerprintRequest{
			Provider:   provider,
			Credential: cred,
			Identity:   e.fingerprint.asIdentity(),
		})
	}()
}

// Report implements gateway.FingerprintPolicy: it announces the fingerprint and a
// lifecycle event, then schedules the next refresh.
func (a *Adapter) Report(ctx context.Context, in gateway.FingerprintRequest) (gateway.ReportResult, error) {
	providerID := in.Provider.ID
	cfg, err := parseConfig(in.Provider)
	if err != nil {
		// The provider blob may be incomplete when called from a background task;
		// fall back to defaults rather than refusing to report.
		cfg = Config{
			APIBase:            DefaultAPIBase,
			ProtocolVersion:    ProtocolVersion,
			ProjectDir:         DefaultProjectDir,
			ReportRefreshHours: DefaultReportRefreshHours,
		}
	}
	base := apiBaseOf(cfg, in.Provider)
	fp := GenerateFingerprint(in.Credential.Secret, cfg.FingerprintSalt, cfg.ProjectDir)

	lifecycle := map[string]any{
		"eventType": "cli_session_exists",
		"metadata": map[string]any{
			"sessionId":  "sess_" + randomHex(8),
			"cliVersion": cfg.ProtocolVersion,
			"mode":       firstNonEmpty(cfg.CLISessionMode, "interactive"),
			"os":         fp.Components.Platform + "-" + fp.Components.Arch,
		},
	}

	client := a.httpClient(cfg, in.Provider)
	fpRes, lcRes := a.postPair(ctx, client, base, in.Credential.Secret, cfg, fp, lifecycle)

	now := a.clock()
	report := credentialReport{
		FingerprintReport: fpRes,
		LifecycleReport:   lcRes,
	}
	if fpRes.OK && lcRes.OK {
		refresh := time.Duration(cfg.ReportRefreshHours) * time.Hour
		if refresh <= 0 {
			refresh = defaultReportRefresh
		}
		report.NextReportAt = now.Add(refresh + time.Duration(randInt63n(int64(defaultReportJitter)))).UnixMilli()
	} else {
		retry := reportJitterMin + time.Duration(randInt63n(int64(reportJitterMax-reportJitterMin)))
		report.NextReportAt = now.Add(retry).UnixMilli()
	}

	a.keys.mu.Lock()
	if e, ok := a.keys.byID[in.Credential.ID]; ok {
		e.report = report
		e.fingerprint = fp
	}
	a.keys.mu.Unlock()
	a.saveReport(providerID, in.Credential.ID, report)

	result := fpRes
	if !fpRes.OK {
		return result, fmt.Errorf("command_code: fingerprint report failed: %s", fpRes.Error)
	}
	if !lcRes.OK {
		return lcRes, fmt.Errorf("command_code: lifecycle report failed: %s", lcRes.Error)
	}
	return result, nil
}

// postPair sends the two announcement calls concurrently and waits for both.
func (a *Adapter) postPair(ctx context.Context, client *http.Client, base, secret string, cfg Config,
	fingerprint Fingerprint, lifecycle map[string]any) (gateway.ReportResult, gateway.ReportResult) {
	type outcome struct {
		result gateway.ReportResult
	}
	fpCh := make(chan outcome, 1)
	lcCh := make(chan outcome, 1)

	post := func(path string, payload any) gateway.ReportResult {
		body, err := json.Marshal(payload)
		if err != nil {
			return gateway.ReportResult{TS: a.clock().UnixMilli(), OK: false, Error: err.Error()}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
		if err != nil {
			return gateway.ReportResult{TS: a.clock().UnixMilli(), OK: false, Error: err.Error()}
		}
		for k, values := range usageHeaders(secret, cfg.ProjectDir, cfg.ProtocolVersion, nil) {
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.ZDR {
			req.Header.Set("x-cmd-zdr", "1")
		}
		resp, err := client.Do(req)
		if err != nil {
			return gateway.ReportResult{TS: a.clock().UnixMilli(), OK: false, Error: truncate(err.Error(), 200)}
		}
		defer func() { _ = resp.Body.Close() }()
		ok := resp.StatusCode >= 200 && resp.StatusCode < 300
		return gateway.ReportResult{TS: a.clock().UnixMilli(), OK: ok, Status: resp.StatusCode}
	}

	go func() { fpCh <- outcome{post("/alpha/fingerprint/record", fingerprint)} }()
	go func() { lcCh <- outcome{post("/alpha/lifecycle-events", lifecycle)} }()
	return (<-fpCh).result, (<-lcCh).result
}

// Sweep implements gateway.Sweeper: it drops cached identity state for
// credentials whose report book has grown stale, and reports what it forgot.
func (a *Adapter) Sweep(now time.Time) []int64 {
	a.keys.mu.Lock()
	defer a.keys.mu.Unlock()
	var dropped []int64
	for id, entry := range a.keys.byID {
		if entry.report.NextReportAt == 0 {
			continue // never reported yet: keep it, the first call will report
		}
		if now.UnixMilli()-entry.report.FingerprintReport.TS > 30*24*3600*1000 {
			delete(a.keys.byID, id)
			dropped = append(dropped, id)
		}
	}
	return dropped
}

// randInt63n returns a random int in [0, n).
func randInt63n(n int64) int64 {
	if n <= 1 {
		return 0
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0
	}
	var v uint64
	for _, b := range buf {
		v = v<<8 | uint64(b)
	}
	return int64(v % uint64(n))
}
