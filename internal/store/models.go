package store

import (
	"database/sql"
	"errors"
	"fmt"
)

const modelColumns = `m.id, m.provider_id, p.name, p.kind, m.display_name, m.public_name,
	m.upstream_name, m.upstream_protocol, m.input_price, m.output_price, m.cached_input_price,
	m.context_window, m.max_tokens, m.supports_stream, m.supports_tools, m.supports_vision,
	m.enabled, m.sort, m.remark, m.created_at, m.updated_at`

const modelSelect = `SELECT ` + modelColumns + `
	FROM models m JOIN providers p ON p.id = m.provider_id`

// ModelFilter narrows a model listing. Zero values mean "no filter".
type ModelFilter struct {
	ProviderID int64
	Enabled    *bool
	Protocol   string
	Query      string
}

// CreateModel inserts a model and returns its id.
func (s *Store) CreateModel(m Model) (int64, error) {
	now := s.nowMs()
	res, err := s.db.Exec(`INSERT INTO models
		(provider_id, display_name, public_name, upstream_name, upstream_protocol,
		 input_price, output_price, cached_input_price, context_window, max_tokens,
		 supports_stream, supports_tools, supports_vision, enabled, sort, remark, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ProviderID, m.DisplayName, m.PublicName, m.UpstreamName, m.UpstreamProtocol,
		m.InputPrice, m.OutputPrice, m.CachedInputPrice, m.ContextWindow, m.MaxTokens,
		boolToInt(m.SupportsStream), boolToInt(m.SupportsTools), boolToInt(m.SupportsVision),
		boolToInt(m.Enabled), m.Sort, m.Remark, now, now)
	if err != nil {
		return 0, translateErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: model id: %w", err)
	}
	return id, nil
}

// UpdateModel replaces the mutable fields of a model.
func (s *Store) UpdateModel(m Model) error {
	res, err := s.db.Exec(`UPDATE models SET
		display_name = ?, public_name = ?, upstream_name = ?, upstream_protocol = ?,
		input_price = ?, output_price = ?, cached_input_price = ?, context_window = ?, max_tokens = ?,
		supports_stream = ?, supports_tools = ?, supports_vision = ?, enabled = ?, sort = ?, remark = ?,
		updated_at = ?
		WHERE id = ?`,
		m.DisplayName, m.PublicName, m.UpstreamName, m.UpstreamProtocol,
		m.InputPrice, m.OutputPrice, m.CachedInputPrice, m.ContextWindow, m.MaxTokens,
		boolToInt(m.SupportsStream), boolToInt(m.SupportsTools), boolToInt(m.SupportsVision),
		boolToInt(m.Enabled), m.Sort, m.Remark, s.nowMs(), m.ID)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// GetModel returns one model by id.
func (s *Store) GetModel(id int64) (*Model, error) {
	row := s.db.QueryRow(modelSelect+` WHERE m.id = ?`, id)
	m, err := scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// GetModelByPublicName resolves the name a client sends in the "model" field.
func (s *Store) GetModelByPublicName(publicName string) (*Model, error) {
	row := s.db.QueryRow(modelSelect+` WHERE m.public_name = ?`, publicName)
	m, err := scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// ListModels returns models matching the filter, ordered for display.
func (s *Store) ListModels(f ModelFilter) ([]Model, error) {
	query := modelSelect + ` WHERE 1 = 1`
	args := []any{}
	if f.ProviderID > 0 {
		query += ` AND m.provider_id = ?`
		args = append(args, f.ProviderID)
	}
	if f.Enabled != nil {
		query += ` AND m.enabled = ?`
		args = append(args, boolToInt(*f.Enabled))
	}
	if f.Protocol != "" {
		query += ` AND m.upstream_protocol = ?`
		args = append(args, f.Protocol)
	}
	if f.Query != "" {
		query += ` AND (m.public_name LIKE ? OR m.display_name LIKE ? OR m.upstream_name LIKE ?)`
		like := "%" + f.Query + "%"
		args = append(args, like, like, like)
	}
	query += ` ORDER BY m.sort, m.public_name`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []Model{}
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan model: %w", err)
		}
		out = append(out, *m)
	}
	return out, translateErr(rows.Err())
}

// DeleteModel removes a model unless a route model references it.
func (s *Store) DeleteModel(id int64) error {
	var refs int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM route_model_members WHERE model_id = ?`, id).Scan(&refs); err != nil {
		return translateErr(err)
	}
	if refs > 0 {
		return &ErrInUse{Reason: "model is a member of a route model", Count: refs}
	}
	res, err := s.db.Exec(`DELETE FROM models WHERE id = ?`, id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// SetModelEnabled toggles a model.
func (s *Store) SetModelEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE models SET enabled = ?, updated_at = ? WHERE id = ?`, boolToInt(enabled), s.nowMs(), id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// CountModelsForProvider reports how many models a provider has.
func (s *Store) CountModelsForProvider(providerID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM models WHERE provider_id = ?`, providerID).Scan(&n)
	return n, translateErr(err)
}

