package codec

import (
	"encoding/json"
	"io"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

func init() { register(anthropicCodec{}) }

// anthropicCodec implements ir.ProtoAnthropic.
type anthropicCodec struct{}

func (anthropicCodec) Protocol() ir.Protocol { return ir.ProtoAnthropic }

// anthropicDefaultMaxTokens is used when a request omits max_tokens, which the
// protocol requires. Omitting it upstream would be rejected outright.
const anthropicDefaultMaxTokens = 4096

// ---- request decoding ----

type anthropicRequest struct {
	Model         string             `json:"model"`
	System        json.RawMessage    `json:"system"`
	Messages      []anthropicMessage `json:"messages"`
	MaxTokens     *int               `json:"max_tokens"`
	Temperature   *float64           `json:"temperature"`
	TopP          *float64           `json:"top_p"`
	StopSequences []string           `json:"stop_sequences"`
	Stream        bool               `json:"stream"`
	Tools         []anthropicTool    `json:"tools"`
	ToolChoice    *struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"tool_choice"`
	Thinking json.RawMessage `json:"thinking"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

// DecodeRequest implements Codec.
func (anthropicCodec) DecodeRequest(body []byte) (*ir.ChatRequest, error) {
	var raw anthropicRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, decodeError(ir.ProtoAnthropic, "invalid JSON", err)
	}

	req := &ir.ChatRequest{
		Model:       raw.Model,
		MaxTokens:   raw.MaxTokens,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
		Stop:        raw.StopSequences,
		Stream:      raw.Stream,
		Metadata:    map[string]any{},
	}
	if req.MaxTokens == nil {
		def := anthropicDefaultMaxTokens
		req.MaxTokens = &def
	}
	if raw.Temperature != nil && *raw.Temperature > 1 {
		t := 1.0
		req.Temperature = &t
		req.Warn("temperature above 1 was clamped to 1")
	}
	if len(raw.Thinking) > 0 && string(raw.Thinking) != "null" {
		req.Metadata["thinking"] = json.RawMessage(raw.Thinking)
	}

	if blocks, err := decodeAnthropicContent(raw.System); err != nil {
		return nil, err
	} else {
		req.System = blocks
	}

	for _, m := range raw.Messages {
		role := ir.RoleUser
		if m.Role == "assistant" {
			role = ir.RoleAssistant
		}
		blocks, calls, err := decodeAnthropicBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		msg := ir.Message{Role: role, ToolCalls: calls}
		for _, b := range blocks {
			if b.Type == ir.BlockToolResult {
				// Tool results belong to their own turn in the canonical form.
				req.Messages = append(req.Messages, ir.Message{
					Role:       ir.RoleTool,
					ToolCallID: b.ToolResult.ToolUseID,
					Blocks:     []ir.Block{b},
				})
				continue
			}
			msg.Blocks = append(msg.Blocks, b)
		}
		if len(msg.Blocks) > 0 || len(msg.ToolCalls) > 0 {
			req.Messages = append(req.Messages, msg)
		}
	}

	for _, t := range raw.Tools {
		if t.Name == "" {
			continue
		}
		req.Tools = append(req.Tools, ir.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schemaOf(t.InputSchema),
		})
	}
	if raw.ToolChoice != nil {
		switch raw.ToolChoice.Type {
		case "auto":
			req.ToolChoice = &ir.ToolChoice{Mode: ir.ToolChoiceAuto}
		case "any":
			req.ToolChoice = &ir.ToolChoice{Mode: ir.ToolChoiceRequired}
		case "none":
			req.ToolChoice = &ir.ToolChoice{Mode: ir.ToolChoiceNone}
		case "tool":
			req.ToolChoice = &ir.ToolChoice{Mode: ir.ToolChoiceTool, Name: raw.ToolChoice.Name}
		}
	}
	return req, nil
}

// decodeAnthropicContent parses a content field that may only hold blocks.
func decodeAnthropicContent(raw json.RawMessage) ([]ir.Block, error) {
	blocks, _, err := decodeAnthropicBlocks(raw)
	return blocks, err
}

