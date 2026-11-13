# 05 · 接口契约（唯一事实源）

| 项目 | 内容 |
| --- | --- |
| 文档编号 | DOC-05 |
| 版本 | v1.0 |
| 日期 | 2026-11-07 |
| 上游 | [01](01-requirements.md) · [02](02-architecture.md) · [03](03-provider-adapter.md) · [04](04-protocol-conversion.md) |
| 约定 | **改接口先改本文档**；控制面使用统一响应包；数据面遵循协议官方格式 |

---

## 1. 通用约定

### 1.1 控制面响应包

保持简单：不引入业务码包。

```json
{ "ok": true, "data": { } }
```

错误：

```json
{ "error": "provider name already exists", "code": "conflict", "field": "name" }
```

| 字段 | 说明 |
| :--- | :--- |
| `error` | 人类可读消息（英文，稳定，客户端可匹配） |
| `code` | 机器可读：`bad_request` / `unauthorized` / `forbidden` / `not_found` / `conflict` / `internal` |
| `field` | 可选，表单字段名，便于 UI 高亮 |

HTTP 状态：`400 / 401 / 403 / 404 / 409 / 500`。
分页：`{ "rows": [], "total": 0, "limit": 20, "offset": 0 }`。

### 1.2 数据面响应

- **成功**：按入站协议返回官方响应体（`application/json` 或 `text/event-stream`），不包响应包；
- **失败**：各协议自己的错误体（`{"error":{"message","type","code?"}}`），HTTP 状态按 [03 §8](03-provider-adapter.md)；
- 响应头回填：`X-Request-ID`、`X-Adapter-Kind`、`X-Provider`、`X-Model`、`X-Usage-Tokens`（非流式）。

### 1.3 鉴权

| 面 | 凭据 | 免鉴权 |
| :--- | :--- | :--- |
| 控制面 `/admin/api/*` | `Authorization: Bearer <PRUNE_ADMIN_TOKEN>` 或 `?token=` | 未设 token 时全放行（仅回环地址） |
| 数据面 `/v1/*` | `Authorization: Bearer sk-prune-*` 或 `x-api-key` | `endpoint.auth_required=false` 或 `allowAnonymous=true` |

- 控制面写操作（非 GET/HEAD）校验 `Origin` 与 `Host` 同源；
- token 比较使用常数时间比较；
- 数据面 Key 校验顺序：**格式 → hash 命中 → 启用 → 未过期 → IP 白名单 → 模型许可 → 额度**；
  任一步失败统一返回，不泄漏具体失败点（除 429 额度外）。

## 2. 控制面接口

### 2.1 概览

#### `GET /admin/api/overview`

```json
{
  "version": "0.3.0",
  "startedAt": 1793500000000,
  "uptimeSec": 3600,
  "host": "127.0.0.1",
  "port": 3050,
  "degraded": false,
  "inflight": 2,
  "providers": { "total": 2, "enabled": 2, "routable": 2 },
  "credentials": { "total": 5, "active": 4, "exhausted": 1 },
  "models": { "total": 18, "enabled": 17, "routable": 15 },
  "clientKeys": { "total": 3, "active": 3 },
  "today": { "requests": 120, "errors": 2, "inputTokens": 41000, "outputTokens": 9000, "cachedTokens": 12000, "cost": 0.82 },
  "last24h": { "points": [{ "ts": 1793496400000, "requests": 5, "inputTokens": 800, "outputTokens": 200 }], "peak": 12, "total": 120 },
  "adapters": [
    { "kind": "command_code", "capabilities": { "stream": true, "tools": true, "vision": true, "fingerprint": true, "quota": true, "protocolVersion": "1.53.1" } }
  ]
}
```

### 2.2 适配器元信息

#### `GET /admin/api/adapters`

控制面渲染「新建供应商」表单用，**完全由注册表驱动**。

