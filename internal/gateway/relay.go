package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/learningdog1/ada_proxy/internal/gateway/codec"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
	"github.com/learningdog1/ada_proxy/internal/logging"
)

// Limits bounds one data-plane call.
type Limits struct {
	// MaxBodyBytes caps the upstream response the gateway buffers for a
	// non-streaming answer.
	MaxBodyBytes int64
	// StreamIdle aborts a stream that goes quiet for too long. The clock restarts
	// on every event, because a model may legitimately think for minutes.
	StreamIdle time.Duration
	// NonStreamIdle is the same idea for a buffered answer.
	NonStreamIdle time.Duration
}

// DefaultLimits returns sane bounds.
func DefaultLimits() Limits {
	return Limits{
		MaxBodyBytes:  32 << 20,
		StreamIdle:    30 * time.Second,
		NonStreamIdle: 90 * time.Second,
	}
}

// Relay orchestrates one call: decode, route, invoke with credential rotation,
// write back in the caller's protocol, then record.
type Relay struct {
	router   *Router
	recorder Recorder
	log      *logging.Logger
	limits   Limits
	// defaultModel is used when the request names none.
	defaultModel string
	now          func() time.Time
}

// NewRelay wires a relay.
func NewRelay(router *Router, recorder Recorder, log *logging.Logger, limits Limits) *Relay {
	if limits.MaxBodyBytes <= 0 {
		limits.MaxBodyBytes = DefaultLimits().MaxBodyBytes
	}
	if limits.StreamIdle <= 0 {
		limits.StreamIdle = DefaultLimits().StreamIdle
	}
	if limits.NonStreamIdle <= 0 {
		limits.NonStreamIdle = DefaultLimits().NonStreamIdle
	}
	return &Relay{router: router, recorder: recorder, log: log, limits: limits, now: time.Now}
}

// SetDefaultModel configures the fallback model name.
func (r *Relay) SetDefaultModel(name string) { r.defaultModel = name }

// CallRequest is one inbound data-plane call.
type CallRequest struct {
	// Inbound is the protocol the client speaks.
	Inbound ir.Protocol
	// Body is the raw request body, already size-limited.
	Body []byte
	// Endpoint is the path, for logging.
	Endpoint string
	// ClientKeyID is 0 for anonymous calls.
	ClientKeyID int64
	// Session carries the client's session hints.
	Session SessionHints
	// Writer receives the response.
	Writer http.ResponseWriter
}

