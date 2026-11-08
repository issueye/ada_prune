package codec

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

func init() { register(openAIChatCodec{}) }

// openAIChatCodec implements ir.ProtoOpenAIChat.
type openAIChatCodec struct{}

func (openAIChatCodec) Protocol() ir.Protocol { return ir.ProtoOpenAIChat }

// ---- request decoding ----

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	MaxTokens           *int            `json:"max_tokens"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	Temperature         *float64        `json:"temperature"`
	TopP                *float64        `json:"top_p"`
	Stop                json.RawMessage `json:"stop"`
	Stream              bool            `json:"stream"`
	Tools               []chatTool      `json:"tools"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls"`
	ReasoningEffort     *string         `json:"reasoning_effort"`
	PromptCacheKey      string          `json:"prompt_cache_key"`
	ResponseFormat      json.RawMessage `json:"response_format"`
}

type chatMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	ToolCalls        []chatToolCall  `json:"tool_calls"`
	ToolCallID       string          `json:"tool_call_id"`
	Name             string          `json:"name"`
}

type chatToolCall struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Index identifies the call a streaming delta belongs to; absent in
	// non-streaming messages.
	Index    int `json:"index"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string        `json:"type"`
	Name     string        `json:"name"`
	Function *chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatContentPart struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	ImageURL     *chatImageURL   `json:"image_url"`
	CacheControl json.RawMessage `json:"cache_control"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

// DecodeRequest implements Codec.
func (openAIChatCodec) DecodeRequest(body []byte) (*ir.ChatRequest, error) {
	var raw chatRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, decodeError(ir.ProtoOpenAIChat, "invalid JSON", err)
	}

	req := &ir.ChatRequest{
		Model:       raw.Model,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
		Stop:        rawStrings(raw.Stop),
		Stream:      raw.Stream,
		Metadata:    map[string]any{},
	}
	if raw.MaxTokens != nil {
		req.MaxTokens = raw.MaxTokens
	} else if raw.MaxCompletionTokens != nil {
		req.MaxTokens = raw.MaxCompletionTokens
	}
	if raw.PromptCacheKey != "" {
		req.Metadata["prompt_cache_key"] = raw.PromptCacheKey
	}
	if raw.ParallelToolCalls != nil {
		req.Metadata["parallel_tool_calls"] = *raw.ParallelToolCalls
	}
	if raw.ReasoningEffort != nil && *raw.ReasoningEffort != "" {
		req.Metadata["reasoning_effort"] = *raw.ReasoningEffort
	}
	if len(raw.ResponseFormat) > 0 && string(raw.ResponseFormat) != "null" {
		req.Metadata["response_format"] = json.RawMessage(raw.ResponseFormat)
	}

	for _, m := range raw.Messages {
		switch m.Role {
		case "system", "developer":
			blocks, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.System = append(req.System, blocks...)
		case "assistant":
			msg := ir.Message{Role: ir.RoleAssistant}
			blocks, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			msg.Blocks = blocks
			if m.ReasoningContent != "" {
				msg.Blocks = append(msg.Blocks, ir.Block{
					Type:     ir.BlockThinking,
					Thinking: &ir.ThinkingBlock{Text: m.ReasoningContent},
				})
			}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, ir.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: argumentsString(tc.Function.Arguments),
				})
			}
			req.Messages = append(req.Messages, msg)
		case "tool":
			blocks, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, ir.Message{
				Role:       ir.RoleTool,
				ToolCallID: m.ToolCallID,
				Name:       m.Name,
				Blocks:     blocks,
			})
		default:
			blocks, err := decodeChatContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, ir.Message{Role: ir.RoleUser, Blocks: blocks})
		}
	}

	for _, t := range raw.Tools {
		def := ir.ToolDef{Name: t.Name}
		if t.Function != nil {
			def.Name = t.Function.Name
			def.Description = t.Function.Description
			def.Parameters = schemaOf(t.Function.Parameters)
		}
		if def.Name == "" {
			req.Warn("a tool definition had no name and was dropped")
			continue
		}
		req.Tools = append(req.Tools, def)
	}
	req.ToolChoice = decodeChatToolChoice(raw.ToolChoice)
	return req, nil
}