```json
{
  "rows": [
    {
      "kind": "command_code",
      "label": "Command Code",
      "accessMode": "reverse_proxy",
      "capabilities": { "stream": true, "tools": true, "vision": true,
                        "modelDiscovery": true, "accountUsage": true,
                        "fingerprint": true, "sessionAffinity": true,
                        "quota": true, "protocolVersion": "1.53.1" },
      "configSchema": [
        { "key": "apiBase", "label": "API 根地址", "type": "string",
          "default": "https://api.commandcode.ai", "required": true,
          "description": "留空则使用供应商 BaseURL" },
        { "key": "cliMode", "label": "CLI 模式", "type": "select",
          "default": "agent", "options": ["agent", "chat"], "required": false },
        { "key": "projectDir", "label": "伪工作目录", "type": "string",
          "default": "C:\\Users\\dev\\projects\\app", "required": false,
          "description": "决定指纹与项目 slug，改动会改变设备身份" }
      ]
    },
    { "kind": "openai_compatible", "label": "OpenAI 兼容", "accessMode": "api", "capabilities": { "...": true }, "configSchema": [] }
  ]
}
```

`accessMode` 是接入方式分类：`api`（持密钥直连供应商 API）/ `reverse_proxy`
（经逆向的客户端/服务中转）。它是适配器的固有属性，由注册表派生，**只读**；
未声明的适配器默认 `api`。

### 2.3 供应商

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/providers?limit&offset&enabled&kind` | 列表（`credentialCount`、`modelCount`、`routable`、`lastProbe`） |
| GET | `/admin/api/providers/{id}` | 详情（含 `adapterConfig` 与 `status` 摘要） |
| POST | `/admin/api/providers` | 创建 |
| PUT | `/admin/api/providers/{id}` | 更新（`adapterConfig` 整体替换） |
| DELETE | `/admin/api/providers/{id}` | 删除（有启用模型 → 409） |
| POST | `/admin/api/providers/{id}/probe` | 连通性测试 |
| GET | `/admin/api/providers/{id}/status` | 适配器运行时状态（`StatusProvider`） |
| POST | `/admin/api/providers/{id}/discover-models` | 拉取上游模型目录（`ModelDiscovery`） |
| POST | `/admin/api/providers/{id}/refresh` | 触发适配器刷新（CC：模型目录 + 账号用量） |

创建/更新 body：

```json
{
  "name": "Command Code 主号",
  "kind": "command_code",
  "baseUrl": "https://api.commandcode.ai",
  "enabled": true,
  "weight": 1,
  "timeoutMs": 0,
  "proxyUrl": "",
  "extraHeaders": { "x-custom": "1" },
  "adapterConfig": { "cliMode": "agent", "projectDir": "C:\\Users\\dev\\projects\\app" },
  "remark": ""
}
```

响应（详情）：

```json
{
  "id": 1, "name": "Command Code 主号", "kind": "command_code", "accessMode": "reverse_proxy",
  "baseUrl": "https://api.commandcode.ai", "enabled": true, "weight": 1,
  "timeoutMs": 0, "proxyUrl": "", "extraHeaders": {}, "adapterConfig": { "cliMode": "agent" },
  "remark": "", "createdAt": 1793500000000, "updatedAt": 1793500000000,
  "credentialCount": 3, "activeCredentialCount": 2, "modelCount": 12, "enabledModelCount": 12,
  "routable": true, "routableReason": "",
  "status": {
    "protocolVersion": "1.53.1",
    "drift": { "lastCheckedAt": 1793500000000, "latestVersion": "1.58.1", "changed": true, "lastError": null },
    "fingerprintsReportedAt": 1793500000000,
    "accountUsageFetchedAt": 1793500000000
  }
}
```

供应商出参的 `accessMode` 与适配器目录同源（随 kind 派生、只读）；
创建/更新 body **不接受**该字段，kind 不可变意味着分类也不可变。

`GET /providers/{id}/status` 返回适配器自定义结构（CC 示例）：

```json
{
  "protocolVersion": "1.53.1",
  "drift": { "lastCheckedAt": 0, "latestVersion": null, "changed": false, "lastError": null },
  "credentials": [
    { "credentialId": 1, "name": "主号", "thumbmark": "ab12…",
      "nextInitAt": 1793532800000,
      "fingerprintReport": { "ts": 1793500000000, "ok": true },
      "lifecycleReport":    { "ts": 1793500000000, "ok": true },
      "window": { "blocked": false, "source": null, "window": null, "until": null } }
  ],
  "modelCatalog": { "source": "dynamic", "count": 74, "fetchedAt": 1793500000000 }
}
```

`POST /providers/{id}/probe` 响应：

```json
{ "ok": true, "status": 200, "durationMs": 231, "detail": "{\"ok\":true,...}", "models": 74 }
```

`POST /providers/{id}/discover-models` 响应（候选，不落库）：

```json
{ "source": "dynamic", "rows": [ { "id": "deepseek/deepseek-v4-flash", "name": "DeepSeek V4 Flash", "plan": "go", "exists": true } ] }
```

### 2.4 凭据

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/credentials?providerId&status` | 列表（`secret` 恒掩码） |
| POST | `/admin/api/credentials` | 创建 `{providerId,name,secret,priority,expiresAt,remark}` |
| PUT | `/admin/api/credentials/{id}` | 更新（`secret` 传掩码/空 → 不改） |
| DELETE | `/admin/api/credentials/{id}` | 删除（若被 pin 固定则一并清除 pin） |
| POST | `/admin/api/credentials/{id}/status` | `{status: "active"|"disabled"|"exhausted"}` |
| POST | `/admin/api/credentials/{id}/test` | 单凭据探活（CC：whoami） |
| POST | `/admin/api/credentials/batch` | `{providerId,text}` 批量导入 |
| POST | `/admin/api/credentials/import-cli` | 从 `~/.commandcode/auth.json` 导入（CC） |
| GET | `/admin/api/credentials/{id}/usage` | 账号用量（CC，缓存读） |
| POST | `/admin/api/credentials/{id}/usage/refresh` | 强制刷新单个 |
| POST | `/admin/api/credentials/usage/refresh` | 强制刷新全部 |
| POST | `/admin/api/credentials/pin` | `{id|null}` 固定用于路由的凭据 |

