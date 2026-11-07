package codec

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

func init() { register(openAIResponsesCodec{}) }

// openAIResponsesCodec implements ir.ProtoOpenAIResponses.
type openAIResponsesCodec struct{}

func (openAIResponsesCodec) Protocol() ir.Protocol { return ir.ProtoOpenAIResponses }

// ---- request decoding ----

type responsesRequest struct {
	Model             string          `json:"model"`
	Instructions      json.RawMessage `json:"instructions"`
	Input             json.RawMessage `json:"input"`
	MaxOutputTokens   *int            `json:"max_output_tokens"`
	Temperature       *float64        `json:"temperature"`
	TopP              *float64        `json:"top_p"`
	Stream            bool            `json:"stream"`
	Tools             []responsesTool `json:"tools"`
	ToolChoice        json.RawMessage `json:"tool_choice"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls"`
	Reasoning         *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	Metadata       map[string]string `json:"metadata"`
	PromptCacheKey string            `json:"prompt_cache_key"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	// function_call
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	CallID    string          `json:"call_id"`
	// function_call_output
	Output json.RawMessage `json:"output"`
	// reasoning
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
}

// reasoningSummary joins the summary parts of a reasoning item.
func reasoningSummary(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var out string
	for _, p := range parts {
		out += p.Text
	}
	return out
}

type responsesPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
	Refusal  string `json:"refusal"`
}

// DecodeRequest implements Codec.
func (openAIResponsesCodec) DecodeRequest(body []byte) (*ir.ChatRequest, error) {
	var raw responsesRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, decodeError(ir.ProtoOpenAIResponses, "invalid JSON", err)
	}

	req := &ir.ChatRequest{
		Model:       raw.Model,
		MaxTokens:   raw.MaxOutputTokens,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
		Stream:      raw.Stream,
		Metadata:    map[string]any{},
	}
	if raw.ParallelToolCalls != nil {
		req.Metadata["parallel_tool_calls"] = *raw.ParallelToolCalls
	}
	if raw.Reasoning != nil && raw.Reasoning.Effort != "" {
		req.Metadata["reasoning_effort"] = raw.Reasoning.Effort
	}
	if raw.PromptCacheKey != "" {
		req.Metadata["prompt_cache_key"] = raw.PromptCacheKey
	}

	if blocks, err := decodeResponsesContent(raw.Instructions, false); err != nil {
		return nil, err
	} else if len(blocks) > 0 {
		req.System = blocks
	}

	if len(raw.Input) > 0 && string(raw.Input) != "null" {
		if text, ok := rawString(raw.Input); ok {
			if text != "" {
				req.Messages = append(req.Messages, ir.Message{
					Role:   ir.RoleUser,
					Blocks: []ir.Block{ir.TextBlock(text)},
				})
			}
		} else {
			var items []responsesItem
			if err := json.Unmarshal(raw.Input, &items); err != nil {
				return nil, decodeError(ir.ProtoOpenAIResponses, "input items", err)
			}
			// A reasoning item applies to the turn that follows it.
			var pending []ir.Block
			for _, item := range items {
				if item.Type == "reasoning" {
					if text := reasoningSummary(item.Summary); text != "" {
						pending = append(pending, ir.Block{
							Type:     ir.BlockThinking,
							Thinking: &ir.ThinkingBlock{Text: text},
						})
					}
					continue
				}
				before := len(req.Messages)
				if err := appendResponsesItem(req, item); err != nil {
					return nil, err
				}
				if len(pending) > 0 && len(req.Messages) > before {
					req.Messages[before].Blocks = append(pending, req.Messages[before].Blocks...)
					pending = nil
				}
			}
		}
	}

	for _, t := range raw.Tools {
		if t.Type != "" && t.Type != "function" {
			req.Warn("only function tools can be converted; dropped one " + t.Type)
			continue
		}
		if t.Name == "" {
			continue
		}
		req.Tools = append(req.Tools, ir.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  schemaOf(t.Parameters),
		})
	}
	req.ToolChoice = decodeResponsesToolChoice(raw.ToolChoice)
	return req, nil
}

// appendResponsesItem folds one input item into the canonical conversation.
func appendResponsesItem(req *ir.ChatRequest, item responsesItem) error {
	switch item.Type {
	case "", "message":
		role := ir.RoleUser
		if item.Role == "assistant" {
			role = ir.RoleAssistant
		}
		blocks, err := decodeResponsesContent(item.Content, true)
		if err != nil {
			return err
		}
		req.Messages = append(req.Messages, ir.Message{Role: role, Blocks: blocks})
		return nil
	case "function_call":
		call := ir.ToolCall{
			ID:        item.CallID,
			Name:      item.Name,
			Arguments: argumentsString(item.Arguments),
		}
		// Attach to the previous assistant turn when it is still open, so a
		// parallel call set stays one message.
		if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == ir.RoleAssistant {
			req.Messages[n-1].ToolCalls = append(req.Messages[n-1].ToolCalls, call)
			return nil
		}
		req.Messages = append(req.Messages, ir.Message{
			Role:      ir.RoleAssistant,
			ToolCalls: []ir.ToolCall{call},
		})
		return nil
	case "function_call_output":
		text, _ := rawString(item.Output)
		if text == "" && len(item.Output) > 0 {
			text = string(item.Output)
		}
		req.Messages = append(req.Messages, ir.Message{
			Role:       ir.RoleTool,
			ToolCallID: item.CallID,
			Blocks:     []ir.Block{ir.TextBlock(text)},
		})
		return nil
	case "reasoning":
		// Reasoning items are upstream echoes; the gateway regenerates reasoning
		// content from the model, so they are dropped.
		return nil
	default:
		req.Warn("unsupported input item type " + item.Type + " was dropped")
		return nil
	}
}

// decodeResponsesContent parses a content field (string or part array).
func decodeResponsesContent(raw json.RawMessage, allowImages bool) ([]ir.Block, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if text, ok := rawString(raw); ok {
		if text == "" {
			return nil, nil
		}
		return []ir.Block{ir.TextBlock(text)}, nil
	}
	var parts []responsesPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, decodeError(ir.ProtoOpenAIResponses, "content parts", err)
	}
	blocks := make([]ir.Block, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			blocks = append(blocks, ir.TextBlock(p.Text))
		case "refusal":
			blocks = append(blocks, ir.TextBlock(p.Refusal))
		case "input_image", "image_url":
			if !allowImages || p.ImageURL == "" {
				continue
			}
			blocks = append(blocks, ir.Block{Type: ir.BlockImage, Image: imageFromURL(p.ImageURL)})
		}
	}
	return blocks, nil
}

func decodeResponsesToolChoice(raw json.RawMessage) *ir.ToolChoice {
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
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	if choice.Name != "" {
		return &ir.ToolChoice{Mode: ir.ToolChoiceTool, Name: choice.Name}
	}
	if choice.Type != "" {
		return &ir.ToolChoice{Mode: choice.Type}
	}
	return nil
}

// ---- request encoding ----

type responsesRequestOut struct {
	Model             string             `json:"model"`
	Instructions      string             `json:"instructions,omitempty"`
	Input             []responsesItemOut `json:"input"`
	MaxOutputTokens   *int               `json:"max_output_tokens,omitempty"`
	Temperature       *float64           `json:"temperature,omitempty"`
	TopP              *float64           `json:"top_p,omitempty"`
	Stream            bool               `json:"stream"`
	Tools             []responsesToolOut `json:"tools,omitempty"`
	ToolChoice        any                `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool              `json:"parallel_tool_calls,omitempty"`
	Reasoning         map[string]any     `json:"reasoning,omitempty"`
	PromptCacheKey    string             `json:"prompt_cache_key,omitempty"`
}