// decodeAnthropicBlocks parses a content field (string or block array) into
// content blocks and tool calls. Tool calls are lifted out because the canonical
// model keeps them beside the message, not inside it.
func decodeAnthropicBlocks(raw json.RawMessage) ([]ir.Block, []ir.ToolCall, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	if text, ok := rawString(raw); ok {
		if text == "" {
			return nil, nil, nil
		}
		return []ir.Block{ir.TextBlock(text)}, nil, nil
	}
	var parts []anthropicBlock
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, decodeError(ir.ProtoAnthropic, "content blocks", err)
	}
	blocks := make([]ir.Block, 0, len(parts))
	var calls []ir.ToolCall
	for _, p := range parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, ir.TextBlock(p.Text))
		case "thinking":
			blocks = append(blocks, ir.Block{
				Type:     ir.BlockThinking,
				Thinking: &ir.ThinkingBlock{Text: p.Thinking, Signature: p.Signature},
			})
		case "image":
			if p.Source == nil {
				continue
			}
			image := &ir.ImageBlock{MediaType: p.Source.MediaType, DataB64: p.Source.Data}
			if p.Source.URL != "" {
				image = &ir.ImageBlock{URL: p.Source.URL}
			}
			blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: image})
		case "tool_result":
			result := &ir.ToolResult{ToolUseID: p.ToolUseID, IsError: p.IsError}
			if inner, _, err := decodeAnthropicBlocks(p.Content); err == nil {
				result.Content = inner
			}
			blocks = append(blocks, ir.Block{Type: ir.BlockToolResult, ToolResult: result})
		case "tool_use":
			calls = append(calls, ir.ToolCall{
				ID:        p.ID,
				Name:      p.Name,
				Arguments: argumentsString(p.Input),
			})
		default:
			continue
		}
	}
	return blocks, calls, nil
}

// ---- request encoding ----

type anthropicRequestOut struct {
	Model         string             `json:"model"`
	System        any                `json:"system,omitempty"`
	Messages      []anthropicMsgOut  `json:"messages"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream"`
	Tools         []anthropicToolOut `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
	Thinking      any                `json:"thinking,omitempty"`
}

type anthropicMsgOut struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicToolOut struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

// EncodeRequest implements Codec.
func (anthropicCodec) EncodeRequest(req *ir.ChatRequest) ([]byte, error) {
	maxTokens := anthropicDefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	out := anthropicRequestOut{
		Model:         req.Model,
		MaxTokens:     maxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.Stop,
		Stream:        req.Stream,
	}
	if system := textOf(req.System); system != "" {
		out.System = system
	}
	if thinking, ok := req.Metadata["thinking"]; ok {
		out.Thinking = thinking
	}

	for _, m := range req.Messages {
		switch m.Role {
		case ir.RoleAssistant:
			blocks := encodeAnthropicBlocks(m.Blocks, false)
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Name,
					"input": rawArguments(tc.Arguments),
				})
			}
			if len(blocks) == 0 {
				blocks = []map[string]any{{"type": "text", "text": ""}}
			}
			out.Messages = append(out.Messages, anthropicMsgOut{Role: "assistant", Content: blocks})
		case ir.RoleTool:
			block := map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     toolResultText(m.Blocks),
			}
			out.Messages = append(out.Messages, anthropicMsgOut{
				Role:    "user",
				Content: []map[string]any{block},
			})
		default:
			out.Messages = append(out.Messages, anthropicMsgOut{
				Role:    "user",
				Content: encodeAnthropicBlocks(m.Blocks, true),
			})
		}
	}

	for _, t := range req.Tools {
		tool := anthropicToolOut{Name: t.Name, Description: t.Description}
		if t.Parameters != nil {
			tool.InputSchema = t.Parameters
		} else {
			tool.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out.Tools = append(out.Tools, tool)
	}
	if tc := req.ToolChoice; tc != nil {
		switch tc.Mode {
		case ir.ToolChoiceAuto:
			out.ToolChoice = map[string]any{"type": "auto"}
		case ir.ToolChoiceRequired:
			out.ToolChoice = map[string]any{"type": "any"}
		case ir.ToolChoiceNone:
			out.ToolChoice = map[string]any{"type": "none"}
		case ir.ToolChoiceTool:
			out.ToolChoice = map[string]any{"type": "tool", "name": tc.Name}
		}
	}
	return marshal(out)
}

func encodeAnthropicBlocks(blocks []ir.Block, allowImages bool) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			out = append(out, map[string]any{
				"type":      "thinking",
				"thinking":  b.Thinking.Text,
				"signature": b.Thinking.Signature,
			})
		case ir.BlockImage:
			if !allowImages || b.Image == nil {
				continue
			}
			if b.Image.URL != "" {
				out = append(out, map[string]any{
					"type":   "image",
					"source": map[string]any{"type": "url", "url": b.Image.URL},
				})
				continue
			}
			media := b.Image.MediaType
			if media == "" {
				media = "image/png"
			}
			out = append(out, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": media,
					"data":       b.Image.DataB64,
				},
			})
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"type": "text", "text": ""})
	}
	return out
}

// rawArguments renders a tool call's JSON string back into an object.
func rawArguments(args string) any {
	if args == "" {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal([]byte(args), &out); err != nil {
		return map[string]any{}
	}
	return out
}

// ---- response decoding ----

type anthropicResponse struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Role       string           `json:"role"`
	Model      string           `json:"model"`
	Content    []anthropicBlock `json:"content"`
	StopReason *string          `json:"stop_reason"`
	Usage      *anthropicUsage  `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// DecodeResponse implements Codec.
func (anthropicCodec) DecodeResponse(body []byte) (*ir.ChatResponse, error) {
	var raw anthropicResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, decodeError(ir.ProtoAnthropic, "invalid JSON", err)
	}
	resp := &ir.ChatResponse{ID: raw.ID, Model: raw.Model}
	for _, b := range raw.Content {
		switch b.Type {
		case "text":
			resp.Blocks = append(resp.Blocks, ir.TextBlock(b.Text))
		case "thinking":
			resp.Blocks = append(resp.Blocks, ir.Block{
				Type:     ir.BlockThinking,
				Thinking: &ir.ThinkingBlock{Text: b.Thinking, Signature: b.Signature},
			})
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ir.ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: argumentsString(b.Input),
			})
		}
	}
	if raw.StopReason != nil {
		resp.FinishReason = stopReasonToFinish(*raw.StopReason)
	}
	if raw.Usage != nil {
		resp.Usage = anthropicUsageToIR(raw.Usage)
	}
	return resp, nil
}

