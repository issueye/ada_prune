# 03 · 供应商适配器规格（核心）

| 项目 | 内容 |
| --- | --- |
| 文档编号 | DOC-03 |
| 版本 | v1.1 |
| 日期 | 2026-11-07 |
| 上游 | [01 §3.2/3.7/3.8](01-requirements.md) · [02 §7](02-architecture.md) |
| 代码位置 | `internal/gateway/adapter.go` · `internal/provider/*` |

---

## 1. 为什么需要适配器层

网关注定要面对两种上游：

- **标准供应商**：OpenAI / Anthropic / 各种兼容网关。它们的请求体就是协议本身，
  网关只要「换个 base URL + 换个鉴权头」即可；
- **私有供应商**：典型是 **Command Code**。它不说 OpenAI 协议，而是：
  - 需要一个**信封**（`config / memory / taste / skills / permissionMode / mode / params`）；
  - 需要**设备指纹**（首次使用后还要主动上报，并周期性刷新）；
  - 需要**会话 id**（`x-session-id` / `threadId`，且只有 UUID 才能进 `threadId`）；
  - 响应是 **NDJSON 事件流**（`text-delta` / `tool-call` / `finish` / `usage`…），不是 SSE；
  - 额度语义是**信用池 + 5 小时/周窗口**，402 与 429 含义与标准完全不同。

如果把上面这些写进网关主干，主干就会被一家供应商绑死。
**适配器层的存在意义：主干只认 IR，所有私有语义都关在 `internal/provider/<kind>` 里。**

### 1.1 分层职责表

| 层 | 知道什么 | 不知道什么 |
| :--- | :--- | :--- |
| `gateway/ir` | 消息/块/工具/usage/finish | 任何供应商 |
| `gateway/codec` | 三种下游协议 ↔ IR | 上游是谁 |
| `gateway/router` | 供应商/凭据/模型/轮转 | 上游 wire 细节 |
| `gateway/relay` | 编排、计时、记账、错误回写 | 上游 wire 细节 |
| `provider/<kind>` | 上游 wire、鉴权、指纹、额度 | 下游协议、数据库 schema |

> **`Kind` 的取值不在 `gateway` 里。** `gateway.Kind` 只是一个字符串类型，
> 具体常量（`provider.KindCommandCode` 等）定义在 `internal/provider/kinds.go`，
> 这样主干永远不需要知道任何一家供应商的名字。

## 2. 适配器接口

```go
package gateway

// Kind 是适配器类型标识，落在 provider.kind 列，也是注册表的键。
// 取值由 internal/provider/kinds.go 定义。
type Kind string

// Adapter 是供应商协议的实现。实现必须并发安全、无全局可变状态。
type Adapter interface {
    Kind() Kind
    Capabilities() Capabilities

    // ValidateConfig 校验 provider.adapter_config，返回可读的字段错误。
    ValidateConfig(cfg ProviderConfig) error

    // Do 把 IR 请求编码为上游 wire 并发送。返回的 *http.Response 由调用方负责关闭。
    // 非 2xx 不作为 error 返回：由 QuotaPolicy/错误映射统一判定。
    Do(ctx context.Context, in *InvokeRequest) (*http.Response, error)

    // DecodeResponse 把上游非流式响应体解回 IR。
    DecodeResponse(body []byte, in *InvokeRequest) (*ir.ChatResponse, error)

    // DecodeStream 把上游流式响应体解回 IR 增量流。
    DecodeStream(r io.Reader, in *InvokeRequest) StreamDecoder
}

// StreamDecoder 逐个产出 IR 增量；io.EOF 表示正常结束。
// 实现方必须处理「流被截断」的情形：由上层根据 chunk 序列判定不完整。
type StreamDecoder interface {
    Next() (*ir.StreamChunk, error)
}

// InvokeRequest 是一次调用的完整上下文（适配器只读）。
type InvokeRequest struct {
    Provider   ProviderConfig   // base_url / extra_headers / adapter_config / timeout / proxy
    Credential Credential       // 明文 secret + id/name（仅内存）
    Model      ModelRef         // upstream_name + upstream_protocol + max_tokens 等
    Request    *ir.ChatRequest  // 规约请求
    Session    SessionHints     // 下游传入的会话线索（见 §5）
    // OnWire 可选：拿到最终发送的上游请求体（测试台/调试用，禁止默认落日志）
    OnWire func(path string, body []byte, headers http.Header)
}
```

