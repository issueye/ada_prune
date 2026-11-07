// Package codec converts between the canonical IR and the three protocols the
// gateway speaks downstream.
//
// Each protocol implements exactly one direction of the pair "this protocol ↔
// IR". Three codecs therefore cover all nine combinations (three passthroughs and
// six cross-protocol conversions). Adding a protocol means adding one file, not
// six converters.
//
// A codec is a pure function set: no network, no files, no global state, safe to
// exercise with table-driven tests.
package codec

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

// Codec converts one protocol to and from the canonical IR.
type Codec interface {
	// Protocol reports which protocol this codec speaks.
	Protocol() ir.Protocol
	// DecodeRequest parses a client request body into the canonical request.
	DecodeRequest(body []byte) (*ir.ChatRequest, error)
	// EncodeRequest renders a canonical request as this protocol's request body.
	EncodeRequest(req *ir.ChatRequest) ([]byte, error)
	// DecodeResponse parses an upstream non-streaming body.
	DecodeResponse(body []byte) (*ir.ChatResponse, error)
	// EncodeResponse renders a canonical response for this protocol.
	EncodeResponse(resp *ir.ChatResponse) ([]byte, error)
	// NewStreamDecoder reads this protocol's streaming body.
	NewStreamDecoder(r io.Reader) StreamDecoder
	// NewStreamEncoder writes this protocol's streaming body.
	NewStreamEncoder(w io.Writer) StreamEncoder
}

// StreamDecoder yields canonical chunks. io.EOF ends a complete stream.
type StreamDecoder interface {
	Next() (*ir.StreamChunk, error)
}

// StreamEncoder writes canonical chunks as this protocol's events. Close writes
// the protocol's terminator.
type StreamEncoder interface {
	Write(chunk *ir.StreamChunk) error
	Close() error
}

var (
	registryMu sync.RWMutex
	registry   = map[ir.Protocol]Codec{}
)

// register is called from each codec's init.
func register(c Codec) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[c.Protocol()] = c
}

// Get returns the codec for a protocol.
func Get(p ir.Protocol) (Codec, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[p]
	if !ok {
		return nil, fmt.Errorf("codec: no codec registered for protocol %q", p)
	}
	return c, nil
}

// MustGet panics when the protocol is unknown. Tests and startup paths use it.
func MustGet(p ir.Protocol) Codec {
	c, err := Get(p)
	if err != nil {
		panic(err)
	}
	return c
}

// Registered returns every registered protocol, sorted for stable assertions.
func Registered() []ir.Protocol {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ir.Protocol, 0, len(registry))
	for p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ConvertRequest re-encodes a request body from one protocol to another.
func ConvertRequest(from, to ir.Protocol, body []byte) ([]byte, error) {
	src, err := Get(from)
	if err != nil {
		return nil, err
	}
	dst, err := Get(to)
	if err != nil {
		return nil, err
	}
	req, err := src.DecodeRequest(body)
	if err != nil {
		return nil, err
	}
	return dst.EncodeRequest(req)
}

// ConvertResponse re-encodes a non-streaming response body.
func ConvertResponse(from, to ir.Protocol, body []byte) ([]byte, error) {
	src, err := Get(from)
	if err != nil {
		return nil, err
	}
	dst, err := Get(to)
	if err != nil {
		return nil, err
	}
	resp, err := src.DecodeResponse(body)
	if err != nil {
		return nil, err
	}
	return dst.EncodeResponse(resp)
}

// ---- shared helpers ----

// decodeError wraps a parse failure with the protocol that failed.
func decodeError(p ir.Protocol, reason string, err error) error {
	return &ir.DecodeError{Protocol: p, Reason: reason, Err: err}
}

// rawString reads a JSON value that is either a string or an array of strings.
func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

// rawStrings normalises "a" / ["a","b"] / null into a slice.
func rawStrings(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil
		}
		return []string{single}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	return nil
}

// marshal encodes without HTML escaping, so tool arguments keep their content.
func marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// stopJSON renders the stop sequences for protocols that accept a string or an
// array.
func stopJSON(stop []string) any {
	switch len(stop) {
	case 0:
		return nil
	case 1:
		return stop[0]
	default:
		return stop
	}
}

// finishToStopReason maps canonical reasons onto Anthropic's vocabulary.
func finishToStopReason(reason string) string {
	if reason == "" {
		return "end_turn"
	}
	switch reason {
	case ir.FinishToolCalls:
		return "tool_use"
	case ir.FinishLength:
		return "max_tokens"
	case ir.FinishContentFilter:
		return "refusal"
	case ir.FinishStop:
		return "end_turn"
	default:
		return reason
	}
}

// stopReasonToFinish maps Anthropic's vocabulary onto canonical reasons.
func stopReasonToFinish(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return ir.FinishStop
	case "max_tokens":
		return ir.FinishLength
	case "tool_use":
		return ir.FinishToolCalls
	case "refusal":
		return ir.FinishContentFilter
	case "":
		return ""
	default:
		return reason
	}
}

// textOf concatenates the text blocks of a slice.
func textOf(blocks []ir.Block) string {
	var out string
	for _, b := range blocks {
		if b.Type == ir.BlockText {
			out += b.Text
		}
	}
	return out
}
