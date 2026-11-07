# 04 · 协议转换引擎规格

| 项目 | 内容 |
| --- | --- |
| 文档编号 | DOC-04 |
| 版本 | v1.0 |
| 日期 | 2026-11-07 |
| 上游 | [01 §3.6](01-requirements.md) · [03 §3.4](03-provider-adapter.md) |
| 代码位置 | `internal/gateway/ir/` · `internal/gateway/codec/` |

---

## 1. 设计原则

**不做两两转换器，而是 3 个编解码器围绕一个规约 IR 互转：**

```
                  ┌────────────────────────────────────┐
  openai_chat ───►│  Decode  →   IR   →  Encode        │───► openai_chat
  openai_resp ───►│                                    │───► openai_resp
  anthropic   ───►│  DecodeStream / EncodeStream       │───► anthropic
                  └────────────────────────────────────┘
                              ▲
                              │  IR 也是适配器的输入/输出
                              ▼
                  provider.Adapter（command_code / openai / anthropic）
```

- 每个协议只实现「本协议 ↔ IR」：3 × 3 = **9 组方向**自动成立
  （3 组同协议透传 + 6 组跨协议互转）；新增下游协议只加一个 codec；
- IR 是**中立超集**：三方共有能力必到，私有扩展进 `Metadata`；
- 编解码器是**纯函数**：`[]byte → IR`、`IR → []byte`；流式是状态机；全部可单测；
- 无法映射的字段进 `warnings[]`，按「最小惊讶」降级，**不 panic、不阻断**。

