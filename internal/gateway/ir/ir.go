// Package ir is the canonical intermediate representation the whole gateway
// speaks.
//
// Every inbound protocol decodes into these types and every adapter encodes from
// them, which is what makes N protocols x M upstreams work with N codecs plus M
// adapters instead of N*M converters.
//
// The package depends on nothing but the standard library and holds no
// mutable state: it is safe to unit test with plain table-driven tests.
package ir

import "fmt"

// Protocol identifies a wire protocol on either side of the gateway.
type Protocol string

const (
	// ProtoOpenAIChat is POST /v1/chat/completions.
	ProtoOpenAIChat Protocol = "openai_chat"
	// ProtoOpenAIResponses is POST /v1/responses.
	ProtoOpenAIResponses Protocol = "openai_responses"
	// ProtoAnthropic is POST /v1/messages.
	ProtoAnthropic Protocol = "anthropic"
	// ProtoVendorNative marks an upstream that speaks its own wire format. It is
	// only ever used as an upstream protocol: no codec handles it, the adapter
	// does.
	ProtoVendorNative Protocol = "vendor_native"
)

// All returns every protocol a downstream client may speak.
func All() []Protocol {
	return []Protocol{ProtoOpenAIChat, ProtoOpenAIResponses, ProtoAnthropic}
}

// Valid reports whether p is a downstream protocol.
func (p Protocol) Valid() bool {
	switch p {
	case ProtoOpenAIChat, ProtoOpenAIResponses, ProtoAnthropic:
		return true
	default:
		return false
	}
}

// ParseProtocol parses a downstream protocol name.
func ParseProtocol(s string) (Protocol, error) {
	p := Protocol(s)
	if !p.Valid() {
		return "", fmt.Errorf("ir: unsupported protocol %q", s)
	}
	return p, nil
}

// Block types.
const (
	// BlockText is a text block.
	BlockText = "text"
	// BlockImage is an image block.
	BlockImage = "image"
	// BlockToolResult is a tool result carried by a user message.
	BlockToolResult = "tool_result"
	// BlockThinking is a reasoning block.
	BlockThinking = "thinking"
)

// Roles. System and developer content is folded into ChatRequest.System.
const (
	// RoleUser is the human turn.
	RoleUser = "user"
	// RoleAssistant is the model turn.
	RoleAssistant = "assistant"
	// RoleTool carries a tool result.
	RoleTool = "tool"
)

// Tool choice modes.
const (
	// ToolChoiceAuto lets the model decide.
	ToolChoiceAuto = "auto"
	// ToolChoiceNone forbids tools.
	ToolChoiceNone = "none"
	// ToolChoiceRequired forces a tool call.
	ToolChoiceRequired = "required"
	// ToolChoiceTool forces one named tool.
	ToolChoiceTool = "tool"
)

// Finish reasons, normalised across protocols.
const (
	// FinishStop is a normal completion.
	FinishStop = "stop"
	// FinishLength hit the token ceiling.
	FinishLength = "length"
	// FinishToolCalls stopped to call tools.
	FinishToolCalls = "tool_calls"
	// FinishContentFilter was filtered.
	FinishContentFilter = "content_filter"
	// FinishError is an upstream-side failure.
	FinishError = "error"
	// FinishUpstreamError is a connection-level failure reported by the vendor.
	FinishUpstreamError = "upstream_error"
)

// Block is one element of message content.
type Block struct {
	Type       string         `json:"type"`
	Text       string         `json:"text,omitempty"`
	Image      *ImageBlock    `json:"image,omitempty"`
	ToolResult *ToolResult    `json:"toolResult,omitempty"`
	Thinking   *ThinkingBlock `json:"thinking,omitempty"`
}

// TextBlock builds a text block.
func TextBlock(text string) Block { return Block{Type: BlockText, Text: text} }

// ImageBlock is image content, as a URL or inline base64.
type ImageBlock struct {
	// URL is an http(s) address.
	URL string `json:"url,omitempty"`
	// MediaType is the MIME type of inline data.
	MediaType string `json:"mediaType,omitempty"`
	// DataB64 is base64-encoded image bytes.
	DataB64 string `json:"dataB64,omitempty"`
}

// ThinkingBlock is reasoning content. Signature is protocol-specific: Anthropic
// requires it, and the Command Code adapter forges one because the upstream
// omits it.
type ThinkingBlock struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

// ToolResult is the result of a tool call, carried on a user/tool turn.
type ToolResult struct {
	ToolUseID string  `json:"toolUseId"`
	Content   []Block `json:"content,omitempty"`
	IsError   bool    `json:"isError,omitempty"`
}

// ToolCall is an assistant-initiated call. Arguments is always a JSON string.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef is a tool definition. Parameters is a JSON Schema object.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ToolChoice is a tool selection.
type ToolChoice struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

