# 07 · 实施路线图

| 项目 | 内容 |
| --- | --- |
| 文档编号 | DOC-07 |
| 版本 | v1.0 |
| 日期 | 2026-11-07 |
| 上游 | [02 §10](02-architecture.md) · [03](03-provider-adapter.md) |

---

## 1. 重写策略

这是一个**纯重写（greenfield）**项目：新目录、新 module、新数据库、新配置、新 HTTP 契约。

| # | 原则 | 说明 |
| :-: | :--- | :--- |
| R1 | **不继承** | 不复用旧项目的数据库 schema、`config.json` 键、环境变量前缀、密钥前缀、管理接口路径 |
| R2 | **借鉴知识，不搬代码** | 旧项目（`ada_proxy - 副本` 等）里经过实测的**协议事实**——信封字段与键序、指纹哈希算法、"finish 缺失即截断"的判定、402/429 的语义区分——作为**规格**写进 [03](03-provider-adapter.md)/[04](04-protocol-conversion.md)，然后重新实现 |
| R3 | **主干先立，适配器后填** | 先做得出 `gateway/ir` + 三个 codec + 一个空适配器的骨架，再接 Command Code |
| R4 | **每阶段可运行** | 每个阶段结束都能 `go build` + `go test` + 起服务打一次接口 |
| R5 | **金标准防止走样** | 信封与指纹的固定测试向量在 P2 前先落盘，实现对着它写 |

> **明确不做**：旧库迁移、旧路径别名、旧环境变量双读、`sk-ccp-*` 前缀兼容、旧表只读保留。
> 旧数据若需要继续使用，由使用者自行保留旧二进制与旧数据目录。

## 2. 分阶段路线

### P0 · 设计与契约冻结（本次交付）

- 产出 [01](01-requirements.md)–[07](07-roadmap.md) 全套文档 + [AGENTS.md](../AGENTS.md)；
- 冻结：适配器接口、IR 类型、控制面路径与 JSON 形状、配置键、环境变量表；
- **验收**：文档评审通过；实现争议以文档为准。

### P1 · 骨架（1 周）

| 任务 | 产出 |
| :--- | :--- |
| 1.1 初始化仓库 | `go.mod`（module `github.com/learningdog1/ada_prune`）、`.gitignore`、`mygo.json`、目录骨架 |
| 1.2 `internal/paths` | 数据目录解析（`PRUNE_DATA_DIR` > 可执行目录旁 `data/`） |
| 1.3 `internal/config` | `data/config.json` 快照 + `PRUNE_*` 环境变量覆盖 + 原子写 |
| 1.4 `internal/secretbox` | AES-256-GCM、`.secret.key` 首启生成（`0600`）、`Mask()` |
| 1.5 `internal/store` | SQLite 连接（单连接 + `busy_timeout`）、9 张表的 schema、降级内存 |
| 1.6 `internal/logging` / `version` / `httpkit` | 行日志、构建版本、JSON/SSE 小工具 |
| 1.7 `internal/gateway/ir` | 规约类型与常量（[04 §3](04-protocol-conversion.md) 全量） |
| 1.8 `internal/app` | 组装根 + 后台任务框架 + 优雅退出 |
| 1.9 `internal/api` 骨架 | `/health`、CORS、准入、panic 恢复 |

**DoD**：`go run ./cmd/prune-server` 起得来，`/health` 返回 OK，SQLite 建表成功，降级路径可测。

### P2 · 适配器层与 Command Code（1.5 周）

| 任务 | 产出 |
| :--- | :--- |
| 2.1 `gateway/adapter.go` | `Adapter` / `Capabilities` / 可选能力接口（Prober/ModelDiscovery/AccountUsage/Status/Sweeper/ConfigSchema）/ `StateStore` |
| 2.2 `provider/registry.go` | `Kind → Factory` 注册表 + `Schemas()` |
| 2.3 **金标准先行** | 用手工构造的期望值落盘 `testdata/golden/envelope.json`、`fingerprint.json`、`fingerprint-salted.json` |
| 2.4 `provider/command_code/envelope.go` | IR → 信封（键序固定） |
| 2.5 `provider/command_code/wire.go` | 请求头（版本 / 环境 / slug / 会话 / traceparent / ZDR）与发送 |
| 2.6 `provider/command_code/fingerprint.go` | 决定论指纹派生 |
| 2.7 `provider/command_code/report.go` | 指纹 + 生命周期上报与调度 |
| 2.8 `provider/command_code/session.go` | 会话 id 派生与 TTL |
| 2.9 `provider/command_code/events.go` | NDJSON 事件 → IR Chunk |
| 2.10 `provider/command_code/quota.go` | 402 / 窗口 429 / 瞬时限速 分类 |
| 2.11 `provider/command_code/{models,accountusage,drift,config}.go` | 模型目录、账号用量、漂移、`adapter_config` schema |

**DoD**：金标准三件套逐字节通过；在 IR 层跑通「文本 / 工具 / 推理 / usage / 截断 / 零输出」六类事件。

### P3 · 控制面（1.5 周）

| 任务 | 产出 |
| :--- | :--- |
| 3.1 `internal/admin` | Service：供应商、凭据、模型、路由模型、客户端密钥、端点、设置、日志、用量 |
| 3.2 `internal/api` 管理路由 | `/admin/api/*` 全量（[05 §2](05-api-contract.md)） |
| 3.3 适配器元信息 | `GET /admin/api/adapters`（schema 驱动表单） |
| 3.4 守卫与错误码 | 409 + `field` + 明确原因 |
| 3.5 契约测试 | 每条路径一条：状态码 + JSON 形状 |

**DoD**：契约 [05 §2](05-api-contract.md) 每条路径都有测试；供应商向导所需的 schema 可从 API 拿到。

### P4 · 协议内核（1.5 周）

| 任务 | 产出 |
| :--- | :--- |
| 4.1 `codec/codec.go` | 接口 + 注册表 + `ConvertRequest/ConvertResponse` |
| 4.2 `codec/openai_chat.go` | 请求/响应/流式三件套 |
| 4.3 `codec/openai_responses.go` | 同上（具名事件 + `sequence_number`） |
| 4.4 `codec/anthropic.go` | 同上（`content_block` 状态机 + 假 thinking 签名） |
| 4.5 `codec/sse.go` | 通用 SSE 读写 |
| 4.6 转换矩阵测试 | 9 组合 × 3 形态（[04 §9](04-protocol-conversion.md)） |
| 4.7 降级告警 | `warnings[]` + 断言 |

**DoD**：转换矩阵全绿；`gateway/codec` 无网络、无文件、无全局状态。

### P5 · 数据面（1 周）

