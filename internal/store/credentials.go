package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/learningdog1/ada_proxy/internal/secretbox"
)

// maskOf is the single place the display form of a secret is decided.
func maskOf(secret string) string { return secretbox.Mask(secret) }

const credentialColumns = `c.id, c.provider_id, p.name, p.kind, c.name, c.secret_cipher, c.secret_mask,
	c.enabled, c.priority, c.status, c.expires_at, c.last_used_at, c.remark, c.created_at, c.updated_at`

const credentialSelect = `SELECT ` + credentialColumns + `
	FROM credentials c JOIN providers p ON p.id = c.provider_id`

// CreateCredential inserts an upstream key, encrypting it before it touches the
// database. The mask is stored alongside for list responses.
func (s *Store) CreateCredential(c Credential) (int64, error) {
	box, err := s.boxOrError()
	if err != nil {
		return 0, err
	}
	if c.Secret == "" {
		return 0, errors.New("store: credential secret is empty")
	}
	cipherText, err := box.Encrypt(c.Secret)
	if err != nil {
		return 0, fmt.Errorf("store: encrypt secret: %w", err)
	}
	now := s.nowMs()
	status := c.Status
	if status == "" {
		status = CredentialActive
	}
	res, err := s.db.Exec(`INSERT INTO credentials
		(provider_id, name, secret_cipher, secret_mask, enabled, priority, status, expires_at, remark, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ProviderID, c.Name, cipherText, maskOf(c.Secret), boolToInt(c.Enabled), c.Priority,
		status, c.ExpiresAt, c.Remark, now, now)
	if err != nil {
		return 0, translateErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: credential id: %w", err)
	}
	return id, nil
}

// UpdateCredential replaces the mutable fields. secret is only re-encrypted when
// it is non-empty, so an update that does not mention the key keeps the stored
// one.
func (s *Store) UpdateCredential(c Credential, secret string) error {
	now := s.nowMs()
	if secret != "" {
		box, err := s.boxOrError()
		if err != nil {
			return err
		}
		cipherText, err := box.Encrypt(secret)
		if err != nil {
			return fmt.Errorf("store: encrypt secret: %w", err)
		}
		res, err := s.db.Exec(`UPDATE credentials SET
			name = ?, secret_cipher = ?, secret_mask = ?, enabled = ?, priority = ?,
			status = ?, expires_at = ?, remark = ?, updated_at = ?
			WHERE id = ?`,
			c.Name, cipherText, maskOf(secret), boolToInt(c.Enabled), c.Priority,
			c.Status, c.ExpiresAt, c.Remark, now, c.ID)
		if err != nil {
			return translateErr(err)
		}
		return requireAffected(res, ErrNotFound)
	}
	res, err := s.db.Exec(`UPDATE credentials SET
		name = ?, enabled = ?, priority = ?, status = ?, expires_at = ?, remark = ?, updated_at = ?
		WHERE id = ?`,
		c.Name, boolToInt(c.Enabled), c.Priority, c.Status, c.ExpiresAt, c.Remark, now, c.ID)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// GetCredential returns one credential. When withSecret is true the plaintext
// is decrypted into Secret; otherwise it stays empty.
func (s *Store) GetCredential(id int64, withSecret bool) (*Credential, error) {
	row := s.db.QueryRow(credentialSelect+` WHERE c.id = ?`, id)
	c, err := scanCredential(row, withSecret, s)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListCredentials returns every credential, optionally filtered by provider
// (0 means all). Secrets are never decrypted here.
func (s *Store) ListCredentials(providerID int64) ([]Credential, error) {
	query := credentialSelect
	args := []any{}
	if providerID > 0 {
		query += ` WHERE c.provider_id = ?`
		args = append(args, providerID)
	}
	query += ` ORDER BY c.provider_id, c.priority DESC, c.id`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []Credential{}
	for rows.Next() {
		c, err := scanCredential(rows, false, s)
		if err != nil {
			return nil, fmt.Errorf("store: scan credential: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, translateErr(err)
	}
	pinned, _ := s.PinnedCredentialID()
	for i := range out {
		out[i].Pinned = pinned == out[i].ID && pinned > 0
	}
	return out, nil
}

// ListRoutableCredentials returns the credentials routing may choose from for a
// provider: enabled, status active, not expired, newest priority first. Secrets
// are decrypted because the adapter needs them.
func (s *Store) ListRoutableCredentials(providerID int64) ([]Credential, error) {
	rows, err := s.db.Query(credentialSelect+`
		WHERE c.provider_id = ? AND c.enabled = 1 AND c.status = ?
		  AND (c.expires_at IS NULL OR c.expires_at > ?)
		ORDER BY c.priority DESC, c.id`,
		providerID, CredentialActive, s.nowMs())
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []Credential{}
	for rows.Next() {
		c, err := scanCredential(rows, true, s)
		if err != nil {
			return nil, fmt.Errorf("store: scan credential: %w", err)
		}
		out = append(out, *c)
	}
	return out, translateErr(rows.Err())
}

// SetCredentialStatus writes active/disabled/exhausted.
func (s *Store) SetCredentialStatus(id int64, status string) error {
	if s.db == nil {
		return errNoDatabase
	}
	switch status {
	case CredentialActive, CredentialDisabled, CredentialExhausted:
	default:
		return fmt.Errorf("store: invalid credential status %q", status)
	}
	res, err := s.db.Exec(`UPDATE credentials SET status = ?, updated_at = ? WHERE id = ?`, status, s.nowMs(), id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// SetCredentialEnabled toggles a credential without changing its status.
func (s *Store) SetCredentialEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE credentials SET enabled = ?, updated_at = ? WHERE id = ?`, boolToInt(enabled), s.nowMs(), id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// TouchCredential records that a credential served a request.
func (s *Store) TouchCredential(id int64) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`UPDATE credentials SET last_used_at = ? WHERE id = ?`, s.nowMs(), id)
}

