package store

import (
	"fmt"
	"strings"
)

// InsertRequest appends one data-plane request row.
func (s *Store) InsertRequest(r RequestRecord) error {
	if s.db == nil {
		return errNoDatabase
	}
	_, err := s.db.Exec(`INSERT INTO requests
		(ts, endpoint, inbound_protocol, provider_id, provider_kind, model, client_key_id, credential_id,
		 status_code, stream, duration_ms, ttft_ms, input_tokens, output_tokens, cached_tokens, cost,
		 finish_reason, error_type, client_disconnected)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.TS, r.Endpoint, r.InboundProtocol, r.ProviderID, r.ProviderKind, r.Model, r.ClientKeyID, r.CredentialID,
		r.StatusCode, boolToInt(r.Stream), r.DurationMs, r.TTFTMs, r.InputTokens, r.OutputTokens, r.CachedTokens,
		r.Cost, r.FinishReason, r.ErrorType, boolToInt(r.ClientDisconnected))
	return translateErr(err)
}

// LogsFilter narrows a request log query. Zero values mean "no filter".
type LogsFilter struct {
	Limit       int
	Offset      int
	Endpoint    string
	Status      string
	ProviderID  int64
	Model       string
	ClientKeyID int64
	From        int64
	To          int64
}

// LogsPage is one page of request logs.
type LogsPage struct {
	Rows   []RequestRow `json:"rows"`
	Total  int64        `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

// ListRequests returns a filtered page of request logs, newest first.
func (s *Store) ListRequests(f LogsFilter) (LogsPage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	where := []string{"1 = 1"}
	args := []any{}
	if f.Endpoint != "" {
		where = append(where, "r.endpoint = ?")
		args = append(args, f.Endpoint)
	}
	switch f.Status {
	case "2xx":
		where = append(where, "r.status_code >= 200 AND r.status_code < 300")
	case "3xx":
		where = append(where, "r.status_code >= 300 AND r.status_code < 400")
	case "4xx":
		where = append(where, "r.status_code >= 400 AND r.status_code < 500")
	case "5xx":
		where = append(where, "r.status_code >= 500")
	}
	if f.ProviderID > 0 {
		where = append(where, "r.provider_id = ?")
		args = append(args, f.ProviderID)
	}
	if f.Model != "" {
		where = append(where, "r.model = ?")
		args = append(args, f.Model)
	}
	if f.ClientKeyID > 0 {
		where = append(where, "r.client_key_id = ?")
		args = append(args, f.ClientKeyID)
	}
	if f.From > 0 {
		where = append(where, "r.ts >= ?")
		args = append(args, f.From)
	}
	if f.To > 0 {
		where = append(where, "r.ts <= ?")
		args = append(args, f.To)
	}
	clause := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM requests r WHERE `+clause, args...).Scan(&total); err != nil {
		return LogsPage{}, translateErr(err)
	}

	query := `SELECT r.id, r.ts, r.endpoint, r.inbound_protocol, r.provider_id, r.provider_kind,
			p.name, r.model, r.client_key_id, k.name, r.credential_id, r.status_code, r.stream,
			r.duration_ms, r.ttft_ms, r.input_tokens, r.output_tokens, r.cached_tokens, r.cost,
			r.finish_reason, r.error_type, r.client_disconnected
		FROM requests r
		LEFT JOIN providers p ON p.id = r.provider_id
		LEFT JOIN client_keys k ON k.id = r.client_key_id
		WHERE ` + clause + ` ORDER BY r.ts DESC, r.id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(query, append(args, limit, f.Offset)...)
	if err != nil {
		return LogsPage{}, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []RequestRow{}
	for rows.Next() {
		var r RequestRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Endpoint, &r.InboundProtocol, &r.ProviderID, &r.ProviderKind,
			&r.ProviderName, &r.Model, &r.ClientKeyID, &r.ClientKeyName, &r.CredentialID, &r.StatusCode,
			&r.Stream, &r.DurationMs, &r.TTFTMs, &r.InputTokens, &r.OutputTokens, &r.CachedTokens, &r.Cost,
			&r.FinishReason, &r.ErrorType, &r.ClientDisconnected); err != nil {
			return LogsPage{}, fmt.Errorf("store: scan request: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return LogsPage{}, translateErr(err)
	}
	return LogsPage{Rows: out, Total: total, Limit: limit, Offset: f.Offset}, nil
}

// GetRequest returns one request row by id.
func (s *Store) GetRequest(id int64) (*RequestRow, error) {
	row := s.db.QueryRow(`SELECT r.id, r.ts, r.endpoint, r.inbound_protocol, r.provider_id, r.provider_kind,
			p.name, r.model, r.client_key_id, k.name, r.credential_id, r.status_code, r.stream,
			r.duration_ms, r.ttft_ms, r.input_tokens, r.output_tokens, r.cached_tokens, r.cost,
			r.finish_reason, r.error_type, r.client_disconnected
		FROM requests r
		LEFT JOIN providers p ON p.id = r.provider_id
		LEFT JOIN client_keys k ON k.id = r.client_key_id
		WHERE r.id = ?`, id)
	var r RequestRow
	if err := row.Scan(&r.ID, &r.TS, &r.Endpoint, &r.InboundProtocol, &r.ProviderID, &r.ProviderKind,
		&r.ProviderName, &r.Model, &r.ClientKeyID, &r.ClientKeyName, &r.CredentialID, &r.StatusCode,
		&r.Stream, &r.DurationMs, &r.TTFTMs, &r.InputTokens, &r.OutputTokens, &r.CachedTokens, &r.Cost,
		&r.FinishReason, &r.ErrorType, &r.ClientDisconnected); err != nil {
		return nil, translateErr(err)
	}
	return &r, nil
}

// CleanupRequests deletes rows older than retentionDays and returns the count.
func (s *Store) CleanupRequests(retentionDays int) int64 {
	if retentionDays < 1 {
		retentionDays = 30
	}
	cutoff := s.nowMs() - int64(retentionDays)*86_400_000
	res, err := s.db.Exec(`DELETE FROM requests WHERE ts < ?`, cutoff)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// CountRequests returns the total number of stored request rows.
func (s *Store) CountRequests() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&n)
	return n, translateErr(err)
}