// anthropicUsageToIR rebuilds the total input from the non-cached part plus the
// cache counters, which is the reverse of what EncodeResponse does.
func anthropicUsageToIR(u *anthropicUsage) ir.Usage {
	cached := u.CacheReadInputTokens + u.CacheCreationInputTokens
	out := ir.Usage{
		PromptTokens:     u.InputTokens + cached,
		CompletionTokens: u.OutputTokens,
		CachedTokens:     cached,
	}
	out.Normalize()
	return out
}

// ---- response encoding ----

// EncodeResponse implements Codec.
func (anthropicCodec) EncodeResponse(resp *ir.ChatResponse) ([]byte, error) {
	content := make([]map[string]any, 0, len(resp.Blocks)+len(resp.ToolCalls))
	for _, b := range resp.Blocks {
		switch b.Type {
		case ir.BlockText:
			content = append(content, map[string]any{"type": "text", "text": b.Text})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			content = append(content, map[string]any{
				"type":      "thinking",
				"thinking":  b.Thinking.Text,
				"signature": b.Thinking.Signature,
			})
		}
	}
	for _, tc := range resp.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": rawArguments(tc.Arguments),
		})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}

	usage := resp.Usage
	usage.Normalize()
	// Anthropic's input_tokens is the non-cached share only.
	uncached := usage.PromptTokens - usage.CachedTokens
	if uncached < 0 {
		uncached = 0
	}

	body := map[string]any{
		"id":          firstNonEmpty(resp.ID, "msg_"+randomSuffix()),
		"type":        "message",
		"role":        "assistant",
		"model":       resp.Model,
		"content":     content,
		"stop_reason": finishToStopReason(resp.FinishReason),
		"usage": map[string]any{
			"input_tokens":                uncached,
			"output_tokens":               usage.CompletionTokens,
			"cache_read_input_tokens":     usage.CachedTokens,
			"cache_creation_input_tokens": 0,
		},
	}
	return marshal(body)
}

// ---- streaming ----

// NewStreamDecoder implements Codec.
func (anthropicCodec) NewStreamDecoder(r io.Reader) StreamDecoder {
	return &anthropicStreamDecoder{reader: newSSEReader(r)}
}