// decodeChatContent turns a content field (string or parts) into blocks.
func decodeChatContent(raw json.RawMessage) ([]ir.Block, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if text, ok := rawString(raw); ok {
		if text == "" {
			return nil, nil
		}
		return []ir.Block{ir.TextBlock(text)}, nil
	}
	var parts []chatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, decodeError(ir.ProtoOpenAIChat, "message content", err)
	}
	blocks := make([]ir.Block, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text", "input_text":
			blocks = append(blocks, ir.TextBlock(p.Text))
		case "image_url", "input_image":
			if p.ImageURL == nil || p.ImageURL.URL == "" {
				continue
			}
			blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: imageFromURL(p.ImageURL.URL)})
		default:
			// Unknown part types are skipped rather than failing the request.
			continue
		}
	}
	return blocks, nil
}

// imageFromURL splits a data URI into inline bytes, otherwise keeps the URL.
func imageFromURL(url string) *ir.ImageBlock {
	if media, data, ok := splitDataURI(url); ok {
		return &ir.ImageBlock{MediaType: media, DataB64: data}
	}
	return &ir.ImageBlock{URL: url}
}

func decodeChatToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if mode, ok := rawString(raw); ok {
		switch mode {
		case ir.ToolChoiceAuto, ir.ToolChoiceNone, ir.ToolChoiceRequired:
			return &ir.ToolChoice{Mode: mode}
		}
		return nil
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	if choice.Type == "function" {
		name := choice.Function.Name
		if name == "" {
			name = choice.Name
		}
		if name != "" {
			return &ir.ToolChoice{Mode: ir.ToolChoiceTool, Name: name}
		}
	}
	if choice.Type == "" {
		return nil
	}
	return &ir.ToolChoice{Mode: choice.Type}
}

// ---- request encoding ----

type chatRequestOut struct {
	Model             string            `json:"model"`
	Messages          []chatMessageOut  `json:"messages"`
	MaxTokens         *int              `json:"max_tokens,omitempty"`
	Temperature       *float64          `json:"temperature,omitempty"`
	TopP              *float64          `json:"top_p,omitempty"`
	Stop              any               `json:"stop,omitempty"`
	Stream            bool              `json:"stream"`
	Tools             []chatToolOut     `json:"tools,omitempty"`
	ToolChoice        any               `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool             `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort   string            `json:"reasoning_effort,omitempty"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitempty"`
	StreamOptions     *chatStreamOption `json:"stream_options,omitempty"`
}

type chatStreamOption struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessageOut struct {
	Role             string            `json:"role"`
	Content          any               `json:"content"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCallOut `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	Name             string            `json:"name,omitempty"`
}

type chatToolCallOut struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatToolOut struct {
	Type     string          `json:"type"`
	Function chatFunctionOut `json:"function"`
}

type chatFunctionOut struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

