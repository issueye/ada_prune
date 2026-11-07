package command_code

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/learningdog1/ada_proxy/internal/gateway"
	"github.com/learningdog1/ada_proxy/internal/gateway/ir"
)

// ErrTruncated means the upstream ended the stream without a finish event, or
// with a finish event that carried no reason. Both mean the answer is
// incomplete, and reporting "stop" for them would be a lie.
var ErrTruncated = errors.New("command_code: upstream stream ended without a complete finish")

// StreamError is an error event delivered inside a stream.
type StreamError struct {
	Status  int
	Code    string
	Message string
	Verdict gateway.QuotaVerdict
}

func (e *StreamError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("command_code: upstream error %s: %s", e.Code, e.Message)
	}
	return "command_code: upstream error: " + e.Message
}

// Event is one line of the upstream's NDJSON response. Pointer and raw fields
// keep "absent" distinct from "zero", which matters for usage.
type Event struct {
	Type            string          `json:"type"`
	Text            string          `json:"text"`
	Delta           string          `json:"delta"`
	ToolCallID      string          `json:"toolCallId"`
	ToolName        string          `json:"toolName"`
	Input           json.RawMessage `json:"input"`
	FinishReason    *string         `json:"finishReason"`
	RawFinishReason *string         `json:"rawFinishReason"`
	Usage           *Usage          `json:"usage"`
	TotalUsage      *Usage          `json:"totalUsage"`
	Error           *EventError     `json:"error"`
	Message         string          `json:"message"`
}

// EventError is the payload of an "error" event.
type EventError struct {
	Message     string     `json:"message"`
	Code        string     `json:"code"`
	StatusCode  *int       `json:"statusCode"`
	IsRetryable *bool      `json:"isRetryable"`
	RateLimit   *RateLimit `json:"rateLimit"`
}

// RateLimit names the window an error refers to, when it does.
type RateLimit struct {
	Window string   `json:"window"`
	Reset  *float64 `json:"reset"`
}

// Usage is the upstream's token accounting. Every field is a pointer so that
// "absent" and "zero" stay distinct.
type Usage struct {
	InputTokens       *int64        `json:"inputTokens"`
	OutputTokens      *int64        `json:"outputTokens"`
	CachedInputTokens *int64        `json:"cachedInputTokens"`
	InputTokenDetails *UsageDetails `json:"inputTokenDetails"`
	TotalTokens       *int64        `json:"totalTokens"`
}

// UsageDetails breaks input down further.
type UsageDetails struct {
	NoCacheTokens    *int64 `json:"noCacheTokens"`
	CacheReadTokens  *int64 `json:"cacheReadTokens"`
	CacheWriteTokens *int64 `json:"cacheWriteTokens"`
}

// Input returns inputTokens, or 0.
func (u *Usage) Input() int64 {
	if u == nil || u.InputTokens == nil {
		return 0
	}
	return *u.InputTokens
}

// Output returns outputTokens, or 0.
func (u *Usage) Output() int64 {
	if u == nil || u.OutputTokens == nil {
		return 0
	}
	return *u.OutputTokens
}

// CacheRead returns the cached input count, falling back to the detail block.
func (u *Usage) CacheRead() int64 {
	if u == nil {
		return 0
	}
	if u.CachedInputTokens != nil {
		return *u.CachedInputTokens
	}
	if u.InputTokenDetails != nil && u.InputTokenDetails.CacheReadTokens != nil {
		return *u.InputTokenDetails.CacheReadTokens
	}
	return 0
}

// CacheWrite returns the cache write count, or 0.
func (u *Usage) CacheWrite() int64 {
	if u == nil || u.InputTokenDetails == nil || u.InputTokenDetails.CacheWriteTokens == nil {
		return 0
	}
	return *u.InputTokenDetails.CacheWriteTokens
}

// NoCacheTokens returns noCacheTokens, or -1 when absent.
func (u *Usage) NoCacheTokens() int64 {
	if u == nil || u.InputTokenDetails == nil || u.InputTokenDetails.NoCacheTokens == nil {
		return -1
	}
	return *u.InputTokenDetails.NoCacheTokens
}

// TextOrDelta returns the event's text, preferring "text" over "delta".
func (e *Event) TextOrDelta() string {
	if e == nil {
		return ""
	}
	if e.Text != "" {
		return e.Text
	}
	return e.Delta
}

// ParseEvent parses one NDJSON line. Blank lines, the [DONE] sentinel, comments
// and unparsable lines return nil so callers skip them without treating them as
// errors.
func ParseEvent(line []byte) *Event {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || string(trimmed) == "[DONE]" || trimmed[0] == ':' {
		return nil
	}
	var ev Event
	if err := json.Unmarshal(trimmed, &ev); err != nil {
		return nil
	}
	if ev.Type == "" {
		return nil
	}
	return &ev
}