type responsesItemOut struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	Name      string `json:"name,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Output    any    `json:"output,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Summary   any    `json:"summary,omitempty"`
}

type responsesToolOut struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

// EncodeRequest implements Codec.
func (openAIResponsesCodec) EncodeRequest(req *ir.ChatRequest) ([]byte, error) {
	out := responsesRequestOut{
		Model:           req.Model,
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Stream:          req.Stream,
		Instructions:    textOf(req.System),
		PromptCacheKey:  metadataString(req.Metadata, "prompt_cache_key"),
	}
	if effort := metadataString(req.Metadata, "reasoning_effort"); effort != "" {
		out.Reasoning = map[string]any{"effort": effort}
	}
	if parallel, ok := req.Metadata["parallel_tool_calls"].(bool); ok {
		out.ParallelToolCalls = &parallel
	}

	for _, m := range req.Messages {
		switch m.Role {
		case ir.RoleAssistant:
			// Reasoning travels as its own item, ahead of the turn it belongs to.
			if thinking := thinkingText(m.Blocks); thinking != "" {
				out.Input = append(out.Input, responsesItemOut{
					Type: "reasoning",
					Summary: []map[string]any{
						{"type": "summary_text", "text": thinking},
					},
				})
			}
			if text := textOf(m.Blocks); text != "" || len(m.ToolCalls) == 0 {
				out.Input = append(out.Input, responsesItemOut{
					Type:    "message",
					Role:    "assistant",
					Content: []map[string]any{{"type": "output_text", "text": text}},
				})
			}
			for _, tc := range m.ToolCalls {
				out.Input = append(out.Input, responsesItemOut{
					Type:      "function_call",
					Name:      tc.Name,
					CallID:    tc.ID,
					Arguments: argumentsOrEmpty(tc.Arguments),
				})
			}
		case ir.RoleTool:
			out.Input = append(out.Input, responsesItemOut{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: toolResultText(m.Blocks),
			})
		default:
			out.Input = append(out.Input, responsesItemOut{
				Type:    "message",
				Role:    "user",
				Content: encodeResponsesContent(m.Blocks),
			})
		}
	}

	for _, t := range req.Tools {
		tool := responsesToolOut{Type: "function", Name: t.Name, Description: t.Description}
		if t.Parameters != nil {
			tool.Parameters = t.Parameters
		} else {
			tool.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out.Tools = append(out.Tools, tool)
	}
	if tc := req.ToolChoice; tc != nil {
		switch tc.Mode {
		case ir.ToolChoiceTool:
			out.ToolChoice = map[string]any{"type": "function", "name": tc.Name}
		default:
			out.ToolChoice = tc.Mode
		}
	}
	return marshal(out)
}

func encodeResponsesContent(blocks []ir.Block) any {
	parts := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
		case ir.BlockImage:
			if b.Image == nil {
				continue
			}
			url := b.Image.URL
			if url == "" && b.Image.DataB64 != "" {
				media := b.Image.MediaType
				if media == "" {
					media = "image/png"
				}
				url = "data:" + media + ";base64," + b.Image.DataB64
			}
			parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
		}
	}
	if len(parts) == 0 {
		return []map[string]any{{"type": "input_text", "text": ""}}
	}
	return parts
}

