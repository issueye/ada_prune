# 02 · 架构与开发计划

| 项目 | 内容 |
| --- | --- |
| 文档编号 | DOC-02 |
| 版本 | v1.0 |
| 日期 | 2026-11-07 |
| 上游 | [01 需求](01-requirements.md) · 契约以 [05](05-api-contract.md) 为准 |
| 模块路径 | 仓库根，Go module `github.com/learningdog1/ada_prune` |

---

## 1. 目标与范围

从零构建一个可独立运行、单二进制部署的**通用本地 AI 网关**：

- 控制面：供应商 / 凭据 / 模型 / 路由模型 / 客户端密钥 / 端点 / 设置 / 日志 / 用量；
- 数据面：三协议入站 → IR → 适配器 → 上游 → IR → 入站协议回写；
- 适配器层：`command_code`（信封、指纹、窗口、账号用量）+ `openai_compatible` + `anthropic`；
- 交付形态：桌面（mygo 原生 UI）+ 无头，两个二进制，共享 `internal/app`。

> 本项目是**纯重写**：不继承任何历史 schema / 配置键 / HTTP 路径 / 环境变量。
> 参考项目（`ada_proxy - 副本` 与 `aluka_cloud/llm`）只提供**设计知识**，
> 结论沉淀在 [03](03-provider-adapter.md) / [04](04-protocol-conversion.md)。

## 2. 分层与依赖方向

```
cmd/prune-desktop (mygo 壳)      cmd/prune-server (headless)
        │                              │
        └──────────┬───────────────────┘
                   ▼
            internal/app                 组装根：配置/存储/网关/HTTP/后台任务
                   │
      ┌────────────┼──────────────┬───────────────┐
      ▼            ▼              ▼               ▼
 internal/api  internal/admin  internal/gateway  internal/provider
 (HTTP 路由)   (控制面业务)      (数据面内核)      (适配器实现)
      │            │              │               │
      └────────────┴──────┬───────┴───────────────┘
                          ▼
                   internal/store            (SQLite / 内存降级)
                   internal/config           (JSON 快照 + ENV 覆盖)
                   internal/secretbox        (AES-256-GCM)
                   internal/logging          (行日志)
                   internal/paths            (data 目录)
```

### 2.1 硬性约束

1. `internal/gateway`（协议内核 + 适配器接口）**只依赖标准库与 `internal/gateway/ir`**；
   不得 import `store` / `admin` / `api`；必须能纯函数单测。
2. `internal/provider/<kind>` 只能依赖 `internal/gateway` 的**接口与 IR**，
   不得反向依赖 `store`：需要的数据由适配器入参（`Credential`、`ProviderConfig`）传入，
   需要持久化走 `StateStore`。
3. `internal/admin` 不 import `net/http`（控制面 HTTP 适配在 `internal/api/adminhttp`），上下文统一 `context.Context`。
4. `internal/api` 只做解析/鉴权/序列化，不写业务规则。
5. 任何包都禁止全局 `var db`；依赖从 `app.New` 显式注入。
6. 上游出网必须带 `context` 超时；下游断开即取消。

## 3. 请求生命周期

### 3.1 控制面

```
Request → Recover → RequestID → Logger → CORS → AdminAuth(token?)
        → api.Handler → admin.Service(校验) → store → JSON
```

### 3.2 数据面

```
POST /v1/chat/completions   Authorization: Bearer sk-prune-...
  → Recover → RequestID → Logger → CORS → Inflight 准入
  → api.DataPlane:
       1) 端点解析（内置或自定义 path → inbound_protocol + target）
       2) 客户端密钥校验（hash 命中/启用/未过期/IP/模型白名单/额度）
       3) 解码：inbound codec.DecodeRequest(body) → IR（含 model 解析 → 目标模型）
       4) 路由：gateway.Router.Pick(model) → (provider, credential, adapter)
       5) 编码：adapter.EncodeRequest(IR) → 上游 wire（CC: 信封；OpenAI: 标准体）
       6) 发送：adapter.Do(ctx, req)（连接池/代理/超时）
       7) 回写：
            非流式：adapter.DecodeResponse → IR → inbound.EncodeResponse
            流式：  adapter.DecodeStream → IR Chunk → inbound.EncodeStream + Flush
       8) 记账：usage → 日志 + 日聚合 + 客户端密钥额度
```

### 3.3 关键时序（额度轮转，以 Command Code 为例）

```
Router.Pick → credential A（active）
adapter.Do → 402 quota_exceeded
  QuotaPolicy.Classify → kind=credit
  store.SetCredentialStatus(A, "exhausted")
  RotateOn(credit)=true → Router.PickNext(skip={A}) → credential B
adapter.Do → 200 NDJSON
  → 正常回写与记账（日志里 credential_id = B）
```

