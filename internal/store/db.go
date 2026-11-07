// Package store persists providers, credentials, models, client keys, request
// logs and daily usage in SQLite.
//
// When the on-disk database cannot be opened the store falls back to an
// in-memory database and reports Degraded(). Callers keep working; the operator
// is told that nothing will survive a restart. Using the same SQL for both
// paths means there is exactly one implementation of every query.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/learningdog1/ada_proxy/internal/secretbox"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrDuplicate is returned when a uniqueness constraint is violated.
var ErrDuplicate = errors.New("store: duplicate")

// ErrInUse is returned when a delete is blocked by a reference.
type ErrInUse struct {
	// Reason is a stable, operator-readable explanation.
	Reason string `json:"reason"`
	// Count is how many rows reference the target, when known.
	Count int64 `json:"count"`
}

func (e *ErrInUse) Error() string { return e.Reason }

// Schema is the full database layout. Every statement is idempotent, so Migrate
// can run on every start.
const Schema = `
CREATE TABLE IF NOT EXISTS providers (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT    NOT NULL UNIQUE,
  kind           TEXT    NOT NULL,
  base_url       TEXT    NOT NULL DEFAULT '',
  enabled        INTEGER NOT NULL DEFAULT 1,
  weight         INTEGER NOT NULL DEFAULT 1,
  timeout_ms     INTEGER NOT NULL DEFAULT 0,
  proxy_url      TEXT    NOT NULL DEFAULT '',
  extra_headers  TEXT    NOT NULL DEFAULT '{}',
  adapter_config TEXT    NOT NULL DEFAULT '{}',
  remark         TEXT    NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_providers_kind ON providers(kind);
CREATE INDEX IF NOT EXISTS idx_providers_enabled ON providers(enabled);

CREATE TABLE IF NOT EXISTS credentials (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id   INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  name          TEXT    NOT NULL,
  secret_cipher TEXT    NOT NULL,
  secret_mask   TEXT    NOT NULL,
  enabled       INTEGER NOT NULL DEFAULT 1,
  priority      INTEGER NOT NULL DEFAULT 0,
  status        TEXT    NOT NULL DEFAULT 'active',
  expires_at    INTEGER,
  last_used_at  INTEGER,
  remark        TEXT    NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  UNIQUE(provider_id, name)
);
CREATE INDEX IF NOT EXISTS idx_credentials_provider ON credentials(provider_id);
CREATE INDEX IF NOT EXISTS idx_credentials_status ON credentials(status);

CREATE TABLE IF NOT EXISTS models (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id        INTEGER NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
  display_name       TEXT    NOT NULL,
  public_name        TEXT    NOT NULL UNIQUE,
  upstream_name      TEXT    NOT NULL,
  upstream_protocol  TEXT    NOT NULL,
  input_price        REAL    NOT NULL DEFAULT 0,
  output_price       REAL    NOT NULL DEFAULT 0,
  cached_input_price REAL    NOT NULL DEFAULT 0,
  context_window     INTEGER NOT NULL DEFAULT 0,
  max_tokens         INTEGER NOT NULL DEFAULT 0,
  supports_stream    INTEGER NOT NULL DEFAULT 1,
  supports_tools     INTEGER NOT NULL DEFAULT 1,
  supports_vision    INTEGER NOT NULL DEFAULT 0,
  enabled            INTEGER NOT NULL DEFAULT 1,
  sort               INTEGER NOT NULL DEFAULT 0,
  remark             TEXT    NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  UNIQUE(provider_id, upstream_name)
);
CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider_id);

CREATE TABLE IF NOT EXISTS route_models (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL UNIQUE,
  strategy   TEXT    NOT NULL DEFAULT 'priority',
  enabled    INTEGER NOT NULL DEFAULT 1,
  remark     TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS route_model_members (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  route_model_id INTEGER NOT NULL REFERENCES route_models(id) ON DELETE CASCADE,
  model_id       INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  priority       INTEGER NOT NULL DEFAULT 0,
  weight         INTEGER NOT NULL DEFAULT 1,
  enabled        INTEGER NOT NULL DEFAULT 1,
  UNIQUE(route_model_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_route_members_route ON route_model_members(route_model_id);

CREATE TABLE IF NOT EXISTS client_keys (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  name                TEXT    NOT NULL,
  key_hash            TEXT    NOT NULL UNIQUE,
  key_prefix          TEXT    NOT NULL,
  enabled             INTEGER NOT NULL DEFAULT 1,
  allowed_models_json TEXT    NOT NULL DEFAULT '[]',
  allowed_ips         TEXT    NOT NULL DEFAULT '',
  quota_tokens        INTEGER NOT NULL DEFAULT 0,
  quota_used          INTEGER NOT NULL DEFAULT 0,
  expires_at          INTEGER,
  last_used_at        INTEGER,
  remark              TEXT    NOT NULL DEFAULT '',
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_client_keys_enabled ON client_keys(enabled);

CREATE TABLE IF NOT EXISTS endpoints (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  name             TEXT    NOT NULL,
  path             TEXT    NOT NULL UNIQUE,
  inbound_protocol TEXT    NOT NULL,
  target_type      TEXT    NOT NULL DEFAULT 'model',
  target_id        INTEGER NOT NULL DEFAULT 0,
  auth_required    INTEGER NOT NULL DEFAULT 1,
  rate_per_min     INTEGER NOT NULL DEFAULT 0,
  timeout_ms       INTEGER NOT NULL DEFAULT 0,
  enabled          INTEGER NOT NULL DEFAULT 1,
  remark           TEXT    NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS requests (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  ts                  INTEGER NOT NULL,
  endpoint            TEXT    NOT NULL,
  inbound_protocol    TEXT    NOT NULL DEFAULT '',
  provider_id         INTEGER,
  provider_kind       TEXT    NOT NULL DEFAULT '',
  model               TEXT    NOT NULL DEFAULT '',
  client_key_id       INTEGER,
  credential_id       INTEGER,
  status_code         INTEGER NOT NULL DEFAULT 0,
  stream              INTEGER NOT NULL DEFAULT 0,
  duration_ms         INTEGER NOT NULL DEFAULT 0,
  ttft_ms             INTEGER NOT NULL DEFAULT 0,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  cached_tokens       INTEGER NOT NULL DEFAULT 0,
  cost                REAL    NOT NULL DEFAULT 0,
  finish_reason       TEXT    NOT NULL DEFAULT '',
  error_type          TEXT    NOT NULL DEFAULT '',
  client_disconnected INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_requests_ts ON requests(ts);
CREATE INDEX IF NOT EXISTS idx_requests_provider ON requests(provider_id);
CREATE INDEX IF NOT EXISTS idx_requests_model ON requests(model);
CREATE INDEX IF NOT EXISTS idx_requests_client_key ON requests(client_key_id);

CREATE TABLE IF NOT EXISTS usage_daily (
  day           TEXT    NOT NULL,
  provider_id   INTEGER NOT NULL DEFAULT 0,
  model         TEXT    NOT NULL DEFAULT '',
  client_key_id INTEGER NOT NULL DEFAULT 0,
  requests      INTEGER NOT NULL DEFAULT 0,
  input_tokens  INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_tokens INTEGER NOT NULL DEFAULT 0,
  cost          REAL    NOT NULL DEFAULT 0,
  PRIMARY KEY (day, provider_id, model, client_key_id)
);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS provider_state (
  provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  key         TEXT    NOT NULL,
  value       TEXT    NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (provider_id, key)
);
`

