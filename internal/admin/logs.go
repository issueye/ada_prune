package admin

import (
	"context"

	"github.com/learningdog1/ada_prune/internal/store"
)

// LogsQuery selects one page of request logs.
type LogsQuery struct {
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

// Logs returns a page of request logs.
func (s *Service) Logs(ctx context.Context, q LogsQuery) (store.LogsPage, error) {
	return s.d.Store.ListRequests(store.LogsFilter{
		Limit:       q.Limit,
		Offset:      q.Offset,
		Endpoint:    q.Endpoint,
		Status:      q.Status,
		ProviderID:  q.ProviderID,
		Model:       q.Model,
		ClientKeyID: q.ClientKeyID,
		From:        q.From,
		To:          q.To,
	})
}

// UsageQuery selects an aggregation.
type UsageQuery struct {
	From    string
	To      string
	GroupBy store.UsageGroup
}

// Usage returns the aggregated usage for a window.
func (s *Service) Usage(ctx context.Context, q UsageQuery) (store.UsagePage, error) {
	group := q.GroupBy
	switch group {
	case store.GroupByDay, store.GroupByModel, store.GroupByProvider, store.GroupByClientKey:
	case "":
		group = store.GroupByDay
	default:
		return store.UsagePage{}, FieldError(400, "groupBy",
			"groupBy must be day, model, provider or clientKey")
	}
	return s.d.Store.UsageAggregate(store.UsageQuery{From: q.From, To: q.To, GroupBy: group})
}

// RecentRequests returns the newest rows, for the dashboard tail.
func (s *Service) RecentRequests(ctx context.Context, limit int) ([]store.RequestRow, error) {
	page, err := s.d.Store.ListRequests(store.LogsFilter{Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Rows, nil
}