### 2.1 能力声明

```go
type Capabilities struct {
    Stream          bool   // 支持流式
    Tools           bool   // 支持工具调用
    Vision          bool   // 支持图片输入
    ModelDiscovery  bool   // 支持从上游拉模型列表
    AccountUsage    bool   // 支持查询账号用量/额度
    Fingerprint     bool   // 需要设备指纹
    SessionAffinity bool   // 需要会话亲和（同凭据复用会话 id）
    Quota           bool   // 有额度语义（需 QuotaPolicy）
    ProtocolVersion string // 适配器实现的上游协议版本
}
```

能力位在三个地方使用：

1. **控制面**：模型表单里「支持流式/工具/视觉」的默认值、供应商详情页显示哪些面板；
2. **数据面**：入站请求要求流式但适配器不支持 → 立刻 400，不发上游；
3. **路由器**：模型声明 `supports_tools` 而适配器不支持工具 → 建模时告警。

> 判定顺序：**适配器能力 ∧ 模型能力**。两者有一为假，就按不支持处理。

### 2.2 可选能力接口

按 Go 惯例用类型断言探测，避免强迫所有适配器实现空方法。

```go
// Prober 提供连通性测试。
type Prober interface {
    Probe(ctx context.Context, in ProbeRequest) (ProbeResult, error)
}

// ModelDiscovery 提供上游模型目录。
type ModelDiscovery interface {
    DiscoverModels(ctx context.Context, in ProbeRequest) ([]DiscoveredModel, error)
}

// AccountUsageProvider 提供「账号维度」的用量/额度面板。
type AccountUsageProvider interface {
    AccountUsage(ctx context.Context, in ProbeRequest) (any, error)
}

// StatusProvider 提供运行时状态（漂移检测、上报结果、窗口阻塞…）。
type StatusProvider interface {
    Status(ctx context.Context, cfg ProviderConfig) any
}

// Sweeper 由 app 的后台任务周期性调用（会话过期、缓存清理）。
type Sweeper interface {
    Sweep(now time.Time) []int64
}

// ConfigSchemaProvider 让控制面渲染动态表单。
type ConfigSchemaProvider interface {
    Schema() []ConfigField
}

// AccessModeProvider 声明接入方式分类；不实现则默认 api。
type AccessModeProvider interface {
    AccessMode() AccessMode
}

type ConfigField struct {
    Key         string   `json:"key"`
    Label       string   `json:"label"`
    Type        string   `json:"type"`     // string | int | bool | select | textarea
    Default     any      `json:"default"`
    Options     []string `json:"options,omitempty"`
    Required    bool     `json:"required"`
    Secret      bool     `json:"secret"`   // true 表示出参掩码
    Description string   `json:"description"`
}
```

**接入方式分类（`AccessMode`）**：`gateway.AccessMode` 是中立枚举——
`api`（持密钥直连供应商 API）与 `reverse_proxy`（经逆向的客户端/服务中转）。
分类是适配器的固有属性：不实现 `AccessModeProvider` 的适配器默认 `api`；
反代适配器（如 `command_code`）在**自己的包**里声明 `reverse_proxy`。
注册表把它写进 `Descriptor.accessMode` 并镜像到供应商出参，控制面据此分组展示；
**不入库**，随 kind 派生，kind 不可变意味着分类也不可变。

### 2.3 注册表

```go
package provider

type Factory func(deps Deps) (gateway.Adapter, error)

func Register(kind gateway.Kind, label string, f Factory)
func Build(d Deps) *Registry

func (r *Registry) Get(kind gateway.Kind) (gateway.Adapter, bool)   // 单例：适配器必须无状态/并发安全
func (r *Registry) Kinds() []gateway.Kind
func (r *Registry) Descriptors() []Descriptor                       // 控制面表单的数据源
```

`Deps` 只放**适配器真正需要、且与 store 无关**的东西：

```go
type Deps struct {
    Log   *logging.Logger
    HTTP  *http.Client   // 共享连接池（关闭空闲连接由 app 负责）
    State gateway.StateStore  // provider_state KV（适配器私有，见 §7）
    Clock func() time.Time
    Rand  io.Reader
}
```

**注册失败不阻断启动**：某个适配器 `New` 返回错误时，`Build` 记日志并跳过它，
其余适配器照常工作——一家供应商的配置损坏不该让整个网关起不来。

