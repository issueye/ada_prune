package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const endpointColumns = `id, name, path, inbound_protocol, target_type, target_id,
	auth_required, rate_per_min, timeout_ms, enabled, remark, created_at, updated_at`

// CreateEndpoint inserts a custom data-plane endpoint.
func (s *Store) CreateEndpoint(e Endpoint) (int64, error) {
	now := s.nowMs()
	res, err := s.db.Exec(`INSERT INTO endpoints
		(name, path, inbound_protocol, target_type, target_id, auth_required, rate_per_min, timeout_ms, enabled, remark, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Name, e.Path, e.InboundProtocol, e.TargetType, e.TargetID,
		boolToInt(e.AuthRequired), e.RatePerMin, e.TimeoutMs, boolToInt(e.Enabled), e.Remark, now, now)
	if err != nil {
		return 0, translateErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: endpoint id: %w", err)
	}
	return id, nil
}

// UpdateEndpoint replaces the mutable fields.
func (s *Store) UpdateEndpoint(e Endpoint) error {
	res, err := s.db.Exec(`UPDATE endpoints SET
		name = ?, path = ?, inbound_protocol = ?, target_type = ?, target_id = ?,
		auth_required = ?, rate_per_min = ?, timeout_ms = ?, enabled = ?, remark = ?, updated_at = ?
		WHERE id = ?`,
		e.Name, e.Path, e.InboundProtocol, e.TargetType, e.TargetID,
		boolToInt(e.AuthRequired), e.RatePerMin, e.TimeoutMs, boolToInt(e.Enabled), e.Remark,
		s.nowMs(), e.ID)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// GetEndpoint returns one endpoint by id.
func (s *Store) GetEndpoint(id int64) (*Endpoint, error) {
	row := s.db.QueryRow(`SELECT `+endpointColumns+` FROM endpoints WHERE id = ?`, id)
	e, err := scanEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// GetEndpointByPath resolves the path a client called.
func (s *Store) GetEndpointByPath(path string) (*Endpoint, error) {
	row := s.db.QueryRow(`SELECT `+endpointColumns+` FROM endpoints WHERE path = ?`, path)
	e, err := scanEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// MatchEndpointByPath returns the custom endpoint with the longest path that
// prefixes the requested path. A path matches exactly, or as a whole segment:
// "/cc" matches "/cc/v1/messages" but not "/ccx". Disabled endpoints still take
// part, so a disabled longer prefix shadows shorter ones instead of silently
// falling through to another target.
func (s *Store) MatchEndpointByPath(path string) (*Endpoint, error) {
	rows, err := s.ListEndpoints()
	if err != nil {
		return nil, err
	}
	var best *Endpoint
	for i := range rows {
		e := &rows[i]
		if !endpointPathMatches(e.Path, path) {
			continue
		}
		if best == nil || len(e.Path) > len(best.Path) {
			best = e
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}

// endpointPathMatches reports whether a configured prefix covers a request
// path. The comparison is segment-aware so "/cc" does not capture "/ccx".
func endpointPathMatches(prefix, path string) bool {
	if prefix == path {
		return true
	}
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	return strings.HasPrefix(path, prefix+"/")
}

// ListEndpoints returns every custom endpoint ordered by path.
func (s *Store) ListEndpoints() ([]Endpoint, error) {
	rows, err := s.db.Query(`SELECT ` + endpointColumns + ` FROM endpoints ORDER BY path`)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []Endpoint{}
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan endpoint: %w", err)
		}
		out = append(out, *e)
	}
	return out, translateErr(rows.Err())
}

// DeleteEndpoint removes a custom endpoint.
func (s *Store) DeleteEndpoint(id int64) error {
	res, err := s.db.Exec(`DELETE FROM endpoints WHERE id = ?`, id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

func scanEndpoint(sc rowsScanner) (*Endpoint, error) {
	e := &Endpoint{}
	if err := sc.Scan(&e.ID, &e.Name, &e.Path, &e.InboundProtocol, &e.TargetType, &e.TargetID,
		&e.AuthRequired, &e.RatePerMin, &e.TimeoutMs, &e.Enabled, &e.Remark,
		&e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return e, nil
}
