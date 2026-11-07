# 01 · 需求分析与验收标准

| 项目 | 内容 |
| --- | --- |
| 文档编号 | DOC-01 |
| 版本 | v1.0 |
| 日期 | 2026-11-07 |
| 上游 | 用户需求（通用本地 AI 网关）· 设计参考 [`aluka_cloud/llm`](file:///E:/codes/go_projects/aluka_cloud/llm) |
| 下游 | [02 架构](02-architecture.md) · [03 供应商适配器](03-provider-adapter.md) · [04 协议转换](04-protocol-conversion.md) · [05 接口契约](05-api-contract.md) · [06 UI 计划](06-ui-plan.md) · [07 路线图](07-roadmap.md) |

---

## 1. 产品定位

**AdaProxy 是一个跑在本机的通用型 AI 网关**：一个单可执行文件，一个本地 SQLite，
一张桌面原生界面（Go 自绘，无 webview、无前端构建），把「多种上游 AI 供应商」统一成
「一套 OpenAI / Anthropic 兼容端点」。

> 本项目**从零重写**，不继承任何历史数据库、配置、HTTP 路径或环境变量；
> 参考项目的协议知识被固化为规格，重新实现。

它要解决的问题有三层：

1. **接入统一**：本地所有工具（Codex、Claude Code、Cline、Continue、自研脚本）都只需要认
   三种标准协议，不再为每家供应商改一遍配置；
2. **凭据统一**：供应商密钥、账号池、客户端密钥集中管理，谁用了多少、花在哪、什么时候失效，一目了然；
3. **差异抽象**：某些供应商（典型如 **Command Code**）并不说标准协议——它有自己的请求信封、
   设备指纹、生命周期上报、窗口额度语义。这些差异必须被**收敛在一个适配器里**，
   而不是泄漏到网关主干、更不是让每个下游客户端各自处理。

### 1.1 设计原则

| # | 原则 | 含义 |
| :-: | :--- | :--- |
| P1 | **主干协议中立** | 网关内部只认规约 IR；任何供应商私有的字段不出现在 `internal/gateway` 主干 |
| P2 | **适配器负责一切上游怪癖** | 信封、鉴权头、指纹、上报、额度语义、错误映射，全部在适配器内完成 |
| P3 | **能力显式声明** | 适配器用 `Capabilities` 声明支持什么（流式/工具/视觉/额度查询/模型发现），主干预先校验 |
| P4 | **失败可降级** | 存储不可用降级内存、上游模型目录不可用回退内置、上报失败只重试不阻断请求 |
| P5 | **单二进制、本地优先** | 不允许引入需要单独部署的中间件；数据只落在本机 `data/` |
| P6 | **契约先行** | 接口先改 [DOC-05](05-api-contract.md)，代码随后 |

## 2. 术语

| 术语 | 英文 / 字段 | 说明 |
| :--- | :--- | :--- |
| 供应商 | Provider | 一个上游 AI 服务（OpenAI、Anthropic、Command Code、任意兼容网关） |
| 适配器 | Adapter | 供应商的**协议实现**：把 IR 变成该上游能懂的请求，并把回答翻回 IR |
| 适配器类型 | `provider.kind` | `command_code` / `openai_compatible` / `anthropic` / `custom`，决定用哪个适配器 |
| 凭据 | Credential | 挂在供应商下的一枚上游密钥/账号，可多枚，支持轮转 |
| 指纹策略 | FingerprintPolicy | 适配器可选实现：为每枚凭据派生稳定的伪设备身份（仅 Command Code 需要） |
| 额度策略 | QuotaPolicy | 适配器可选实现：解释上游「没钱了/超额了」并给出恢复时间 |
| 模型 | Model | 供应商下的可调用模型：对外名、上游名、协议、价格、能力 |
| 路由模型 | RouteModel | 把多个模型聚合成一个对外名，按权重/优先级选择（可选能力） |
| 客户端密钥 | ClientKey | 网关签发给下游工具的密钥 `sk-adp-*`，只存哈希 |
| 端点 | Endpoint | 对外路径 + 入站协议 + 目标模型（内置三条 + 可选自定义） |
| 入站协议 | Inbound Protocol | 下游使用的协议：`openai_chat` / `openai_responses` / `anthropic` |
| 上游协议 | Upstream Protocol | 适配器实际使用的协议，由 `model.upstream_protocol` + `provider.kind` 决定 |
| IR | Canonical IR | 协议转换的规约中间表示 |
| 信封 | Envelope | Command Code 特有的请求外壳（`config/memory/taste/skills/params`） |
| 窗口 | Window | Command Code 的限流窗口（5 小时 / 周 / 信用），到点自动恢复 |

## 3. 功能需求

### 3.1 F1 供应商管理（Provider Management）

- 增删改查；字段：
  - `name`（唯一）、`kind`（适配器类型）、`base_url`、`enabled`、`weight`、
  - `timeout_ms`、`proxy_url`（http/https/socks5）、`extra_headers`（JSON）、
  - `adapter_config`（JSON，适配器私有配置，见 §3.2）、`remark`。
- **适配器类型选择器**：新建供应商时先选 `kind`，界面只显示该类型需要的字段
  （例如 Command Code 才显示「项目目录 / 指纹盐 / CLI 模式 / ZDR」）。
- **连通性测试**：用该供应商下任一启用凭据发起一次最小探活
  （优先适配器自带的 `Probe`，否则 `GET {base}/v1/models`，再退化为 HTTP 可达性），
  返回耗时、HTTP 状态、原文摘要（截断 200 字符）。
- **模型发现**：适配器实现 `ModelDiscovery` 时，控制面提供「从上游拉取模型」，
  拉取结果作为**候选**，一键导入为 `model`（按 `upstream_name` 去重）。
- 停用供应商 → 其下模型在数据面**不可路由**；拥有启用模型的供应商**禁止删除**（返回 409）。

### 3.2 F2 适配器配置与私有状态

- `provider.adapter_config` 是 **JSON 对象**，由适配器自行定义 schema、自行校验；
  控制面只做「JSON 合法性」与「适配器声明的字段白名单」校验，不解释语义。
- 适配器可声明**运行时状态面**（`StatusProvider`），由控制面渲染为供应商详情页的一块面板：
  - Command Code：协议版本、漂移检测结果、指纹上报最近结果、窗口阻塞列表、账号用量缓存时间；
  - 通用：最近一次探活、模型目录刷新时间。
- 适配器私有状态**不得**写入网关主表，只能落 `provider_state`（KV）或适配器自己的缓存。

### 3.3 F3 凭据（密钥）管理

- 一个供应商下可挂多枚凭据（对应「多账号」），字段：
  `name`、`secret`（加密存储）、`enabled`、`priority`、`expires_at`、`remark`。
- 出参恒为掩码（`user_abcd…wxyz`）；更新时传回掩码表示**不修改**。
- **落库加密**：`AES-256-GCM`，密钥来自 `data/.secret.key`（首次启动自动生成，权限 `0600`）。
- **轮转与选择**：数据面按
  `人工固定（pin） > priority > id` 选凭据；
  被适配器判定为「耗尽（credit）」的凭据落库为 `exhausted`，窗口限制只做**运行时内存阻塞**。
- **批量导入**：支持粘贴多行（每行一枚密钥，可带 `名称,密钥`），最多 50 行，重复自动跳过。
- CLI 一键导入（仅 Command Code）：读取 `~/.commandcode/auth.json`，导入为凭据。
- 删除守卫：删除供应商时若其下有启用模型 → 409；删除凭据本身**始终允许**，
  若它正被 `route_pinned_credential_id` 固定，则一并清除 pin。

### 3.4 F4 模型管理

- 增删改查；字段：
  - `provider_id`、`display_name`（对外名，数据面 `model` 参数用它）、
  - `upstream_name`（发往上游的真实名）、`upstream_protocol`（openai_chat / openai_responses / anthropic / vendor_native）、
  - `input_price` / `output_price` / `cached_input_price`（元每百万 token）、
  - `context_window`、`max_tokens`、
  - `supports_stream` / `supports_tools` / `supports_vision`（能力位）、
  - `enabled`、`sort`、`remark`。
- **对外名唯一**（全局），`(provider_id, upstream_name)` 唯一。
- **模型别名**：对外名与上游名解耦；`/v1/models` 只暴露对外名。
- **可路由校验**：模型可路由 ⇔ 模型启用 ∧ 供应商启用 ∧ 供应商有可用凭据。
  控制面在模型列表直接给出「可路由/不可路由 + 原因」。
- **路由模型（RouteModel，可选能力）**：把 N 个同协议模型聚合成一个对外名，
  按 `priority`（主备）或 `weight`（加权轮询）选择；成员不可路由时自动跳过。
- 被客户端密钥白名单或路由模型引用的模型禁止删除（返回 409）。

### 3.5 F5 客户端密钥与端点

- 客户端密钥：`sk-adp-<32hex>`，创建时**仅返回一次明文**，落库只存 `key_hash` + `key_prefix`。
  - 字段：`name`、`enabled`、`allowed_models`（空=全部）、`allowed_ips`（CIDR 列表）、
    `quota_tokens`（0=不限）、`quota_used`、`expires_at`、`remark`；
  - 支持启用/停用、用量重置、轮换（重新签发）、删除；
- 端点：
  - **内置只读端点**（无需配置）：`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、`/v1/models`；
  - **自定义端点**：`path` + `inbound_protocol` + 目标（模型/路由模型）+ 是否要求鉴权 + 单 IP 限流；
  - 端点决定入站协议，转发方向 = 入站协议 → 目标模型的上游协议，相同则透传。

### 3.6 F6 协议转换（核心）

- 三个编解码器 + 规约 IR 覆盖 **9 组方向 × {请求, 非流式, 流式}**（3 组同协议透传 + 6 组跨协议互转）：

| 入站 ↓ / 出站 → | openai_chat | openai_responses | anthropic |
| :--- | :-: | :-: | :-: |
| openai_chat | 透传 | ✅ | ✅ |
| openai_responses | ✅ | 透传 | ✅ |
| anthropic | ✅ | ✅ | 透传 |

- **第四种「上游协议」：`vendor_native`**（如 Command Code 的 `<NDJSON 事件流>`）。
  它不是对下游协议，只作为适配器的**内部线格式**：适配器把 IR 编成私有信封、
  把私有事件流解回 IR，主干对此无感。
- 工具调用三态互转（`tools/tool_calls` ↔ `function_call` ↔ `tool_use/tool_result`）；
- 系统提示、多模态图片、`max_tokens` 语义、鉴权头差异自动适配；
- 无法无损映射 → 记 `warnings[]` 并降级，**不阻断、不 panic**。

### 3.7 F7 指纹与账号会话抽象（Command Code 专项，但接口通用）

- 定义通用接口 `FingerprintPolicy`：
  `Derive(credential) → Identity`、`ReportSchedule() → 周期`、`Report(ctx, identity)`。
- 默认无指纹（返回 `nil`，表示该供应商不需要）。
- Command Code 实现：`sha256(salt \0 apiKey \0 field)` 决定论派生（CPU/内存/时区/网卡/主机名/
  git email），`thumbmark` 与官方 `command-code:device-fingerprint:v1` 盐一致；
  **同一枚密钥永远得到同一台设备**，重启不变。
- 上报调度：首次请求后 8h ± 2h 刷新，失败 1–5 分钟重试。
- 相关能力均带 UI 面板：指纹列表、上报结果、下次上报时间（见 [06](06-ui-plan.md)）。
- ≥ 2 条金标准用例（`testdata/golden/fingerprint*.json`）必须逐字节一致。

### 3.8 F8 额度与轮转抽象

- 通用接口 `QuotaPolicy`：
  - `Classify(status, body) → (kind, retryAfter)`，`kind ∈ {credit, window, rate_limit, none}`；
  - `RotateOn(kind) bool`（信用耗尽/窗口到顶轮转，瞬时限速不轮转）。
- 数据面行为：
  - `credit` → 凭据落库 `exhausted` + 同请求内换下一枚凭据重试（每枚最多一次）；
  - `window` → 内存标记到恢复时刻（不落库），换下一枚；
  - `rate_limit` → 原样返回，不轮转（换账号等于换指纹，只在确属耗尽时才值得）。
- **每笔请求每枚凭据最多尝试一次**；全部失败则返回最后一次上游响应。

### 3.9 F9 可观测：请求日志与用量

- 每笔数据面调用落一条记录：
  `ts / endpoint / inbound_protocol / provider / model / client_key / credential /
   status_code / stream / duration_ms / ttft_ms / input_tokens / output_tokens / cached_tokens /
   cost / finish_reason / error_type / client_disconnected`。
- usage 来源：上游返回 → 平台估算（`estimated=true`）。
- 日聚合：`day × provider × model × client_key` 维度的请求数、token、费用。
- 控制面提供：概览卡片、24 小时柱状、按天/模型/供应商/密钥排行、明细检索（分页 + 多维过滤）、
  日志 SSE 实时流（桌面 UI 用它做「实时尾巴」）。
- 流式请求在流结束后记账（含客户端提前断开：按已收到的 usage 或估算记）。

### 3.10 F10 桌面与无头双形态

- 桌面（`cmd/adp-desktop`，mygo 原生 UI）：窗口 + 托盘，进程内起服务，关窗不退出。
- 无头（`cmd/adp-server`）：无 GUI 依赖，`/health`、`/admin/api/*`、`/v1/*`、优雅退出。
- 两者共用同一个 `internal/app` 组装根。

## 4. 非功能需求

| 维度 | 要求 |
| :--- | :--- |
| 兼容性 | 下游与官方 SDK 兼容（OpenAI Chat/Responses、Anthropic Messages）；上游接入通过适配器扩展，不修改主干 |
| 性能 | 网关自身开销（不含上游）P95 < 20ms；适配器与 IR 层零网络依赖（可纯函数单测） |
| 流式 | 边收边发、`Flush` 即时；客户端断开即取消上游；`WriteTimeout` 必须为 0 |
| 安全 | 上游密钥 AES-256-GCM 落库；客户端密钥只存哈希；日志禁打印明文密钥与完整报文；管理写操作校验同源 Origin，支持 `ADP_ADMIN_TOKEN`（常数时间比较） |
| 可用 | 存储失败降级内存；上游失败统一 502/透传语义；单二进制、无外部依赖 |
| 可测试 | IR/适配器纯函数单测；`httptest` 假上游端到端；金标准报文比对 |
| 可观测 | 结构化行日志（含 path/model/provider/duration）；`/health` |
| 可扩展 | 新增一个供应商 = 新增一个 `internal/provider/<kind>` 包 + 注册一行，主干零改动 |

## 5. 访问模型

本地单用户场景，不引入 RBAC。控制面有两级：

| 级别 | 凭据 | 说明 |
| :--- | :--- | :--- |
| 本机 Open | 无（默认） | 仅监听 `127.0.0.1` 时默认放行 |
| 令牌 | `ADP_ADMIN_TOKEN`（`Authorization: Bearer` 或 `?token=`） | 设了就强制；非回环监听时启动日志告警 |
| 数据面 | `Authorization: Bearer sk-adp-*` / `x-api-key` | 客户端密钥；`endpoint.auth_required=false` 时允许匿名（仅限内网） |

> 若未来需要多用户/多租户，再按 [05 §2](05-api-contract.md) 预留的 `creator` 字段扩展，本期不做。

## 6. 验收标准（DoD）

1. 空目录启动 → 自动建库、生成 `.secret.key`、写入默认配置 → 桌面窗口可以打开；
2. **新建 Command Code 供应商 + 凭据 → 拉取模型 → 建客户端密钥 → 三种协议（含流式）调用同一模型成功**；
3. 新建 `openai_compatible` 供应商 → 模型 → 用 **Anthropic 入站协议**调用成功（跨协议转换生效）；
4. 9 组转换方向（3 透传 + 6 互转）在非流式与流式下均通过单测（`go test ./internal/gateway/...`）；
5. 上游 402/窗口 429 → 自动轮转到下一枚凭据，且落库/内存标记正确；瞬时限速不轮转；
6. 指纹派生与 `testdata/golden/fingerprint*.json` 完全一致；信封与 `envelope.json` 一致；
7. 日志页可见每笔请求的 provider/model/token/耗时/费用；用量页聚合正确；客户端密钥额度生效；
8. 凭据出参恒掩码；客户端密钥明文仅创建时出现一次；日志无明文密钥；
9. `go vet ./... && go test ./...` 全绿；桌面与无头两个二进制均可构建（`scripts/build-windows.ps1`）；
10. 删除供应商/模型/凭据的守卫全部生效（409 + 明确原因）；
11. `grep -rniE "command_code|command-code|commandcode|cliMode|threadId|zdr|envelope" internal/gateway/` 为空。

## 7. 不在本期范围

- Embeddings / Rerank / 图像生成 / 语音（Realtime WebSocket）——预留 `Endpoint` 扩展点；
- 分布式部署、多实例共享额度（本期进程内）；
- 计费与支付、账单导出；
- 语义缓存、提示词模板市场、内容审核；
- Web 前端（本期坚持原生 UI；若未来需要，按 [06 §8](06-ui-plan.md) 的静态内嵌方案扩展）。