func scanModel(sc rowsScanner) (*Model, error) {
	m := &Model{}
	if err := sc.Scan(&m.ID, &m.ProviderID, &m.ProviderName, &m.ProviderKind, &m.DisplayName,
		&m.PublicName, &m.UpstreamName, &m.UpstreamProtocol, &m.InputPrice, &m.OutputPrice,
		&m.CachedInputPrice, &m.ContextWindow, &m.MaxTokens, &m.SupportsStream, &m.SupportsTools,
		&m.SupportsVision, &m.Enabled, &m.Sort, &m.Remark, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return m, nil
}

// ---- route models ----

// CreateRouteModel inserts a route model and its members in one transaction.
func (s *Store) CreateRouteModel(r RouteModel) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, translateErr(err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.nowMs()
	res, err := tx.Exec(`INSERT INTO route_models (name, strategy, enabled, remark, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, r.Name, r.Strategy, boolToInt(r.Enabled), r.Remark, now, now)
	if err != nil {
		return 0, translateErr(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: route model id: %w", err)
	}
	if err := insertRouteMembers(tx, id, r.Members); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, translateErr(err)
	}
	return id, nil
}

// UpdateRouteModel replaces the scalar fields and all members.
func (s *Store) UpdateRouteModel(r RouteModel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return translateErr(err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`UPDATE route_models SET name = ?, strategy = ?, enabled = ?, remark = ?, updated_at = ?
		WHERE id = ?`, r.Name, r.Strategy, boolToInt(r.Enabled), r.Remark, s.nowMs(), r.ID)
	if err != nil {
		return translateErr(err)
	}
	if err := requireAffected(res, ErrNotFound); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM route_model_members WHERE route_model_id = ?`, r.ID); err != nil {
		return translateErr(err)
	}
	if err := insertRouteMembers(tx, r.ID, r.Members); err != nil {
		return err
	}
	return translateErr(tx.Commit())
}

func insertRouteMembers(tx *sql.Tx, routeModelID int64, members []RouteModelMember) error {
	for _, m := range members {
		if _, err := tx.Exec(`INSERT INTO route_model_members
			(route_model_id, model_id, priority, weight, enabled) VALUES (?, ?, ?, ?, ?)`,
			routeModelID, m.ModelID, m.Priority, m.Weight, boolToInt(m.Enabled)); err != nil {
			return translateErr(err)
		}
	}
	return nil
}

// GetRouteModel returns one route model with its members.
func (s *Store) GetRouteModel(id int64) (*RouteModel, error) {
	r := &RouteModel{}
	err := s.db.QueryRow(`SELECT id, name, strategy, enabled, remark, created_at, updated_at
		FROM route_models WHERE id = ?`, id).
		Scan(&r.ID, &r.Name, &r.Strategy, &r.Enabled, &r.Remark, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, translateErr(err)
	}
	members, err := s.listRouteMembers(id)
	if err != nil {
		return nil, err
	}
	r.Members = members
	return r, nil
}

// ListRouteModels returns every route model with members.
func (s *Store) ListRouteModels() ([]RouteModel, error) {
	rows, err := s.db.Query(`SELECT id, name, strategy, enabled, remark, created_at, updated_at
		FROM route_models ORDER BY id`)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []RouteModel{}
	for rows.Next() {
		var r RouteModel
		if err := rows.Scan(&r.ID, &r.Name, &r.Strategy, &r.Enabled, &r.Remark, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan route model: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, translateErr(err)
	}
	for i := range out {
		members, err := s.listRouteMembers(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Members = members
	}
	return out, nil
}

func (s *Store) listRouteMembers(routeModelID int64) ([]RouteModelMember, error) {
	rows, err := s.db.Query(`SELECT rm.id, rm.route_model_id, rm.model_id, rm.priority, rm.weight, rm.enabled,
			m.display_name, m.public_name, p.name, p.kind
		FROM route_model_members rm
		JOIN models m ON m.id = rm.model_id
		JOIN providers p ON p.id = m.provider_id
		WHERE rm.route_model_id = ?
		ORDER BY rm.priority DESC, rm.id`, routeModelID)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []RouteModelMember{}
	for rows.Next() {
		var m RouteModelMember
		if err := rows.Scan(&m.ID, &m.RouteModelID, &m.ModelID, &m.Priority, &m.Weight, &m.Enabled,
			&m.DisplayName, &m.PublicName, &m.ProviderName, &m.ProviderKind); err != nil {
			return nil, fmt.Errorf("store: scan route member: %w", err)
		}
		out = append(out, m)
	}
	return out, translateErr(rows.Err())
}

// DeleteRouteModel removes a route model; members go with it via cascade.
func (s *Store) DeleteRouteModel(id int64) error {
	res, err := s.db.Exec(`DELETE FROM route_models WHERE id = ?`, id)
	if err != nil {
		return translateErr(err)
	}
	return requireAffected(res, ErrNotFound)
}

// ResolveRouteModelCandidates returns the enabled members of a route model as
// full models, ordered by priority. Routing filters them further.
func (s *Store) ResolveRouteModelCandidates(routeModelID int64) ([]Model, error) {
	rows, err := s.db.Query(modelSelect+`
		WHERE m.id IN (
			SELECT model_id FROM route_model_members
			WHERE route_model_id = ? AND enabled = 1 AND (SELECT enabled FROM models mm WHERE mm.id = model_id) = 1
		)
		ORDER BY (SELECT priority FROM route_model_members rm
			WHERE rm.route_model_id = ? AND rm.model_id = m.id) DESC, m.id`,
		routeModelID, routeModelID)
	if err != nil {
		return nil, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []Model{}
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan route candidate: %w", err)
		}
		out = append(out, *m)
	}
	return out, translateErr(rows.Err())
}