| 任务 | 产出 |
| :--- | :--- |
| 5.1 `gateway/router.go` | 模型解析（`public_name` → model → provider）、凭据选择（pin > priority > weight）、跳过集合、轮转 |
| 5.2 `gateway/relay.go` | 编排：解码 → 路由 → 编码 → 发送 → 回写 → 记账 |
| 5.3 `gateway/quota.go` | 通用内存窗口 + `QuotaPolicy` 接入 + 落库 `exhausted` |
| 5.4 `gateway/usage.go` / `errors.go` | usage 提取与估算、费用、错误体映射 |
| 5.5 记账 | 请求日志、日聚合 upsert、客户端密钥额度 |
| 5.6 端到端 | `httptest` 假上游 × 三协议 × 流式/非流式 × 断连 |

**DoD**：三协议入站到 CC 上游全通；402 轮转、窗口阻塞与恢复、瞬时限速不轮转均有测试。

### P6 · 第二与第三供应商（1 周）

| 任务 | 产出 |
| :--- | :--- |
| 6.1 `provider/openai` | `openai_compatible`：复用 chat/responses codec、`/v1/models` 探活与发现 |
| 6.2 `provider/anthropic` | 原生 Anthropic：`x-api-key` + `anthropic-version`，529 `overloaded` |
| 6.3 跨供应商验证 | Anthropic 入站 → OpenAI 上游；Chat 入站 → CC 上游 |
| 6.4 mock 上游扩展 | `tools/mockupstream` 按 kind 提供三条路由 |

**DoD**：9 组转换在「真实或 mock 上游」下全部可用。

### P7 · 桌面 UI（1.5 周）

| 任务 | 产出 |
| :--- | :--- |
| 7.1 导航与骨架 | 监控 / 网关 / 设置 三组 + 能力驱动显隐 |
| 7.2 供应商页 + 向导 | 卡片选类型 + 动态表单 + 状态 Tab |
| 7.3 模型 / 凭据 / 客户端密钥 / 端点页 | CRUD + 守卫提示 |
| 7.4 总览 + 健康度 | KPI、24h 柱状、供应商健康度表 |
| 7.5 日志实时尾巴 | SSE 订阅 + 退避重连 |
| 7.6 无窗口测试 | 导航、空状态、错误条、向导步骤 |

**DoD**：[06 §9](06-ui-plan.md) 验收项全绿；手工走查一遍全流程。

### P8 · 打包与发布（0.5 周）

| 任务 | 产出 |
| :--- | :--- |
| 8.1 二进制 | `AdaPrune.exe`（desktop）、`prune-server.exe`（headless） |
| 8.2 打包脚本 | 图标 / 版本信息 / DPI manifest / GUI 子系统自检 |
| 8.3 冒烟脚本 | 建供应商 → 凭据 → 模型 → 客户端密钥 → 三协议调用 → 查日志 |
| 8.4 README | 定位、快速开始、架构图、文档索引 |

**DoD**：干净目录下单个 exe 启动，走通 [01 §6](01-requirements.md) 全部 DoD。

## 3. 工作量估算

| 阶段 | 人日 | 可并行 |
| :--- | :-: | :--- |
| P0 设计 | 3 | — |
| P1 骨架 | 5 | P4 可并行启动 |
| P2 适配器 | 8 | P4 |
| P3 控制面 | 8 | P4 |
| P4 协议内核 | 8 | P2 / P3 |
| P5 数据面 | 5 | — |
| P6 新供应商 | 5 | P7 可并行 |
| P7 UI | 8 | P6 |
| P8 打包 | 3 | — |
| **合计** | **≈ 53 人日** | 串行关键路径 ≈ 33 人日 |

## 4. 依赖与取舍

| 决策 | 结论 | 理由 |
| :--- | :--- | :--- |
| 存储 | SQLite（`modernc.org/sqlite`） | 纯 Go、无 CGO、单文件、本地优先 |
| Web 框架 | 标准库 `net/http` + `ServeMux` | 路由数量有限（≤ 40），不引入 gin；SSE 与中间件手写更可控 |
| ORM | 无，手写 SQL | 表少、查询固定；避免 GORM 带来的方言复杂度 |
| DI | 手工构造（`app.New`） | 依赖图浅，`samber/do` 是过度设计 |
| UI | mygo v0.7.4 原生 | 单二进制、无 webview、无前端构建 |
| 加密 | `crypto/aes` + GCM | 标准库即可 |
| 令牌 | 不做 JWT | 单机单用户，管理面只需一个可选的静态 token |

## 5. 风险与缓解

| 风险 | 影响 | 缓解 |
| :--- | :--- | :--- |
| CC 上游协议漂移 | 信封/事件解析失效 | 版本常量集中一处；漂移检测只告警；解析宽松（未知字段忽略）；金标准可快速定位差异 |
| 三协议转换丢字段 | 客户端静默降级 | 转换矩阵 + `warnings[]`；失败语义（截断/零输出）单测锁定 |
| 主干被供应商语义污染 | 失去通用性 | [AGENTS.md §2.1](../AGENTS.md) 的 grep 判据进 CI |
| SQLite 单连接竞争 | 高并发写阻塞 | 日志走队列批量写；只读查询走独立只读连接 |
| 动态表单过度设计 | 前端复杂度膨胀 | schema 只支持 5 种字段类型；复杂配置用 textarea(JSON) 兜底 |
| 指纹配置被误改 | 换设备 = 上游风控 | UI 上修改 `projectDir` / `fingerprintSalt` 需二次确认并提示后果 |

## 6. 旧项目可借鉴的「知识清单」（只借鉴结论，不搬代码）

| 知识点 | 结论 | 落在哪 |
| :--- | :--- | :--- |
| 信封键序 | `config, memory, taste, skills, permissionMode, threadId, mode, params` | [03 §3.3](03-provider-adapter.md) |
| 空 system 占位 | 塞一个空格可避免上游注入约 7.5K token 默认提示 | [03 §3.3](03-provider-adapter.md) |
| 工具名改写 | 4 组别名（`bash_output→shell_output` 等） | [03 §3.3](03-provider-adapter.md) |
| 指纹算法 | `sha256(salt \0 key \0 field)` + 官方固定盐 + 「摘要最大者」选池 | [03 §3.5](03-provider-adapter.md) |
| finish 语义 | `rawFinishReason ?? finishReason`；缺失/空 → 截断 → 502 | [03 §3.4](03-provider-adapter.md) |
| usage 语义 | Anthropic 只计非缓存输入；Responses 含缓存（两处相反） | [03 §3.4](03-provider-adapter.md) |
| 402 vs 429 | 信用耗尽落库换号；窗口到顶内存阻塞；瞬时限速不轮转 | [03 §3.7](03-provider-adapter.md) |
| 上游模型目录 | `GET /provider/v1/models`，TTL 5 分钟，失败回退内置 | [03 §3.8](03-provider-adapter.md) |
| 漂移检测 | 查 npm 最新版本，只告警不改声明版本 | [03 §3.10](03-provider-adapter.md) |