type anthropicStreamDecoder struct {
	reader       *sseReader
	done         bool
	id           string
	model        string
	inputTokens  int
	cacheRead    int
	cacheWrite   int
	outputTokens int
	finished     bool
}

// Next implements StreamDecoder.
func (d *anthropicStreamDecoder) Next() (*ir.StreamChunk, error) {
	for {
		if d.done {
			return nil, io.EOF
		}
		event, err := d.reader.Next()
		if err != nil {
			if err == io.EOF && !d.done {
				return nil, &streamCodecError{message: "message stream ended before message_stop"}
			}
			return nil, err
		}
		if event.isDone() {
			d.done = true
			return &ir.StreamChunk{Kind: ir.ChunkDone}, nil
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
			continue
		}
		eventType, _ := payload["type"].(string)
		if eventType == "" {
			eventType = event.Event
		}

		switch eventType {
		case "message_start":
			if message, ok := payload["message"].(map[string]any); ok {
				d.id, _ = message["id"].(string)
				d.model, _ = message["model"].(string)
				if usage, ok := message["usage"].(map[string]any); ok {
					d.inputTokens = intOf(usage["input_tokens"])
					d.cacheRead = intOf(usage["cache_read_input_tokens"])
					d.cacheWrite = intOf(usage["cache_creation_input_tokens"])
				}
			}
			return &ir.StreamChunk{Kind: ir.ChunkStart, ID: d.id, Model: d.model}, nil
		case "content_block_start":
			if block, ok := payload["content_block"].(map[string]any); ok {
				blockType, _ := block["type"].(string)
				if blockType == "tool_use" {
					index := intOf(payload["index"])
					return &ir.StreamChunk{
						Kind:       ir.ChunkToolCall,
						Index:      index,
						ToolCallID: stringOf(block["id"]),
						ToolName:   stringOf(block["name"]),
					}, nil
				}
			}
		case "content_block_delta":
			delta, ok := payload["delta"].(map[string]any)
			if !ok {
				continue
			}
			index := intOf(payload["index"])
			switch stringOf(delta["type"]) {
			case "text_delta":
				if text := stringOf(delta["text"]); text != "" {
					return &ir.StreamChunk{Kind: ir.ChunkText, Text: text}, nil
				}
			case "thinking_delta":
				if text := stringOf(delta["thinking"]); text != "" {
					return &ir.StreamChunk{Kind: ir.ChunkThinking, Text: text}, nil
				}
			case "input_json_delta":
				return &ir.StreamChunk{
					Kind:      ir.ChunkToolCall,
					Index:     index,
					ArgsDelta: stringOf(delta["partial_json"]),
				}, nil
			}
		case "message_delta":
			if usage, ok := payload["usage"].(map[string]any); ok {
				d.inputTokens = maxInt(d.inputTokens, intOf(usage["input_tokens"]))
				d.cacheRead = maxInt(d.cacheRead, intOf(usage["cache_read_input_tokens"]))
				d.outputTokens = maxInt(d.outputTokens, intOf(usage["output_tokens"]))
			}
			if d.finished {
				continue
			}
			d.finished = true
			reason := ""
			if delta, ok := payload["delta"].(map[string]any); ok {
				reason = stopReasonToFinish(stringOf(delta["stop_reason"]))
			}
			return &ir.StreamChunk{
				Kind:         ir.ChunkFinish,
				FinishReason: reason,
				Usage:        d.usage(),
			}, nil
		case "message_stop":
			d.done = true
			return &ir.StreamChunk{Kind: ir.ChunkDone}, nil
		case "error":
			message := "upstream error"
			if errObj, ok := payload["error"].(map[string]any); ok {
				if m := stringOf(errObj["message"]); m != "" {
					message = m
				}
			}
			return nil, &streamCodecError{message: message}
		}
	}
}

func (d *anthropicStreamDecoder) usage() *ir.Usage {
	cached := d.cacheRead + d.cacheWrite
	usage := &ir.Usage{
		PromptTokens:     d.inputTokens + cached,
		CompletionTokens: d.outputTokens,
		CachedTokens:     cached,
	}
	usage.Normalize()
	return usage
}

type streamCodecError struct{ message string }

func (e *streamCodecError) Error() string { return "codec: " + e.message }

