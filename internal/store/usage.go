package store

import (
	"fmt"
	"time"
)

// RecordUsage adds one request to the daily aggregate. The key is
// (day, provider, model, client key), which is the finest grain the UI asks for.
func (s *Store) RecordUsage(ts int64, providerID int64, model string, clientKeyID int64,
	inputTokens, outputTokens, cachedTokens int64, cost float64) error {
	if s.db == nil {
		return errNoDatabase
	}
	day := time.UnixMilli(ts).UTC().Format("2006-01-02")
	_, err := s.db.Exec(`INSERT INTO usage_daily
		(day, provider_id, model, client_key_id, requests, input_tokens, output_tokens, cached_tokens, cost)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)
		ON CONFLICT(day, provider_id, model, client_key_id) DO UPDATE SET
			requests      = requests + 1,
			input_tokens  = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			cached_tokens = cached_tokens + excluded.cached_tokens,
			cost          = cost + excluded.cost`,
		day, providerID, model, clientKeyID, inputTokens, outputTokens, cachedTokens, cost)
	return translateErr(err)
}

// UsageGroup selects the aggregation dimension.
type UsageGroup string

const (
	// GroupByDay aggregates per calendar day.
	GroupByDay UsageGroup = "day"
	// GroupByModel aggregates per public model name.
	GroupByModel UsageGroup = "model"
	// GroupByProvider aggregates per provider.
	GroupByProvider UsageGroup = "provider"
	// GroupByClientKey aggregates per client key.
	GroupByClientKey UsageGroup = "clientKey"
)

// UsageQuery selects an aggregation window and dimension.
type UsageQuery struct {
	From    string
	To      string
	GroupBy UsageGroup
}

// UsagePage is the usage response.
type UsagePage struct {
	Rows    []UsageRow   `json:"rows"`
	Summary UsageSummary `json:"summary"`
}