> 上表的每一项都写成了**可验收的规格**（含金标准或断言），实现时对着规格写即可。

## 7. 提交纪律

- 一个阶段一个 PR；PR 描述必须引用本文档的 P 编号；
- 任何契约变更先改 [05](05-api-contract.md)，PR 里贴 diff；
- 金标准文件（`testdata/golden/*`）一旦落盘，变更必须在 PR 里说明**为什么**必须变；
- 每次合并前：`gofmt -l .`、`go vet ./...`、`go test ./...`、桌面构建自检全部通过。

## 8. 实施状态

### P1 · 骨架 — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| 模块与目录骨架 | `go.mod`（`github.com/learningdog1/ada_prune`）、`cmd/`、`internal/` | `go build ./...` |
| 数据目录解析 | `internal/paths` | `paths_test.go`（env 覆盖 / 参数优先 / 绝对路径） |
| 配置单文件 + env | `internal/config` | `config_test.go`（默认写入 / env 钉住 / 持久化 / 未知键保留 / 校验） |
| AES-256-GCM + 掩码 | `internal/secretbox` | `secretbox_test.go`（往返 / 新鲜 nonce / 篡改拒绝 / 密钥文件 / 短密钥不外泄） |
| SQLite schema + CRUD | `internal/store`（9 张表） | `store_test.go`（13 组用例） |
| 行日志 | `internal/logging` | `log_test.go`（级别过滤 / 文件 sink / nil 安全） |
| HTTP 小工具 | `internal/httpkit` | `httpkit_test.go`（限长读取 / SSE 写出） |
| 协议 IR 类型 | `internal/gateway/ir` | `ir_test.go`（协议解析 / finish 归一 / usage） |
| 组装根 + 后台任务 | `internal/app` | 经 `internal/api` 端到端 |
| HTTP 骨架 | `internal/api` | `server_test.go`（health / 404 / 501 / CORS / 请求 id / 管理面鉴权 / 设置往返 / 同源校验） |
| 无头入口 | `cmd/prune-server` | 实跑：`/health` → `OK`，`/admin/api/*` 正常 |

实测记录（`PRUNE_PORT=3999`，临时数据目录）：

```
GET  /health               200 OK
GET  /admin/api/overview   200 {"providers":{"total":0,...},"degraded":false}
GET  /admin/api/status     200 {"databasePath":"...\ada_prune.db","envPinned":["port"]}
GET  /admin/api/settings   200 (全部配置键)
PUT  /admin/api/settings   200 {"ok":true,"saved":{"logLevel":"debug","maxBodyMB":64}}
PUT  /admin/api/settings   403 (Origin: http://evil.example) → cross-origin write rejected
GET  /v1/models            200 {"object":"list","data":[]}
POST /v1/chat/completions  501 {"error":{"type":"not_implemented"}}
```

首启自动生成：`config.json`、`.secret.key`（32 字节）、`ada_prune.db`、`logs/`。

自检：`gofmt -l .` 干净、`go vet ./...` 通过、`go test ./...` **64 个用例全绿**。

### P2 · 适配器层与 Command Code — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| 适配器契约 | `internal/gateway/adapter.go`（`Adapter` / `Capabilities` / 可选能力 / `FingerprintPolicy` / `QuotaPolicy` / `StateStore` / `ConfigField`） | 编译期约束；`gateway/` 无任何供应商标识 |
| 注册表 | `internal/provider/registry.go` + `kinds.go` | `registry_test.go`（失败隔离 / 描述符 / 重复注册 panic / nil 安全） |
| 信封构造 | `provider/command_code/envelope.go` | `envelope_test.go`（8 组）+ `envelope.json` 金标准 |
| 请求头与发送 | `provider/command_code/wire.go` | `adapter_test.go`（path/Bearer/版本/会话/slug 断言） |
| NDJSON 事件桥 | `provider/command_code/events.go` | `events_test.go`（14 组：文本/推理/工具/用量/截断/错误/大行） |
| 指纹派生 | `provider/command_code/fingerprint.go` | `fingerprint_test.go`（决定论/盐/哈希公式/池边界）+ 2 份金标准 |
| 指纹与生命周期上报 | `provider/command_code/report.go` | 上报结果落 `provider_state`；失败退避 |
| 会话亲和 | `provider/command_code/session.go` | `session_test.go`（TTL/优先级/清扫/UUID 形态） |
| 额度语义 | `provider/command_code/quota.go` | `quota_test.go`（402 信用 / 429 窗口 / 瞬时限速 / 不轮转） |
| 模型目录 | `provider/command_code/models.go`（67 个内置模型） | 动态失败回退内置；dynamic 模式如实报错 |
| 账号用量 | `provider/command_code/accountusage.go`（whoami + credits ∥ subscriptions + summary） | 缓存 5 分钟；部分失败降级 |
| 漂移检测 | `provider/command_code/drift.go` | 只告警不改版本 |
| 装配 | `internal/app`（`stateStore` 适配 + 共享 HTTP 客户端 + 注册表） | `GET /admin/api/adapters` 实跑返回 kind/能力/schema |

金标准（`testdata/golden/`）：

| 文件 | 锁定内容 |
| :--- | :--- |
| `envelope.json` | 信封键序、system 块、工具名改写、tool-result 形状 |
| `fingerprint.json` | 默认盐下的设备身份 |
| `fingerprint-salted.json` | 自定义盐派生出的另一台设备 |

`go test ./...` 目前 **128 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

> 装饰性约束已生效：`internal/gateway` 内不存在 `command_code` / `cliMode` / `threadId` / `zdr` /
> `envelope` 等字样（`Kind` 常量已下放到 `internal/provider/kinds.go`）。

### P3 · 控制面 — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| 供应商 | `internal/admin/providers.go` + `/admin/api/providers*` | CRUD、kind 不可改、适配器自校验 config、探活、状态、模型发现 |
| 凭据 | `internal/admin/credentials.go` + `/admin/api/credentials*` | 加密落库、掩码出参、批量导入、CLI 导入、pin、状态、单凭据探活、账号用量 |
| 模型 | `internal/admin/models.go` + `/admin/api/models*` | CRUD、批量导入、可路由判定与原因、协议校验、价格校验 |
| 路由模型 | `internal/admin/routemodels.go` + `/admin/api/route-models*` | 成员去重、策略校验、成员必须存在 |
| 客户端密钥 | `internal/admin/clientkeys.go` + `/admin/api/client-keys*` | 一次性明文、轮换、用量归零、IP 白名单校验 |
| 端点 | `internal/admin/endpoints.go` + `/admin/api/endpoints*` | 内置三条只读、路径校验、禁止重定义内置路径、目标校验 |
| 导出 | `Service.Export` + `/admin/api/export` | 只出掩码，绝无明文密钥 |
| 契约测试 | `internal/api/control_test.go` | 10 组端到端用例（含 409 守卫与字段级 400） |

