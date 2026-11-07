<p align="left">
  <img src="docs/screenshots/logo.png" width="96" alt="AdaProxy">
</p>

# AdaProxy

**通用型本地 AI 网关**：一个单可执行文件，一张桌面原生界面，把多种上游 AI 供应商
统一成一套 OpenAI / Anthropic 兼容端点。

> 状态：**P0–P8 全部完成**——三适配器、数据面、控制面、桌面原生 UI、打包与冒烟测试都已就绪。
> 设计文档见 [docs/](docs/README.md)，进度见 [docs/07-roadmap.md §8](docs/07-roadmap.md)。

---

## 这是什么

```
下游工具（Codex / Claude Code / Cline / 脚本）
        │  OpenAI Chat · OpenAI Responses · Anthropic Messages
        ▼
┌───────────────────────────────────────────────┐
│  AdaProxy（本机单进程）                        │
│                                               │
│   协议内核（IR，9 组转换）                      │
│        │                                      │
│        ▼                                      │
│   供应商适配器层                                │
│    ├── command_code   信封 / 指纹 / 窗口 / 上报 │
│    ├── openai_compat  标准 OpenAI              │
│    ├── anthropic      标准 Anthropic           │
│    └── custom         自定义扩展                │
└───────────────────────────────────────────────┘
        │
        ▼
   上游 AI 供应商
```

**核心设计**：Command Code 不是一个特殊的上游，而是适配器层里 `provider/command_code`
的一个实现。它的信封（envelope）、设备指纹（fingerprint）、会话（session）、
额度窗口（quota window）、生命周期上报（lifecycle report）全部收敛在该适配器内，
网关主干只认中立 IR。

## 能力一览

| # | 能力 | 说明 |
| :-: | :--- | :--- |
| 1 | **供应商管理** | 多供应商（CC / OpenAI 兼容 / Anthropic / 自定义），BaseURL、超时、代理、附加头、**适配器动态配置**、连通性测试、模型发现、运行时状态面板 |
| 2 | **模型管理** | 对外名与上游名解耦、上游协议、价格、上下文、能力位、可路由判定、批量导入 |
| 3 | **密钥管理** | 上游凭据（多账号轮转、AES-256-GCM 加密、批量/CLI 导入、pin）+ 客户端密钥（`sk-adp-*`、额度、模型白名单、IP 白名单、轮换） |
| 4 | **协议转换** | 3 编解码器 + IR，9 组方向互转，覆盖请求 / 非流式 / 流式 |
| 5 | **额度与轮转** | 信用耗尽落库换号；窗口到顶内存阻塞到期自恢复；瞬时限速不轮转 |
| 6 | **指纹抽象** | `FingerprintPolicy` 通用接口；CC 实现决定论伪设备 + 周期性上报 |
| 7 | **可观测** | 请求日志（含 TTFT / 费用 / 供应商维度）、日聚合、实时日志流、账号用量面板 |
| 8 | **双形态交付** | 桌面（mygo 原生 UI，无 webview）+ 无头服务，单二进制 |

## 文档索引

| 文档 | 内容 |
| :--- | :--- |
| [docs/01-requirements.md](docs/01-requirements.md) | 需求、功能清单 F1–F10、非功能需求、验收标准 |
| [docs/02-architecture.md](docs/02-architecture.md) | 分层与依赖方向、数据模型、装配、后台任务、里程碑 |
| [docs/03-provider-adapter.md](docs/03-provider-adapter.md) | **供应商适配器规格**：接口、能力位、Command Code 实现、指纹与额度抽象 |
| [docs/04-protocol-conversion.md](docs/04-protocol-conversion.md) | 协议规约 IR、三 Codec、9 组转换、流式状态机、CC 事件桥 |
| [docs/05-api-contract.md](docs/05-api-contract.md) | 控制面与数据面接口契约（唯一事实源） |
| [docs/06-ui-plan.md](docs/06-ui-plan.md) | 桌面原生 UI 信息架构、页面与交互 |
| [docs/07-roadmap.md](docs/07-roadmap.md) | 实施路线（P0–P8）、工作量、技术取舍、风险 |
| [AGENTS.md](AGENTS.md) | 开发者 / 智能体协作红线 |

## 数据面端点