> 每枚凭据每笔请求最多一次；`window` 类型只做内存标记（到期自动恢复），
> `rate_limit` 类型**不轮转**，原样返回给下游。

## 4. 配置

配置是 **`data/config.json` 单文件 + 环境变量覆盖**：
首启写默认值；环境变量优先且从不写回；未知键保留（向前兼容新版本）。

```jsonc
{
  "port": 3050,
  "host": "127.0.0.1",
  "logFile": "",
  "logLevel": "info",
  "logRetentionDays": 30,

  "defaultModel": "",              // 请求未带 model 时的兜底（对外名）
  "allowAnonymous": false,         // 数据面是否允许无客户端密钥（内网调试）

  "maxBodyMB": 100,
  "streamIdleMs": 30000,           // 流式空闲看门狗（逐块重置）
  "nonStreamIdleMs": 90000,
  "maxInflight": 0,                // 0 = 不限
  "keepAliveTimeoutMs": 65000,

  "modelRefreshIntervalMs": 300000,     // 仅对实现 ModelDiscovery 的适配器
  "accountUsageRefreshMs": 300000,      // 仅对实现 AccountUsage 的适配器
  "driftCheckIntervalMs": 86400000
}
```

- 环境变量：`PRUNE_` + 路径大写下划线（如 `PRUNE_DATA_DIR`、`PRUNE_PORT`、`PRUNE_LOG_LEVEL`）。
- 供应商私有参数（原 CC 的 `apiBase` / `zdr` / `cliMode` / `fingerprintSalt` / `deviceProjectDir` 等）
  **不属于全局配置**，一律落在 `providers.adapter_config` JSON 里，由适配器自己定义与校验。
- `Validate()`：`port ∈ [1,65535]`、`maxInflight ≥ 0`、`maxBodyMB > 0`、`streamIdleMs > 0`。

## 5. 装配（`internal/app`）

```go
func New(opts Options) (*App, error) {
    dirs  := paths.Resolve(opts.DataDir)          // PRUNE_DATA_DIR > 传入 > 可执行目录/data
    cfg   := config.Open(dirs)                    // JSON 快照 + ENV 覆盖
    log   := logging.New(cfg.LogFile)
    box   := secretbox.Open(dirs.SecretKey())     // 缺失则生成 32B key (0600)
    st    := store.Open(dirs.DB(), box)           // 失败降级内存
    st.Migrate()                                  // 建表（幂等）
    reg   := provider.DefaultRegistry()           // command_code / openai_compatible / anthropic
    rt    := gateway.NewRouter(st, reg, cfg)      // 模型解析 + 凭据选择 + 轮转
    relay := gateway.NewRelay(rt, st, log)        // 数据面内核（计时/记账/错误映射）
    svc   := admin.New(admin.Deps{...})           // 控制面业务
    srv   := api.New(api.Options{...})            // /v1 + /admin/api + /health
    // 后台：日志保留清理、适配器 Sweep、窗口清扫、模型目录刷新、账号用量刷新、漂移检测
}
```

- `App` 对外暴露：`Cfg`、`Store`、`Registry`、`Router`、`Admin`、`Server`、`StartedAt`；
  适配器运行时对象由 UI 通过 `Adapter` 的 `StatusProvider` 能力获取，**不直接持有**。
- 关闭顺序：取消后台 ctx → 等待 goroutine → 关闭 telemetry → 关闭 store → 关闭 logger。

## 6. 数据模型

> 存储：SQLite（`modernc.org/sqlite`，单连接 + `busy_timeout=5000`）。
> 时间列统一 `INTEGER` 毫秒时间戳；布尔列用 `INTEGER 0/1`；JSON 列用 `TEXT`。

| 表 | 关键字段 | 索引 |
| :--- | :--- | :--- |
| `providers` | `id, name, kind, base_url, enabled, weight, timeout_ms, proxy_url, extra_headers, adapter_config, remark, created_at, updated_at` | `uk_name`、`idx_kind`、`idx_enabled` |
| `credentials` | `id, provider_id, name, secret_cipher, secret_mask, enabled, priority, status(active/disabled/exhausted), expires_at, last_used_at, remark, created_at, updated_at` | `idx_provider`、`idx_status`、`uk_provider_name` |
| `models` | `id, provider_id, display_name, public_name, upstream_name, upstream_protocol, input_price, output_price, cached_input_price, context_window, max_tokens, supports_stream, supports_tools, supports_vision, enabled, sort, remark` | `uk_public_name`、`uk_provider_upstream`、`idx_provider` |
| `route_models` | `id, name, strategy(priority/weighted), enabled, remark` | `uk_name` |
| `route_model_members` | `id, route_model_id, model_id, priority, weight, enabled` | `idx_route_model` |
| `client_keys` | `id, name, key_hash, key_prefix, key_encrypted, enabled, allowed_models_json, allowed_ips, quota_tokens, quota_used, expires_at, last_used_at, remark` | `uk_key_hash`、`idx_enabled` |
| `endpoints` | `id, name, path, inbound_protocol, target_type(model/route_model), target_id, auth_required, rate_per_min, timeout_ms, enabled, remark` | `uk_path` |
| `requests` | `id, ts, endpoint, inbound_protocol, provider_id, provider_kind, model, client_key_id, credential_id, status_code, stream, duration_ms, ttft_ms, input_tokens, output_tokens, cached_tokens, cost, finish_reason, error_type, client_disconnected` | `idx_ts`、`idx_provider`、`idx_model`、`idx_client_key` |
| `usage_daily` | `day, provider_id, model, client_key_id, requests, input_tokens, output_tokens, cached_tokens, cost` | `pk(day, provider_id, model, client_key_id)` |
| `settings` | `key, value` | `pk_key`（运行时 KV：路由 pin 等） |
| `provider_state` | `provider_id, key, value, updated_at` | `pk(provider_id,key)`（适配器私有状态，不污染主表） |