凭据行：

```json
{ "id": 1, "providerId": 1, "providerName": "Command Code 主号", "providerKind": "command_code",
  "name": "主号", "secret": "user_abc1…wxyz", "masked": true,
  "enabled": true, "status": "active", "priority": 0, "pinned": false,
  "expiresAt": null, "lastUsedAt": 1793500000000, "createdAt": 1793500000000 }
```

批量导入结果：

```json
{ "created": [ { "id": 5, "name": "account-5" } ], "existing": [ { "id": 2, "name": "account-2" } ], "unmatchedLines": 0 }
```

账号用量行（CC）：

```json
{ "credentialId": 1, "name": "主号", "status": "active", "fetchedAt": 1793500000000, "error": null,
  "pinned": false,
  "data": {
    "whoami": { "userName": "u", "email": "u@example.com", "orgId": "org_1" },
    "plan": { "id": "individual-go", "name": "Go", "status": "active", "daysRemaining": 12, "monthlyTotal": 10 },
    "credits": { "monthly": 3.2, "purchased": 0, "free": 0, "totalRemaining": 3.2, "belowThreshold": false },
    "period": { "totalCount": 210, "successRate": 0.98, "totalCost": 6.8, "totalTokens": 1200000 }
  } }
```

### 2.5 模型

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/models?providerId&enabled&q&limit&offset` | 列表 |
| GET | `/admin/api/models/{id}` | 详情 |
| POST | `/admin/api/models` | 创建 |
| PUT | `/admin/api/models/{id}` | 更新 |
| DELETE | `/admin/api/models/{id}` | 删除（被引用 → 409） |
| POST | `/admin/api/models/import` | `{providerId, rows:[{upstreamName,displayName?,protocol?,prices?}]}` 批量导入 |

模型行：

```json
{
  "id": 10,
  "providerId": 1, "providerName": "Command Code 主号", "providerKind": "command_code",
  "displayName": "DeepSeek V4 Flash",
  "publicName": "Command Code 主号/deepseek/deepseek-v4-flash",
  "upstreamName": "deepseek/deepseek-v4-flash",
  "upstreamProtocol": "vendor_native",
  "inputPrice": 0, "outputPrice": 0, "cachedInputPrice": 0,
  "contextWindow": 128000, "maxTokens": 64000,
  "supportsStream": true, "supportsTools": true, "supportsVision": true,
  "enabled": true, "sort": 0,
  "routable": true, "routableReason": ""
}
```

请求体（创建/更新）：

```json
{
  "providerId": 1, "displayName": "DeepSeek V4 Flash",
  "publicName": "", "upstreamName": "deepseek/deepseek-v4-flash",
  "upstreamProtocol": "vendor_native",
  "inputPrice": 0.5, "outputPrice": 1.5, "cachedInputPrice": 0.1,
  "contextWindow": 128000, "maxTokens": 64000,
  "supportsStream": true, "supportsTools": true, "supportsVision": true,
  "enabled": true, "sort": 0, "remark": ""
}
```

> `publicName` 留空时按 `{provider.name}/{upstreamName}` 生成；一旦被客户端密钥或路由模型引用，
> 修改 `publicName` 需二次确认。

### 2.6 路由模型（可选能力）

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/route-models` | 列表（含成员） |
| POST | `/admin/api/route-models` | `{name,strategy,members:[{modelId,priority,weight}]}` |
| PUT | `/admin/api/route-models/{id}` | 更新（成员整体替换） |
| DELETE | `/admin/api/route-models/{id}` | 删除 |