| 路径 | 协议 |
| :--- | :--- |
| `POST /v1/chat/completions` | OpenAI Chat |
| `POST /v1/responses` | OpenAI Responses |
| `POST /v1/messages` | Anthropic Messages |
| `GET /v1/models` | 当前客户端密钥可见模型 |

## 控制面

```
监控   总览 / 请求日志 / 用量统计 / 账号用量
网关   供应商 / 模型 / 路由模型 / 凭据 / 客户端密钥
设置   端点 / 设备指纹 / 设置 / 关于
```

导航由适配器能力驱动：没有账号用量能力的适配器，就不显示「账号用量」页；
没有指纹的适配器，就不显示「设备指纹」页。

## 技术栈

- **语言**：Go 1.27+
- **存储**：SQLite（`modernc.org/sqlite`，纯 Go，无 CGO），失败降级内存
- **HTTP**：标准库 `net/http` + `ServeMux`，无 Web 框架
- **桌面**：[mygo](https://github.com/egoist/mygo) v0.7.4 原生 UI（Go 自绘）
- **加密**：AES-256-GCM（`data/.secret.key`）
- **交付**：桌面（`AdaProxy.exe`）与无头（`adp-server.exe`）两个单可执行文件

## 快速开始

```bash
# 无头（默认 127.0.0.1:3050，数据在可执行文件旁的 data/）
go run ./cmd/adp-server

# 桌面（原生窗口 + 进程内网关）
go run ./cmd/adp-desktop

# 指定数据目录与端口
ADP_DATA_DIR=./data ADP_PORT=3999 go run ./cmd/adp-server
```

首启会在数据目录生成 `config.json`、`.secret.key`（AES-256-GCM 主密钥）、
`adaproxy.db`（SQLite）与 `logs/`。

```bash
curl http://127.0.0.1:3050/health
curl http://127.0.0.1:3050/admin/api/overview
curl http://127.0.0.1:3050/admin/api/adapters
```

## 构建与发布

```powershell
# Windows：GUI 无控制台桌面程序 + 无头服务
powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1

# 只做一次普通构建（跳过 mygo 的图标/清单注入；产物仍无控制台窗口）
powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1 -Plain
```

产物：

| 文件 | 说明 |
| :--- | :--- |
| `build/windows-amd64/AdaProxy.exe` | 桌面应用，**GUI 子系统**（脚本会读 PE 头校验，仍是控制台就报错），带图标、版本信息与 DPI manifest |
| `build/windows-amd64/adp-server.exe` | 无头服务，控制台程序 |

桌面应用与无头服务共享同一份数据目录格式与业务层，区别只在于是否开窗。

## 冒烟测试

```powershell
powershell -ExecutionPolicy Bypass -File scripts/smoke.ps1
```

脚本会起一个假上游（`tools/mockupstream`，说 Command Code 的 NDJSON 协议）与网关，
在临时目录里跑完整链路：

```
建供应商 → 加凭据 → 建模型 → 建客户端密钥
  → /v1/chat/completions（非流式）
  → /v1/messages（Anthropic 入站，转成 Chat 打上游）
  → /v1/chat/completions（流式，校验 [DONE]）
  → 无密钥调用应得 401
  → 日志与用量已落库
```

`-Keep` 保留临时数据目录便于排查；`-GatewayPort` / `-UpstreamPort` 可改端口。

## 接入方式

```bash
# OpenAI 兼容
curl http://127.0.0.1:3050/v1/chat/completions \
  -H 'Authorization: Bearer sk-adp-xxxx' -H 'Content-Type: application/json' \
  -d '{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"你好"}]}'

# Anthropic 兼容（Claude Code 等）
export ANTHROPIC_BASE_URL=http://127.0.0.1:3050
export ANTHROPIC_AUTH_TOKEN=sk-adp-xxxx
```

## 测试

```bash
gofmt -l .        # 必须为空
go vet ./...      # 必须通过
go test ./...     # 208 个用例
```

## 许可

MIT。Command Code 协议层与指纹算法派生自
[MAXeaglet/commandcode-proxy](https://github.com/MAXeaglet/commandcode-proxy)（MIT）；
沿用 [LICENSE](LICENSE) 与 [NOTICE](NOTICE)。