// EncodeRequest implements Codec.
func (openAIChatCodec) EncodeRequest(req *ir.ChatRequest) ([]byte, error) {
	out := chatRequestOut{
		Model:           req.Model,
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Stop:            stopJSON(req.Stop),
		Stream:          req.Stream,
		ReasoningEffort: metadataString(req.Metadata, "reasoning_effort"),
		PromptCacheKey:  metadataString(req.Metadata, "prompt_cache_key"),
	}
	if out.Stream {
		out.StreamOptions = &chatStreamOption{IncludeUsage: true}
	}
	if parallel, ok := req.Metadata["parallel_tool_calls"].(bool); ok {
		out.ParallelToolCalls = &parallel
	}

	if system := textOf(req.System); system != "" {
		out.Messages = append(out.Messages, chatMessageOut{Role: "system", Content: system})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case ir.RoleAssistant:
			msg := chatMessageOut{Role: "assistant"}
			msg.Content = encodeChatContent(m.Blocks, false)
			msg.ReasoningContent = thinkingText(m.Blocks)
			for _, tc := range m.ToolCalls {
				call := chatToolCallOut{ID: tc.ID, Type: "function"}
				call.Function.Name = tc.Name
				call.Function.Arguments = argumentsOrEmpty(tc.Arguments)
				msg.ToolCalls = append(msg.ToolCalls, call)
			}
			out.Messages = append(out.Messages, msg)
		case ir.RoleTool:
			msg := chatMessageOut{
				Role:       "tool",
				ToolCallID: m.ToolCallID,
				Name:       m.Name,
				Content:    toolResultText(m.Blocks),
			}
			out.Messages = append(out.Messages, msg)
		default:
			out.Messages = append(out.Messages, chatMessageOut{
				Role:    "user",
				Content: encodeChatContent(m.Blocks, true),
			})
		}
	}

	for _, t := range req.Tools {
		tool := chatToolOut{Type: "function"}
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		if t.Parameters != nil {
			tool.Function.Parameters = t.Parameters
		} else {
			tool.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out.Tools = append(out.Tools, tool)
	}
	if tc := encodeChatToolChoice(req.ToolChoice); tc != nil {
		out.ToolChoice = tc
	}
	return marshal(out)
}

// encodeChatContent renders blocks as a string when possible, else as parts.
func encodeChatContent(blocks []ir.Block, allowImages bool) any {
	var parts []map[string]any
	var text string
	simple := true
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			text += b.Text
		case ir.BlockImage:
			if !allowImages || b.Image == nil {
				continue
			}
			simple = false
			url := b.Image.URL
			if url == "" && b.Image.DataB64 != "" {
				media := b.Image.MediaType
				if media == "" {
					media = "image/png"
				}
				url = "data:" + media + ";base64," + b.Image.DataB64
			}
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": url},
			})
		}
	}
	if simple {
		return text
	}
	out := make([]map[string]any, 0, len(parts)+1)
	if text != "" {
		out = append(out, map[string]any{"type": "text", "text": text})
	}
	out = append(out, parts...)
	return out
}

func encodeChatToolChoice(tc *ir.ToolChoice) any {
	if tc == nil {
		return nil
	}
	switch tc.Mode {
	case ir.ToolChoiceAuto, ir.ToolChoiceNone, ir.ToolChoiceRequired:
		return tc.Mode
	case ir.ToolChoiceTool:
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	default:
		return nil
	}
}