### 6.1 关键字段语义

- `providers.kind`：适配器类型，注册表的键（`command_code` / `openai_compatible` / `anthropic` / `custom`）；
- `providers.adapter_config`：适配器私有 JSON，控制面只做「JSON 合法 + schema 白名单」校验；
- `models.upstream_protocol`：`openai_chat` / `openai_responses` / `anthropic` / `vendor_native`
  （`vendor_native` 表示由适配器内部处理私有 wire）；
- `models.public_name`：对外模型 id，数据面 `model` 参数与 `/v1/models` 都用它；全局唯一；
- `client_keys.allowed_models_json`：`[]` 表示全部；`allowed_ips`：逗号分隔 CIDR/IP；
- `client_keys.key_encrypted`：secretbox 加密的明文副本，供控制面（桌面「复制」）取回；
  `key_hash` 仍用于数据面校验；旧库升级后该列为 NULL，轮换一次即补齐；
  明文只经 `admin.Service.RevealClientKey` 出口，不提供 HTTP 路由；
- `credentials.status`：`exhausted` 由额度策略写入（只有 `active` 参与路由）；
- `settings` 里固定键 `route_pinned_credential_id` 存人工固定的凭据。

## 7. 数据面内核（`internal/gateway`）

```
gateway/
├── ir/               规约类型（ChatRequest/ChatResponse/StreamChunk/Usage/Tool…）
├── codec/            三协议编解码 + SSE 读写
│   ├── codec.go          Codec 接口 + 注册表
│   ├── openai_chat.go
│   ├── openai_responses.go
│   ├── anthropic.go
│   └── sse.go            通用 SSE 解析/写出
├── adapter.go        Adapter / Capabilities / FingerprintPolicy / QuotaPolicy / 可选能力
├── router.go         模型解析（public_name → model → provider）与凭据选择/轮转
├── relay.go          编排：decode → route → encode → do → writeback → record
├── quota.go          通用额度判定（内存窗口 + 存储状态）
├── usage.go          usage 提取与回退估算、费用计算
└── errors.go         上游错误 → 下游协议错误体
```

### 7.1 Adapter 接口（详见 [03](03-provider-adapter.md)）

```go
type Adapter interface {
    Kind() Kind
    Capabilities() Capabilities
    ValidateConfig(cfg ProviderConfig) error
    Do(ctx context.Context, in *InvokeRequest) (*http.Response, error)
    DecodeResponse(body []byte, in *InvokeRequest) (*ir.ChatResponse, error)
    DecodeStream(r io.Reader, in *InvokeRequest) StreamDecoder
}
```

### 7.2 流式回写红线