// EventFinishReason reads the effective finish reason:
// rawFinishReason ?? finishReason, with "other" and an empty raw reason counting
// as no signal at all.
func EventFinishReason(ev *Event) (string, bool) {
	raw := ev.RawFinishReason
	fin := ev.FinishReason
	rawEmpty := raw == nil || strings.TrimSpace(*raw) == ""
	if rawEmpty && fin != nil && *fin == "other" {
		return "", false
	}
	if rawEmpty {
		if fin == nil {
			return "", false
		}
		v := strings.TrimSpace(*fin)
		return v, v != ""
	}
	v := strings.TrimSpace(*raw)
	return v, v != ""
}

// MapFinishReason normalises an upstream reason. Unknown values pass through
// rather than being folded into "stop": a client that sees a strange reason can
// still tell the answer may be incomplete.
func MapFinishReason(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	if r == "" {
		return ""
	}
	switch r {
	case "tool-calls", "tool_calls", "tool_use", "tool-use":
		return ir.FinishToolCalls
	case "length", "max_tokens", "max_output_tokens", "model_context_window_exceeded":
		return ir.FinishLength
	case "stop", "end_turn":
		return ir.FinishStop
	case "content_filter", "refusal":
		return ir.FinishContentFilter
	}
	if isNetworkFailure(r) {
		return ir.FinishUpstreamError
	}
	return r
}

// isNetworkFailure matches connection-level failures, which only ever appear in
// rawFinishReason. Reading only finishReason reported them as normal completions.
func isNetworkFailure(r string) bool {
	for _, prefix := range []string{"network", "connection", "upstream"} {
		if !strings.HasPrefix(r, prefix) {
			continue
		}
		rest := strings.TrimLeft(strings.TrimPrefix(r, prefix), "-_ ")
		if rest == "error" {
			return true
		}
	}
	return false
}

// IncompleteDetail explains why a stream did not complete, or "" when it did.
func IncompleteDetail(sawFinish bool, finishReason string) string {
	switch {
	case !sawFinish:
		return "no finish event"
	case finishReason == ir.FinishUpstreamError:
		return "provider reported a connection failure"
	case finishReason == "":
		return "finish event without a finish reason"
	default:
		return ""
	}
}

// toIRUsage converts upstream accounting into the canonical form.
//
// PromptTokens is the total input (cache read and write included) and
// CachedTokens is the cached share. Protocol codecs decide the vendor-specific
// split: Anthropic reports only the non-cached part, OpenAI Responses reports the
// total.
func toIRUsage(u *Usage) *ir.Usage {
	if u == nil {
		return nil
	}
	out := &ir.Usage{
		PromptTokens:     int(u.Input()),
		CompletionTokens: int(u.Output()),
		CachedTokens:     int(u.CacheRead() + u.CacheWrite()),
	}
	if u.TotalTokens != nil {
		out.TotalTokens = int(*u.TotalTokens)
	}
	out.Normalize()
	// A generation that produced no output but consumed input is a failed
	// generation; billing the caller for its input would be wrong.
	if out.CompletionTokens == 0 {
		out.PromptTokens = 0
		out.CachedTokens = 0
		out.TotalTokens = 0
	}
	return out
}

// decodeError maps an "error" event onto the gateway's error vocabulary.
func decodeError(ev *Event) (status int, code, message string, verdict gateway.QuotaVerdict) {
	message = "unknown upstream error"
	reported := 0
	if ev != nil && ev.Error != nil {
		if ev.Error.Message != "" {
			message = ev.Error.Message
		}
		code = ev.Error.Code
		if ev.Error.StatusCode != nil {
			reported = *ev.Error.StatusCode
		}
	}
	if ev != nil && ev.Message != "" && (ev.Error == nil || ev.Error.Message == "") {
		message = ev.Message
	}
	if m := embeddedStatus.FindStringSubmatch(message); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			reported = n
		}
	}
	if reported == 0 {
		reported = 502
	}
	return reported, code, message, classify(reported, "")
}

var embeddedStatus = regexp.MustCompile(`^<(\d{3})>`)

// ---- stream decoding ----

// streamDecoder turns NDJSON lines into canonical chunks. It is synchronous: the
// relay wraps it with an idle watchdog and a cancellation check.
type streamDecoder struct {
	sc *bufio.Scanner
	in *gateway.InvokeRequest

	pending       *Event
	started       bool
	sawFinish     bool
	finishReason  string
	finishEmitted bool
}