// UsageAggregate returns the grouped usage rows and the window summary.
func (s *Store) UsageAggregate(q UsageQuery) (UsagePage, error) {
	where := "1 = 1"
	args := []any{}
	if q.From != "" {
		where += " AND day >= ?"
		args = append(args, q.From)
	}
	if q.To != "" {
		where += " AND day <= ?"
		args = append(args, q.To)
	}

	var groupExpr, labelExpr string
	switch q.GroupBy {
	case GroupByModel:
		groupExpr, labelExpr = "model", "model"
	case GroupByProvider:
		groupExpr, labelExpr = "provider_id", "provider_id"
	case GroupByClientKey:
		groupExpr, labelExpr = "client_key_id", "client_key_id"
	default:
		groupExpr, labelExpr = "day", "day"
	}

	query := fmt.Sprintf(`SELECT %s,
			SUM(requests), SUM(input_tokens), SUM(output_tokens), SUM(cached_tokens), SUM(cost)
		FROM usage_daily WHERE %s
		GROUP BY %s ORDER BY %s`, labelExpr, where, groupExpr, labelExpr)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return UsagePage{}, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := []UsageRow{}
	for rows.Next() {
		var r UsageRow
		var label any
		if err := rows.Scan(&label, &r.Requests, &r.InputTokens, &r.OutputTokens,
			&r.CachedTokens, &r.Cost); err != nil {
			return UsagePage{}, fmt.Errorf("store: scan usage: %w", err)
		}
		switch q.GroupBy {
		case GroupByModel:
			r.Model, _ = label.(string)
		case GroupByProvider:
			r.ProviderID = toInt64(label)
		case GroupByClientKey:
			r.ClientKeyID = toInt64(label)
		default:
			r.Day, _ = label.(string)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return UsagePage{}, translateErr(err)
	}

	summary, err := s.UsageSummary(q.From, q.To)
	if err != nil {
		return UsagePage{}, err
	}
	return UsagePage{Rows: out, Summary: summary}, nil
}

// UsageSummary returns totals for a window, computed from the request log so it
// includes the error rate.
func (s *Store) UsageSummary(from, to string) (UsageSummary, error) {
	clause := "1 = 1"
	args := []any{}
	if from != "" {
		clause += " AND day >= ?"
		args = append(args, from)
	}
	if to != "" {
		clause += " AND day <= ?"
		args = append(args, to)
	}

	var out UsageSummary
	err := s.db.QueryRow(`SELECT COALESCE(SUM(requests),0), COALESCE(SUM(input_tokens),0),
			COALESCE(SUM(output_tokens),0), COALESCE(SUM(cached_tokens),0), COALESCE(SUM(cost),0)
		FROM usage_daily WHERE `+clause, args...).
		Scan(&out.Requests, &out.InputTokens, &out.OutputTokens, &out.CachedTokens, &out.Cost)
	if err != nil {
		return out, translateErr(err)
	}

	// Errors come from the request log, which is the only place a status code
	// lives. The window is converted from day granularity to timestamps.
	errClause := "1 = 1"
	errArgs := []any{}
	if from != "" {
		errClause += " AND ts >= ?"
		errArgs = append(errArgs, dayStartMs(from))
	}
	if to != "" {
		errClause += " AND ts < ?"
		errArgs = append(errArgs, dayStartMs(to)+86_400_000)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE `+errClause+` AND status_code >= 400`, errArgs...).
		Scan(&out.Errors); err != nil {
		return out, translateErr(err)
	}
	if out.Requests > 0 {
		out.SuccessRate = float64(out.Requests-out.Errors) / float64(out.Requests)
	}
	return out, nil
}

// Last24Hours returns the rolling 24 hour chart buckets, oldest first.
func (s *Store) Last24Hours(now int64, buckets int) (Last24Hours, error) {
	if buckets <= 0 {
		buckets = 24
	}
	const bucketMs = int64(3_600_000)
	start := now - int64(buckets)*bucketMs

	rows, err := s.db.Query(`SELECT ts, status_code FROM requests WHERE ts >= ? AND ts < ?`, start, now+1)
	if err != nil {
		return Last24Hours{}, translateErr(err)
	}
	defer func() { _ = rows.Close() }()

	points := make([]HourBucket, buckets)
	for i := range points {
		points[i] = HourBucket{TS: start + int64(i)*bucketMs}
	}
	for rows.Next() {
		var ts, status int64
		if err := rows.Scan(&ts, &status); err != nil {
			return Last24Hours{}, fmt.Errorf("store: scan chart: %w", err)
		}
		idx := int((ts - start) / bucketMs)
		if idx < 0 || idx >= buckets {
			continue
		}
		points[idx].Requests++
		if status >= 400 {
			points[idx].Errors++
		}
	}
	if err := rows.Err(); err != nil {
		return Last24Hours{}, translateErr(err)
	}

	out := Last24Hours{Points: points}
	for _, p := range points {
		out.Total += p.Requests
		if p.Requests > out.Peak {
			out.Peak = p.Requests
		}
	}
	return out, nil
}

// TodayStats returns aggregates since a timestamp (usually local midnight).
func (s *Store) TodayStats(since int64) (UsageSummary, error) {
	var out UsageSummary
	err := s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cached_tokens),0), COALESCE(SUM(cost),0)
		FROM requests WHERE ts >= ?`, since).
		Scan(&out.Requests, &out.InputTokens, &out.OutputTokens, &out.CachedTokens, &out.Cost)
	if err != nil {
		return out, translateErr(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE ts >= ? AND status_code >= 400`, since).
		Scan(&out.Errors); err != nil {
		return out, translateErr(err)
	}
	if out.Requests > 0 {
		out.SuccessRate = float64(out.Requests-out.Errors) / float64(out.Requests)
	}
	return out, nil
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case []byte:
		var n int64
		_, _ = fmt.Sscan(string(t), &n)
		return n
	case string:
		var n int64
		_, _ = fmt.Sscan(t, &n)
		return n
	default:
		return 0
	}
}

func dayStartMs(day string) int64 {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return 0
	}
	return t.UTC().UnixMilli()
}