## 3. Command Code 适配器（`provider/command_code`）

### 3.1 模块拆分

| 文件 | 职责 |
| :--- | :--- |
| `adapter.go` | `Adapter` 实现：Do / DecodeResponse / DecodeStream / Probe / Status；组合下面各文件 |
| `envelope.go` | IR → `CCRequest` 信封（键序固定） |
| `wire.go` | 上游请求头（`x-command-code-version`、`x-cli-environment`、`traceparent`…）与发送 |
| `events.go` | NDJSON 事件解析 → IR Chunk |
| `fingerprint.go` | 指纹派生（决定论） |
| `report.go` | 指纹 / 生命周期上报与调度 |
| `session.go` | 会话 id 派生与 TTL |
| `quota.go` | 额度分类（402 / 窗口 429）与 `QuotaPolicy` |
| `models.go` | 内置模型目录 + 动态拉取 |
| `accountusage.go` | whoami / credits / subscriptions / summary |
| `drift.go` | 协议漂移检测 |
| `config.go` | `adapter_config` 定义与校验（`ConfigSchema`） |

### 3.2 `adapter_config` 字段

```jsonc
{
  "apiBase": "https://api.commandcode.ai",   // 上游根地址（也可用 provider.base_url，二选一，base_url 优先）
  "protocolVersion": "1.53.1",               // 声明版本；空则用适配器常量
  "cliMode": "agent",                        // 信封 mode
  "cliSessionMode": "interactive",           // 生命周期上报 metadata.mode
  "projectDir": "C:\\Users\\dev\\projects\\app", // 伪工作目录
  "fingerprintSalt": "",                     // 空=用官方固定盐派生默认设备
  "emptySystemPlaceholder": true,            // 无 system 时塞一个空格（省 7.5K token）
  "zdr": false,                              // x-cmd-zdr: 1
  "sessionTtlHours": 12,
  "reportRefreshHours": 8,
  "modelCatalog": "builtin|dynamic|hybrid"    // 默认 hybrid
}
```

- 所有键都有默认值，缺省即默认；未知键保留（向前兼容）。
- `ValidateConfig` 只校验类型与取值范围，不联网。
- 该 JSON 由 `ConfigSchema()` 声明，控制面据此动态渲染表单（[05 §2.2](05-api-contract.md)）。

### 3.3 请求编码（信封）

信封的**键序与字段语义是上游契约**，用 `testdata/golden/envelope.json` 锁定。要点：

1. **字段顺序固定**：`config, memory, taste, skills, permissionMode, threadId, mode, params`；
   `encoding/json` 按 struct 顺序输出，因此 struct 定义不可重排。
2. **system 独立成块数组**：`params.system[]`，块间以 `"\n"` 连接（除最后一块）；
   `prompt_cache_key` 且无 cache 标记时，给最后一块加 `{"type":"ephemeral"}`。
3. **空 system 占位**：`emptySystemPlaceholder=true` 时写 `" "`，避免上游注入约 7.5K token 的默认提示。
4. **会话**：`x-session-id` 头总是带；`threadId` 仅当 session 是 UUID 时才写（否则省略）。
5. **工具名改写**：`bash_output→shell_output`、`task_output→shell_output`、
   `tool_search→search_tools`、`read_multiple_files→read_file`。
   tool-call 与 tool-result **两侧都要改写**。
6. **max_tokens**：默认 64000，上限 200000；请求未给时用模型配置，再退默认值。
7. **`stream` 恒为 true**：CC 上游总是流式；非流式由网关聚合。

上游头（`wire.go`）：

| 头 | 值 |
| :--- | :--- |
| `Content-Type` | `application/json` |
| `User-Agent` | `cli` |
| `x-command-code-version` | 适配器声明的版本 |
| `x-cli-environment` | `production` |
| `x-project-slug` | slugify(projectDir) |
| `x-taste-learning` | `false` |
| `x-session-id` | 会话 id |
| `Authorization` | `Bearer <secret>` |
| `traceparent` | `00-<16B hex>-<8B hex>-01` |
| `x-cmd-zdr` | `1`（仅 ZDR 开启时） |

### 3.4 响应解码（NDJSON 事件桥）

CC 响应是 **NDJSON**（每行一个 JSON 对象），不是 SSE。事件 → IR 的映射：

