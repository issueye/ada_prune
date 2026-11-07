package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const providerColumns = `id, name, kind, base_url, enabled, weight, timeout_ms, proxy_url,
	extra_headers, adapter_config, remark, created_at, updated_at`

// CreateProvider inserts a provider and returns its id.
func (s *Store) CreateProvider(p Provider) (int64, error) {
	if s.db == nil {
		return 0, errors.New("store: no database")
	}
	headers, err := marshalStringMap(p.ExtraHeaders)
	if err != nil {
		return 0, fmt.Errorf("store: extra headers: %w", err)
	}
	adapter, err := marshalAnyMap(p.AdapterConfig)
	if err != nil {
		return 0, fmt.Errorf("store: adapter config: %w", err)
	}
	now := s.nowMs()
	res, err := s.db.Exec(`INSERT INTO providers
		(name, kind, base_url, enabled, weight, timeout_ms, proxy_url, extra_headers, adapter_config, remark, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Kind, p.BaseURL, boolToInt(p.Enabled), p.Weight, p.TimeoutMs,
		p.ProxyURL, headers, adapter, p.Remark, now, now)
	if err != nil {
		return 0, translateErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: provider id: %w", err)
	}
	return id, nil
}

// UpdateProvider replaces the mutable fields of a provider.
func (s *Store) UpdateProvider(p Provider) error {
	if s.db == nil {
		return errors.New("store: no database")
	}
	headers, err := marshalStringMap(p.ExtraHeaders)
	if err != nil {
		return fmt.Errorf("store: extra headers: %w", err)
	}
	adapter, err := marshalAnyMap(p.AdapterConfig)
	if err != nil {
		return fmt.Errorf("store: adapter config: %w", err)
	}
	// kind is deliberately not updatable: it selects the adapter, and changing
	// it would leave credentials and models pointing at a different protocol.
	res, err := s.db.Exec(`UPDATE providers SET
		name = ?, base_url = ?, enabled = ?, weight = ?, timeout_ms = ?, proxy_url = ?,
		extra_headers = ?, adapter_config = ?, remark = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.BaseURL, boolToInt(p.Enabled), p.Weight, p.TimeoutMs, p.ProxyURL,
		headers, adapter, p.Remark, s.nowMs(), p.ID)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// GetProvider returns one provider with its derived counts.
func (s *Store) GetProvider(id int64) (*Provider, error) {
	row := s.db.QueryRow(`SELECT `+providerColumns+`,
		(SELECT COUNT(*) FROM credentials WHERE provider_id = providers.id),
		(SELECT COUNT(*) FROM credentials WHERE provider_id = providers.id AND status = 'active' AND enabled = 1),
		(SELECT COUNT(*) FROM models WHERE provider_id = providers.id),
		(SELECT COUNT(*) FROM models WHERE provider_id = providers.id AND enabled = 1)
		FROM providers WHERE id = ?`, id)
	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// GetProviderByName looks a provider up by its unique name.
func (s *Store) GetProviderByName(name string) (*Provider, error) {
	row := s.db.QueryRow(`SELECT `+providerColumns+`,
		(SELECT COUNT(*) FROM credentials WHERE provider_id = providers.id),
		(SELECT COUNT(*) FROM credentials WHERE provider_id = providers.id AND status = 'active' AND enabled = 1),
		(SELECT COUNT(*) FROM models WHERE provider_id = providers.id),
		(SELECT COUNT(*) FROM models WHERE provider_id = providers.id AND enabled = 1)
		FROM providers WHERE name = ?`, name)
	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListProviders returns every provider ordered by id.
func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT ` + providerColumns + `,
		(SELECT COUNT(*) FROM credentials WHERE provider_id = providers.id),
		(SELECT COUNT(*) FROM credentials WHERE provider_id = providers.id AND status = 'active' AND enabled = 1),
		(SELECT COUNT(*) FROM models WHERE provider_id = providers.id),
		(SELECT COUNT(*) FROM models WHERE provider_id = providers.id AND enabled = 1)
		FROM providers ORDER BY id`)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan provider: %w", err)
		}
		out = append(out, *p)
	}
	return out, translateErr(rows.Err())
}

// DeleteProvider removes a provider. A provider with enabled models cannot be
// deleted: the operator has to move or disable them first, because deleting them
// silently would break client keys that name those models. Disabled models are
// removed with the provider, along with their route model memberships.
func (s *Store) DeleteProvider(id int64) error {
	var enabledModels int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM models WHERE provider_id = ? AND enabled = 1`, id).Scan(&enabledModels); err != nil {
		return translateErr(err)
	}
	if enabledModels > 0 {
		return &ErrInUse{Reason: "provider has enabled models", Count: enabledModels}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return translateErr(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM route_model_members WHERE model_id IN
		(SELECT id FROM models WHERE provider_id = ?)`, id); err != nil {
		return translateErr(err)
	}
	if _, err := tx.Exec(`DELETE FROM models WHERE provider_id = ?`, id); err != nil {
		return translateErr(err)
	}
	res, err := tx.Exec(`DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return translateErr(err)
	}
	if err := requireAffected(res, ErrNotFound); err != nil {
		return err
	}
	return translateErr(tx.Commit())
}

// SetProviderEnabled toggles a provider.
func (s *Store) SetProviderEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE providers SET enabled = ?, updated_at = ? WHERE id = ?`, boolToInt(enabled), s.nowMs(), id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// rowsScanner is satisfied by both *sql.Row and *sql.Rows.
type rowsScanner interface {
	Scan(dest ...any) error
}

func scanProvider(sc rowsScanner) (*Provider, error) {
	p := &Provider{}
	var headers, adapter string
	if err := sc.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.Enabled, &p.Weight,
		&p.TimeoutMs, &p.ProxyURL, &headers, &adapter, &p.Remark,
		&p.CreatedAt, &p.UpdatedAt, &p.CredentialCount, &p.ActiveCredentialCount,
		&p.ModelCount, &p.EnabledModelCount); err != nil {
		return nil, err
	}
	if err := p.decode(headers, adapter); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) decode(headers, adapter string) error {
	if err := json.Unmarshal([]byte(orEmptyObject(headers)), &p.ExtraHeaders); err != nil {
		return fmt.Errorf("store: decode extra headers: %w", err)
	}
	if err := json.Unmarshal([]byte(orEmptyObject(adapter)), &p.AdapterConfig); err != nil {
		return fmt.Errorf("store: decode adapter config: %w", err)
	}
	return nil
}

func marshalStringMap(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalAnyMap(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func orEmptyObject(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

func requireAffected(res sql.Result, notFound error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	return nil
}