// ---- response decoding ----

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      chatMessage `json:"message"`
		Delta        chatMessage `json:"delta"`
		FinishReason *string     `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

type chatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// DecodeResponse implements Codec.
func (openAIChatCodec) DecodeResponse(body []byte) (*ir.ChatResponse, error) {
	var raw chatResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, decodeError(ir.ProtoOpenAIChat, "invalid JSON", err)
	}
	resp := &ir.ChatResponse{ID: raw.ID, Model: raw.Model}
	if len(raw.Choices) > 0 {
		choice := raw.Choices[0]
		blocks, err := decodeChatContent(choice.Message.Content)
		if err != nil {
			return nil, err
		}
		resp.Blocks = blocks
		if choice.Message.ReasoningContent != "" {
			resp.Blocks = append(resp.Blocks, ir.Block{
				Type:     ir.BlockThinking,
				Thinking: &ir.ThinkingBlock{Text: choice.Message.ReasoningContent},
			})
		}
		for _, tc := range choice.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ir.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: argumentsString(tc.Function.Arguments),
			})
		}
		if choice.FinishReason != nil {
			resp.FinishReason = ir.NormalizeFinish(*choice.FinishReason)
		}
	}
	if raw.Usage != nil {
		resp.Usage = ir.Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		}
		if raw.Usage.PromptTokensDetails != nil {
			resp.Usage.CachedTokens = raw.Usage.PromptTokensDetails.CachedTokens
		}
		resp.Usage.Normalize()
	}
	return resp, nil
}

// ---- response encoding ----

// EncodeResponse implements Codec.
func (openAIChatCodec) EncodeResponse(resp *ir.ChatResponse) ([]byte, error) {
	message := map[string]any{"role": "assistant"}
	text := textOf(resp.Blocks)
	if text != "" {
		message["content"] = text
	} else {
		message["content"] = nil
	}
	if thinking := thinkingText(resp.Blocks); thinking != "" {
		message["reasoning_content"] = thinking
	}
	if len(resp.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			calls = append(calls, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": argumentsOrEmpty(tc.Arguments),
				},
			})
		}
		message["tool_calls"] = calls
	}

	usage := resp.Usage
	usage.Normalize()
	body := map[string]any{
		"id":      firstNonEmpty(resp.ID, "chatcmpl-"+randomSuffix()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": openAIFinish(resp.FinishReason),
		}},
		"usage": map[string]any{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": usage.CachedTokens,
			},
		},
	}
	return marshal(body)
}

func openAIFinish(reason string) string {
	switch reason {
	case "", ir.FinishStop:
		return "stop"
	case ir.FinishToolCalls:
		return "tool_calls"
	case ir.FinishLength:
		return "length"
	case ir.FinishContentFilter:
		return "content_filter"
	default:
		return reason
	}
}

// ---- streaming ----

// chatStreamChunk is one SSE payload of a chat completion stream.
type chatStreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Delta        chatMessage `json:"delta"`
		FinishReason *string     `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage,omitempty"`
}

// NewStreamDecoder implements Codec.
func (openAIChatCodec) NewStreamDecoder(r io.Reader) StreamDecoder {
	return &chatStreamDecoder{reader: newSSEReader(r)}
}

type chatStreamDecoder struct {
	reader  *sseReader
	done    bool
	started bool
	model   string
	id      string
	// queue lets one SSE payload expand into a start chunk plus its content.
	queue []*ir.StreamChunk
}

// Next implements StreamDecoder.
func (d *chatStreamDecoder) Next() (*ir.StreamChunk, error) {
	for {
		if len(d.queue) > 0 {
			chunk := d.queue[0]
			d.queue = d.queue[1:]
			return chunk, nil
		}
		if d.done {
			return nil, io.EOF
		}
		event, err := d.reader.Next()
		if err != nil {
			return nil, err
		}
		if event.isDone() {
			d.done = true
			return &ir.StreamChunk{Kind: ir.ChunkDone}, nil
		}
		var raw chatStreamChunk
		if err := json.Unmarshal([]byte(event.Data), &raw); err != nil {
			continue
		}
		if d.id == "" && raw.ID != "" {
			d.id = raw.ID
			d.model = raw.Model
		}
		if !d.started {
			d.started = true
			d.queue = append(d.queue, &ir.StreamChunk{Kind: ir.ChunkStart, ID: d.id, Model: d.model})
		}
		if raw.Usage != nil {
			usage := ir.Usage{
				PromptTokens:     raw.Usage.PromptTokens,
				CompletionTokens: raw.Usage.CompletionTokens,
				TotalTokens:      raw.Usage.TotalTokens,
			}
			if raw.Usage.PromptTokensDetails != nil {
				usage.CachedTokens = raw.Usage.PromptTokensDetails.CachedTokens
			}
			usage.Normalize()
			d.queue = append(d.queue, &ir.StreamChunk{Kind: ir.ChunkUsage, Usage: &usage})
			// No continue: compatible gateways fold the finish reason into the
			// usage frame, so the choices below must still be examined.
		}
		if len(raw.Choices) == 0 {
			continue
		}
		choice := raw.Choices[0]
		if text, ok := rawString(choice.Delta.Content); ok && text != "" {
			d.queue = append(d.queue, &ir.StreamChunk{Kind: ir.ChunkText, Text: text})
		}
		if choice.Delta.ReasoningContent != "" {
			d.queue = append(d.queue, &ir.StreamChunk{Kind: ir.ChunkThinking, Text: choice.Delta.ReasoningContent})
		}
		if len(choice.Delta.ToolCalls) > 0 {
			for _, tc := range choice.Delta.ToolCalls {
				d.queue = append(d.queue, &ir.StreamChunk{
					Kind: ir.ChunkToolCall,
					// The tool call's own index, not the choice's: clients
					// reassemble argument fragments per call, and a delta may
					// carry fragments of several parallel calls.
					Index:      tc.Index,
					ToolCallID: tc.ID,
					ToolName:   tc.Function.Name,
					ArgsDelta:  argumentsString(tc.Function.Arguments),
				})
			}
		}
		// A finish reason may share a frame with the last delta, so it is queued
		// after the content instead of replacing it.
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			d.queue = append(d.queue, &ir.StreamChunk{
				Kind:         ir.ChunkFinish,
				FinishReason: ir.NormalizeFinish(*choice.FinishReason),
			})
		}
	}
}

