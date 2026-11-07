package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/secretbox"
	"github.com/learningdog1/ada_prune/internal/store"
)

// maxImportLines bounds a pasted batch, so one paste cannot create thousands of
// rows.
const maxImportLines = 50

// CredentialInput is the create/update payload.
type CredentialInput struct {
	ProviderID int64  `json:"providerId"`
	Name       string `json:"name"`
	Secret     string `json:"secret"`
	Enabled    *bool  `json:"enabled"`
	Priority   *int   `json:"priority"`
	ExpiresAt  *int64 `json:"expiresAt"`
	Remark     string `json:"remark"`
}

// ListCredentials returns credentials, optionally for one provider. Secrets are
// always masked.
func (s *Service) ListCredentials(ctx context.Context, providerID int64) ([]store.Credential, error) {
	if providerID > 0 {
		if _, err := s.d.Store.GetProvider(providerID); err != nil {
			return nil, translateStoreErr(err, "provider")
		}
	}
	return s.d.Store.ListCredentials(providerID)
}

// GetCredential returns one credential with its mask and pin state.
func (s *Service) GetCredential(ctx context.Context, id int64) (store.Credential, error) {
	return s.getCredential(id)
}

// CreateCredential encrypts and stores an upstream key.
func (s *Service) CreateCredential(ctx context.Context, in CredentialInput) (store.Credential, error) {
	if in.ProviderID <= 0 {
		return store.Credential{}, FieldError(400, "providerId", "providerId is required")
	}
	if _, err := s.d.Store.GetProvider(in.ProviderID); err != nil {
		return store.Credential{}, translateStoreErr(err, "provider")
	}
	name, err := validateName("name", in.Name, true)
	if err != nil {
		return store.Credential{}, err
	}
	secret := strings.TrimSpace(in.Secret)
	if secret == "" {
		return store.Credential{}, FieldError(400, "secret", "secret is required")
	}
	if secretbox.IsMask(secret) {
		return store.Credential{}, FieldError(400, "secret", "secret looks masked; send the real key")
	}

	row := store.Credential{
		ProviderID: in.ProviderID,
		Name:       name,
		Secret:     secret,
		Enabled:    true,
		Status:     store.CredentialActive,
		ExpiresAt:  in.ExpiresAt,
		Remark:     strings.TrimSpace(in.Remark),
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.Priority != nil {
		row.Priority = *in.Priority
	}
	if err := validateExpiry(row.ExpiresAt); err != nil {
		return store.Credential{}, err
	}

	id, err := s.d.Store.CreateCredential(row)
	if err != nil {
		return store.Credential{}, translateStoreErr(err, "credential")
	}
	return s.getCredential(id)
}

// UpdateCredential changes metadata. An empty or masked secret keeps the stored
// one; the plaintext never round-trips through the UI.
func (s *Service) UpdateCredential(ctx context.Context, id int64, in CredentialInput) (store.Credential, error) {
	existing, err := s.d.Store.GetCredential(id, false)
	if err != nil {
		return store.Credential{}, translateStoreErr(err, "credential")
	}
	name, err := validateName("name", firstNonEmpty(in.Name, existing.Name), true)
	if err != nil {
		return store.Credential{}, err
	}

	row := *existing
	row.Name = name
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.Priority != nil {
		row.Priority = *in.Priority
	}
	if in.ExpiresAt != nil {
		row.ExpiresAt = in.ExpiresAt
	}
	if in.Remark != "" {
		row.Remark = strings.TrimSpace(in.Remark)
	}
	if err := validateExpiry(row.ExpiresAt); err != nil {
		return store.Credential{}, err
	}

	secret := strings.TrimSpace(in.Secret)
	if secretbox.IsMask(secret) {
		secret = ""
	}
	if err := s.d.Store.UpdateCredential(row, secret); err != nil {
		return store.Credential{}, translateStoreErr(err, "credential")
	}
	return s.getCredential(id)
}

// SetCredentialStatus writes active/disabled/exhausted.
func (s *Service) SetCredentialStatus(ctx context.Context, id int64, status string) error {
	if err := s.d.Store.SetCredentialStatus(id, status); err != nil {
		return translateStoreErr(err, "credential")
	}
	return nil
}

// DeleteCredential removes a credential and clears the routing pin if needed.
func (s *Service) DeleteCredential(ctx context.Context, id int64) error {
	if err := s.d.Store.DeleteCredential(id); err != nil {
		return translateStoreErr(err, "credential")
	}
	return nil
}

// PinCredential pins (id > 0) or unpins (id <= 0) the routing credential.
func (s *Service) PinCredential(ctx context.Context, id int64) error {
	if id > 0 {
		if _, err := s.d.Store.GetCredential(id, false); err != nil {
			return translateStoreErr(err, "credential")
		}
	}
	if err := s.d.Store.PinCredential(id); err != nil {
		return translateStoreErr(err, "credential")
	}
	return nil
}

// TestCredential probes one credential in isolation.
func (s *Service) TestCredential(ctx context.Context, id int64) (gateway.ProbeResult, error) {
	cred, err := s.d.Store.GetCredential(id, true)
	if err != nil {
		return gateway.ProbeResult{}, translateStoreErr(err, "credential")
	}
	p, err := s.d.Store.GetProvider(cred.ProviderID)
	if err != nil {
		return gateway.ProbeResult{}, translateStoreErr(err, "provider")
	}
	adapter, err := s.adapterFor(p.Kind)
	if err != nil {
		return gateway.ProbeResult{}, err
	}
	prober, ok := adapter.(gateway.Prober)
	if !ok {
		return gateway.ProbeResult{}, FieldError(400, "kind",
			"adapter %q does not support connectivity checks", s.kindLabel(p.Kind))
	}

	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	return prober.Probe(probeCtx, gateway.ProbeRequest{
		Provider:   providerConfigOf(*p),
		Credential: credentialOf(*cred),
	})
}

// BatchImportResult reports what a pasted key list produced.
type BatchImportResult struct {
	Created        []store.Credential `json:"created"`
	Skipped        []string           `json:"skipped"`
	UnmatchedLines int                `json:"unmatchedLines"`
}

// BatchImport parses "name,secret" or bare "secret" lines.
func (s *Service) BatchImport(ctx context.Context, providerID int64, text string) (BatchImportResult, error) {
	if _, err := s.d.Store.GetProvider(providerID); err != nil {
		return BatchImportResult{}, translateStoreErr(err, "provider")
	}

	existing, err := s.d.Store.ListCredentials(providerID)
	if err != nil {
		return BatchImportResult{}, err
	}
	knownName := make(map[string]bool, len(existing))
	for _, c := range existing {
		knownName[c.Name] = true
	}

	var result BatchImportResult
	lines := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lines++
		if lines > maxImportLines {
			return BatchImportResult{}, FieldError(400, "text",
				"at most %d keys can be imported at once", maxImportLines)
		}

		name, secret := splitImportLine(line)
		if secret == "" {
			result.UnmatchedLines++
			continue
		}
		if name == "" {
			name = fmt.Sprintf("account-%d", len(existing)+len(result.Created)+1)
		}
		if knownName[name] {
			result.Skipped = append(result.Skipped, name)
			continue
		}

		created, createErr := s.CreateCredential(ctx, CredentialInput{
			ProviderID: providerID,
			Name:       name,
			Secret:     secret,
		})
		if createErr != nil {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		knownName[name] = true
		result.Created = append(result.Created, created)
	}
	return result, nil
}

// splitImportLine understands "name,secret" and bare "secret".
func splitImportLine(line string) (name, secret string) {
	if idx := strings.Index(line, ","); idx > 0 {
		candidateName := strings.TrimSpace(line[:idx])
		candidateSecret := strings.TrimSpace(line[idx+1:])
		if candidateSecret != "" {
			return candidateName, candidateSecret
		}
	}
	return "", strings.TrimSpace(line)
}

// CLIImportResult reports a one-click import from the vendor's CLI login.
type CLIImportResult struct {
	Found  bool              `json:"found"`
	Path   string            `json:"path"`
	Mask   string            `json:"mask,omitempty"`
	Import *store.Credential `json:"import,omitempty"`
	// Existing is true when the same key was already present.
	Existing bool `json:"existing"`
}

// ImportCLI reads the adapter's CLI login file, when the adapter has one.
func (s *Service) ImportCLI(ctx context.Context, providerID int64) (CLIImportResult, error) {
	p, err := s.d.Store.GetProvider(providerID)
	if err != nil {
		return CLIImportResult{}, translateStoreErr(err, "provider")
	}
	adapter, err := s.adapterFor(p.Kind)
	if err != nil {
		return CLIImportResult{}, err
	}
	source, ok := adapter.(interface{ CLILoginPath() string })
	if !ok {
		return CLIImportResult{}, FieldError(400, "kind",
			"adapter %q has no CLI login file to import", s.kindLabel(p.Kind))
	}
	path := source.CLILoginPath()
	result := CLIImportResult{Path: path}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return result, nil
		}
		return result, Errorf(500, "cannot read %s: %v", path, readErr)
	}

	secret := extractCLISecret(raw)
	if secret == "" {
		return result, Errorf(400, "no API key found in %s", path)
	}
	result.Found = true
	result.Mask = secretbox.Mask(secret)

	existing, err := s.d.Store.ListCredentials(providerID)
	if err != nil {
		return result, err
	}
	for _, c := range existing {
		if c.SecretMask == result.Mask {
			result.Existing = true
			return result, nil
		}
	}

	created, createErr := s.CreateCredential(ctx, CredentialInput{
		ProviderID: providerID,
		Name:       "CLI import " + time.Now().Format("2006-01-02 15:04"),
		Secret:     secret,
	})
	if createErr != nil {
		return result, createErr
	}
	result.Import = &created
	return result, nil
}