// ---- response decoding ----

type responsesResponse struct {
	ID     string          `json:"id"`
	Model  string          `json:"model"`
	Status string          `json:"status"`
	Output []responsesItem `json:"output"`
	Usage  *struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		TotalTokens        int `json:"total_tokens"`
		InputTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

// DecodeResponse implements Codec.
func (openAIResponsesCodec) DecodeResponse(body []byte) (*ir.ChatResponse, error) {
	var raw responsesResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, decodeError(ir.ProtoOpenAIResponses, "invalid JSON", err)
	}
	resp := &ir.ChatResponse{ID: raw.ID, Model: raw.Model}
	for _, item := range raw.Output {
		switch item.Type {
		case "message":
			blocks, err := decodeResponsesContent(item.Content, false)
			if err != nil {
				return nil, err
			}
			resp.Blocks = append(resp.Blocks, blocks...)
		case "reasoning":
			if text := reasoningSummary(item.Summary); text != "" {
				resp.Blocks = append(resp.Blocks, ir.Block{
					Type:     ir.BlockThinking,
					Thinking: &ir.ThinkingBlock{Text: text},
				})
			}
		case "function_call":
			resp.ToolCalls = append(resp.ToolCalls, ir.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: argumentsString(item.Arguments),
			})
		}
	}
	resp.FinishReason = responsesFinish(raw.Status, raw.IncompleteDetails)
	// A completed response that emitted function calls ended on a tool call; the
	// protocol has no dedicated status for it.
	if resp.FinishReason == ir.FinishStop && len(resp.ToolCalls) > 0 {
		resp.FinishReason = ir.FinishToolCalls
	}
	if raw.Usage != nil {
		resp.Usage = ir.Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		}
		if raw.Usage.InputTokensDetails != nil {
			resp.Usage.CachedTokens = raw.Usage.InputTokensDetails.CachedTokens
		}
		resp.Usage.Normalize()
	}
	return resp, nil
}

