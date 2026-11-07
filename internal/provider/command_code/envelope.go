package command_code

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/learningdog1/ada_prune/internal/gateway"
	"github.com/learningdog1/ada_prune/internal/gateway/ir"
)

// The envelope is the vendor's request contract. Field order is part of the
// wire format and must stay: config, memory, taste, skills, permissionMode,
// threadId, mode, params. Go's encoding/json emits struct fields in declaration
// order, so these structs must not be reordered.
//
// testdata/golden/envelope.json locks the shape.

// Request is the top-level envelope.
type Request struct {
	Config         EnvelopeConfig `json:"config"`
	Memory         any            `json:"memory"`
	Taste          any            `json:"taste"`
	Skills         any            `json:"skills"`
	PermissionMode string         `json:"permissionMode"`
	ThreadID       string         `json:"threadId,omitempty"`
	Mode           string         `json:"mode"`
	Params         Params         `json:"params"`
}

// EnvelopeConfig is the fake project context.
type EnvelopeConfig struct {
	WorkingDir    string `json:"workingDir"`
	Date          string `json:"date"`
	Environment   string `json:"environment"`
	Structure     []any  `json:"structure"`
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	MainBranch    string `json:"mainBranch"`
	GitStatus     string `json:"gitStatus"`
	RecentCommits []any  `json:"recentCommits"`
}

// Params carries the model, conversation and sampling options.
type Params struct {
	Model             string        `json:"model"`
	Messages          []Message     `json:"messages"`
	MaxTokens         int64         `json:"max_tokens"`
	Stream            bool          `json:"stream"`
	System            []SystemBlock `json:"system,omitempty"`
	Temperature       *float64      `json:"temperature,omitempty"`
	ReasoningEffort   *string       `json:"reasoning_effort,omitempty"`
	Tools             []Tool        `json:"tools"`
	ToolChoice        any           `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool         `json:"parallel_tool_calls,omitempty"`
}

// Message is one wire turn: role plus content blocks.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// Block is one content block. Only the fields of its Type are set.
type Block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	Image        string          `json:"image,omitempty"`
	MimeType     string          `json:"mimeType,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	ToolName     string          `json:"toolName,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       *ToolOutput     `json:"output,omitempty"`
}

// ToolOutput is the textual result of a tool call.
type ToolOutput struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// SystemBlock is one system prompt block with an optional cache breakpoint.
type SystemBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// Tool is a wire tool definition: no "type" wrapper, schema named input_schema.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolChoice is the wire tool_choice.
type ToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// BuildOptions are the runtime knobs envelope construction needs.
type BuildOptions struct {
	Config Config
	// ThreadID is written only when it is a UUID.
	ThreadID string
	// PromptCacheKey places a cache breakpoint when the client set none.
	PromptCacheKey string
	// Now overrides the clock in tests.
	Now func() time.Time
}

// toolNameAliases mirrors the rewrites the official CLI applies before sending.
var toolNameAliases = map[string]string{
	"bash_output":         "shell_output",
	"task_output":         "shell_output",
	"tool_search":         "search_tools",
	"read_multiple_files": "read_file",
}

// ToWireToolName rewrites a client tool name into the name the upstream knows.
func ToWireToolName(name string) string {
	if alias, ok := toolNameAliases[name]; ok {
		return alias
	}
	return name
}

var dataURIMedia = regexp.MustCompile(`^data:([^;,]+)`)