实测（`PRUNE_PORT=3997`）：

```
POST /providers            → routing 前置：routable=false, reason="provider has no active credential"
POST /credentials          → secret="user_012…cdef"（掩码）
POST /models               → publicName 默认取 upstreamName，加凭据后 routable=true
POST /route-models         → members=1
GET  /endpoints            → 3 条内置（builtin=true）
DELETE /models/{id}        → 409（被路由模型引用）
POST /client-keys          → token 一次性返回，keyPrefix 可展示
POST /client-keys/{id}/rotate → 新 token
```

设计取舍：`publicName` 默认等于 `upstreamName`（上游名本身已带命名空间），
避免非 ASCII 的模型 id；跨供应商重名由唯一索引拦成 409。

`go test ./...` 目前 **139 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P4 · 协议内核 — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| Codec 契约与注册表 | `gateway/codec/codec.go` | 注册 3 个协议；`ConvertRequest/ConvertResponse` 辅助 |
| SSE 读写 | `gateway/codec/sse.go` | 多行 data、注释、CRLF、超长行 |
| OpenAI Chat | `gateway/codec/openai_chat.go` | 请求/响应/流式三件套；`stream_options.include_usage` 自动开启 |
| OpenAI Responses | `gateway/codec/openai_responses.go` | 具名事件 + `sequence_number`；reasoning item；工具调用增量 |
| Anthropic Messages | `gateway/codec/anthropic.go` | content block 状态机；`tool_use/tool_result`；`max_tokens` 必填兜底 |
| 转换矩阵 | `codec/convert_test.go` | **9 组方向 × {请求, 非流式, 流式}** |
| 流式回环 | `codec/stream_test.go` | 每协议 encode→decode 回环 + 线上帧形态断言 + 截断检测 |

关键语义（都有测试锁定）：

- **usage 拆分**：IR 存「总输入 + 缓存」，Anthropic 编码时减掉缓存、Responses 保留总量；
  反解时再拼回总量——两处相反，代码里各有一组断言。
- **finish 推断**：Responses 的 `status` 没有 `tool_calls`，完成态里出现 `function_call`
  就推断为 `tool_calls`；Anthropic 的 `stop_reason` 直接映射。
- **截断检测**：Anthropic 流缺 `message_stop`、Responses 流缺 `response.completed`
  都返回错误，不假装成功。
- **思考内容**：Chat 用 `reasoning_content`、Responses 用 `reasoning` item 的
  `summary`、Anthropic 用 `thinking` 块；三者互转不丢。

`go test ./...` 目前 **160 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P5 · 数据面 — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| 目录契约 | `gateway/catalog.go`（`Catalog` / `AdapterResolver` / `Record` / `Recorder`） | 网关不 import `store`；app 提供实现 |
| 凭据路由 | `gateway/router.go` | pin 优先、优先级排序、窗口阻塞过滤、候选链 |
| 额度窗口 | `gateway/quota.go` | 内存阻塞 + 到期自动恢复 + 清扫 |
| 编排内核 | `gateway/relay.go` | 解码→路由→轮转→回写→记账；TTFT、空闲看门狗、断连取消 |
| 错误映射 | `gateway/errors.go` | 上游状态 → 三协议错误体；402 明确「非瞬时限速」 |
| 费用 | `Relay.Cost` | 每百万 token 计价，缓存价缺省回退输入价 |
| HTTP 入口 | `api/dataplane.go` | 端点解析、客户端密钥、IP/额度/模型白名单、会话线索 |
| 记账 | `app/recorder.go` | 队列 + 后台 worker；写日志行、日聚合、密钥额度 |
| 观测接口 | `/admin/api/logs`、`/admin/api/usage` | 分页过滤与四种聚合维度 |
| 端到端 | `api/dataplane_test.go` | 假上游跑通三协议、流式、轮转、鉴权、记账 |

实测（`PRUNE_PORT=3995`，上游故意不可达）：

```
POST /v1/chat/completions  → 502（上游失败，如实返回）
GET  /admin/api/logs       → 记录 provider/model/status/duration，statusCode=502
GET  /admin/api/usage      → {"model":"m1","requests":1,"summary":{"errors":1,...}}
```

端到端测试（假上游 NDJSON）覆盖：

- Chat 非流式 / 流式（SSE 拼出 "hello stub"，末帧 `[DONE]`）；
- Anthropic 入站拿到 `type=message` + `stop_reason=end_turn`；
- Responses 入站拿到 `object=response` + `output_text`；
- 未知模型 → 三协议各自的 404；
- 402 信用耗尽 → 自动换号、旧号落库 `exhausted`；
- `/v1/models` 按密钥白名单过滤；
- 记账异步落库（日志行 + 日聚合）。

`go test ./...` 目前 **168 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P6 · 第二与第三供应商 — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| `openai_compatible` | `provider/openai/` | chat / responses 两条路径、自定义鉴权头（Azure `api-key`）、`/v1/models` 探活与发现、`/v1` 前缀去重 |
| `anthropic` | `provider/anthropic/` | `/v1/messages`、`x-api-key` + `anthropic-version`、`max_tokens` 兜底（请求 → 模型 → 4096） |
| 额度语义差异 | 两个适配器各自的 `QuotaPolicy` | OpenAI：429 限速、402 视为信用；Anthropic：429/529/503 均为限速 |
| 跨供应商验证 | `api/dataplane_test.go` | **Anthropic 入站 → OpenAI 上游**、Responses 入站 → Responses 上游、Chat → Chat 透传 |
| 注册表 | 三个 `init()` 注册 | `GET /admin/api/adapters` 返回 3 个 kind + 各自 schema |

关键取舍：

- **标准供应商不写转换**：`openai_compatible` 直接用网关自己的 OpenAI codec，
  适配器只决定 base URL、路径与鉴权头。这正是 IR 设计的回报。
- **探活的判定不同**：OpenAI 兼容用 `GET /v1/models`（2xx 即可）；
  Anthropic 用最小 `POST /v1/messages`，**400 也算可达**——它证明密钥被接受、
  端点理解请求，只是载荷不足。
- **不因瞬时限速轮转**：两个适配器都只在 402（信用）时轮转；
  Anthropic 没有信用池语义，529 overloaded 也不换号。

跨供应商实测（假上游）：