func responsesFinish(status string, incomplete *struct {
	Reason string `json:"reason"`
}) string {
	if status == "incomplete" && incomplete != nil {
		switch incomplete.Reason {
		case "max_output_tokens":
			return ir.FinishLength
		case "content_filter":
			return ir.FinishContentFilter
		}
	}
	switch status {
	case "completed", "":
		return ir.FinishStop
	case "failed":
		return ir.FinishError
	default:
		return ir.FinishStop
	}
}

// ---- response encoding ----

// EncodeResponse implements Codec.
func (openAIResponsesCodec) EncodeResponse(resp *ir.ChatResponse) ([]byte, error) {
	output := make([]map[string]any, 0, 3)
	if thinking := thinkingText(resp.Blocks); thinking != "" {
		output = append(output, map[string]any{
			"type": "reasoning",
			"id":   "rs_" + randomSuffix(),
			"summary": []any{map[string]any{
				"type": "summary_text",
				"text": thinking,
			}},
		})
	}
	if text := textOf(resp.Blocks); text != "" || len(resp.ToolCalls) == 0 {
		output = append(output, map[string]any{
			"type":   "message",
			"id":     "msg_" + randomSuffix(),
			"role":   "assistant",
			"status": "completed",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        text,
				"annotations": []any{},
			}},
		})
	}
	for _, tc := range resp.ToolCalls {
		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        "fc_" + randomSuffix(),
			"call_id":   tc.ID,
			"name":      tc.Name,
			"arguments": argumentsOrEmpty(tc.Arguments),
			"status":    "completed",
		})
	}

	usage := resp.Usage
	usage.Normalize()
	body := map[string]any{
		"id":         firstNonEmpty(resp.ID, "resp_"+randomSuffix()),
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     responsesStatus(resp.FinishReason),
		"model":      resp.Model,
		"output":     output,
		"usage": map[string]any{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
			"total_tokens":  usage.TotalTokens,
			"input_tokens_details": map[string]any{
				"cached_tokens": usage.CachedTokens,
			},
		},
	}
	if resp.FinishReason == ir.FinishLength {
		body["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	return marshal(body)
}

func responsesStatus(finish string) string {
	switch finish {
	case ir.FinishLength, ir.FinishContentFilter:
		return "incomplete"
	case ir.FinishError:
		return "failed"
	default:
		return "completed"
	}
}

// ---- streaming ----

// NewStreamDecoder implements Codec.
func (openAIResponsesCodec) NewStreamDecoder(r io.Reader) StreamDecoder {
	return &responsesStreamDecoder{reader: newSSEReader(r)}
}

type responsesStreamDecoder struct {
	reader       *sseReader
	done         bool
	sawCompleted bool
	sawCreated   bool
	sawTool      bool
	model        string
	id           string
	// queue holds chunks produced by one event that must be delivered one at a
	// time: completion carries usage, the finish reason and the terminator.
	queue []*ir.StreamChunk
}

// Next implements StreamDecoder.
func (d *responsesStreamDecoder) Next() (*ir.StreamChunk, error) {
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
			if err == io.EOF && !d.sawCompleted {
				return nil, &streamCodecError{message: "response stream ended before response.completed"}
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
		case "response.created", "response.in_progress":
			if !d.sawCreated {
				d.sawCreated = true
				if response, ok := payload["response"].(map[string]any); ok {
					d.id, _ = response["id"].(string)
					d.model, _ = response["model"].(string)
				}
				return &ir.StreamChunk{Kind: ir.ChunkStart, ID: d.id, Model: d.model}, nil
			}
		case "response.output_text.delta":
			if text, ok := payload["delta"].(string); ok && text != "" {
				return &ir.StreamChunk{Kind: ir.ChunkText, Text: text}, nil
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if text, ok := payload["delta"].(string); ok && text != "" {
				return &ir.StreamChunk{Kind: ir.ChunkThinking, Text: text}, nil
			}
		case "response.function_call_arguments.delta":
			delta, _ := payload["delta"].(string)
			name, _ := payload["name"].(string)
			callID, _ := payload["call_id"].(string)
			if delta != "" || name != "" {
				return &ir.StreamChunk{
					Kind:       ir.ChunkToolCall,
					ToolCallID: callID,
					ToolName:   name,
					ArgsDelta:  delta,
				}, nil
			}
		case "response.output_item.added":
			if item, ok := payload["item"].(map[string]any); ok {
				if itemType, _ := item["type"].(string); itemType == "function_call" {
					name, _ := item["name"].(string)
					callID, _ := item["call_id"].(string)
					d.sawTool = true
					return &ir.StreamChunk{Kind: ir.ChunkToolCall, ToolCallID: callID, ToolName: name}, nil
				}
			}
		case "response.completed", "response.incomplete", "response.failed":
			d.sawCompleted = true
			finish := ir.FinishStop
			if response, ok := payload["response"].(map[string]any); ok {
				status := ""
				if incomplete, ok := response["incomplete_details"].(map[string]any); ok {
					status, _ = incomplete["reason"].(string)
				}
				finish = responsesFinishFromEvent(eventType, status)
				// The protocol has no "tool_calls" status: a completed response
				// that emitted function calls ended on a tool call.
				if finish == ir.FinishStop && d.sawTool {
					finish = ir.FinishToolCalls
				}
				if usage, ok := parseResponsesUsage(response["usage"]); ok {
					d.queue = append(d.queue, &ir.StreamChunk{Kind: ir.ChunkUsage, Usage: usage})
				}
			}
			// Deliver usage first, then the finish reason, then the terminator.
			d.done = true
			d.queue = append(d.queue,
				&ir.StreamChunk{Kind: ir.ChunkFinish, FinishReason: finish},
				&ir.StreamChunk{Kind: ir.ChunkDone},
			)
		}
	}
}

func responsesFinishFromEvent(eventType, incompleteReason string) string {
	switch eventType {
	case "response.incomplete":
		if incompleteReason == "max_output_tokens" {
			return ir.FinishLength
		}
		return ir.FinishContentFilter
	case "response.failed":
		return ir.FinishError
	default:
		return ir.FinishStop
	}
}

func parseResponsesUsage(raw any) (*ir.Usage, bool) {
	payload, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	number := func(key string) int {
		if v, ok := payload[key].(float64); ok {
			return int(v)
		}
		return 0
	}
	usage := &ir.Usage{
		PromptTokens:     number("input_tokens"),
		CompletionTokens: number("output_tokens"),
		TotalTokens:      number("total_tokens"),
	}
	if details, ok := payload["input_tokens_details"].(map[string]any); ok {
		if cached, ok := details["cached_tokens"].(float64); ok {
			usage.CachedTokens = int(cached)
		}
	}
	usage.Normalize()
	return usage, true
}

// NewStreamEncoder implements Codec.
func (openAIResponsesCodec) NewStreamEncoder(w io.Writer) StreamEncoder {
	return &responsesStreamEncoder{writer: newSSEWriter(w), created: time.Now().Unix()}
}

type responsesStreamEncoder struct {
	writer   *sseWriter
	id       string
	model    string
	created  int64
	seq      int
	msgItem  bool
	toolItem bool
	usage    *ir.Usage
	text     bool
	closed   bool
}

// Write implements StreamEncoder.
func (e *responsesStreamEncoder) Write(chunk *ir.StreamChunk) error {
	switch chunk.Kind {
	case ir.ChunkStart:
		e.id = firstNonEmpty(chunk.ID, "resp_"+randomSuffix())
		e.model = chunk.Model
		return e.event("response.created", map[string]any{
			"response": e.responseObject("in_progress", nil),
		})
	case ir.ChunkText:
		if !e.msgItem {
			if err := e.addMessageItem(); err != nil {
				return err
			}
		}
		e.text = true
		return e.event("response.output_text.delta", map[string]any{
			"delta": chunk.Text,
		})
	case ir.ChunkThinking:
		return e.event("response.reasoning_summary_text.delta", map[string]any{
			"delta": chunk.Text,
		})
	case ir.ChunkToolCall:
		if !e.toolItem {
			e.toolItem = true
			if err := e.event("response.output_item.added", map[string]any{
				"output_index": 1,
				"item": map[string]any{
					"type":    "function_call",
					"id":      "fc_" + randomSuffix(),
					"call_id": chunk.ToolCallID,
					"name":    chunk.ToolName,
				},
			}); err != nil {
				return err
			}
		}
		delta := firstNonEmpty(chunk.ArgsDelta, chunk.ArgsComplete)
		if delta == "" {
			return nil
		}
		return e.event("response.function_call_arguments.delta", map[string]any{
			"delta": delta,
		})
	case ir.ChunkUsage:
		e.usage = chunk.Usage
		return nil
	case ir.ChunkFinish:
		return nil
	case ir.ChunkDone:
		return e.complete()
	}
	return nil
}

// Close implements StreamEncoder.
func (e *responsesStreamEncoder) Close() error {
	if e.closed {
		return nil
	}
	return e.complete()
}

func (e *responsesStreamEncoder) complete() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if e.text || e.msgItem || e.toolItem {
		if err := e.event("response.output_item.done", map[string]any{"output_index": 0}); err != nil {
			return err
		}
	}
	return e.event("response.completed", map[string]any{
		"response": e.responseObject("completed", e.usage),
	})
}