// Call serves one request. Errors are written to the response; the return value
// reports whether the call succeeded for metrics purposes.
func (r *Relay) Call(ctx context.Context, in CallRequest) bool {
	started := r.now()
	w := in.Writer

	callCodec, err := codec.Get(in.Inbound)
	if err != nil {
		r.writeError(w, in.Inbound, NewClientError(http.StatusBadRequest, "unsupported inbound protocol"))
		return false
	}

	req, err := callCodec.DecodeRequest(in.Body)
	if err != nil {
		r.writeError(w, in.Inbound, InvalidRequest("", err.Error()))
		return false
	}
	modelName := req.Model
	if modelName == "" {
		modelName = r.defaultModel
	}
	if modelName == "" {
		r.writeError(w, in.Inbound, InvalidRequest("model", "model is required"))
		return false
	}

	route, err := r.router.Resolve(modelName)
	if err != nil {
		r.writeError(w, in.Inbound, routeError(err, modelName))
		return false
	}

	// Capability checks happen before any upstream call: paying for a request the
	// adapter cannot honour would be worse than refusing it.
	if problem := capabilityProblem(req, route); problem != nil {
		r.writeError(w, in.Inbound, problem)
		return false
	}

	// Upstreams address models by their own name, not the gateway's public one.
	req.Model = route.Model.UpstreamName

	resp, credential, herr := r.invoke(ctx, route, req, in.Session)
	if herr != nil {
		r.writeError(w, in.Inbound, herr)
		r.record(in, route, credential, Record{
			TS: started.UnixMilli(), StatusCode: herr.Status, Stream: req.Stream,
			DurationMs: r.now().Sub(started).Milliseconds(), ErrorType: herr.Type,
		})
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	// Content negotiation: a client that asked for a stream and one that did not
	// take different paths, but both end in the same recorder.
	if req.Stream {
		return r.stream(ctx, w, in, route, credential, resp, req, started)
	}
	return r.buffered(w, in, route, credential, resp, req, started)
}

// invoke sends the request, rotating credentials when the vendor says the account
// is exhausted.
func (r *Relay) invoke(ctx context.Context, route *Route, req *ir.ChatRequest,
	session SessionHints) (*http.Response, Credential, *ClientError) {
	quota, hasQuota := route.Adapter.(QuotaPolicy)
	tried := map[int64]bool{}
	var lastCredential Credential

	for i := 0; i < len(route.Credentials); i++ {
		credential := route.Credentials[i]
		if tried[credential.ID] {
			continue
		}
		tried[credential.ID] = true
		lastCredential = credential

		callCtx := ctx
		var cancel context.CancelFunc
		if route.Provider.TimeoutMs > 0 {
			callCtx, cancel = context.WithTimeout(ctx, time.Duration(route.Provider.TimeoutMs)*time.Millisecond)
		}

		resp, err := route.Adapter.Do(callCtx, &InvokeRequest{
			Provider:   route.Provider,
			Credential: credential,
			Model:      route.Model,
			Request:    req,
			Session:    session,
		})
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, credential, NewClientError(http.StatusBadGateway, "request cancelled")
			}
			return nil, credential, &ClientError{
				Status:     http.StatusBadGateway,
				Type:       "upstream_error",
				Message:    "upstream request failed: " + err.Error(),
				RetryAfter: 10,
			}
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			r.router.Touch(credential.ID)
			return resp, credential, nil
		}

		body := readLimited(resp.Body, 64<<10)
		_ = resp.Body.Close()

		verdict := QuotaVerdict{Kind: QuotaNone}
		if hasQuota {
			verdict = quota.Classify(resp.StatusCode, []byte(body))
		}

		rotate := hasQuota && quota.RotateOn(verdict.Kind)
		if !rotate {
			return nil, credential, MapUpstreamStatus(resp.StatusCode, []byte(body))
		}

		switch verdict.Kind {
		case QuotaCredit:
			if err := r.router.MarkCredentialExhausted(credential.ID); err != nil {
				r.log.Warn("Cannot mark credential exhausted", map[string]any{
					"credentialId": credential.ID, "error": err.Error(),
				})
			}
			r.log.Warn("Upstream credit exhausted, rotating", map[string]any{
				"credentialId": credential.ID, "provider": route.Provider.Name,
			})
		case QuotaWindow:
			r.router.BlockWindow(credential.ID, verdict.Window, verdict.Until)
			r.log.Warn("Upstream window exhausted, rotating", map[string]any{
				"credentialId": credential.ID, "window": verdict.Window, "until": verdict.Until,
			})
		}

		if i == len(route.Credentials)-1 {
			return nil, credential, MapUpstreamStatus(resp.StatusCode, []byte(body))
		}
	}

	return nil, lastCredential, NewClientError(http.StatusTooManyRequests,
		"no upstream credential is currently available")
}

