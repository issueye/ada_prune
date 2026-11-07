package app

import (
	"context"
	"sync"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/logging"
	"github.com/learningdog1/ada_proxy/internal/store"
)

// recorderQueueSize bounds the number of finished calls waiting to be written.
// When the queue is full, records are dropped rather than blocking a response:
// losing an analytics row is better than stalling a stream.
const recorderQueueSize = 512

// recorder persists finished calls on a background worker.
type recorder struct {
	st  *store.Store
	log *logging.Logger
	ch  chan gateway.Record

	closeOnce sync.Once
}

// newRecorder returns a recorder whose worker is started by Run.
func newRecorder(st *store.Store, log *logging.Logger) *recorder {
	return &recorder{st: st, log: log, ch: make(chan gateway.Record, recorderQueueSize)}
}

// Record enqueues one call. It never blocks.
func (r *recorder) Record(rec gateway.Record) {
	select {
	case r.ch <- rec:
	default:
		r.log.Warn("Request record dropped: queue full", map[string]any{
			"endpoint": rec.Endpoint, "model": rec.Model,
		})
	}
}

// Run drains the queue until ctx is cancelled, then flushes what is left.
func (r *recorder) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.flush()
			return
		case rec := <-r.ch:
			r.write(rec)
		}
	}
}

// flush writes whatever is already queued, without waiting for new work.
func (r *recorder) flush() {
	for {
		select {
		case rec := <-r.ch:
			r.write(rec)
		default:
			return
		}
	}
}

// Close stops accepting new records.
func (r *recorder) Close() {
	r.closeOnce.Do(func() {
		// The channel is not closed: a late Record call must not panic.
	})
}

func (r *recorder) write(rec gateway.Record) {
	row := store.RequestRecord{
		TS:                 rec.TS,
		Endpoint:           rec.Endpoint,
		InboundProtocol:    string(rec.InProtocol),
		ProviderKind:       rec.ProviderKind,
		Model:              rec.Model,
		StatusCode:         int64(rec.StatusCode),
		Stream:             rec.Stream,
		DurationMs:         rec.DurationMs,
		TTFTMs:             rec.TTFTMs,
		InputTokens:        int64(rec.Usage.PromptTokens),
		OutputTokens:       int64(rec.Usage.CompletionTokens),
		CachedTokens:       int64(rec.Usage.CachedTokens),
		Cost:               rec.Cost,
		FinishReason:       rec.FinishReason,
		ErrorType:          rec.ErrorType,
		ClientDisconnected: rec.ClientDisconnected,
	}
	if rec.ProviderID > 0 {
		id := rec.ProviderID
		row.ProviderID = &id
	}
	if rec.CredentialID > 0 {
		id := rec.CredentialID
		row.CredentialID = &id
	}
	if rec.ClientKeyID > 0 {
		id := rec.ClientKeyID
		row.ClientKeyID = &id
	}

	if err := r.st.InsertRequest(row); err != nil {
		r.log.Warn("Cannot persist request record", map[string]any{"error": err.Error()})
		return
	}
	if err := r.st.RecordUsage(rec.TS, rec.ProviderID, rec.Model, rec.ClientKeyID,
		row.InputTokens, row.OutputTokens, row.CachedTokens, rec.Cost); err != nil {
		r.log.Warn("Cannot persist usage aggregate", map[string]any{"error": err.Error()})
	}
	if rec.ClientKeyID > 0 {
		r.st.AddClientKeyUsage(rec.ClientKeyID, row.InputTokens+row.OutputTokens)
	}
}