func newStreamDecoder(r io.Reader, in *gateway.InvokeRequest) *streamDecoder {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	return &streamDecoder{sc: sc, in: in}
}

// Next returns the next canonical chunk, io.EOF at the end of a complete stream,
// or an error when the stream is truncated or reports a failure.
func (d *streamDecoder) Next() (*ir.StreamChunk, error) {
	for {
		if d.pending == nil {
			if !d.sc.Scan() {
				break
			}
			d.pending = ParseEvent(d.sc.Bytes())
			if d.pending == nil {
				continue
			}
		}

		// Open the stream before the first real event, keeping it pending so
		// nothing is dropped.
		if !d.started {
			d.started = true
			return d.startChunk(), nil
		}

		ev := d.pending
		d.pending = nil
		switch ev.Type {
		case "text-delta":
			if text := ev.TextOrDelta(); text != "" {
				return &ir.StreamChunk{Kind: ir.ChunkText, Text: text}, nil
			}
		case "reasoning-delta":
			if text := ev.TextOrDelta(); text != "" {
				return &ir.StreamChunk{Kind: ir.ChunkThinking, Text: text}, nil
			}
		case "tool-call":
			args := "{}"
			if len(ev.Input) > 0 {
				args = string(normalizeArguments(string(ev.Input)))
			}
			return &ir.StreamChunk{
				Kind:         ir.ChunkToolCall,
				ToolCallID:   ev.ToolCallID,
				ToolName:     ev.ToolName,
				ArgsComplete: args,
			}, nil
		case "finish", "finish-step":
			d.sawFinish = true
			if reason, ok := EventFinishReason(ev); ok {
				d.finishReason = MapFinishReason(reason)
			}
			if usage := toIRUsage(firstUsage(ev.TotalUsage, ev.Usage)); usage != nil {
				return &ir.StreamChunk{Kind: ir.ChunkUsage, Usage: usage}, nil
			}
		case "error":
			status, code, message, verdict := decodeError(ev)
			return nil, &StreamError{Status: status, Code: code, Message: message, Verdict: verdict}
		}
	}

	if err := d.sc.Err(); err != nil {
		return nil, fmt.Errorf("command_code: read stream: %w", err)
	}
	if detail := IncompleteDetail(d.sawFinish, d.finishReason); detail != "" {
		return nil, fmt.Errorf("%w (%s)", ErrTruncated, detail)
	}
	if !d.finishEmitted && d.finishReason != "" {
		d.finishEmitted = true
		return &ir.StreamChunk{Kind: ir.ChunkFinish, FinishReason: d.finishReason}, nil
	}
	return nil, io.EOF
}

func (d *streamDecoder) startChunk() *ir.StreamChunk {
	model := ""
	if d.in != nil {
		model = d.in.Model.UpstreamName
	}
	return &ir.StreamChunk{Kind: ir.ChunkStart, Model: model}
}

func firstUsage(values ...*Usage) *Usage {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// ---- non-streaming aggregation ----

// aggregate folds a complete NDJSON body into one canonical response. The
// upstream always streams, so this is how a non-streaming client is served.
func aggregate(body []byte, in *gateway.InvokeRequest) (*ir.ChatResponse, error) {
	dec := newStreamDecoder(strings.NewReader(string(body)), in)
	out := &ir.ChatResponse{Model: modelName(in)}
	var text strings.Builder
	var thinking strings.Builder

	for {
		chunk, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch chunk.Kind {
		case ir.ChunkText:
			text.WriteString(chunk.Text)
		case ir.ChunkThinking:
			thinking.WriteString(chunk.Text)
		case ir.ChunkToolCall:
			out.ToolCalls = append(out.ToolCalls, ir.ToolCall{
				ID:        chunk.ToolCallID,
				Name:      chunk.ToolName,
				Arguments: chunk.ArgsComplete,
			})
		case ir.ChunkUsage:
			if chunk.Usage != nil {
				out.Usage = *chunk.Usage
			}
		case ir.ChunkFinish:
			out.FinishReason = chunk.FinishReason
		}
	}

	if text.Len() > 0 {
		out.Blocks = append(out.Blocks, ir.TextBlock(text.String()))
	}
	if thinking.Len() > 0 {
		out.Blocks = append(out.Blocks, ir.Block{
			Type:     ir.BlockThinking,
			Thinking: &ir.ThinkingBlock{Text: thinking.String()},
		})
	}
	if out.FinishReason == "" {
		out.FinishReason = ir.FinishStop
	}
	return out, nil
}

func modelName(in *gateway.InvokeRequest) string {
	if in == nil {
		return ""
	}
	if in.Model.UpstreamName != "" {
		return in.Model.UpstreamName
	}
	return in.Request.Model
}