// stream forwards the answer as the client's protocol expects it.
func (r *Relay) stream(ctx context.Context, w http.ResponseWriter, in CallRequest,
	route *Route, credential Credential, resp *http.Response, req *ir.ChatRequest, started time.Time) bool {
	encoder, err := codec.Get(in.Inbound)
	if err != nil {
		r.writeError(w, in.Inbound, NewClientError(http.StatusBadRequest, "unsupported inbound protocol"))
		return false
	}

	// Headers must be sent before the first byte of the body.
	writeStreamHeaders(w, in.Inbound)
	streamEncoder := encoder.NewStreamEncoder(w)
	decoder := route.Adapter.DecodeStream(resp.Body, &InvokeRequest{
		Provider: route.Provider, Credential: credential, Model: route.Model, Request: req, Session: in.Session,
	})

	var (
		usage        ir.Usage
		finish       string
		ttft         int64
		sentAnything bool
		decodeErr    error
	)

	ticks, stop := pumpChunks(ctx, decoder)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			r.record(in, route, credential, Record{
				TS: started.UnixMilli(), StatusCode: 200, Stream: true, TTFTMs: ttft,
				Usage: usage, FinishReason: finish, ClientDisconnected: true,
				DurationMs: r.now().Sub(started).Milliseconds(),
			})
			return false

		case <-time.After(r.limits.StreamIdle):
			// The upstream went quiet. Abort it, then tell the client in-band so
			// it does not wait forever on a dead stream.
			r.log.Warn("Upstream stream idle timeout", map[string]any{
				"provider": route.Provider.Name, "model": route.Model.PublicName,
				"idleMs": r.limits.StreamIdle.Milliseconds(),
			})
			_ = streamEncoder.Close()
			r.record(in, route, credential, Record{
				TS: started.UnixMilli(), StatusCode: 504, Stream: true, TTFTMs: ttft,
				Usage: usage, ErrorType: "stream_idle_timeout",
				DurationMs: r.now().Sub(started).Milliseconds(),
			})
			return false

		case result, ok := <-ticks:
			if !ok {
				// Stream finished.
				_ = streamEncoder.Close()
				status := http.StatusOK
				errorType := ""
				if decodeErr != nil {
					status = http.StatusBadGateway
					errorType = "upstream_stream_error"
				} else if finish == "" && !sentAnything {
					status = http.StatusBadGateway
					errorType = "upstream_stream_error"
				}
				r.record(in, route, credential, Record{
					TS: started.UnixMilli(), StatusCode: status, Stream: true, TTFTMs: ttft,
					Usage: usage, FinishReason: finish, ErrorType: errorType,
					DurationMs: r.now().Sub(started).Milliseconds(),
				})
				return decodeErr == nil
			}
			if result.err != nil {
				decodeErr = result.err
				// Render the failure inside the stream: headers are already sent.
				failure := NewClientError(http.StatusBadGateway, result.err.Error())
				_ = streamEncoder.Write(&ir.StreamChunk{
					Kind: ir.ChunkText,
					Text: "",
				})
				_ = writeInBandError(w, in.Inbound, failure)
				_ = streamEncoder.Close()
				continue
			}

			chunk := result.chunk
			switch chunk.Kind {
			case ir.ChunkText, ir.ChunkToolCall:
				if ttft == 0 {
					ttft = r.now().Sub(started).Milliseconds()
				}
			case ir.ChunkUsage:
				if chunk.Usage != nil {
					usage = *chunk.Usage
				}
			case ir.ChunkFinish:
				finish = chunk.FinishReason
			}
			if chunk.Kind == ir.ChunkDone {
				continue
			}
			sentAnything = true
			if err := streamEncoder.Write(chunk); err != nil {
				// Writing to a gone client: stop reading upstream.
				r.record(in, route, credential, Record{
					TS: started.UnixMilli(), StatusCode: 200, Stream: true, TTFTMs: ttft,
					Usage: usage, FinishReason: finish, ClientDisconnected: true,
					DurationMs: r.now().Sub(started).Milliseconds(),
				})
				return false
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		if decodeErr != nil {
			// The error reached the client; stop the loop.
			r.record(in, route, credential, Record{
				TS: started.UnixMilli(), StatusCode: 502, Stream: true, TTFTMs: ttft,
				Usage: usage, ErrorType: "upstream_stream_error",
				DurationMs: r.now().Sub(started).Milliseconds(),
			})
			return false
		}
	}
}

type chunkResult struct {
	chunk *ir.StreamChunk
	err   error
}