- 必须设置 `text/event-stream`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`；
- 每个事件写出后 `Flush()`；
- 边读边写，不整包缓存（保 TTFT）；`ctx` 绑定下游，断开即取消；
- 出站 SSE 形态由**入站协议**决定，绝不泄漏上游私有事件名。

## 8. 目录结构

```
ada_prune/
├── go.mod                     module github.com/learningdog1/ada_prune
├── mygo.json                  桌面打包描述
├── README.md  AGENTS.md  LICENSE  NOTICE
├── docs/                      01..07 + screenshots/
├── cmd/
│   ├── prune-desktop/main.go    mygo 窗口 + 托盘
│   └── prune-server/main.go     无头服务
├── internal/
│   ├── app/                   组装根 + 后台任务
│   ├── api/                   HTTP 路由、数据面入口、管理面适配器、CORS、准入
│   ├── admin/                 控制面业务（Service），无 HTTP 依赖
│   ├── gateway/
│   │   ├── ir/
│   │   ├── codec/
│   │   ├── adapter.go  router.go  relay.go  quota.go  usage.go  errors.go
│   ├── provider/
│   │   ├── registry.go
│   │   ├── command_code/      信封/指纹/上报/会话/窗口/事件桥/模型目录/账号用量/漂移
│   │   ├── openai/            openai_compatible
│   │   └── anthropic/         anthropic 原生
│   ├── store/                 SQLite schema + CRUD + 聚合
│   ├── config/                config.json 快照 + env
│   ├── secretbox/             AES-256-GCM + 掩码
│   ├── desktop/               mygo 原生 UI（state/view/page_*）
│   ├── telemetry/             记录 + 事件总线
│   └── logging/  paths/  version/  httpkit/
├── scripts/build-windows.ps1
├── testdata/golden/           信封 / 指纹 / 协议样例
└── tools/{mockupstream,seed,golden,logo}
```

## 9. 后台任务

| 任务 | 周期 | 说明 |
| :--- | :--- | :--- |
| 日志保留清理 | 启动 + 每 24h | 按 `logRetentionDays` 删 `requests` |
| 适配器 Sweep | 每 1h | 调 `Adapter.Sweep(now)`（CC：会话过期 + 指纹状态清理） |
| 内存窗口清扫 | 每 1min | 过期窗口自动解除阻塞 |
| 模型目录刷新 | 每 `modelRefreshIntervalMs` | 仅对实现 `ModelDiscovery` 的适配器 |
| 账号用量刷新 | 每 `accountUsageRefreshMs` | 仅对实现 `AccountUsage` 的适配器 |
| 协议漂移检测 | 启动 + 每 `driftCheckIntervalMs` | 仅 CC：比对上游版本，只告警不改版本 |
| 指纹/生命周期上报 | 按凭据下次上报时间 | 仅 CC；失败 1–5 分钟重试 |

> 所有后台任务必须接受 `ctx` 并在取消后退出；`App.Close` 等待其结束。

## 10. 里程碑

| 里程碑 | 内容 | DoD |
| :--- | :--- | :--- |
| **P0 设计** | 文档集 + 契约冻结 | 评审通过 |
| **P1 骨架** | module、`paths/config/secretbox/store/logging`、`gateway/ir`、`app`、`api` 骨架 | `prune-server` 可启动，`/health` OK，建表成功 |
| **P2 适配器层** | `Adapter` 接口 + registry + `command_code` 全部实现 | 金标准通过；IR 层六类事件跑通 |
| **P3 控制面** | 供应商/凭据/模型/路由模型/客户端密钥/端点/设置 | 契约 §2 每条路径有测试 |
| **P4 协议内核** | IR + 三 Codec + 9 组合矩阵 | 转换矩阵全绿 |
| **P5 数据面** | Router + Relay + 额度 + 记账 | 三协议 × 流式/非流式 × 轮转可用 |
| **P6 第二供应商** | `openai_compatible` + `anthropic` | 跨协议互转真实上游可用 |
| **P7 桌面 UI** | 三组导航 + 新页面 + 实时日志 | 无窗口测试 + 手工走查 |
| **P8 打包** | Windows 打包、冒烟、README | 单 exe 全链路 |

依赖：`P1 → P2 → (P3 ∥ P4) → P5 → P6 → P7 → P8`。

## 11. 测试策略

| 层次 | 工具 | 重点 |
| :--- | :--- | :--- |
| 单元 | `go test` | IR/Codec 9 组合、指纹派生（金标准）、信封构造（金标准）、usage/费用、额度分类、掩码 |
| 存储 | `go test` + 临时 SQLite | CRUD、唯一约束、删除守卫、日聚合 upsert、降级路径 |
| 适配器 | `httptest` 假上游 | CC 事件流解析、错误映射、轮转、窗口阻塞与恢复 |
| 端到端 | `httptest` + 三协议 | 入站 → 上游 → 入站，流式与非流式、客户端断连 |
| UI | `ui.NewTester` | 导航、空状态、错误条、表格渲染（无窗口） |
| 冒烟 | PowerShell 脚本 | 建供应商 → 凭据 → 模型 → 客户端密钥 → 三协议调用 → 查日志 |

## 12. 风险

| 风险 | 处置 |
| :--- | :--- |
| CC 上游协议漂移（CLI 版本升级） | 版本常量集中一处；漂移检测只告警；信封/事件解析保持宽松（未知字段忽略） |
| 流式被 `WriteTimeout` 截断 | `http.Server.WriteTimeout = 0`，用空闲看门狗兜底 |
| 多协议转换丢字段 | `warnings[]` + 单列测试矩阵；宁可降级不阻断 |
| 单连接 SQLite 成瓶颈 | 请求日志写走 `telemetry` 队列批量落库；只读查询短事务 |
| 主干被供应商语义污染 | [AGENTS.md §2.1](../AGENTS.md) 的 grep 判据进 CI |