```
POST /v1/messages  (Anthropic 入站)
  → 上游收到 POST /v1/chat/completions，model="gpt-upstream"
  → 客户端收到 {"type":"message","content":[{"type":"text","text":"from openai"}],"stop_reason":"end_turn"}

POST /v1/responses (Responses 入站, openai_responses 模型)
  → 上游收到 POST /v1/responses
  → 客户端收到 object="response" + output_text="from responses"
```

`go test ./...` 目前 **195 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 · 桌面 UI — 已完成（首版）

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| 应用壳 | `ui/run.go`、`ui/shell.go`、`ui/chrome.go`、`ui/sidebar.go` | 自绘标题栏、分组侧栏、页头、能力驱动 |
| 总览 | `ui/dashboard.go` | 5 格指标带、24 小时柱状图、服务信息 |
| 请求日志 | `ui/logs.go`、`ui/state_logs.go` | 端点/状态过滤、状态着色、TTFT 与费用列 |
| 用量统计 | `ui/usage.go`、`ui/state_usage.go` | 四种分组（天/模型/供应商/密钥）+ 汇总块 |
| 供应商 | `ui/providers.go`、`ui/provider_form.go` | 列表、探活、启停、删除、适配器能力详情 |
| 模型 | `ui/models.go`、`ui/state_models.go` | 搜索、可路由原因、启停 |
| 凭据 | `ui/credentials.go`、`ui/state_keys.go` | 掩码展示、状态药丸、固定、重置、删除、批量刷新额度 |
| 客户端密钥 | `ui/keys.go`、`ui/state_keys.go` | 创建（明文只显示一次）、轮换、删除、额度列 |
| 端点 | `ui/endpoints.go` | 内置 + 自定义、协议与鉴权标记 |
| 设置 | `ui/settings.go`、`ui/state_settings.go` | 服务/协议/日志分组、env 钉住提示、保存 |
| 关于 | `ui/about.go` | 版本、数据目录、协程数、已注册适配器 |
| 无窗口测试 | `ui/*_test.go` | 7 组用例：渲染、导航、空状态、env 提示、适配器列表 |

实现要点：

- 窗口用 `mygo`（v0.7.4）原生绘制，无 webview、无前端构建；
  关窗只是隐藏，网关继续服务。
- 界面直接调用 `admin.Service`，与 HTTP 控制面共用同一套业务规则。
- 数据加载在 goroutine 里完成，回主线程用 `Window.Update` 落地；
  写操作失败通过页头错误药丸提示，不静默吞错。
- 总览与日志页每 5 秒静默刷新（不显示 spinner，避免闪烁）。

尚未完成（P8 或后续）：

- 供应商 / 模型 / 密钥的**新建表单**（目前通过 HTTP API 创建，界面只做管理与观测）；
- 托盘图标与打包（P8）；
- 账号用量面板（Command Code 能力已实现，界面未画）。

`go test ./...` 目前 **208 个用例**，`gofmt -l .` 与 `go vet ./...` 干净；
`build/prune-desktop.exe` 与 `build/prune-server.exe` 均可构建。

### P8 · 打包与发布 — 已完成

| 交付项 | 落点 | 验证 |
| :--- | :--- | :--- |
| 应用图标 | `resources/icon.png`（512）、`icon-small.png`（128）、`tools/logo/gen.py` 生成 | 视觉检查 |
| 打包脚本 | `scripts/build-windows.ps1` | 产出 `AdaPrune.exe`（GUI 子系统）与 `prune-server.exe`（控制台） |
| 子系统自检 | 脚本读 PE 头 `OptionalHeader.Subsystem` | 若非 2（GUI）则报错 |
| mygo 工具依赖 | `go.mod` 的 `tool github.com/egoist/mygo/cmd/mygo` | `go tool mygo version` → 0.7.4 |
| 假上游 | `tools/mockupstream` | 说 NDJSON、带 402 拒绝、模型目录与账号端点 |
| 冒烟脚本 | `scripts/smoke.ps1` | 全链路通过（见下） |
| README | 构建、冒烟、接入三节 | — |

打包实测：

```
Building AdaPrune 0.1.0 (dev) for windows/amd64
  AdaPrune.exe (mygo build: icon, manifest, version)  16.2 MB - subsystem: GUI (no console)
  prune-server.exe                                       12.1 MB - headless
```

冒烟实测（`scripts/smoke.ps1`，临时目录 + 假上游）：

```
  OK  three protocol paths, auth, logs and usage all behave
```

覆盖：建供应商 → 加凭据 → 建模型（可路由）→ 建客户端密钥 →
`/v1/chat/completions` 非流式、`/v1/messages` 跨协议、流式 `[DONE]`、
无密钥 401、日志与用量落库。

**打包顺序的坑**：mygo CLI 会清空 `build/windows-<arch>`，所以脚本先出桌面程序、
再出无头服务；反过来会把 `prune-server.exe` 删掉。

### P7 修订：界面缺陷修复（首轮走查后）

首版界面在真机上暴露了四个问题，均已修复，并各自留下回归手段：

| 问题 | 现象 | 修法 |
| :--- | :--- | :--- |
| 标题栏与窗口按钮重叠 | 「刷新」压在关闭 ✕ 下面 | 按 `c.TitleBar()` 的 `Left/Right` 内边距布局；刷新移到页头 |
| 表格行被裁剪 | 两行模型表第二行只露半截 | 表格内不放交互控件；操作移到选中后的详情面板 |
| 24 小时图只剩一根柱 | 柱子居中、空桶不可见 | 每个桶是一列、柱底对齐；空桶画 3px 基线 |
| 设置页表单松散、保存按钮在屏幕外 | 标签与控件间距过大；保存要滚动 | `fieldWith(label, hint, control)` 分组；保存移回顶部 |

回归手段：

- `view_test.go` 的交互测试：点行 → 详情面板出现 → 断言面板标题与动作按钮存在；
- `table_test.go`：两行表必须都渲染（第二行文本可见）；
- `render_test.go`（`RENDER_UI=1` 时运行）：把 10 个页面渲染成 PNG 到 `build/ui/`，
  供人眼走查——正是它让上面四个问题被看见。

### P7 追加：状态页脚与供应商表单

| 交付项 | 说明 |
| :--- | :--- |
| 状态页脚 | 窗口底部 26 DIP：左「数据目录」，右「存储模式 · 适配器数 · 版本」 |
| 供应商新建/编辑表单 | 名称、类型（仅新建）、BaseURL、上游密钥、网络代理、自定义 Header、启用 |
| 密钥写入语义 | 新建必填并落为「默认」凭据；编辑时留空/掩码 = 不修改，填新值 = 轮换 |
| 自定义 Header | 多行 `Key: Value` 文本解析，非法行给出具体行号错误 |
| 校验 | 名称必填、新建需选类型与密钥、Header 格式；错误显示在表单顶部 |
| 回归测试 | `provider_form_test.go`：创建、编辑（不轮换密钥）、四种校验失败、Header 解析 |