// BuildRequest converts a canonical request into the vendor envelope.
func BuildRequest(req *ir.ChatRequest, in *gateway.InvokeRequest, opts BuildOptions) *Request {
	if req == nil {
		req = &ir.ChatRequest{}
	}
	cfg := opts.Config
	if cfg.CLIMode == "" {
		cfg.CLIMode = "agent"
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	profile := ProfileFor(cfg.ProjectDir)

	systemBlocks, chatMessages := splitSystem(req)
	chatMessages = hoistToolResults(chatMessages)

	// Tool call id -> name, so tool results can carry the name the upstream
	// expects even when the client omitted it.
	toolNameByID := make(map[string]string)
	for _, m := range chatMessages {
		if m.Role != ir.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				toolNameByID[tc.ID] = ToWireToolName(tc.Name)
			}
		}
	}

	wire := make([]Message, 0, len(chatMessages))
	for _, m := range chatMessages {
		switch m.Role {
		case ir.RoleUser:
			wire = append(wire, convertUserMessage(m)...)
		case ir.RoleAssistant:
			wire = append(wire, convertAssistantMessage(m))
		case ir.RoleTool:
			wire = append(wire, Message{
				Role: "tool",
				Content: []Block{{
					Type:       "tool-result",
					ToolCallID: m.ToolCallID,
					ToolName:   firstNonEmpty(toolNameByID[m.ToolCallID], m.Name),
					Output:     &ToolOutput{Type: "text", Value: toolResultValue(m)},
				}},
			})
		default:
			// Unknown roles are folded into user content so the upstream's
			// validation does not reject the whole request.
			wire = append(wire, Message{
				Role:    "user",
				Content: []Block{{Type: "text", Text: m.Text()}},
			})
		}
	}

	if in.Session.PromptCacheKey != "" && !hasCacheMarker(systemBlocks, wire) && len(systemBlocks) > 0 {
		systemBlocks[len(systemBlocks)-1].CacheControl = json.RawMessage(`{"type":"ephemeral"}`)
	}
	// The CLI terminates every system block except the last with a newline so
	// cache breakpoints survive concatenation.
	for i := 0; i+1 < len(systemBlocks); i++ {
		systemBlocks[i].Text += "\n"
	}

	model := in.Model.UpstreamName
	if model == "" {
		model = req.Model
	}
	maxTokens := int64(64000)
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = int64(*req.MaxTokens)
	} else if in.Model.MaxTokens > 0 {
		maxTokens = int64(in.Model.MaxTokens)
	}
	if maxTokens > 200_000 {
		maxTokens = 200_000
	}

	params := Params{
		Model:     model,
		Messages:  wire,
		MaxTokens: maxTokens,
		Stream:    true, // the upstream always streams; the gateway aggregates
		Tools:     convertTools(req.Tools),
	}
	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	} else if cfg.EmptySystemPlaceholder {
		params.System = []SystemBlock{{Type: "text", Text: " "}}
	}
	if req.Temperature != nil {
		params.Temperature = req.Temperature
	}
	if effort, ok := req.Metadata["reasoning_effort"].(string); ok && effort != "" {
		params.ReasoningEffort = &effort
	}
	if tc := convertToolChoice(req.ToolChoice); tc != nil {
		params.ToolChoice = tc
	}
	if parallel, ok := req.Metadata["parallel_tool_calls"].(bool); ok {
		params.ParallelToolCalls = &parallel
	}

	out := &Request{
		Config: EnvelopeConfig{
			WorkingDir:    profile.ProjectDir,
			Date:          now().UTC().Format("2006-01-02"),
			Environment:   profile.Platform,
			Structure:     []any{},
			IsGitRepo:     false,
			CurrentBranch: "",
			MainBranch:    "",
			GitStatus:     "",
			RecentCommits: []any{},
		},
		Memory:         nil,
		Taste:          nil,
		Skills:         nil,
		PermissionMode: "standard",
		Mode:           cfg.CLIMode,
		Params:         params,
	}
	if IsUUID(opts.ThreadID) {
		out.ThreadID = opts.ThreadID
	}
	return out
}

// ProjectSlug is the slug of the fake working directory, sent as x-project-slug.
func (r *Request) ProjectSlug() string { return SlugifyProjectPath(r.Config.WorkingDir) }

// splitSystem separates system content from the conversation. Canonical requests
// keep system prompts on the request, but a message carrying the system role is
// still accepted and moved.
func splitSystem(req *ir.ChatRequest) ([]SystemBlock, []ir.Message) {
	var system []SystemBlock
	for _, b := range req.System {
		if b.Type != ir.BlockText {
			continue
		}
		system = append(system, SystemBlock{Type: "text", Text: b.Text})
	}
	messages := make([]ir.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			for _, b := range m.Blocks {
				if b.Type == ir.BlockText && b.Text != "" {
					system = append(system, SystemBlock{Type: "text", Text: b.Text})
				}
			}
			continue
		}
		messages = append(messages, m)
	}
	return system, messages
}

// hoistToolResults turns tool-result blocks carried on user turns into their own
// tool turns, which is the shape the envelope expects.
func hoistToolResults(messages []ir.Message) []ir.Message {
	out := make([]ir.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role != ir.RoleUser || len(m.Blocks) == 0 {
			out = append(out, m)
			continue
		}
		var rest []ir.Block
		for _, b := range m.Blocks {
			if b.Type != ir.BlockToolResult || b.ToolResult == nil {
				rest = append(rest, b)
				continue
			}
			var text strings.Builder
			for _, inner := range b.ToolResult.Content {
				if inner.Type == ir.BlockText {
					text.WriteString(inner.Text)
				}
			}
			out = append(out, ir.Message{
				Role:       ir.RoleTool,
				ToolCallID: b.ToolResult.ToolUseID,
				Blocks:     []ir.Block{{Type: ir.BlockText, Text: text.String()}},
			})
		}
		if len(rest) > 0 {
			out = append(out, ir.Message{Role: ir.RoleUser, Blocks: rest})
		}
	}
	return out
}

