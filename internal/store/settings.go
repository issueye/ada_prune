package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// errNoDatabase is returned when the store was never opened successfully. It is
// a normal condition during shutdown, when background writers may still run.
var errNoDatabase = errors.New("store: no database")

// GetSetting reads a runtime setting. It returns "" when unset.
func (s *Store) GetSetting(key string) (string, error) {
	if s.db == nil {
		return "", errNoDatabase
	}
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, translateErr(err)
}

// SetSetting writes a runtime setting.
func (s *Store) SetSetting(key, value string) error {
	if s.db == nil {
		return errNoDatabase
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return translateErr(err)
}

// DeleteSetting removes a runtime setting.
func (s *Store) DeleteSetting(key string) error {
	if s.db == nil {
		return errNoDatabase
	}
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return translateErr(err)
}

// GetProviderState reads one adapter-private value stored as JSON.
func (s *Store) GetProviderState(providerID int64, key string, v any) (bool, error) {
	if s.db == nil {
		return false, errNoDatabase
	}
	var raw string
	err := s.db.QueryRow(`SELECT value FROM provider_state WHERE provider_id = ? AND key = ?`, providerID, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, translateErr(err)
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("store: decode provider state %q: %w", key, err)
	}
	return true, nil
}

// SetProviderState writes one adapter-private value as JSON.
func (s *Store) SetProviderState(providerID int64, key string, v any) error {
	if s.db == nil {
		return errNoDatabase
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("store: encode provider state %q: %w", key, err)
	}
	_, err = s.db.Exec(`INSERT INTO provider_state (provider_id, key, value, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		providerID, key, string(raw), s.nowMs())
	return translateErr(err)
}

// DeleteProviderState removes one adapter-private value.
func (s *Store) DeleteProviderState(providerID int64, key string) error {
	if s.db == nil {
		return errNoDatabase
	}
	_, err := s.db.Exec(`DELETE FROM provider_state WHERE provider_id = ? AND key = ?`, providerID, key)
	return translateErr(err)
}