| CC 事件 | 字段 | IR |
| :--- | :--- | :--- |
| `text-delta` | `text` / `delta` | `ChunkText` |
| `reasoning-delta` | 同上 | `ChunkThinking` |
| `tool-call` | `toolCallId` / `toolName` / `input` | `ChunkToolCall`（**完整一次**，`ArgsComplete`） |
| `finish` / `finish-step` | `finishReason` / `rawFinishReason` / `totalUsage` | `ChunkUsage` + `ChunkFinish` |
| `error` | `error.message/code/statusCode/rateLimit` | `*StreamError`（带额度判定） |

**finish 语义（关键）**：

- 有效原因取 `rawFinishReason ?? finishReason`；`finishReason=="other"` 且无 raw → 视为**无信号**；
- 流结束但**没见到 finish 事件** → 判定为截断 → 502（可重试），**绝不补 `stop`**；
- finish 见到但原因为空 → 同样视为截断；
- 原因含 `network-*` / `connection-*` / `upstream-error` → `upstream_error` → 502；
- 输出 token 为 0 → 上层转 429（让下游 SDK 自动重试）。

**usage 语义（关键）**：

- 适配器统一输出 `PromptTokens = 全部输入（含缓存）`、`CachedTokens = 缓存读写之和`；
- **按入站协议做拆分是 codec 的职责**（[04 §5.6](04-protocol-conversion.md)）：
  Anthropic 只需非缓存部分、Responses 要含缓存，两处语义相反；
- `outputTokens == 0` 时把 input 归零（上游失败的生成不该计费给下游）。

### 3.5 指纹（`FingerprintPolicy` 实现）

```go
// FingerprintPolicy 是通用接口：不适用的供应商不实现即可。
type FingerprintPolicy interface {
    Derive(cfg ProviderConfig, cred Credential) Identity  // 决定论：同凭据同配置 → 同身份
    Schedule() ReportSchedule
    Report(ctx context.Context, in FingerprintRequest) ReportResult
}

type Identity struct {
    Thumbmark  string
    Components map[string]any   // 平台/CPU/内存/时区/网卡哈希…
}
```

> `Derive` 接受 `ProviderConfig` 而不是在适配器里缓存盐：指纹盐属于供应商配置，
> 放进可变状态会在并发下串味。

CC 实现要点（算法是上游契约，必须精确复现）：

1. `FingerprintSalt = "command-code:device-fingerprint:v1"` 是官方固定盐；
   可配置的 `fingerprintSalt` 只决定**派生哪台伪设备**，不改变哈希算法。
2. 逐字段 `sha256(salt \0 secret \0 field)`；池内选择用「摘要最大者」而不是取模，
   这样往池子里加一项只影响真正命中的凭据。
3. 派生字段：`machineId`（UUID 形态）、`macs`（2–5 张，排序）、`hostname`（`DESKTOP-XXXX`）、
   `osUser`、`gitEmail`、`cpuModel/cores`、`memGiB`、`timezone`、`platform/arch/osRelease`。
4. `thumbmark = sha256(SALT \0 "machine" \0 join([machineId, macs], ","))`。
5. 上报组件是**信号的哈希**：`sha256(SALT \0 lower(value))`，空值不出现。
6. **同凭据必同设备**：身份不落库（可从密钥重算），但上报结果落 `provider_state`。
7. 上报：`POST /alpha/fingerprint/record` + `POST /alpha/lifecycle-events`
   （后者 `eventType: "cli_session_exists"`），两者并发，任一失败则 1–5 分钟后重试；
   全部成功则 8h + rand(0..2h) 后再来。
8. 金标准：`testdata/golden/fingerprint.json` 与 `fingerprint-salted.json` 必须逐字节一致。

### 3.6 会话（`SessionHints` + 亲和）

- 下游显式给了 `x-session-id` / `x-claude-code-session-id` / `session_id`（长度 ≥ 8）→ 直接沿用；
- 否则下游给了 `prompt_cache_key`（长度 ≥ 8）→ 用它；
- 否则按凭据取/建会话：TTL 12h + rand(0..1h)，过期重建；
- 会话随凭据隔离：换凭据 = 换会话 = 换设备，**同一请求内轮转后必须重新派生**。

### 3.7 额度（`QuotaPolicy` 实现）

