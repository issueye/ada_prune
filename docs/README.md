# AdaProxy 文档索引

> 通用型本地 AI 网关（Local AI Gateway）设计套件。
> 本目录是**设计阶段**的唯一事实源：先定契约，再写代码。

| 项目 | 内容 |
| --- | --- |
| 产品代号 | **AdaProxy**（`ada_proxy`） |
| 版本 | v1.0（设计稿，从零重写） |
| 日期 | 2026-11-07 |
| 设计参考 | [`aluka_cloud/llm`](file:///E:/codes/go_projects/aluka_cloud/llm)（供应商/模型/密钥/协议转换网关） |

---

## 1. 一句话定位

一个跑在本机的**通用 AI 网关**：对下游只暴露 OpenAI Chat / OpenAI Responses /
Anthropic Messages 三种标准协议；对上游用**供应商适配器**接入任意服务
——**Command Code 只是其中一个适配器**，它的私有信封、设备指纹、窗口额度、生命周期上报
全部收敛在 `internal/provider/command_code/` 内，不泄漏到网关主干。

```
下游 SDK / CLI ──► 统一出口（三协议） ──► 协议内核（IR） ──► 供应商适配器 ──► 上游
                                                  ├─ command_code（信封 + 指纹 + 窗口）
                                                  ├─ openai_compatible
                                                  ├─ anthropic
                                                  └─ 任意自定义适配器
```

> 本项目是**纯重写**：不继承旧项目的数据库、配置、HTTP 路径、环境变量。
> 旧代码里经过实测的**协议知识**被固化为规格（[03](03-provider-adapter.md) / [04](04-protocol-conversion.md)），
> 然后重新实现。

## 2. 文档清单

| 文档 | 内容 | 读者 |
| :--- | :--- | :--- |
| [01-requirements.md](01-requirements.md) | 产品定位、术语、功能需求 F1–F10、非功能需求、验收标准 DoD | 全员 |
| [02-architecture.md](02-architecture.md) | 分层与依赖方向、数据模型、装配、后台任务、里程碑 | 后端 |
| [03-provider-adapter.md](03-provider-adapter.md) | **供应商适配器规格**：Adapter 接口、能力位、Command Code 实现、指纹与额度抽象 | 后端 |
| [04-protocol-conversion.md](04-protocol-conversion.md) | 协议规约 IR、三 Codec、9 组转换、流式状态机、Command Code 事件桥 | 后端 |
| [05-api-contract.md](05-api-contract.md) | 控制面 `/admin/api/*` 与数据面 `/v1/*` 接口契约（唯一事实源） | 前后端 |
| [06-ui-plan.md](06-ui-plan.md) | 桌面原生 UI 信息架构、页面与交互、组件与设计语言 | 前端/UI |
| [07-roadmap.md](07-roadmap.md) | 实施路线（P0–P8）、工作量、技术取舍、风险 | 全员 |
| [AGENTS.md](../AGENTS.md) | 开发者 / 智能体协作红线 | 全员 |

## 3. 阅读顺序

1. **01** 搞清楚「要做什么、做到什么程度算完成」；
2. **02** 看骨架：包怎么分、表怎么建、谁依赖谁；
3. **03** 是本项目的**核心差异点**：适配器怎么把「指令码」这类私有上游纳入通用框架；
4. **04** 是协议转换内核，决定「三协议互转」是否成立；
5. **05** 是契约，改接口必须先改它；
6. **06/07** 是落地执行部分。

## 4. 关键设计取舍

| 维度 | 决策 | 理由 |
| :--- | :--- | :--- |
| 上游接入 | **适配器插件化**，`provider.kind` 选实现 | 新增供应商只加一个包，主干零改动 |
| 协议转换 | **3 个 Codec + 1 个 IR**，而非两两转换器 | 9 组方向自动成立；新增协议只加一个 Codec |
| Command Code | 作为 `command_code` 适配器 | 信封/指纹/窗口/上报都是"怪癖"，必须被隔离 |
| 指纹 | 通用 `FingerprintPolicy` 接口 | 其它供应商返回 nil；将来第二家需要伪设备时直接复用 |
| 额度 | 通用 `QuotaPolicy` + 四类语义 | credit/window/rate_limit/none 决定是否轮转 |
| 控制面表单 | 后端返回 `configSchema`，前端动态渲染 | 新增适配器时界面自动适配 |
| 存储 | SQLite，手写 SQL | 单机、表少、纯 Go、无 CGO |
| 前端 | mygo 原生 UI | 单二进制、无 webview、无前端构建 |