func (e *responsesStreamEncoder) addMessageItem() error {
	e.msgItem = true
	return e.event("response.output_item.added", map[string]any{
		"output_index": 0,
		"item": map[string]any{
			"type":   "message",
			"id":     "msg_" + randomSuffix(),
			"role":   "assistant",
			"status": "in_progress",
		},
	})
}

func (e *responsesStreamEncoder) responseObject(status string, usage *ir.Usage) map[string]any {
	out := map[string]any{
		"id":         firstNonEmpty(e.id, "resp_"+randomSuffix()),
		"object":     "response",
		"created_at": e.created,
		"status":     status,
		"model":      e.model,
	}
	if usage != nil {
		u := *usage
		u.Normalize()
		out["usage"] = map[string]any{
			"input_tokens":  u.PromptTokens,
			"output_tokens": u.CompletionTokens,
			"total_tokens":  u.TotalTokens,
			"input_tokens_details": map[string]any{
				"cached_tokens": u.CachedTokens,
			},
		}
	}
	return out
}

// event writes one named event with a monotonic sequence_number.
func (e *responsesStreamEncoder) event(name string, payload map[string]any) error {
	e.seq++
	payload["type"] = name
	payload["sequence_number"] = e.seq
	raw, err := marshal(payload)
	if err != nil {
		return err
	}
	return e.writer.Event(name, string(raw))
}

var _ = fmt.Sprintf