```go
type QuotaPolicy interface {
    Classify(status int, body []byte) QuotaVerdict
    RotateOn(kind QuotaKind) bool
}

type QuotaKind string
const (
    QuotaNone      QuotaKind = "none"
    QuotaCredit    QuotaKind = "credit"      // 信用耗尽 → 落库 exhausted
    QuotaWindow    QuotaKind = "window"      // 窗口到顶 → 内存阻塞到 Until
    QuotaRateLimit QuotaKind = "rate_limit"  // 瞬时限速 → 不轮转
)
```

CC 判定规则：

| 条件 | Kind | 动作 |
| :--- | :--- | :--- |
| HTTP 402 | `credit` | 凭据 `status=exhausted`，轮转 |
| HTTP 429 + `rateLimit.window` | `window` | 内存阻塞到 `until`，轮转 |
| HTTP 429 + body 含 `USAGE_EXCEEDED` | `window`（window=`usage`） | 同上 |
| HTTP 429 其它 | `rate_limit` | 不轮转，原样返回（带 `retry_after`） |
| 事件 `error` 携带 `statusCode`/`<NNN>` 前缀 | 按上表 | 同上 |
| 网络错误 / 5xx | `none` | 502/透传，不轮转 |

其它适配器：`openai_compatible` 只有 `rate_limit`（429）与 `none`；
`anthropic` 额外识别 `overloaded_error`（529 → `rate_limit`，不轮转）。

### 3.8 模型目录

- 内置目录（67 个模型 + 最低套餐档位）作为**候选**，不直接成为网关模型；
  用户在「模型管理」里一键导入，导入时自动填 `upstream_name` 与默认价格 0。
- 动态目录：`GET {base}/provider/v1/models`（仅高套餐可用），TTL 5 分钟，
  失败静默回退内置目录（`hybrid`）；`dynamic` 模式则如实报错。
- 刷新结果落内存缓存与 `provider_state`。

### 3.9 账号用量（可选能力）

四连请求：`whoami?limits=1` → `credits` ∥ `subscriptions` → `usage/summary`，
结果按凭据缓存（默认 5 分钟），控制面可手动刷新单个/全部。
纯读缓存：**快照接口不触网**，只有显式刷新才打上游。

### 3.10 漂移检测

`GET https://registry.npmjs.org/command-code/latest` 比对 `protocolVersion`。
**只告警不改版本**：宣称一个自己不会说的版本本身就是可疑信号。

### 3.11 CLI 登录导入

适配器实现 `CLILoginPath() string` 时，控制面提供「一键导入」：
读取 `~/.commandcode/auth.json`，取出 `apiKey`/`token`/`key`，去重后落库为凭据。

## 4. 标准供应商适配器

两个标准适配器都经 `provider.HTTPClientFor(a.http, in.Provider.ProxyURL)` 选客户端：
供应商级 `proxyUrl` 为空时直接用共享客户端；非空时按「代理地址 + 超时」缓存一个
`http.Transport`（连接池复用），改代理地址下一次请求即生效，无需重启。

### 4.1 `openai_compatible`

- `Do`：`{base}/v1/chat/completions`（或模型 `upstream_protocol=openai_responses` 时 `/v1/responses`），
  `Authorization: Bearer <secret>` + `extra_headers`；
- 请求：直接用 `codec/openai_chat`（或 responses）把 IR 编成标准体；
- 响应：用同一 codec 解回 IR；流式按 SSE 解析；
- `Capabilities`：全开（除 `Fingerprint`/`SessionAffinity`/`AccountUsage`）；
- `QuotaPolicy`：429 → `rate_limit`（不轮转），401/403 → `none`（授权错误，直接透传）。

### 4.2 `anthropic`

- `Do`：`{base}/v1/messages`，`x-api-key: <secret>` + `anthropic-version: 2023-06-01`；
- 请求/响应走 `codec/anthropic`；流式命名事件（`message_start`/`content_block_delta`/…）；
- `max_tokens` 必填：IR 未给时用模型 `max_tokens`，再退 4096，并记 warning。

### 4.3 `custom`（预留）

用于「用户自己写一个 Go 文件注册适配器」的场景：
`provider/registry.go` 暴露 `Register`，用户在 `internal/provider/custom` 里实现并存根即可。
本期只留常量与文档。

## 5. SessionHints 与下游线索

```go
type SessionHints struct {
    SessionID       string  // 下游显式会话（头）
    PromptCacheKey  string  // OpenAI 的 prompt_cache_key
    ClientKeyID     int64
    RequestID       string
}
```