func intOf(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// NewStreamEncoder implements Codec.
func (anthropicCodec) NewStreamEncoder(w io.Writer) StreamEncoder {
	return &anthropicStreamEncoder{
		writer: newSSEWriter(w),
		index:  -1,
	}
}

type anthropicStreamEncoder struct {
	writer      *sseWriter
	id          string
	model       string
	started     bool
	index       int
	openBlock   bool
	currentKind string
	usage       *ir.Usage
	closed      bool
}

// Write implements StreamEncoder.
func (e *anthropicStreamEncoder) Write(chunk *ir.StreamChunk) error {
	switch chunk.Kind {
	case ir.ChunkStart:
		e.id = firstNonEmpty(chunk.ID, "msg_"+randomSuffix())
		e.model = chunk.Model
		e.started = true
		return e.event("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":      e.id,
				"type":    "message",
				"role":    "assistant",
				"model":   e.model,
				"content": []any{},
				"usage":   map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	case ir.ChunkText:
		if err := e.ensureBlock("text"); err != nil {
			return err
		}
		return e.event("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.index,
			"delta": map[string]any{"type": "text_delta", "text": chunk.Text},
		})
	case ir.ChunkThinking:
		if err := e.ensureBlock("thinking"); err != nil {
			return err
		}
		return e.event("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.index,
			"delta": map[string]any{"type": "thinking_delta", "thinking": chunk.Text},
		})
	case ir.ChunkToolCall:
		// Tool calls always get their own content block, even after text.
		if e.currentKind != "tool" {
			if err := e.closeBlock(); err != nil {
				return err
			}
			e.index++
			e.openBlock = true
			e.currentKind = "tool"
			if err := e.event("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": e.index,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    chunk.ToolCallID,
					"name":  chunk.ToolName,
					"input": map[string]any{},
				},
			}); err != nil {
				return err
			}
		}
		delta := firstNonEmpty(chunk.ArgsDelta, chunk.ArgsComplete)
		if delta == "" {
			return nil
		}
		return e.event("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": e.index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": delta},
		})
	case ir.ChunkUsage:
		e.usage = chunk.Usage
		return nil
	case ir.ChunkFinish:
		if err := e.closeBlock(); err != nil {
			return err
		}
		payload := map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": finishToStopReason(chunk.FinishReason), "stop_sequence": nil},
		}
		if e.usage != nil {
			usage := *e.usage
			usage.Normalize()
			uncached := usage.PromptTokens - usage.CachedTokens
			if uncached < 0 {
				uncached = 0
			}
			payload["usage"] = map[string]any{
				"input_tokens":                uncached,
				"output_tokens":               usage.CompletionTokens,
				"cache_read_input_tokens":     usage.CachedTokens,
				"cache_creation_input_tokens": 0,
			}
		}
		return e.event("message_delta", payload)
	case ir.ChunkDone:
		return e.stop()
	}
	return nil
}

// Close implements StreamEncoder.
func (e *anthropicStreamEncoder) Close() error {
	if e.closed {
		return nil
	}
	return e.stop()
}

// Abort implements StreamEncoder. A failed stream must not end with
// message_stop: clients read it as "the answer is complete".
func (e *anthropicStreamEncoder) Abort() {
	e.closed = true
}

func (e *anthropicStreamEncoder) stop() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if !e.started {
		// A stream that closed before starting still needs a valid terminator.
		return e.event("message_stop", map[string]any{"type": "message_stop"})
	}
	return e.event("message_stop", map[string]any{"type": "message_stop"})
}

func (e *anthropicStreamEncoder) ensureBlock(kind string) error {
	if e.openBlock && e.currentKind == kind {
		return nil
	}
	if err := e.closeBlock(); err != nil {
		return err
	}
	e.index++
	e.openBlock = true
	e.currentKind = kind
	block := map[string]any{"type": kind}
	if kind == "thinking" {
		block["thinking"] = ""
		block["signature"] = ""
	} else {
		block["text"] = ""
	}
	return e.event("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         e.index,
		"content_block": block,
	})
}

func (e *anthropicStreamEncoder) closeBlock() error {
	if !e.openBlock {
		return nil
	}
	e.openBlock = false
	e.currentKind = ""
	return e.event("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": e.index,
	})
}

func (e *anthropicStreamEncoder) event(name string, payload map[string]any) error {
	raw, err := marshal(payload)
	if err != nil {
		return err
	}
	return e.writer.Event(name, string(raw))
}

var _ = time.Now