> 设计参考 [`aluka_cloud/llm`](file:///E:/codes/go_projects/aluka_cloud/llm) 的 `internal/protocol`，
> 本项目的 IR 额外提供 `ChunkThinking`（thinking / reasoning_content）与 `vendor_native` 上游协议位。

## 2. 三种下游协议概览

| 维度 | OpenAI Chat | OpenAI Responses | Anthropic Messages |
| :--- | :--- | :--- | :--- |
| 端点 | `POST /v1/chat/completions` | `POST /v1/responses` | `POST /v1/messages` |
| 鉴权 | `Authorization: Bearer` | `Authorization: Bearer` | `x-api-key` + `anthropic-version` |
| 系统提示 | `messages[role=system]` | `instructions` | 顶层 `system` |
| 会话输入 | `messages[]` | `input`（字符串或 item 数组） | `messages[]`（role 仅 user/assistant） |
| 输出上限 | `max_tokens` / `max_completion_tokens` | `max_output_tokens` | `max_tokens`（**必填**） |
| 工具定义 | `tools[{type:function,function:{...}}]` | `tools[{type:function,name,parameters}]` | `tools[{name,description,input_schema}]` |
| 工具选择 | `tool_choice` | `tool_choice` | `tool_choice` |
| 助手工具调用 | `message.tool_calls[]` | `output[type=function_call]` | `content[type=tool_use]` |
| 工具结果 | `messages[role=tool]` | `input[type=function_call_output]` | `content[type=tool_result]` |
| 图片 | `content[{type:image_url,image_url:{url}}]` | `input_image`（`image_url`） | `content[{type:image,source:{...}}]` |
| 推理内容 | `reasoning_content` / `reasoning` | `reasoning.summary` | `thinking` 块（含 `signature`） |
| 完成原因 | `finish_reason` | `status` + `incomplete_details` | `stop_reason` |
| usage | `{prompt,completion,total}` | `{input,output,total}` | `{input_tokens,output_tokens,...}` |
| 流式形态 | `data: {...}` + `data: [DONE]`，`choices[].delta` | 命名事件 `response.*` | 命名事件 `message_start/content_block_delta/message_stop` |

> 版本基线：OpenAI 兼容 2024–2026 稳定形态；Anthropic `anthropic-version: 2023-06-01`。
> 版本头可在供应商 `extra_headers` 或模型级头里覆盖。

## 3. IR 类型（`internal/gateway/ir`）

```go
package ir

type Protocol string
const (
    ProtoOpenAIChat      Protocol = "openai_chat"
    ProtoOpenAIResponses Protocol = "openai_responses"
    ProtoAnthropic       Protocol = "anthropic"
    // ProtoVendorNative 只作为「上游协议」存在：由适配器内部处理私有 wire，
    // 不对应任何下游编解码器。
    ProtoVendorNative    Protocol = "vendor_native"
)

// —— 请求 ——
type ChatRequest struct {
    Model       string       // 上游真实模型名（已由 Router 解析）
    System      []Block
    Messages    []Message
    Tools       []ToolDef
    ToolChoice  *ToolChoice
    Temperature *float64
    TopP        *float64
    MaxTokens   *int
    Stop        []string
    Stream      bool
    Metadata    map[string]any // 供应商扩展（尽力透传）
    Warnings    []string       // 降级告警（不回传给下游，只落日志/测试台）
}

type Role string // user | assistant | tool

type Message struct {
    Role       Role
    Blocks     []Block
    ToolCalls  []ToolCall
    ToolCallID string
    Name       string
}

type Block struct {
    Type       string   // text | image | tool_result | thinking
    Text       string
    Image      *ImageBlock
    ToolResult *ToolResult
    Thinking   *ThinkingBlock
}

type ImageBlock struct {
    URL       string   // http(s) 或 data URI
    MediaType string   // image/png...
    DataB64   string   // 二选一
}

type ThinkingBlock struct {
    Text      string
    Signature string // Anthropic thinking signature（CC 用假签名）
}

type ToolResult struct {
    ToolUseID string
    Content   []Block
    IsError   bool
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments string // 统一为 JSON 字符串
}

type ToolDef struct {
    Name        string
    Description string
    Parameters  map[string]any // JSON Schema
}

type ToolChoice struct {
    Mode string // auto | none | required | tool
    Name string
}

// —— 用量 ——
type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    CachedTokens     int
    Estimated        bool   // true 表示平台估算
}

// —— 非流式响应 ——
type ChatResponse struct {
    ID           string
    Model        string
    Blocks       []Block
    ToolCalls    []ToolCall
    FinishReason string  // stop|length|tool_calls|content_filter|error|upstream_error
    Usage        Usage
    Warnings     []string
}

// —— 流式增量 ——
type ChunkKind string
const (
    ChunkStart    ChunkKind = "start"
    ChunkText     ChunkKind = "text"
    ChunkThinking ChunkKind = "thinking"
    ChunkToolCall ChunkKind = "tool_call"
    ChunkUsage    ChunkKind = "usage"
    ChunkFinish   ChunkKind = "finish"
    ChunkDone     ChunkKind = "done"
)

type StreamChunk struct {
    Kind         ChunkKind
    ID           string
    Model        string
    Index        int
    Text         string
    ToolCallID   string
    ToolName     string
    ArgsDelta    string
    ArgsComplete string   // 供应商一次给全量参数时用（CC 的 tool-call 事件）
    Signature    string   // thinking 签名
    FinishReason string
    Usage        *Usage
}
```

规约常量（字符串取值必须稳定，是跨协议映射的公共词汇）：

```go
const (
    BlockText = "text"; BlockImage = "image"; BlockToolResult = "tool_result"; BlockThinking = "thinking"
    RoleUser = "user"; RoleAssistant = "assistant"; RoleTool = "tool"
    FinishStop = "stop"; FinishLength = "length"; FinishToolCalls = "tool_calls"
    FinishContentFilter = "content_filter"; FinishError = "error"
)
```

## 4. Codec 接口

```go
package codec

type Codec interface {
    Protocol() ir.Protocol
    DecodeRequest(body []byte) (*ir.ChatRequest, error)
    EncodeRequest(req *ir.ChatRequest) ([]byte, error)
    DecodeResponse(body []byte) (*ir.ChatResponse, error)
    EncodeResponse(resp *ir.ChatResponse) ([]byte, error)
    NewStreamDecoder(r io.Reader) StreamDecoder
    NewStreamEncoder(w io.Writer) StreamEncoder
}

type StreamDecoder interface{ Next() (*ir.StreamChunk, error) }   // io.EOF 结束
type StreamEncoder interface {
    Write(chunk *ir.StreamChunk) error
    Close() error     // 写出终止事件（[DONE] / message_stop / response.completed）
}

func Get(p ir.Protocol) (Codec, error)     // 注册表；未注册返回错误
func Registered() []ir.Protocol            // 排序，便于测试断言
func ConvertRequest(from, to ir.Protocol, body []byte) ([]byte, error)
func ConvertResponse(up, down ir.Protocol, body []byte) ([]byte, error)
```

## 5. 逐字段映射规则

### 5.1 系统提示

| 方向 | 规则 |
| :--- | :--- |
| → IR | Chat：收集 `role=system|developer`；Responses：`instructions`（字符串或 item 数组）；Anthropic：顶层 `system`（字符串或块数组） |
| IR → | Chat：作为 `messages[0]`；Responses：`instructions`；Anthropic：`system` 块数组 |

- 多段 system 合并策略：**按块保留**（Anthropic/Responses 支持块数组），
  Chat 合并为单条文本，块间以 `"\n"` 连接。
- `cache_control` 断点：三方各自编码；Chat 无此概念 → 丢弃并记 warning（除非 `prompt_cache_key` 存在，见 §7.2）。

### 5.2 消息与内容块

| IR | Chat | Responses | Anthropic |
| :--- | :--- | :--- | :--- |
| `text` | `content: "..."` 或 `[{type:text}]` | `input_text` | `{type:text,text}` |
| `image` | `{type:image_url,image_url:{url}}` | `{type:input_image,image_url}` | `{type:image,source:{type:base64|url,...}}` |
| `tool_result` | `{role:tool,tool_call_id,content}` | `{type:function_call_output,call_id,output}` | `{type:tool_result,tool_use_id,content,is_error}` |
| `thinking` | `reasoning_content` | `reasoning.summary` | `{type:thinking,thinking,signature}` |

- **角色收敛**：IR 只有 `user/assistant/tool`；`system/developer` 进 `System`。
- **未知角色**降级为 `user` 并记 warning（不要让上游因校验失败拒绝整条请求）。
- Anthropic 的 `user` 消息可以同时含 `tool_result` 与文本块；转 Chat 时**必须拆成多条**
  （`role=tool` 一条 + `role=user` 一条），这是最常见的丢信息点，必须有测试。

### 5.3 工具

| 维度 | 规则 |
| :--- | :--- |
| 定义 | Chat 嵌套 `function{}` → 扁平；Responses 已扁平；Anthropic `input_schema` |
| 选择 | `auto/none/required` 三态直通；`tool` 带 name |
| 调用参数 | 统一为 **JSON 字符串**（IR `ToolCall.Arguments`）；对象 → `json.Marshal` |
| 流式分片 | Chat/Responses 按 `index` 累积 `arguments`；Anthropic 用 `input_json_delta`；CC 一次给全量 → `ArgsComplete` |

- 工具名改写只在 **CC 适配器**内做（`bash_output→shell_output` 等），IR 里保留原名。
- `parallel_tool_calls`：Chat 有、其它无 → 只在 IR `Metadata` 保留，转出时忽略并记 warning。

### 5.4 采样参数

| IR | Chat | Responses | Anthropic |
| :--- | :--- | :--- | :--- |
| `MaxTokens` | `max_tokens`（或 `max_completion_tokens`） | `max_output_tokens` | `max_tokens`（**必填**，缺则 4096 + warning） |
| `Temperature` | 同名 | 同名 | 同名（范围 0–1，超出截断 + warning） |
| `TopP` | 同名 | 同名 | 同名 |
| `Stop` | `stop`（string 或 []string） | 无 → 忽略 + warning | `stop_sequences` |

### 5.5 完成原因

| IR | Chat `finish_reason` | Responses `status` | Anthropic `stop_reason` |
| :--- | :--- | :--- | :--- |
| `stop` | `stop` | `completed` | `end_turn` |
| `length` | `length` | `incomplete` + `incomplete_details.reason=max_output_tokens` | `max_tokens` |
| `tool_calls` | `tool_calls` | `completed`（有 function_call item） | `tool_use` |
| `content_filter` | `content_filter` | `incomplete` + `content_filter` | `refusal` |
| `upstream_error` | 不写（走错误体） | 不写 | 不写 |

> `pause_turn` 映射为 `length`（at least tells the truth about incompleteness）。

### 5.6 usage 的三方语义（关键）

IR 里的 `Usage` 是**中立且完整**的：`PromptTokens` 是全部输入（含缓存读写），
`CachedTokens` 是缓存读写之和。**各协议"input_tokens"的含义不同，由 codec 负责拆分**：

| 协议 | 输出到 `usage` 的输入值 | 依据 |
| :--- | :--- | :--- |
| OpenAI Chat | `prompt_tokens = PromptTokens`，另给 `prompt_tokens_details.cached_tokens` | 总数 + 细节 |
| OpenAI Responses | `input_tokens = PromptTokens`（含缓存） | 官方口径 |
| Anthropic | `input_tokens = PromptTokens - CachedTokens`（下限 0） | 官方口径：只计非缓存部分 |

> 两处语义**相反**，把其中一个的值直接透给另一个会让下游把缓存算两遍。
> 适配器不参与这个拆分：它只负责给出总量与缓存量。

`CompletionTokens == 0` 时，适配器把输入也归零——上游失败的生成不该计费给下游；
非流式聚合与流式收尾都必须保持这个语义。

## 6. 流式状态机

### 6.1 通用要求

- 解码器按**上游协议**的事件语法解析（SSE `data:` / 命名事件 / NDJSON 单行 JSON）；
- 编码器按**入站协议**的语法写出，绝不泄漏上游专有事件名；
- 每写一个事件后由上层 `Flush()`；
- `ChunkDone` 只能出现在最后：编码器 `Close()` 负责写终止标记（`[DONE]` / `response.completed` / `message_stop`）。

### 6.2 openai_chat 编码器

```
chunk n: data: {"id":"chatcmpl-..","object":"chat.completion.chunk","created":..,
                "model":"..","choices":[{"index":0,"delta":{"content":".."},"finish_reason":null}]}
最后:   data: {"..","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{...}}
        data: [DONE]
```

- 首个 chunk 带 `delta.role="assistant"`；
- 工具调用 chunk：`delta.tool_calls[{index,id,type:"function",function:{name,arguments}}]`，
  后续只带 `arguments` 增量；
- `usage` 只在最后一个 chunk（或 `stream_options.include_usage` 时单独一帧）。

### 6.3 openai_responses 编码器

命名事件序列：

```
response.created → response.in_progress
response.output_item.added (message|function_call)
response.content_part.added
response.output_text.delta  ×N
response.content_part.done → response.output_item.done
response.completed  (含 usage 与 status)
```

- 每个事件带单调递增 `sequence_number`；
- `output_index` / `content_index` / `item_id` 必须稳定；
- 工具调用走 `function_call_arguments.delta`。

### 6.4 anthropic 编码器

```
message_start       {message:{id,type:"message",role:"assistant",content:[],model,usage:{input_tokens,output_tokens:0}}}
content_block_start {index:0, content_block:{type:"text",text:""}}
content_block_delta {index:0, delta:{type:"text_delta",text:".."}}
content_block_stop  {index:0}
... 工具: content_block_start(tool_use) → input_json_delta ×N → content_block_stop
message_delta       {delta:{stop_reason:"end_turn",stop_sequence:null}, usage:{output_tokens:N}}
message_stop
```

- `ping` 事件可穿插（心跳），不参与语义；
- thinking：`content_block_start(type:"thinking")` → `thinking_delta` → `signature_delta` → stop；
- 假签名：CC 不给签名时用 `sha256(文本)[:32]` 生成，形状合法即可。

### 6.5 解码器状态机（以 Anthropic 为例）

```
[message_start] → 记录 id/model/input_tokens → ChunkStart
[content_block_start text] → 记录 index
[content_block_delta text_delta] → ChunkText
[content_block_start tool_use] → 记录 index/id/name
[content_block_delta input_json_delta] → ChunkToolCall{ArgsDelta}
[content_block_stop] → （若 tool_use）ChunkToolCall{ArgsComplete=""} 收尾
[message_delta] → ChunkUsage(只 output) + ChunkFinish(stop_reason)
[message_stop] → ChunkDone
```

## 7. Command Code 事件桥（`ProtoVendorNative`）

CC 的上游不是标准协议，桥接实现在 `provider/command_code/events.go`，但它**产出的就是 IR Chunk**，
所以下游侧完全复用三个编码器。

### 7.1 事件映射

| CC NDJSON 事件 | IR Chunk |
| :--- | :--- |
| （首个事件前） | `ChunkStart{ID: 生成, Model: 请求模型}` |
| `text-delta` | `ChunkText{Text: text\|delta}` |
| `reasoning-delta` | `ChunkThinking{Text}` |
| `tool-call` | `ChunkToolCall{ArgsComplete: argumentsJSON(input)}` |
| `finish` / `finish-step` | `ChunkUsage{TotalUsage}` + `ChunkFinish{MapFinishReason(reason)}` |
| `error` | 由 `QuotaPolicy` 判定；非额度错误 → 流内错误帧（502 语义） |

### 7.2 缓存断点

CC 支持 `cache_control: {type:"ephemeral"}`。规则：

- 下游显式给了 cache 标记 → 保留；
- 下游给了 `prompt_cache_key` 且无任何标记 → 给**最后一个 system 块**加标记
  （它对应 OpenAI 的 cached prefix 语义）；
- 都没有 → 不加。

### 7.3 非流式聚合

CC 恒流式，非流式请求由网关聚合：

1. 收到 `ChunkStart` → 分配 `chatcmpl-` / `msg_` / `resp_` id（按入站协议）；
2. 累积文本、推理、工具调用（按 id 索引，`ArgsComplete` 直接采用）；
3. `ChunkFinish` 记录 finishReason 与 usage；
4. 结束后：
   - 无 finish 事件 → **502 截断**；
   - finish 无原因 / 原因 `upstream_error` → **502**；
   - 输出 token 为 0 且无内容 → **429**（让 SDK 重试）；
   - 否则按入站协议编成单条响应体。

## 8. 降级与告警清单（`warnings[]`）

| 场景 | 处理 |
| :--- | :--- |
| Chat → Anthropic，无 `max_tokens` | 填 4096，warning |
| Anthropic `stop_reason=tool_use` → Chat 无工具定义 | warning，照常输出 `tool_calls` |
| Responses `stop` 序列 → Chat | 忽略，warning |
| Chat `response_format` / Responses `text.format` | **上游不支持时不静默丢弃**：在开流前 400 并带 `param`，让客户端走提示词降级 |
| Anthropic `temperature > 1` | 截断到 1，warning |
| 未知 finish_reason | 原样透传（不折叠成 `stop`） |
| 图片 URL 无法访问 | 原样透传，由上游报错 |

## 9. 测试矩阵

| # | 入站 → 出站 | 请求 | 非流式 | 流式 |
| :-: | :--- | :-: | :-: | :-: |
| 1 | chat → chat（透传） | ✅ | ✅ | ✅ |
| 2 | chat → responses | ✅ | ✅ | ✅ |
| 3 | chat → anthropic | ✅ | ✅ | ✅ |
| 4 | responses → chat | ✅ | ✅ | ✅ |
| 5 | responses → anthropic | ✅ | ✅ | ✅ |
| 6 | anthropic → chat | ✅ | ✅ | ✅ |
| 7 | anthropic → responses | ✅ | ✅ | ✅ |
| 8 | responses → responses（透传） | ✅ | ✅ | ✅ |
| 9 | anthropic → anthropic（透传） | ✅ | ✅ | ✅ |
| 10 | （适配器事件桥）NDJSON → 每个出站协议 | — | ✅ | ✅ |

> 实现在 `internal/gateway/codec/convert_test.go`（9 × 请求/非流式）与
> `stream_test.go`（9 × 流式回环 + 线上帧形态 + 截断检测）。

附加用例：

- 工具调用三态互转（含并行调用、参数分片、`tool_result` 拆分）；
- 多模态图片三态互转；
- 系统提示块数组与 cache 断点；
- 空 system + `emptySystemPlaceholder`；
- 截断流 / 零输出 / 上游错误 三种异常路径在**每个**出站协议下的表现。

约定：表驱动 + 黄金报文（`testdata/golden/`），断言「事件序列」而不只是「最终 JSON」。

## 10. 落地备注（实现后补充）

以下是实现时不得不做、且值得记住的取舍：

| 主题 | 取舍 |
| :--- | :--- |
| Responses 的思考内容 | 请求/响应都用独立 `reasoning` item，`summary[].text` 承载文本；没有摘要的 reasoning item 解码时忽略 |
| Responses 的 finish | 协议没有 `tool_calls` 状态：完成态里出现 `function_call` 就推断为 `tool_calls` |
| Chat 的 usage | 流式请求自动加 `stream_options.include_usage`，否则末帧拿不到用量 |
| Anthropic 的 `max_tokens` | 请求缺失时补 4096 并记 warning；编码时同样兜底 |
| Anthropic 的 `tool_choice` | 协议没有 `none`；转换时该模式被省略（上游按其默认 auto 处理） |
| 工具结果归属 | 解码时把 `tool_result` 拆成独立 `role=tool` 轮次，编码时再按协议合回去 |
| 流式终止 | 三个编码器的 `Close()` 都幂等：`Done` 已写终止帧时不会写第二份 |