// NewStreamEncoder implements Codec.
func (openAIChatCodec) NewStreamEncoder(w io.Writer) StreamEncoder {
	return &chatStreamEncoder{
		writer:      newSSEWriter(w),
		created:     time.Now().Unix(),
		toolByIndex: map[int]int{},
		toolByID:    map[string]int{},
	}
}

type chatStreamEncoder struct {
	writer   *sseWriter
	id       string
	model    string
	created  int64
	usage    *ir.Usage
	started  bool
	finished bool
	closed   bool
	// toolIdx is the next wire index to hand out. Tool calls stream as many
	// fragments that clients reassemble by the tool_calls array index, so the
	// maps below give every fragment of one call the same slot: keyed by
	// upstream call id when one is present, else by the chunk's tool-call
	// index. Slots are allocated per call, never per chunk.
	toolIdx     int
	toolByIndex map[int]int
	toolByID    map[string]int
}

// Write implements StreamEncoder.
func (e *chatStreamEncoder) Write(chunk *ir.StreamChunk) error {
	switch chunk.Kind {
	case ir.ChunkStart:
		e.id = firstNonEmpty(chunk.ID, "chatcmpl-"+randomSuffix())
		e.model = chunk.Model
		e.started = true
		return e.emit(map[string]any{"role": "assistant"}, nil)
	case ir.ChunkText:
		return e.emit(map[string]any{"content": chunk.Text}, nil)
	case ir.ChunkThinking:
		return e.emit(map[string]any{"reasoning_content": chunk.Text}, nil)
	case ir.ChunkToolCall:
		if chunk.ToolCallID == "" && chunk.ToolName == "" &&
			chunk.ArgsDelta == "" && chunk.ArgsComplete == "" {
			// A frame that names neither a call nor any arguments has nothing
			// a client could reassemble; emitting it would open a phantom call.
			return nil
		}
		index := e.toolSlotFor(chunk)
		call := map[string]any{
			"index": index,
			"type":  "function",
			"function": map[string]any{
				"arguments": firstNonEmpty(chunk.ArgsDelta, chunk.ArgsComplete),
			},
		}
		if chunk.ToolCallID != "" {
			call["id"] = chunk.ToolCallID
		}
		if chunk.ToolName != "" {
			call["function"].(map[string]any)["name"] = chunk.ToolName
		}
		return e.emit(map[string]any{"tool_calls": []any{call}}, nil)
	case ir.ChunkUsage:
		e.usage = chunk.Usage
		return nil
	case ir.ChunkFinish:
		e.finished = true
		return e.emit(map[string]any{}, strPtr(openAIFinish(chunk.FinishReason)))
	case ir.ChunkDone:
		if e.closed {
			return nil
		}
		if e.usage != nil {
			if err := e.emitUsage(); err != nil {
				return err
			}
		}
		e.closed = true
		return e.writer.Data("[DONE]")
	default:
		return nil
	}
}

