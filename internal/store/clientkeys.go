// Package-level helpers for client keys are below; this file holds the CRUD and
// the verification path used by the data plane.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ClientKeyPrefix is the visible prefix of a minted client key.
const ClientKeyPrefix = "sk-prune-"

// ErrClientKeyInvalid means the presented token does not resolve to an enabled,
// unexpired key.
var ErrClientKeyInvalid = errors.New("store: invalid client key")

const clientKeyColumns = `id, name, key_hash, key_prefix, enabled, allowed_models_json, allowed_ips,
	quota_tokens, quota_used, expires_at, last_used_at, remark, created_at, updated_at`

// CreatedClientKey carries the plaintext token, which is returned exactly once.
type CreatedClientKey struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	KeyPrefix string `json:"keyPrefix"`
	Token     string `json:"token"`
}

// CreateClientKey mints a key, stores only its hash and prefix, and returns the
// plaintext once.
func (s *Store) CreateClientKey(k ClientKey) (CreatedClientKey, error) {
	if strings.TrimSpace(k.Name) == "" {
		return CreatedClientKey{}, errors.New("store: client key name is empty")
	}
	token, err := mintToken()
	if err != nil {
		return CreatedClientKey{}, err
	}
	now := s.nowMs()
	res, err := s.db.Exec(`INSERT INTO client_keys
		(name, key_hash, key_prefix, enabled, allowed_models_json, allowed_ips,
		 quota_tokens, quota_used, expires_at, remark, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
		k.Name, hashToken(token), prefixOf(token), boolToInt(k.Enabled), JSONList(k.AllowedModels),
		k.AllowedIPs, k.QuotaTokens, k.ExpiresAt, k.Remark, now, now)
	if err != nil {
		return CreatedClientKey{}, translateErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return CreatedClientKey{}, fmt.Errorf("store: client key id: %w", err)
	}
	return CreatedClientKey{ID: id, Name: k.Name, KeyPrefix: prefixOf(token), Token: token}, nil
}

// UpdateClientKey replaces the mutable fields.
func (s *Store) UpdateClientKey(k ClientKey) error {
	res, err := s.db.Exec(`UPDATE client_keys SET
		name = ?, enabled = ?, allowed_models_json = ?, allowed_ips = ?, quota_tokens = ?,
		expires_at = ?, remark = ?, updated_at = ?
		WHERE id = ?`,
		k.Name, boolToInt(k.Enabled), JSONList(k.AllowedModels), k.AllowedIPs, k.QuotaTokens,
		k.ExpiresAt, k.Remark, s.nowMs(), k.ID)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// GetClientKey returns one key by id. The hash is not exposed.
func (s *Store) GetClientKey(id int64) (*ClientKey, error) {
	row := s.db.QueryRow(`SELECT `+clientKeyColumns+` FROM client_keys WHERE id = ?`, id)
	k, err := scanClientKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// ListClientKeys returns every key ordered by id.
func (s *Store) ListClientKeys() ([]ClientKey, error) {
	rows, err := s.db.Query(`SELECT ` + clientKeyColumns + ` FROM client_keys ORDER BY id`)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []ClientKey{}
	for rows.Next() {
		k, err := scanClientKey(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan client key: %w", err)
		}
		out = append(out, *k)
	}
	return out, translateErr(rows.Err())
}

// VerifyClientKey resolves a plaintext token to an enabled, unexpired key and
// records the use.
func (s *Store) VerifyClientKey(token string) (*ClientKey, error) {
	if !strings.HasPrefix(token, ClientKeyPrefix) {
		return nil, ErrClientKeyInvalid
	}
	row := s.db.QueryRow(`SELECT `+clientKeyColumns+` FROM client_keys WHERE key_hash = ?`, hashToken(token))
	k, err := scanClientKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrClientKeyInvalid
	}
	if err != nil {
		return nil, translateErr(err)
	}
	now := s.nowMs()
	if !k.Enabled {
		return nil, ErrClientKeyInvalid
	}
	if k.ExpiresAt != nil && *k.ExpiresAt > 0 && *k.ExpiresAt <= now {
		return nil, ErrClientKeyInvalid
	}
	_, _ = s.db.Exec(`UPDATE client_keys SET last_used_at = ? WHERE id = ?`, now, k.ID)
	k.LastUsedAt = &now
	return k, nil
}

// SetClientKeyEnabled toggles a key.
func (s *Store) SetClientKeyEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE client_keys SET enabled = ?, updated_at = ? WHERE id = ?`, boolToInt(enabled), s.nowMs(), id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// DeleteClientKey removes a key.
func (s *Store) DeleteClientKey(id int64) error {
	res, err := s.db.Exec(`DELETE FROM client_keys WHERE id = ?`, id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// RotateClientKey mints a new token for an existing key and returns the
// plaintext once. The old token stops working immediately.
func (s *Store) RotateClientKey(id int64) (string, error) {
	token, err := mintToken()
	if err != nil {
		return "", err
	}
	res, err := s.db.Exec(`UPDATE client_keys SET key_hash = ?, key_prefix = ?, updated_at = ? WHERE id = ?`,
		hashToken(token), prefixOf(token), s.nowMs(), id)
	if err != nil {
		return "", translateErr(err)
	}
	if err := requireAffected(res, ErrNotFound); err != nil {
		return "", err
	}
	return token, nil
}

// ResetClientKeyUsage zeroes the accumulated quota for a key.
func (s *Store) ResetClientKeyUsage(id int64) error {
	res, err := s.db.Exec(`UPDATE client_keys SET quota_used = 0, updated_at = ? WHERE id = ?`, s.nowMs(), id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// AddClientKeyUsage adds the tokens a request consumed. It is called after the
// response finishes, so a client disconnect still bills what was produced.
func (s *Store) AddClientKeyUsage(id int64, tokens int64) {
	if s.db == nil || id <= 0 || tokens <= 0 {
		return
	}
	_, _ = s.db.Exec(`UPDATE client_keys SET quota_used = quota_used + ? WHERE id = ?`, tokens, id)
}

func scanClientKey(sc rowsScanner) (*ClientKey, error) {
	k := &ClientKey{}
	var allowed string
	if err := sc.Scan(&k.ID, &k.Name, new(string), &k.KeyPrefix, &k.Enabled, &allowed, &k.AllowedIPs,
		&k.QuotaTokens, &k.QuotaUsed, &k.ExpiresAt, &k.LastUsedAt, &k.Remark,
		&k.CreatedAt, &k.UpdatedAt); err != nil {
		return nil, err
	}
	k.AllowedModels = ParseJSONList(allowed)
	return k, nil
}

func mintToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("store: mint token: %w", err)
	}
	return ClientKeyPrefix + hex.EncodeToString(buf[:]), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func prefixOf(token string) string {
	const visible = 16
	if len(token) <= visible {
		return token
	}
	return token[:visible] + "…"
}