适配器可自由使用（CC 用它生成 `x-session-id`/`threadId`），其它适配器可忽略。

## 6. 「新增一个供应商」的标准步骤

1. 新建 `internal/provider/<kind>/`，实现 `Adapter`（+ 按需 `Prober`/`ModelDiscovery`/…）；
2. 在 `config.go` 里定义 `adapter_config` 字段与 `Schema()`；
3. 在 `init()` 里 `provider.Register(provider.KindXxx, "Label", New)`；
4. 若需要指纹/额度语义，实现 `FingerprintPolicy`/`QuotaPolicy` 并在 `Capabilities` 里声明；
   若是反代接入，实现 `AccessModeProvider` 声明 `reverse_proxy`（不实现默认 `api`）；
5. 补单测：`httptest` 假上游 + 请求体断言 + 事件/响应解码断言；
6. 控制面零改动（动态表单 + 状态面板自动出现）。

> 验收：`internal/gateway` 与 `internal/api` **不产生任何 diff**。

### 6.1 已实现适配器

| kind | 接入方式 | 状态 | 说明 |
| :--- | :--- | :--- | :--- |
| `command_code` | 反代 | ✅ P2 | 信封/指纹/上报/会话/窗口/事件桥/模型目录/账号用量/漂移 |
| `openai_compatible` | API | ✅ P6 | 复用 chat/responses codec；自定义鉴权头；模型列表探活与发现 |
| `anthropic` | API | ✅ P6 | 原生 Messages；`max_tokens` 兜底；400 视为探活成功 |
| `custom` | — | ⏳ 预留 | 用户自行实现并 `Register` |

## 7. 适配器私有状态（`provider_state`）

- 表：`provider_state(provider_id, key, value, updated_at)`，`value` 为 JSON 文本。
- 用途：CC 的指纹上报结果、下次上报时间、协议漂移结果、动态模型目录缓存、账号用量缓存元信息。
- API：`StateStore.Get/Set/Delete(providerID, key, v any)`，实现方负责序列化。
- **禁止**把凭据明文、完整上游报文写进这里。

## 8. 错误映射

适配器只负责「识别语义」，映射到下游协议的统一由 `gateway/errors.go` 完成：

| 上游情形 | 网关 HTTP | 下游 `type` | 备注 |
| :--- | :--- | :--- | :--- |
| 400 参数错 | 400 | `invalid_request_error` | 保留上游 message |
| 401/403 授权 | 401 | `authentication_error` | 沿用一个 401，不泄漏差别 |
| 402 信用耗尽 | 429 | `rate_limit_error` | message 明确「不是瞬时限速」 |
| 404 模型不存在 | 404 | `not_found` | |
| 429 限速/窗口 | 429 | `rate_limit_error` | 带 `retry_after` |
| 5xx / 网络 | 502 | `upstream_error` | 可重试；`retry_after: 10` |
| 流被截断 | 502 | `upstream_error` | 绝不谎报 `stop` |
| 零输出 | 429 | `rate_limit_error` | 让 SDK 自动重试 |
| 入站报文非法 | 400 | `invalid_request_error` | 带 `param` |

## 9. 测试矩阵（适配器层）

| 用例 | 断言 |
| :--- | :--- |
| 信封金标准 | `golden/envelope.json` 逐字节一致（含键序、工具名改写、tool-result 形状） |
| 指纹金标准 | `golden/fingerprint.json`、`fingerprint-salted.json` 一致 |
| 指纹性质 | 同凭据决定论、不同凭据不同设备、盐改变设备不改平台档案、哈希公式 |
| 会话派生 | 同一凭据两次调用会话相同；换凭据后不同；下游显式头优先；TTL 过期 |
| NDJSON 解析 | 文本/推理/工具/结束/usage 事件正确映射；未知事件忽略；大行不截断 |
| 截断流 | 无 finish → 错误；finish 无原因 → 错误；`network-error` → 错误 |
| 402 轮转 | 分类为 `credit` 且 `RotateOn` 为真 |
| 窗口 429 | 分类为 `window` 且带恢复时间；瞬时限速为 `rate_limit` 且不轮转 |
| usage 语义 | 适配器输出总量；非流式聚合正确 |
| 模型目录 | 动态成功走动态；hybrid 失败回退内置；dynamic 失败如实报错 |
| 注册表 | 失败适配器被跳过、描述符含 schema、重复注册 panic |