// pumpChunks reads the decoder on a goroutine so the caller can enforce an idle
// timeout and client cancellation. The returned stop function aborts it.
func pumpChunks(ctx context.Context, decoder StreamDecoder) (<-chan chunkResult, func()) {
	out := make(chan chunkResult)
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			chunk, err := decoder.Next()
			select {
			case out <- chunkResult{chunk: chunk, err: err}:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var once bool
	return out, func() {
		if once {
			return
		}
		once = true
		close(done)
	}
}

// buffered serves a non-streaming client from the upstream body.
func (r *Relay) buffered(w http.ResponseWriter, in CallRequest, route *Route,
	credential Credential, resp *http.Response, req *ir.ChatRequest, started time.Time) bool {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, r.limits.MaxBodyBytes))
	if err != nil {
		r.writeError(w, in.Inbound, NewClientError(http.StatusBadGateway, "reading upstream response: "+err.Error()))
		r.record(in, route, credential, Record{
			TS: started.UnixMilli(), StatusCode: 502, ErrorType: "upstream_read_error",
			DurationMs: r.now().Sub(started).Milliseconds(),
		})
		return false
	}

	canonical, err := route.Adapter.DecodeResponse(raw, &InvokeRequest{
		Provider: route.Provider, Credential: credential, Model: route.Model, Request: req, Session: in.Session,
	})
	if err != nil {
		failure := TruncatedStreamError(err.Error())
		r.writeError(w, in.Inbound, failure)
		r.record(in, route, credential, Record{
			TS: started.UnixMilli(), StatusCode: failure.Status, ErrorType: failure.Type,
			DurationMs: r.now().Sub(started).Milliseconds(),
		})
		return false
	}

	outCodec, err := codec.Get(in.Inbound)
	if err != nil {
		r.writeError(w, in.Inbound, NewClientError(http.StatusBadRequest, "unsupported inbound protocol"))
		return false
	}
	// Zero-output answers are a retryable upstream failure, not a valid empty
	// response: returning 200 would make clients store an empty turn.
	if canonical.Usage.CompletionTokens == 0 && canonical.Text() == "" && len(canonical.ToolCalls) == 0 {
		failure := &ClientError{
			Status:     http.StatusTooManyRequests,
			Type:       "rate_limit_error",
			Message:    "upstream produced no output",
			RetryAfter: 10,
		}
		r.writeError(w, in.Inbound, failure)
		r.record(in, route, credential, Record{
			TS: started.UnixMilli(), StatusCode: failure.Status, ErrorType: failure.Type,
			DurationMs: r.now().Sub(started).Milliseconds(),
		})
		return false
	}

	body, err := outCodec.EncodeResponse(canonical)
	if err != nil {
		r.writeError(w, in.Inbound, NewClientError(http.StatusBadGateway, "encoding response: "+err.Error()))
		return false
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Provider", route.Provider.Name)
	w.Header().Set("X-Model", route.Model.PublicName)
	w.Header().Set("X-Usage-Tokens", usageHeader(canonical.Usage))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)

	cost := Cost(route.Model, canonical.Usage)
	r.record(in, route, credential, Record{
		TS: started.UnixMilli(), StatusCode: 200, Usage: canonical.Usage, Cost: cost,
		FinishReason: canonical.FinishReason,
		DurationMs:   r.now().Sub(started).Milliseconds(),
	})
	return true
}

// writeStreamHeaders sets the SSE headers for the client's protocol.
func writeStreamHeaders(w http.ResponseWriter, in ir.Protocol) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	if in != ir.ProtoOpenAIResponses {
		// Responses streams are also SSE; the header name is kept for clarity.
	}
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// writeInBandError emits an error frame after the stream has started.
func writeInBandError(w http.ResponseWriter, in ir.Protocol, failure *ClientError) error {
	payload := EncodeClientError(in, failure)
	if in == ir.ProtoAnthropic {
		_, err := w.Write([]byte("event: error\ndata: " + string(payload) + "\n\n"))
		return err
	}
	_, err := w.Write([]byte("data: " + string(payload) + "\n\n"))
	return err
}