实测：表单两列布局，在 1280×860 下一屏可见保存/取消；页脚与内容区不重叠。

### P7 二次修订：托盘 / 弹窗 / 协议 / 页脚

| 改动 | 说明 |
| :--- | :--- |
| 托盘图标 | `ui/tray.png`（32×32，由 `tools/logo/gen.py` 的图标缩放）；左键唤出窗口，菜单 打开管理界面 / 复制服务地址 / 退出；托盘不可用时只提示，不影响启动 |
| 供应商改为弹窗 | 用 `ui.Modal` 打开，Esc / 点遮罩关闭；从任何页面都能打开，与背后的列表不再混淆 |
| 类型 → 协议 | 下拉只有 `Anthropic Messages` / `OpenAI Chat` / `OpenAI Responses`；适配器由协议推导 |
| 状态信息移到页脚 | 标题栏只留品牌与版本；「运行中 / 服务地址 / 数据目录 / 在途 / 运行时长 / 存储 / 适配器数」全部进页脚 |

关于 Command Code：协议下拉里**不再出现**（它不是一种标准协议），
但 `command_code` 适配器仍然注册在进程里，历史供应商与模型继续工作。
彻底删除该适配器是另一件事——会同时删掉它的信封、指纹、窗口与上报实现及测试，
需要单独立项。

### P7 三次修订：表格操作列与选中行对比度

真机走查发现：选中行铺满主题蓝（`Accent`）后，单元格里显式设置的 `Text`/`TextMuted`
压过了继承的 `AccentText`，蓝底灰字无法阅读；行内操作按钮又撑高行、挤占列宽。

| 改动 | 说明 |
| :--- | :--- |
| 操作列 | 供应商 / 模型 / 凭据 / 客户端密钥四张表末尾加固定「操作」列（84 DIP），内放紧凑的 `ui.PopoverMenuButton`（「更多」，11px） |
| 下拉菜单 | 菜单项即原详情面板的动作（编辑/探活/启停/删除、固定/重置、轮换等）；菜单是窗口内 overlay，不撑行高、不被表格裁剪 |
| 选中行配色 | `rowColors` / `pillTone` 按选中态切换：选中行文字与药丸统一用 `AccentText`（白），药丸底色用半透明白；未选中保持原 `Text`/`TextMuted` 与语义色 |
| 详情面板只读化 | 动作全部收进下拉后，详情面板只展示状态与能力；面板里的药丸用 `Row` 包裹，避免被拉伸到卡片全宽 |

回归手段：

- `table_test.go` 新增 `TestTableRowActionsDropdown`（开菜单 → 点「停用」→ 菜单关闭 →
  轮询确认落库）与 `TestSelectedRowPalette`（选中/未选中两套配色对 `LightTheme` 断言）；
- `render_test.go` 增渲染 `providers-actions.png`（菜单展开态），人眼走查确认
  overlay 绘制在详情面板之上、选中行白字可读。

`go test ./...` 目前 **210 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 四次修订：模型卡片网格与上游模型发现

模型页从表格改为**卡片网格**（`ui.Grid` 三列），并接通了此前只有 HTTP API 的
模型发现链路（`admin.Service.DiscoverModels` / `ImportModels`，桌面端首次接线）。

| 改动 | 说明 |
| :--- | :--- |
| 模型卡片 | 每张卡片：对外名 + 「更多」下拉（启用/停用）、上游名、供应商 · 协议、能力徽标、价格与上下文（未设置如实显示）、可路由与启停药丸；不可路由原因单独一行 |
| 发现弹窗 | 模型页「从上游拉取」打开 `ui.Modal`：供应商下拉只列 `ModelDiscovery=true` 的适配器 → 「拉取模型列表」走 `GET /v1/models`（OpenAI 兼容）→ 复选框列表（已导入的禁用并标记）→ 「导入所选」按供应商当前协议批量创建 |
| 数据接线 | `/models` 页的 fetch 同时加载供应商列表供弹窗选择；导入协议取 `protocolOf`，表单之外的适配器回退 `vendor_native` |
| 移除 | 模型表格、选中态与只读详情面板（信息全部上卡片）；`modelSel` / `modelTable` / `capabilityList` 删除 |

回归手段：

- `models_test.go`：卡片菜单停用落库；httptest 假上游跑通 发现 → 勾选 → 导入
  （断言协议继承与已导入标记）；无可发现供应商时的引导与 Esc 关闭；
- `table_test.go` 的布局守卫改为卡片网格（每张卡片都必须渲染）；
- `render_test.go` 增渲染 `models-discover.png`（弹窗勾选态，种子数据不触网）。

`go test ./...` 目前 **213 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 五次修订：一次性明文复制按钮

客户端密钥页的一次性明文提示条右侧加「复制」按钮：经 `ui.Context.WriteClipboard`
写系统剪贴板（headless 测试走 tester 内存剪贴板），flash 提示「已复制到剪贴板」。
轮换产生的新明文共用同一提示条与按钮。

回归手段：`view_test.go` 的 `TestClientKeyCopyToClipboard` 断言点击后剪贴板内容
等于明文且出现确认提示；`render_test.go` 增渲染 `keys-created.png`（假密钥）。

`go test ./...` 目前 **214 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 六次修订：供应商接入方式分类（反代 / API）

供应商新增**类型**维度：`gateway.AccessMode` 中立枚举（`api` / `reverse_proxy`），
分类由适配器经可选接口 `AccessModeProvider` 自行声明（不实现默认 `api`），
`command_code` 声明为 `reverse_proxy`。注册表 `Descriptor.accessMode` 与
`admin.ProviderView.accessMode` 同源镜像，HTTP 出参自动携带；**不入库**，
随 kind 派生、只读。`internal/api` 零 diff。

| 改动 | 说明 |
| :--- | :--- |
| 列表 | 供应商表格加「类型」列：反代（强调色）/ API（中性色）徽标，选中行转白；详情面板同款徽标 |
| 新建表单 | 先选类型（API / 反代）：API 下维持三协议选择；反代下从注册表选适配器（当前为 Command Code），桌面端首次支持直接创建 CC 供应商 |
| 协议列 | 非三协议的适配器显示注册表标签（如 Command Code），不再露出原始 kind |

回归手段：

- `registry_test.go`：默认 `api` / 声明 `reverse_proxy` / 未知 kind 与 nil 注册表兜底；
- `command_code`：`AccessMode()` 声明断言；
- `provider_form_test.go`：反代分组创建 CC 落库（kind、accessMode、空 adapterConfig）；
  无适配器目录时「请选择适配器」校验；`view_test.go`：列表与详情的双徽标；
