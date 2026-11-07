package admin

import (
	"context"
	"net"
	"strings"

	"github.com/learningdog1/ada_proxy/internal/store"
)

// ClientKeyInput is the create payload.
type ClientKeyInput struct {
	Name          string   `json:"name"`
	Enabled       *bool    `json:"enabled"`
	AllowedModels []string `json:"allowedModels"`
	AllowedIPs    string   `json:"allowedIps"`
	QuotaTokens   int64    `json:"quotaTokens"`
	ExpiresAt     *int64   `json:"expiresAt"`
	Remark        string   `json:"remark"`
}

// ListClientKeys returns every client key.
func (s *Service) ListClientKeys(ctx context.Context) ([]store.ClientKey, error) {
	return s.d.Store.ListClientKeys()
}

// CreateClientKey mints a key and returns the plaintext exactly once.
func (s *Service) CreateClientKey(ctx context.Context, in ClientKeyInput) (store.CreatedClientKey, error) {
	name, err := validateName("name", in.Name, true)
	if err != nil {
		return store.CreatedClientKey{}, err
	}
	if err := validateAllowedIPs(in.AllowedIPs); err != nil {
		return store.CreatedClientKey{}, err
	}
	if in.QuotaTokens < 0 {
		return store.CreatedClientKey{}, FieldError(400, "quotaTokens", "quotaTokens must not be negative")
	}
	if err := validateExpiry(in.ExpiresAt); err != nil {
		return store.CreatedClientKey{}, err
	}

	row := store.ClientKey{
		Name:          name,
		Enabled:       true,
		AllowedModels: in.AllowedModels,
		AllowedIPs:    strings.TrimSpace(in.AllowedIPs),
		QuotaTokens:   in.QuotaTokens,
		ExpiresAt:     in.ExpiresAt,
		Remark:        strings.TrimSpace(in.Remark),
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}

	created, err := s.d.Store.CreateClientKey(row)
	if err != nil {
		return store.CreatedClientKey{}, translateStoreErr(err, "client key")
	}
	return created, nil
}

// UpdateClientKey replaces the mutable fields of a key.
func (s *Service) UpdateClientKey(ctx context.Context, id int64, in ClientKeyInput) (store.ClientKey, error) {
	existing, err := s.d.Store.GetClientKey(id)
	if err != nil {
		return store.ClientKey{}, translateStoreErr(err, "client key")
	}
	name, err := validateName("name", firstNonEmpty(in.Name, existing.Name), true)
	if err != nil {
		return store.ClientKey{}, err
	}
	if err := validateAllowedIPs(in.AllowedIPs); err != nil {
		return store.ClientKey{}, err
	}
	if in.QuotaTokens < 0 {
		return store.ClientKey{}, FieldError(400, "quotaTokens", "quotaTokens must not be negative")
	}

	row := *existing
	row.Name = name
	row.AllowedIPs = strings.TrimSpace(in.AllowedIPs)
	row.QuotaTokens = in.QuotaTokens
	if in.AllowedModels != nil {
		row.AllowedModels = in.AllowedModels
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.ExpiresAt != nil {
		row.ExpiresAt = in.ExpiresAt
	}
	if in.Remark != "" {
		row.Remark = strings.TrimSpace(in.Remark)
	}
	if err := validateExpiry(row.ExpiresAt); err != nil {
		return store.ClientKey{}, err
	}
	if err := s.d.Store.UpdateClientKey(row); err != nil {
		return store.ClientKey{}, translateStoreErr(err, "client key")
	}
	return s.getClientKey(id)
}

// RotateClientKey mints a new token for an existing key.
func (s *Service) RotateClientKey(ctx context.Context, id int64) (string, error) {
	token, err := s.d.Store.RotateClientKey(id)
	if err != nil {
		return "", translateStoreErr(err, "client key")
	}
	return token, nil
}

// ResetClientKeyUsage zeroes a key's accumulated usage.
func (s *Service) ResetClientKeyUsage(ctx context.Context, id int64) error {
	if err := s.d.Store.ResetClientKeyUsage(id); err != nil {
		return translateStoreErr(err, "client key")
	}
	return nil
}

// SetClientKeyEnabled toggles a key.
func (s *Service) SetClientKeyEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.d.Store.SetClientKeyEnabled(id, enabled); err != nil {
		return translateStoreErr(err, "client key")
	}
	return nil
}

// DeleteClientKey removes a key. Historical request rows keep the id, which is
// why the log joins by name and tolerates a missing key.
func (s *Service) DeleteClientKey(ctx context.Context, id int64) error {
	if err := s.d.Store.DeleteClientKey(id); err != nil {
		return translateStoreErr(err, "client key")
	}
	return nil
}

// GetClientKey returns one key.
func (s *Service) GetClientKey(ctx context.Context, id int64) (store.ClientKey, error) {
	return s.getClientKey(id)
}

func (s *Service) getClientKey(id int64) (store.ClientKey, error) {
	row, err := s.d.Store.GetClientKey(id)
	if err != nil {
		return store.ClientKey{}, translateStoreErr(err, "client key")
	}
	return *row, nil
}

// validateAllowedIPs accepts a comma-separated list of IPs and CIDRs.
func validateAllowedIPs(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	for _, part := range strings.Split(trimmed, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return FieldError(400, "allowedIps", "%q is not a valid CIDR", entry)
			}
			continue
		}
		if net.ParseIP(entry) == nil {
			return FieldError(400, "allowedIps", "%q is not a valid IP address", entry)
		}
	}
	return nil
}