```json
{ "id": 1, "name": "fast", "strategy": "priority", "enabled": true,
  "members": [ { "id": 1, "routeModelId": 1, "modelId": 10, "priority": 1, "weight": 1,
                 "enabled": true, "displayName": "DeepSeek V4 Flash",
                 "publicName": "OpenAI 主号/deepseek/deepseek-v4-flash",
                 "providerName": "OpenAI 主号", "providerKind": "openai_compatible" } ] }
```

- `name` 是数据面对外 ID：客户端在 `model` 字段原样回传即可，无需知道成员模型。
  **不得与任何模型 `publicName` 相同**（数据面先解析普通模型，同名路由将不可达）；
  冲突返回 409，`field=name`。
- `strategy=priority`：成员按 `priority` 降序、同序按 id 升序依次尝试，
  取第一个有可用凭据的成员；某成员不可路由时自动跳到下一个。
  `strategy=weighted` 为预留值，当前路由行为与 priority 相同。
- `members[].enabled=false` 的成员不参与路由；路由模型本身 `enabled=false`
  时整个 ID 不可用。
- 成员可来自不同供应商；修改成员集合走 PUT（整体替换）。

### 2.7 客户端密钥

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/client-keys` | 列表 |
| POST | `/admin/api/client-keys` | 创建（响应含一次性明文 `token`） |
| PUT | `/admin/api/client-keys/{id}` | 更新（名称/白名单/IP/额度/过期） |
| DELETE | `/admin/api/client-keys/{id}` | 删除 |
| POST | `/admin/api/client-keys/{id}/rotate` | 轮换（返回新明文） |
| POST | `/admin/api/client-keys/{id}/reset-usage` | 用量归零 |

```json
{ "id": 1, "name": "codex-local", "keyPrefix": "sk-prune-abcd…",
  "enabled": true, "allowedModels": [], "allowedIps": ["127.0.0.1/32"],
  "quotaTokens": 0, "quotaUsed": 12000, "expiresAt": null,
  "lastUsedAt": 1793500000000, "createdAt": 1793500000000 }
```

创建响应额外含：`{ "token": "sk-prune-<32hex>" }`（**仅此一次**）。

### 2.8 端点

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/endpoints` | 列表（含内置三条只读项，`builtin: true`） |
| POST | `/admin/api/endpoints` | 创建 |
| PUT | `/admin/api/endpoints/{id}` | 更新 |
| DELETE | `/admin/api/endpoints/{id}` | 删除（内置不可删） |

```json
{ "id": 4, "name": "Claude Code 入口", "path": "/cc/v1/messages",
  "inboundProtocol": "anthropic", "targetType": "model", "targetId": 10,
  "authRequired": true, "ratePerMin": 0, "timeoutMs": 0, "enabled": true, "builtin": false }
```