- `render_test.go`：`providers.png` 双徽标、`providers-form-reverse.png` 反代分组表单。

`go test ./...` 目前 **217 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 七次修订：表单描述收进图标 tooltip

字段的备注/描述不再各占一行灰字：`fieldWith` 把 hint 改为标签右侧的
11px info SVG 图标（`ui.Icon` + `Element.Tooltip`，悬停/聚焦 600ms 显示），
设置页与供应商表单同步生效；供应商弹窗的副标题说明收进标题旁同款图标。
图标带 `Label("<字段名>说明")`，读屏与测试可定位。表单高度明显下降。

回归手段：`TestFieldHintShowsAsTooltip` 断言描述悬停前不占布局、悬停后
出现；`render_test.go` 增渲染 `providers-form-tooltip.png`（tooltip 展开态）。

`go test ./...` 目前 **218 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 八次修订：供应商表单长字段独占一排

表单布局改为：名称与类型同行（编辑时无类型，名称独占一排）；协议/适配器、
BaseURL、网络代理、上游密钥、自定义 Header 各独占一排，长输入获得完整宽度。
协议/适配器分支抽为 `providerProtocolField`，行为不变（新建反代选适配器、
新建 API 选协议、编辑仅 OpenAI 系可切换，其余只读）。

回归手段：既有表单与 tooltip 用例全绿；`providers-form.png`、
`providers-form-reverse.png`、`providers-form-tooltip.png` 重渲染确认新布局。

`go test ./...` 目前 **218 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 九次修订：类型下拉溢出与按钮排列

类型列宽 110 小于 Select 控件的最小宽度（`theme.Space(35)` = 140），
下拉框溢出弹窗右边界；列宽改为 150 收进弹窗。底部操作按钮整行居右
（`FillWidth().Justify(ui.End)`），取消在左、保存在右。

回归手段：`TestProviderFormActionsLayout` 断言取消在保存左侧、同一行、
整行居右；`providers-form.png` / `providers-form-reverse.png` 重渲染确认
类型下拉不再溢出。

`go test ./...` 目前 **219 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 十次修订：模型对外名对齐契约（供应商/模型）+ 卡片复制

契约 §2.5 早已约定 `publicName` 留空按 `{provider.name}/{upstreamName}` 生成，
但 `defaultPublicName` 一直只返回上游名。本次对齐：新建 / 导入的模型默认
对外名即「供应商/上游名」，`GET /v1/models` 的 `id` 与客户端请求里的
`model` 字段都是这一整串（路由精确匹配，不拆斜杠）；存量模型不改名。
模型卡片标题行加「复制」按钮（复制对外名，flash 确认），标题悬停 tooltip
显示完整 ID；发现弹窗每行预览导入后的对外名。

回归手段：`TestModelsListingUsesNamespacedID` 端到端断言 /v1/models 列出
命名空间 ID 且该 ID 可路由；`TestModelCRUDAndRoutability` 精确钉住默认名；
`TestModelDiscoveryImport` 断言导入名与弹窗预览；`TestModelCardCopiesPublicName`
断言剪贴板内容；`models.png` / `models-discover.png` 重渲染目检。

`go test ./...` 目前 **221 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 十一次修订：模型页手动添加入口 + /v1/models 只列可路由模型

模型页顶部在「从上游拉取」之外补「添加模型」：弹窗选供应商、填上游名
（显示名可选），并实时预览对外模型 ID `{供应商名}/{上游名}`，右侧「复制 ID」
写入剪贴板；保存时 `publicName` 留空由服务端生成，上游协议取供应商当前协议
（供应商适配器无三种协议时回退 `vendor_native`）。空状态提示同步更新。

`GET /v1/models` 对齐契约 §3.4：改走 `admin.Service.ListModels` 并跳过
`Routable=false` 的模型——供应商停用或无活跃凭据时不再对外宣称这些模型。

回归手段：`TestModelManualAddDialog` 断言预览、复制与创建后的对外名；
`TestModelsListingSkipsNonRoutable` 断言加凭据前后列表的变化。

`go test ./...` 目前 **223 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 十二次修订：模型卡片支持删除

模型卡片「更多」下拉在启用/停用后补「删除」，走 `admin.Service.DeleteModel`
（后台执行、成功后刷新网格、失败红色 flash）。被路由模型成员引用时服务端
返回 409，界面如实展示原因，不做静默隐藏。

回归手段：`TestModelCardDeletesModel` 断言菜单删除后卡片从网格消失。

`go test ./...` 目前 **224 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 十三次修订：模型编辑（上下文 / 能力 / 三种价格）

模型卡片「更多」下拉补「编辑」：弹窗与添加共用同一表单，编辑时供应商与
上游协议只读，可改上游名、显示名、上下文窗口、最大输出、输入 / 缓存 / 输出
价格（每百万 token），并切换「支持识图」「支持工具调用」「启用」。对外模型
ID 原样保留（`publicName` 回填旧值），避免改了上游名就让客户端手上的 ID 失效；
数值字段按非负整数 / 非负数字校验，非法输入在弹窗内提示、不打服务端。

回归手段：`TestModelEditDialog` 断言弹窗预填、保存后上下文 / 价格 / 能力落库，
且 `publicName` 不变；`models-form.png` / `models-edit.png` 重渲染目检。

`go test ./...` 目前 **225 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 十四次修订：Token 统计模块

用量页从「按天流水」升级为以 token 为主的统计页：

- 顶部加时间范围（近 7 天 / 近 30 天（默认）/ 全部），与分组选择并列；
  范围同时约束汇总、趋势图和明细表，切换后重取；
- 汇总六块改为 请求（附平均每请求 token）、输入 token、输出 token、
  缓存 token（附占输入比例）、合计 token（输入 + 输出）、费用；
- 新增「Token 趋势」：按天堆叠柱，输入在下、输出在上；7 / 30 天视图
  补齐无用量日期（基线短柱），全部视图只画最近 60 天；下方给合计 / 峰值
  与首尾日期。缓存 token 是输入的子集，不叠加进柱子，避免重复计数；
- 明细表新增「合计」列（每行输入 + 输出）。

数据来自既有 `UsageAggregate`，未改契约：桌面端对 `/usage` 同时请求所选
分组与 `GroupByDay` 两份结果（`usageFrom()` 生成 UTC 起始日）。

回归手段：`TestUsageTokenStats` 断言六块汇总、趋势合计与平均每请求 token；
`TestUsageTableTotalColumn` 断言明细「合计」列；`TestUsageRangeWindow` 断言
40 天前的用量在 30 天视图隐藏、切「全部」后出现；`usage.png` 重渲染目检。

`go test ./...` 目前 **228 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P7 十五次修订：路由模型模块