// toolSlotFor maps one IR tool-call chunk onto a stable wire index. A call id
// the encoder has already seen continues that call; an unseen call id opens a
// new slot even when it reuses an index another call claimed (decoders that
// cannot tell parallel calls apart leave the index at zero). A fragment
// without an id joins whichever call claimed its index.
func (e *chatStreamEncoder) toolSlotFor(chunk *ir.StreamChunk) int {
	if chunk.ToolCallID != "" {
		if slot, ok := e.toolByID[chunk.ToolCallID]; ok {
			return slot
		}
		slot := e.toolIdx
		e.toolIdx++
		e.toolByID[chunk.ToolCallID] = slot
		if _, claimed := e.toolByIndex[chunk.Index]; !claimed {
			e.toolByIndex[chunk.Index] = slot
		}
		return slot
	}
	if slot, ok := e.toolByIndex[chunk.Index]; ok {
		return slot
	}
	slot := e.toolIdx
	e.toolIdx++
	e.toolByIndex[chunk.Index] = slot
	return slot
}

// Close implements StreamEncoder. It is idempotent: a Done chunk already wrote
// the terminator.
func (e *chatStreamEncoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.writer.Data("[DONE]")
}

func (e *chatStreamEncoder) emit(delta map[string]any, finishReason *string) error {
	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": finishReason,
	}
	payload := map[string]any{
		"id":      firstNonEmpty(e.id, "chatcmpl-"+randomSuffix()),
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []any{choice},
	}
	raw, err := marshal(payload)
	if err != nil {
		return err
	}
	return e.writer.Data(string(raw))
}

func (e *chatStreamEncoder) emitUsage() error {
	usage := *e.usage
	usage.Normalize()
	payload := map[string]any{
		"id":      firstNonEmpty(e.id, "chatcmpl-"+randomSuffix()),
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": usage.CachedTokens,
			},
		},
	}
	raw, err := marshal(payload)
	if err != nil {
		return err
	}
	return e.writer.Data(string(raw))
}

// DecodeResponse is declared above; these helpers are shared by the codecs.

func strPtr(s string) *string { return &s }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// randomSuffix makes a short opaque id suffix. It is not security sensitive.
func randomSuffix() string {
	var buf [8]byte
	for i := range buf {
		buf[i] = byte(time.Now().UnixNano() >> (i * 4))
	}
	const alphabet = "abcdef0123456789"
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

func argumentsString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if s, ok := rawString(raw); ok {
		return s
	}
	return string(raw)
}

func argumentsOrEmpty(args string) string {
	if args == "" {
		return "{}"
	}
	return args
}

func toolResultText(blocks []ir.Block) string {
	var out string
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out += b.Text
		case ir.BlockToolResult:
			if b.ToolResult == nil {
				out += b.Text
				continue
			}
			out += textOf(b.ToolResult.Content)
		}
	}
	return out
}

func thinkingText(blocks []ir.Block) string {
	var out string
	for _, b := range blocks {
		if b.Type == ir.BlockThinking && b.Thinking != nil {
			out += b.Thinking.Text
		}
	}
	return out
}

func metadataString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta[key].(string); ok {
		return v
	}
	return ""
}

func schemaOf(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// splitDataURI splits "data:<media>;base64,<payload>".
func splitDataURI(uri string) (media, data string, ok bool) {
	if !startsWith(uri, "data:") {
		return "", "", false
	}
	rest := uri[len("data:"):]
	semi := indexOf(rest, ";base64,")
	if semi < 0 {
		return "", "", false
	}
	return rest[:semi], rest[semi+len(";base64,"):], true
}

func startsWith(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = fmt.Sprintf