### 2.9 日志与用量

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/logs?limit&offset&endpoint&status&providerId&model&clientKeyId&from&to` | 分页 |
| GET | `/admin/api/logs/stream` | SSE 实时流 |
| GET | `/admin/api/usage?from&to&groupBy=day\|model\|provider\|clientKey` | 聚合 |
| GET | `/admin/api/usage/summary?from&to` | 汇总卡（请求数 / 成功率 / token / 费用） |

日志行：

```json
{ "id": 9001, "ts": 1793500000000, "endpoint": "/v1/chat/completions",
  "inboundProtocol": "openai_chat", "providerId": 1, "providerKind": "command_code",
  "providerName": "Command Code 主号", "model": "deepseek/deepseek-v4-flash",
  "clientKeyId": 1, "clientKeyName": "codex-local", "credentialId": 2,
  "statusCode": 200, "stream": true, "durationMs": 4210, "ttftMs": 380,
  "inputTokens": 1200, "outputTokens": 300, "cachedTokens": 800,
  "cost": 0.0009, "estimated": false,
  "finishReason": "stop", "errorType": null, "clientDisconnected": false }
```

### 2.10 设置

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/admin/api/settings` | 当前设置（密钥类只回 `has*` 布尔） |
| PUT | `/admin/api/settings` | 保存补丁（返回 `restartRequired` / `ignored`） |
| POST | `/admin/api/settings/reset` | 恢复默认（保留密钥） |

> `restartRequired` 覆盖 `host`、`port`、`maxBodyMB`、`maxInflight`、`keepAliveTimeoutMs`：
> 它们在进程启动时读取，保存后需重启才生效。

### 2.11 运维

| 方法 | 路径 | 说明 |
| :--- | :--- | :--- |
| GET | `/health` | 纯文本 `OK`（探活，不鉴权） |
| GET | `/admin/api/status` | 运行态：连接池、内存窗口数、适配器状态、后台任务 |
| GET | `/admin/api/export` | 导出配置（**不含明文密钥**） |
| POST | `/admin/api/import` | 导入配置（合并语义） |

## 3. 数据面接口

### 3.1 端点一览

| 路径 | 入站协议 | 说明 |
| :--- | :--- | :--- |
| `POST /v1/chat/completions` | `openai_chat` | 透传或转换 |
| `POST /v1/responses` | `openai_responses` | 透传或转换 |
| `POST /v1/messages` | `anthropic` | 透传或转换 |
| `GET /v1/models` | — | 当前 Key 可见模型（`allowed_models` 过滤） |
| `GET /models` | — | 同上；根路径别名，便于只配 `{base}/{path}` 的客户端 |
| 自定义 `/{path}` | 由端点定义 | 别名/聚合/限流 |

### 3.2 请求处理顺序

```
1. 端点解析（内置 → 自定义最长前缀）
2. 准入（maxInflight，/health 与管理面豁免）
3. 客户端密钥校验（见 §1.3）
4. 读 body（限制 maxBodyMB，默认 100MB）
5. 入站 codec.DecodeRequest → IR；提取/覆盖 model
6. Router.Resolve(model) → {provider, credential, adapter, modelRef}
   - 模型不存在/停用 → 404 not_found
   - 供应商停用 / 无可用凭据 → 429 upstream_unavailable
7. 能力校验（stream/tools/vision × 适配器能力 × 模型能力）
8. adapter.Do(...)
9. 回写（非流式聚合 / 流式透传转换）
10. 记账（日志 + 日聚合 + 客户端密钥额度）
```

### 3.3 请求/响应头

| 方向 | 头 | 说明 |
| :--- | :--- | :--- |
| 入 | `Authorization: Bearer sk-prune-*` | 客户端密钥（Anthropic 亦可 `x-api-key`） |
| 入 | `x-session-id` / `x-claude-code-session-id` / `session_id` | 会话线索（透传适配器） |
| 入 | `x-cmd-zdr: 1` | ZDR（CC 适配器识别） |
| 入 | `X-Request-ID` | 可选，缺省生成 |
| 出 | `X-Request-ID` / `X-Provider` / `X-Model` | 回填 |
| 出 | `X-Usage-Tokens` | 非流式时 `{in,out,cached}` 简写 |