// DeleteCredential removes a credential and clears the routing pin when it
// pointed at this row.
func (s *Store) DeleteCredential(id int64) error {
	res, err := s.db.Exec(`DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return translateErr(err)
	}
	if err := requireAffected(res, ErrNotFound); err != nil {
		return err
	}
	if pinned, _ := s.PinnedCredentialID(); pinned == id {
		_ = s.DeleteSetting(SettingPinnedCredential)
	}
	return nil
}

// CountCredentialsForProvider reports how many credentials a provider has.
func (s *Store) CountCredentialsForProvider(providerID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM credentials WHERE provider_id = ?`, providerID).Scan(&n)
	return n, translateErr(err)
}

func scanCredential(sc rowsScanner, withSecret bool, s *Store) (*Credential, error) {
	c := &Credential{}
	var cipherText string
	if err := sc.Scan(&c.ID, &c.ProviderID, &c.ProviderName, &c.ProviderKind, &c.Name,
		&cipherText, &c.SecretMask, &c.Enabled, &c.Priority, &c.Status,
		&c.ExpiresAt, &c.LastUsedAt, &c.Remark, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if withSecret {
		box, err := s.boxOrError()
		if err != nil {
			return nil, err
		}
		plain, err := box.Decrypt(cipherText)
		if err != nil {
			return nil, fmt.Errorf("store: decrypt credential %d: %w", c.ID, err)
		}
		c.Secret = plain
	}
	return c, nil
}

// SettingPinnedCredential holds the id of the credential routing should prefer.
const SettingPinnedCredential = "route_pinned_credential_id"

// PinnedCredentialID returns the routing pin, or 0 when nothing is pinned.
func (s *Store) PinnedCredentialID() (int64, error) {
	raw, err := s.GetSetting(SettingPinnedCredential)
	if err != nil || raw == "" {
		return 0, err
	}
	n, convErr := strconv.ParseInt(raw, 10, 64)
	if convErr != nil || n <= 0 {
		return 0, nil
	}
	return n, nil
}

// PinCredential pins (id > 0) or unpins (id <= 0) the routing credential.
func (s *Store) PinCredential(id int64) error {
	if id <= 0 {
		return s.DeleteSetting(SettingPinnedCredential)
	}
	return s.SetSetting(SettingPinnedCredential, strconv.FormatInt(id, 10))
}