// Message is one conversation turn.
type Message struct {
	Role       string     `json:"role"`
	Blocks     []Block    `json:"blocks,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Text concatenates the text blocks of a message.
func (m Message) Text() string {
	var out string
	for _, b := range m.Blocks {
		if b.Type == BlockText {
			out += b.Text
		}
	}
	return out
}

// Usage is token accounting. Estimated marks values the gateway computed because
// the upstream reported none.
type Usage struct {
	PromptTokens     int  `json:"promptTokens"`
	CompletionTokens int  `json:"completionTokens"`
	TotalTokens      int  `json:"totalTokens"`
	CachedTokens     int  `json:"cachedTokens"`
	Estimated        bool `json:"estimated"`
}

// Normalize fills TotalTokens when it is missing.
func (u *Usage) Normalize() {
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
}

// ChatRequest is the canonical request.
type ChatRequest struct {
	Model       string
	System      []Block
	Messages    []Message
	Tools       []ToolDef
	ToolChoice  *ToolChoice
	Temperature *float64
	TopP        *float64
	MaxTokens   *int
	Stop        []string
	Stream      bool
	// Metadata carries vendor extensions that do not fit the model above.
	Metadata map[string]any
	// Warnings records lossy conversions. It is for the log and the test bench,
	// never sent to the client.
	Warnings []string
}

// Warn appends one conversion warning.
func (r *ChatRequest) Warn(msg string) { r.Warnings = append(r.Warnings, msg) }

// SystemText joins the system blocks into one string.
func (r *ChatRequest) SystemText() string {
	var out string
	for i, b := range r.System {
		if b.Type != BlockText {
			continue
		}
		if i > 0 && out != "" {
			out += "\n"
		}
		out += b.Text
	}
	return out
}

// ChatResponse is the canonical non-streaming response.
type ChatResponse struct {
	ID           string
	Model        string
	Blocks       []Block
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	Warnings     []string
}

// Text concatenates the response's text blocks.
func (r *ChatResponse) Text() string {
	var out string
	for _, b := range r.Blocks {
		if b.Type == BlockText {
			out += b.Text
		}
	}
	return out
}

// ChunkKind is the kind of one streaming increment.
type ChunkKind string

const (
	// ChunkStart opens the stream and carries the response id and model.
	ChunkStart ChunkKind = "start"
	// ChunkText is a text delta.
	ChunkText ChunkKind = "text"
	// ChunkThinking is a reasoning delta.
	ChunkThinking ChunkKind = "thinking"
	// ChunkToolCall is a tool call delta, or a complete call when ArgsComplete
	// is set.
	ChunkToolCall ChunkKind = "tool_call"
	// ChunkUsage carries token accounting, usually at the end.
	ChunkUsage ChunkKind = "usage"
	// ChunkFinish carries the finish reason.
	ChunkFinish ChunkKind = "finish"
	// ChunkDone terminates the stream.
	ChunkDone ChunkKind = "done"
)

// StreamChunk is one streaming increment.
type StreamChunk struct {
	Kind ChunkKind
	// ID and Model are set on ChunkStart.
	ID    string
	Model string
	// Index identifies the choice or content block.
	Index int
	// Text is the delta for ChunkText and ChunkThinking.
	Text string
	// ToolCallID, ToolName, ArgsDelta and ArgsComplete describe a tool call.
	// ArgsComplete is used when the upstream delivers the whole argument object
	// at once instead of in fragments.
	ToolCallID   string
	ToolName     string
	ArgsDelta    string
	ArgsComplete string
	// Signature accompanies an Anthropic thinking block.
	Signature string
	// FinishReason is set on ChunkFinish.
	FinishReason string
	// Usage is set on ChunkUsage.
	Usage *Usage
}

// DecodeError means an inbound body did not match the protocol it claimed.
type DecodeError struct {
	Protocol Protocol
	// Reason is a short, client-safe explanation.
	Reason string
	// Param names the offending field, when there is one.
	Param string
	Err   error
}

func (e *DecodeError) Error() string {
	if e.Param != "" {
		return fmt.Sprintf("ir: cannot decode %s body (%s): %v", e.Protocol, e.Param, e.Err)
	}
	return fmt.Sprintf("ir: cannot decode %s body: %s: %v", e.Protocol, e.Reason, e.Err)
}

// Unwrap exposes the underlying error.
func (e *DecodeError) Unwrap() error { return e.Err }

// NormalizeFinish maps a vendor finish reason onto the canonical vocabulary.
// Unknown values are returned lower-cased rather than guessed: a client that sees
// an unfamiliar reason can still tell the answer may be incomplete.
func NormalizeFinish(reason string) string {
	switch reason {
	case "", FinishStop, FinishLength, FinishToolCalls, FinishContentFilter, FinishError, FinishUpstreamError:
		return reason
	case "tool-calls", "tool_use", "tool-use", "function_call":
		return FinishToolCalls
	case "max_tokens", "max_output_tokens", "model_context_window_exceeded":
		return FinishLength
	case "end_turn":
		return FinishStop
	case "refusal":
		return FinishContentFilter
	default:
		return reason
	}
}