### 3.4 `GET /v1/models`

```json
{ "object": "list",
  "data": [ { "id": "Command Code 主号/deepseek/deepseek-v4-flash", "object": "model",
              "created": 1793500000, "owned_by": "command_code",
              "context_window": 128000, "capabilities": { "stream": true, "tools": true, "vision": true } } ] }
```

- `id` 即模型的 `publicName`（默认 `{provider.name}/{upstreamName}`，见 §2.5）
  或路由模型的 `name`（见 §2.6）；客户端在请求的 `model` 字段原样回传即可路由，
  网关不做拆分解析。
- `GET /models` 是 `/v1/models` 的别名，响应完全一致。
- 只列**可路由**且被当前 Key 允许的模型：普通模型在前（按 `sort` 再按 id），
  随后是**已启用且至少有一个可路由成员**的路由模型。
- 路由模型条目同样 `object=model`，`owned_by="route_model"`；能力取可路由成员的
  **交集**（`stream/tools/vision`），`context_window` 取可路由成员的最小值——
  故障转移可能落到任一成员，元数据按最保守的一档对外宣告。
- Key 的 `allowedModels` 白名单同时匹配普通模型的 `publicName` 与路由模型的 `name`。
- Anthropic 形态（`GET /v1/models` 且入站为 anthropic）额外返回 `{ "has_more": false, "first_id": ..., "last_id": ... }`。

### 3.5 错误体

**OpenAI Chat / Responses**：

```json
{ "error": { "message": "…", "type": "rate_limit_error", "code": "quota_exceeded", "param": null } }
```

**Anthropic**：

```json
{ "type": "error", "error": { "type": "rate_limit_error", "message": "…" } }
```

状态码与 `type` 的对应见 [03 §8](03-provider-adapter.md)。

### 3.6 流式

- `Content-Type: text/event-stream; charset=utf-8`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`；
- 错误发生在**已开流之后**：在流内发一条错误帧后关闭，不再改 HTTP 状态；
- 客户端断开：立即取消上游请求，日志记 `client_disconnected=true`。

## 4. 事件流（控制面 SSE）

`GET /admin/api/logs/stream`：

```
: connected

data: {"id":9002,"ts":1793500001000,"endpoint":"/v1/messages","statusCode":200,...}

: ping
```

- 每 15s 一条注释心跳；
- 客户端断开即退订；
- 事件体与 `GET /admin/api/logs` 的行结构一致。

## 5. 环境变量

| 变量 | 说明 |
| :--- | :--- |
| `PRUNE_DATA_DIR` | 数据目录 |
| `PRUNE_ADMIN_TOKEN` | 管理面令牌 |
| `PRUNE_HOST` / `PRUNE_PORT` | 监听地址 |
| `PRUNE_MAX_INFLIGHT` | 全局在途上限 |
| `PRUNE_KEEPALIVE_TIMEOUT_MS` | keep-alive |
| `PRUNE_MAX_BODY_MB` | 请求体上限 |
| `PRUNE_STREAM_IDLE_MS` / `PRUNE_NONSTREAM_IDLE_MS` | 空闲看门狗 |
| `PRUNE_DISABLE_DRIFT` / `PRUNE_DISABLE_ACCOUNT_USAGE` | 关闭后台任务（测试用） |
| `PRUNE_LOG_LEVEL` / `PRUNE_LOG_FILE` | 日志级别与文件 |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | 上游出网代理（供应商级 `proxyUrl` 优先） |

> 环境变量一律 `PRUNE_` 前缀，映射规则为路径大写下划线（`server.port` → `PRUNE_PORT`）。

## 6. 契约变更流程

1. 先改本文档（含示例 JSON）；
2. 再改 `internal/admin`（业务）与 `internal/api`（适配）；
3. 补 `internal/api/*_test.go` 的契约测试（断言状态码 + JSON 形状）；
4. 前端/桌面按文档开发，不读后端源码猜字段。