// Store owns the database connection.
type Store struct {
	db       *sql.DB
	box      *secretbox.Box
	degraded bool
	mu       sync.Mutex
	now      func() time.Time
}

// Open opens (and creates) the database at path. A failure to open the file
// never returns an error: the store degrades to an in-memory database and
// reports it through Degraded().
func Open(path string, box *secretbox.Box) *Store {
	s := &Store{box: box, now: time.Now}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return s.degrade(fmt.Errorf("create data directory: %w", err))
	}
	db, err := s.connect(path)
	if err != nil {
		return s.degrade(err)
	}
	s.db = db
	return s
}

// OpenMemory returns a store backed by an in-memory database. It is what the
// degraded path uses, and what tests use when they do not need a file.
func OpenMemory(box *secretbox.Box) *Store {
	s := &Store{box: box, now: time.Now}
	db, err := s.connect("file::memory:?cache=shared")
	if err != nil {
		s.degraded = true
		return s
	}
	s.db = db
	return s
}

func (s *Store) connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection: SQLite serialises writes anyway, and this removes the
	// SQLITE_BUSY races a pool would introduce.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		// WAL is an optimisation, not a requirement.
		_, _ = db.Exec("PRAGMA journal_mode = DELETE")
	}
	return db, nil
}

func (s *Store) degrade(err error) *Store {
	s.degraded = true
	fmt.Fprintf(os.Stderr, "[store] on-disk database unavailable (%v); using in-memory storage, nothing will persist\n", err)
	db, memErr := s.connect("file::memory:?cache=shared")
	if memErr != nil {
		fmt.Fprintf(os.Stderr, "[store] in-memory database also failed: %v\n", memErr)
		return s
	}
	s.db = db
	return s
}

// Migrate creates the schema. It is idempotent and safe to run on every start.
func (s *Store) Migrate() error {
	if s.db == nil {
		return errors.New("store: no database")
	}
	if _, err := s.db.Exec(Schema); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Degraded reports whether persistence is unavailable.
func (s *Store) Degraded() bool { return s.degraded }

// DB exposes the connection for services that own their own queries (logs,
// usage aggregations). Callers must not close it.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database. The handle is kept so a late background write
// returns "database is closed" instead of panicking on a nil pointer.
func (s *Store) Close() {
	if s.db != nil {
		_ = s.db.Close()
	}
}

// nowMs returns the current time in Unix milliseconds.
func (s *Store) nowMs() int64 { return s.now().UnixMilli() }

// boxOrError returns the cipher box, or an error when the store was opened
// without one (only tests do that).
func (s *Store) boxOrError() (*secretbox.Box, error) {
	if s.box == nil {
		return nil, errors.New("store: no secret box configured")
	}
	return s.box, nil
}

// translateErr maps driver errors onto the package's sentinels.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case contains(msg, "UNIQUE constraint failed"):
		return ErrDuplicate
	case contains(msg, "FOREIGN KEY constraint failed"):
		return &ErrInUse{Reason: "referenced by another row"}
	case contains(msg, "no such row"):
		return ErrNotFound
	}
	return err
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// boolToInt and intToBool keep the 0/1 convention in one place.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(n int) bool { return n != 0 }