// writeError renders a failure with the status still changeable.
func (r *Relay) writeError(w http.ResponseWriter, in ir.Protocol, failure *ClientError) {
	if failure == nil {
		failure = NewClientError(http.StatusInternalServerError, "internal error")
	}
	if failure.RetryAfter > 0 {
		w.Header().Set("Retry-After", itoa(failure.RetryAfter))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(failure.Status)
	_, _ = w.Write(EncodeClientError(in, failure))
}

// record files one call, never blocking the response on persistence.
func (r *Relay) record(in CallRequest, route *Route, credential Credential, rec Record) {
	if r.recorder == nil {
		return
	}
	rec.Endpoint = in.Endpoint
	rec.InProtocol = in.Inbound
	rec.ClientKeyID = in.ClientKeyID
	if route != nil {
		rec.ProviderID = route.Provider.ID
		rec.ProviderKind = string(route.Provider.Kind)
		rec.Model = route.Model.PublicName
	}
	rec.CredentialID = credential.ID
	if rec.TS == 0 {
		rec.TS = r.now().UnixMilli()
	}
	r.recorder.Record(rec)
}

// capabilityProblem refuses a request the adapter cannot serve before spending an
// upstream call.
func capabilityProblem(req *ir.ChatRequest, route *Route) *ClientError {
	caps := route.Adapter.Capabilities()
	if req.Stream && !caps.Stream {
		return InvalidRequest("stream", "the selected model does not support streaming")
	}
	if len(req.Tools) > 0 && (!caps.Tools || !route.Model.SupportsTools) {
		return InvalidRequest("tools", "the selected model does not support tools")
	}
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == ir.BlockImage && (!caps.Vision || !route.Model.SupportsVision) {
				return InvalidRequest("messages", "the selected model does not support images")
			}
		}
	}
	return nil
}

// routeError maps a resolution failure onto a client error.
func routeError(err error, model string) *ClientError {
	switch {
	case errors.Is(err, ErrModelNotFound), errors.Is(err, ErrRouteModelNotFound):
		return &ClientError{
			Status:  http.StatusNotFound,
			Type:    "not_found_error",
			Message: "model " + model + " is not available",
			Code:    "model_not_found",
		}
	case errors.Is(err, ErrNoCredential):
		return &ClientError{
			Status:     http.StatusTooManyRequests,
			Type:       "rate_limit_error",
			Message:    "no upstream credential is currently available",
			RetryAfter: 30,
		}
	case errors.As(err, new(*AdapterError)):
		return NewClientError(http.StatusInternalServerError, err.Error())
	default:
		return NewClientError(http.StatusBadGateway, err.Error())
	}
}

// Cost computes the price of one call from the model's per-million-token prices.
func Cost(model ModelRef, usage ir.Usage) float64 {
	uncached := usage.PromptTokens - usage.CachedTokens
	if uncached < 0 {
		uncached = 0
	}
	cachedPrice := model.CachedInputPrice
	if cachedPrice == 0 {
		cachedPrice = model.InputPrice
	}
	const perMillion = 1_000_000
	return float64(uncached)/perMillion*model.InputPrice +
		float64(usage.CachedTokens)/perMillion*cachedPrice +
		float64(usage.CompletionTokens)/perMillion*model.OutputPrice
}

// usageHeader renders "in,out,cached" for the response headers.
func usageHeader(u ir.Usage) string {
	return itoa(u.PromptTokens) + "," + itoa(u.CompletionTokens) + "," + itoa(u.CachedTokens)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// readLimited reads at most n bytes of a body.
func readLimited(r io.Reader, n int64) string {
	if n <= 0 {
		n = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r, n))
	if err != nil {
		return string(raw)
	}
	return string(raw)
}
