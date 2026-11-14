# AGENTS.md · 协作红线

本文件约束**所有**参与 AdaPrune 的开发者与智能体。与代码冲突时以本文档为准；
与 [docs/05-api-contract.md](docs/05-api-contract.md) 冲突时，先改契约再改代码。

---

## 1. 项目一句话

本地 AI 网关：**主干协议中立**，**Command Code 只是一个供应商适配器**。

> **本项目是纯重写（greenfield rewrite）**：不继承旧项目（`ada_proxy - 副本` 等）的
> 数据库、配置、HTTP 路径、环境变量、密钥前缀。旧代码里值得保留的**算法与协议知识**
> 会重新实现进适配器，但**没有任何兼容层**。

## 2. 绝对不做

1. **不允许把供应商私有语义写进主干**。
   `internal/gateway`（`ir/`、`codec/`、`relay.go`、`router.go`、`adapter.go`）里
   禁止出现任何**供应商标识或私有线格式**，包括但不限于：
   `command_code`、`command-code`、`commandcode`、`x-command-code-version`、
   `api.commandcode.ai`、`cliMode`、`threadId`、`zdr`、`x-cmd-zdr`、`envelope`、
   `lifecycle-events`、`fingerprint/record`。
   判定标准：

   ```bash
   grep -rniE "command_code|command-code|commandcode|cliMode|threadId|zdr|envelope|lifecycle-events" internal/gateway/   # 必须为空
   ```

   > 通用抽象（`Adapter`、`FingerprintPolicy`、`QuotaPolicy`、`SessionHints` 等**中立概念**）允许存在于主干；
   > 但**如何派生、如何上报、如何分类**必须全部在 `internal/provider/<kind>` 里实现。
2. **不允许适配器反向依赖存储**。
   `internal/provider/**` 禁止 import `internal/store`、`internal/admin`、`internal/api`。
   需要的数据由 `InvokeRequest` 传入；需要的持久化走 `StateStore`。
3. **不允许明文密钥出现在任何出口**。
   - 数据库：上游密钥必须 `secretbox.Encrypt`；
   - API 出参：恒掩码（`secretbox.Mask`）；
   - 日志：禁止打印密钥、`Authorization`、完整上游请求/响应体；
   - 测试快照/金标准：只允许假密钥。
4. **不允许引入向后兼容层**。
   本项目**不做**旧库/旧配置/旧路径/旧密钥前缀/旧环境变量的兼容与迁移。
   确需保留某个与旧系统相同的语义，只能作为**新设计的自然结果**，不得为兼容而妥协设计。
5. **不允许静默降级上游失败**。
   流被截断、finish 缺失、上游连接错误 → 一律 502（可重试），
   **绝不补 `stop` 谎报完成**；零输出 → 429。
6. **不允许在数据面自动重试非额度错误**（会重复计费）。

## 3. 必须做

1. **契约先行**：改接口 → 先改 [docs/05-api-contract.md](docs/05-api-contract.md) → 再改代码 → 补契约测试。
2. **新增供应商只加一个包**：`internal/provider/<kind>/` + `init()` 注册；
   `internal/gateway` 与 `internal/api` 零 diff。
3. **纯函数内核**：`gateway/ir`、`gateway/codec` 必须无网络、无文件、无全局状态，可表驱动单测。
4. **`context` 贯穿**：上游出网必须带超时；下游断开即取消上游。
5. **流式红线**：`WriteTimeout=0`、每事件 `Flush()`、边读边写、入站协议决定出站事件形态。
6. **能力先校验**：入站要求流式/工具/视觉时，先查「适配器能力 ∧ 模型能力」，不支持直接 400，不打上游。
7. **降级要留痕**：无法无损映射的字段记 `warnings[]`，不 panic、不阻断。
8. **提交前自检**：
   ```bash
   gofmt -l .        # 必须为空
   go vet ./...      # 必须通过
   go test ./...     # 必须全绿
   ```

## 4. 目录归属（谁能改什么）

| 目录 | 职责 | 修改约束 |
| :--- | :--- | :--- |
| `internal/gateway/ir` | 规约类型 | 变更需同步 [04](docs/04-protocol-conversion.md)，向后兼容 |
| `internal/gateway/codec` | 三协议编解码 | 新协议只加文件；不改已有映射的语义（只修 bug） |
| `internal/gateway/*.go` | 编排/路由/额度 | 禁止出现供应商专有名词 |
| `internal/provider/<kind>` | 适配器实现 | 供应商私有语义**只能**在这里 |
| `internal/store` | 持久化 | 表结构变更需同步 [02 §6](docs/02-architecture.md) |
| `internal/admin` | 控制面业务 | 不 import `net/http`（HTTP 适配在 `internal/api/adminhttp`） |
| `internal/api` | HTTP 适配 | 只解析/鉴权/序列化，不写业务规则 |
| `internal/api/adminhttp` | 控制面路由 | 只做参数解析与响应序列化，业务全在 `admin.Service` |
| `ui` | 原生 UI | 只调 `admin.Service` 与 `app`，不直接碰 store |
| `docs/` | 设计 | 改契约必须改文档 |
| `testdata/golden/` | 金标准 | 变更必须在提交说明里写清原因 |

## 5. 命名与风格

- 模块名 `github.com/learningdog1/ada_prune`；包名短小写；
- 文件名用下划线分段（`openai_chat.go`、`page_providers.go`）；
- 错误值用 `errors.New`/`fmt.Errorf("...: %w")`，对外的错误消息**英文且稳定**（客户端可匹配）；
- 导出符号必须有注释，首句以符号名开头；
- 任何 `context.Context` 必须是函数第一个参数；
- 不使用 `panic` 处理业务错误（仅用于「不可能发生」的初始化失败）。

## 6. 测试红线

1. `gateway/codec` 的每个方向都必须有请求/非流式/流式三条用例；
2. CC 适配器的信封与指纹必须与金标准**逐字节**一致；
3. 任何「失败语义」（截断、零输出、额度耗尽、瞬时限速、客户端断开）都必须有测试；
4. 存储层必须有「空库启动 / 建表 / 唯一约束 / 删除守卫」测试；
5. UI 的 `ui.NewTester` 用例不得依赖真实网络与真实数据目录。

## 7. 安全红线

- 禁止把任何密钥写入日志、错误消息、HTTP 响应、金标准文件；
- 管理写操作必须校验同源 `Origin`；`PRUNE_ADMIN_TOKEN` 比较必须常数时间；
- 非回环监听时启动日志必须告警；
- 上传/导入类接口必须有大小上限；
- 依赖新增需说明理由（本项目坚持「单二进制、纯 Go、无 CGO」）。

## 8. 提交与评审

- 一个里程碑一个 PR，PR 标题带阶段编号（如 `P2: command_code 适配器`）；
- PR 描述必须包含：改动范围、契约影响、验证方式（跑了哪些测试）；
- 金标准变更、行为变化必须单列一节；
- 评审关注：是否泄漏供应商语义到主干、是否有回归测试。

## 9. 文档纪律

- 设计变更先改 `docs/`，再改代码；
- 文档中的 JSON 示例必须与契约一致（改契约时一起改示例）；
- 新增适配器时，必须在 [docs/03 §6](docs/03-provider-adapter.md) 的「新增供应商标准步骤」后追加实际实现说明。