侧栏「网关」组新增独立页 `/route-models`，把不同供应商下的模型聚合成一个对外 ID：

- 卡片网格：路由 ID + 复制 + 更多（编辑 / 启停 / 删除），策略与成员数，
  成员逐行显示 `供应商 · 对外模型 ID · P{优先级}`，底部可路由 / 启停药丸；
- 新建 / 编辑弹窗：路由 ID（即客户端 `model` 字段）、备注、启停、只读策略、
  全模型清单（跨供应商、可滚动）+ 复选框 + 每成员优先级 + 全选 / 清空；
  至少一个成员，优先级非负整数；编辑保留成员的 `enabled` 与 `weight`；
- 契约同步：`name` 不得与普通模型 `publicName` 相同（409，`field=name`）；
  `GET /v1/models` 现在也列出**已启用且至少一个成员可路由**的路由模型，
  `owned_by="route_model"`，能力取可路由成员交集、上下文取最小值；
  Key 白名单同时匹配模型 `publicName` 与路由 `name`。

数据面仍走既有 `Catalog.Resolve`：先查普通模型，再按路由 `name` 解析成员链；
路由请求的 `model` 字段只需填路由 ID。

回归手段：`TestRouteModelCreateSpansProviders` 断言跨供应商选择、优先级与复制 ID；
`TestRouteModelEditAndDelete` 断言成员整体替换与删除；
`TestRouteModelRoutesByPublicID` 端到端断言 `/v1/models` 列出路由 ID 且以该 ID
请求命中上游；`TestRouteModelValidation` 断言同名模型 ID 返回 409；
`route-models.png` / `route-form.png` 重渲染目检。

`go test ./...` 目前 **231 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P9 · 流式用量、供应商代理与表单编辑锁定

P9 首轮（`b9f865d`）交付桌面 UI 打磨与流式可靠性修复后，实际客户端又暴露了
四处问题，本轮一并收口：

| 问题 | 现象 | 修法 |
| :--- | :--- | :--- |
| 流式用量丢失 | relay 不转发解码器的 `ChunkDone`，只调编码器 `Close()`；chat 编码器把 usage 帧写在 `Done` 分支，导致流式客户端永远见不到 token 用量 | `Close()` 补写 usage 帧（在 `[DONE]` 之前），与 `Done` 分支共用 `emitUsage()` |
| 无 id 的完整工具调用被拼成一条 | 上游每帧给一个完整调用、省略 id 并复用 `index 0`，编码器把两次调用的参数串成一个非法 JSON | 编码器按「槽内参数已是完整 JSON」判定新调用，另开 wire `index`（`toolSlots`） |
| 供应商代理不生效 | 标准适配器的 `proxyUrl` 只影响 CC 适配器；`openai_compatible` / `anthropic` 始终走共享客户端 | `provider.HTTPClientFor(base, proxyURL)`：空代理复用共享客户端，非空按「代理 + 超时」缓存 transport；`Do` / `Probe` / `DiscoverModels` 统一经它 |
| 行菜单编辑错对象 | 行内「更多 → 编辑」无需先选中行，旧代码按 `providerSel` 记述对象；后台刷新重排后可能改错供应商 | 表单改记 `formProviderID`，协议行与保存都按 id 回查；供应商消失时协议行退化为提示 |

另外补上 `.gitattributes`：`*.go` 固定 LF（Windows 的 `core.autocrlf=true`
会把工作区写成 CRLF，让 `gofmt -l .` 把每个文件都报出来），`testdata/golden/**`
标记 `-text`（金标准逐字节比较，行尾也属于内容）。

回归手段：`TestChatStreamEncoderEmitsUsageOnClose` 断言 `Close()` 在 `[DONE]`
前写出 usage 帧；`TestChatStreamCompleteCallsWithoutIDsStaySeparate` 与
`TestChatStreamArgsCompleteChunksWithoutIDStaySeparate` 断言两条独立调用且参数
各自是合法 JSON；`TestDoUsesProviderProxy`（两个标准适配器各一份）与
`TestHTTPClientFor*` 断言代理真的被走、空代理复用共享客户端、按地址缓存；
`TestEditProviderFromRowAction` 断言行菜单编辑按 id 定位并保留协议。

`go test ./...` 目前 **263 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### P10 · 数据面失败语义与超时收口

本轮只修数据面，不新增接口：

| 问题 | 现象 | 修法 |
| :--- | :--- | :--- |
| 供应商超时误伤响应体 | `adapter.Do` 返回后立即 `cancel()`，随后读取 `resp.Body` 一律 `context canceled`；只要配置 `timeoutMs > 0`，非流式与流式都失败 | 成功响应体包一层 `cancelOnCloseBody`，超时在响应体 `Close()` 时才释放；非 2xx/错误路径立即释放 |
| 失败流仍写成功终止符 | 空闲超时、解码错误、缺 finish 的截断路径都调用 `Close()`，客户端收到 `[DONE]` / `message_stop` / `response.completed`，把截断当成完整答案 | `StreamEncoder` 新增 `Abort()`；失败路径写流内错误帧后 `Abort()`，保留已发内容但不写终止符 |
| `finish_reason=error` 被当成成功 | relay 只把空 finish 视为截断，`error` / `upstream_error` 仍按 200 记账 | `completionFailure()` 统一判定；失败 finish 立即 502 + 流内错误 |
| 非流式无空闲看门狗 | `Limits.NonStreamIdle` 定义后从未使用；上游发完响应头后挂起会无限等待 | `readBodyWithIdle()` 在缓冲读取上套可重置计时器，超时返回 504，记录 `upstream_read_timeout` |

另外把流式 `select` 里的 `time.After` 换成单个可重置 `Timer`，避免每收到一个 chunk 泄漏一个计时器。

回归手段：`TestProviderTimeoutCoversResponseBody`（`timeoutMs > 0` 的流式/非流式均成功）、
`TestStreamIdleTimeoutDoesNotFakeCompletion`（504 + 流内错误 + 无 `[DONE]`）、
`TestNonStreamIdleTimeout`（504 `upstream_read_timeout`）、
`TestStreamFinishErrorIsUpstreamFailure`（失败 finish → 502）、
`TestStreamEncoderAbortWithholdsTerminator`（三协议 `Abort()` 后 `Close()` 零写入）。

`go test ./...` 目前 **270 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

### 全部里程碑完成

P0–P10 交付完毕。剩余的可选项（不在原计划内）：

- 供应商 / 模型 / 密钥的新建表单（目前只能走 HTTP API）；
- 托盘图标；
- macOS dmg 与 CI；
- 账号用量面板。

`go test ./...` 目前 **263 个用例**，`gofmt -l .` 与 `go vet ./...` 干净。