// extractCLISecret reads the key out of the various shapes a CLI login file uses.
func extractCLISecret(raw []byte) string {
	var payload struct {
		APIKey string `json:"apiKey"`
		Key    string `json:"key"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return firstNonEmpty(payload.APIKey, payload.Token, payload.Key)
}

// CredentialUsage returns the adapter's account snapshot for one credential.
func (s *Service) CredentialUsage(ctx context.Context, id int64, refresh bool) (any, error) {
	cred, err := s.d.Store.GetCredential(id, true)
	if err != nil {
		return nil, translateStoreErr(err, "credential")
	}
	p, err := s.d.Store.GetProvider(cred.ProviderID)
	if err != nil {
		return nil, translateStoreErr(err, "provider")
	}
	adapter, err := s.adapterFor(p.Kind)
	if err != nil {
		return nil, err
	}
	usageProvider, ok := adapter.(gateway.AccountUsageProvider)
	if !ok {
		return nil, FieldError(400, "kind",
			"adapter %q does not expose account usage", s.kindLabel(p.Kind))
	}

	usageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	request := gateway.ProbeRequest{
		Provider:   providerConfigOf(*p),
		Credential: credentialOf(*cred),
	}
	if refresh {
		if refresher, ok := adapter.(interface {
			RefreshAccountUsage(context.Context, gateway.ProbeRequest) (any, error)
		}); ok {
			return refresher.RefreshAccountUsage(usageCtx, request)
		}
	}
	return usageProvider.AccountUsage(usageCtx, request)
}

// RefreshAllUsage refreshes every credential of every usage-capable provider.
type RefreshAllResult struct {
	Refreshed int      `json:"refreshed"`
	Failed    []string `json:"failed,omitempty"`
}

// RefreshAllUsage walks providers and credentials, refreshing snapshots.
func (s *Service) RefreshAllUsage(ctx context.Context) (RefreshAllResult, error) {
	providers, err := s.d.Store.ListProviders()
	if err != nil {
		return RefreshAllResult{}, err
	}
	var result RefreshAllResult
	for _, p := range providers {
		adapter, adapterErr := s.adapterFor(p.Kind)
		if adapterErr != nil {
			continue
		}
		if _, ok := adapter.(gateway.AccountUsageProvider); !ok {
			continue
		}
		creds, listErr := s.d.Store.ListCredentials(p.ID)
		if listErr != nil {
			continue
		}
		for _, c := range creds {
			if _, usageErr := s.CredentialUsage(ctx, c.ID, true); usageErr != nil {
				result.Failed = append(result.Failed, fmt.Sprintf("%s/%s", p.Name, c.Name))
				continue
			}
			result.Refreshed++
		}
	}
	return result, nil
}

func (s *Service) getCredential(id int64) (store.Credential, error) {
	row, err := s.d.Store.GetCredential(id, false)
	if err != nil {
		return store.Credential{}, translateStoreErr(err, "credential")
	}
	pinned, _ := s.d.Store.PinnedCredentialID()
	row.Pinned = pinned == id && id > 0
	return *row, nil
}

func validateExpiry(expiresAt *int64) error {
	if expiresAt == nil || *expiresAt == 0 {
		return nil
	}
	if *expiresAt < time.Now().Add(-time.Minute).UnixMilli() {
		return FieldError(400, "expiresAt", "expiresAt is in the past")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// defaultCLILoginPath returns the vendor CLI's login file, when one exists.
func defaultCLILoginPath(vendorDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, vendorDir, "auth.json")
}