func convertUserMessage(m ir.Message) []Message {
	var parts []Block
	for _, b := range m.Blocks {
		switch b.Type {
		case ir.BlockText:
			parts = append(parts, Block{Type: "text", Text: b.Text})
		case ir.BlockImage:
			if b.Image == nil {
				continue
			}
			if b.Image.URL != "" {
				parts = append(parts, Block{
					Type:  "image",
					Image: b.Image.URL,
				})
				continue
			}
			media := b.Image.MediaType
			if media == "" {
				media = "image/png"
			}
			parts = append(parts, Block{
				Type:     "image",
				Image:    "data:" + media + ";base64," + b.Image.DataB64,
				MimeType: media,
			})
		case ir.BlockThinking:
			// Reasoning is assistant-side content; a user turn carrying it is
			// folded into text so nothing is silently dropped.
			if b.Thinking != nil && b.Thinking.Text != "" {
				parts = append(parts, Block{Type: "text", Text: b.Thinking.Text})
			}
		}
	}
	if len(parts) == 0 {
		parts = []Block{{Type: "text", Text: ""}}
	}
	return []Message{{Role: "user", Content: parts}}
}

func convertAssistantMessage(m ir.Message) Message {
	var parts []Block
	if text := m.Text(); text != "" {
		parts = append(parts, Block{Type: "text", Text: text})
	}
	for _, b := range m.Blocks {
		if b.Type == ir.BlockThinking && b.Thinking != nil && b.Thinking.Text != "" {
			parts = append(parts, Block{Type: "reasoning", Text: b.Thinking.Text})
		}
	}
	for _, tc := range m.ToolCalls {
		parts = append(parts, Block{
			Type:       "tool-call",
			ToolCallID: tc.ID,
			ToolName:   ToWireToolName(tc.Name),
			Input:      normalizeArguments(tc.Arguments),
		})
	}
	if len(parts) == 0 {
		parts = []Block{{Type: "text", Text: ""}}
	}
	return Message{Role: "assistant", Content: parts}
}

// toolResultValue renders a tool turn's content as the upstream's text value.
func toolResultValue(m ir.Message) string {
	var b strings.Builder
	for _, block := range m.Blocks {
		switch block.Type {
		case ir.BlockText:
			b.WriteString(block.Text)
		case ir.BlockToolResult:
			if block.ToolResult == nil {
				// Already flattened by the codec: the text lives on the block.
				b.WriteString(block.Text)
				continue
			}
			for _, inner := range block.ToolResult.Content {
				if inner.Type == ir.BlockText {
					b.WriteString(inner.Text)
				}
			}
		}
	}
	return b.String()
}

func convertTools(defs []ir.ToolDef) []Tool {
	out := make([]Tool, 0, len(defs))
	for _, d := range defs {
		schema := json.RawMessage("{}")
		if len(d.Parameters) > 0 {
			if buf, err := json.Marshal(d.Parameters); err == nil {
				schema = buf
			}
		}
		out = append(out, Tool{
			Name:        ToWireToolName(d.Name),
			Description: d.Description,
			InputSchema: schema,
		})
	}
	return out
}

func convertToolChoice(choice *ir.ToolChoice) *ToolChoice {
	if choice == nil {
		return nil
	}
	switch choice.Mode {
	case ir.ToolChoiceAuto, ir.ToolChoiceNone, ir.ToolChoiceRequired:
		return &ToolChoice{Type: choice.Mode}
	case ir.ToolChoiceTool:
		return &ToolChoice{Type: "tool", Name: ToWireToolName(choice.Name)}
	default:
		return nil
	}
}

// normalizeArguments guarantees a JSON object literal for a tool call's input.
func normalizeArguments(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage("{}")
	}
	if !json.Valid([]byte(trimmed)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

func hasCacheMarker(system []SystemBlock, messages []Message) bool {
	for _, b := range system {
		if len(b.CacheControl) > 0 {
			return true
		}
	}
	for _, m := range messages {
		for _, b := range m.Content {
			if len(b.CacheControl) > 0 {
				return true
			}
		}
	}
	return false
}

// MediaTypeFromDataURI extracts the MIME type of a data URI.
func MediaTypeFromDataURI(uri string) string {
	m := dataURIMedia.FindStringSubmatch(uri)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}
