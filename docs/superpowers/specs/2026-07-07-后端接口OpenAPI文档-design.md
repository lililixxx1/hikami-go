# 设计:后端接口 OpenAPI 文档(为前端重写服务)

> 状态:**待用户审核** · 日期 2026-07-07 · 形式 OpenAPI 3.0 YAML + 手写 Markdown 总览 + 静态渲染页
> 目标:在不动后端的前提下,把现有 ~80 个 HTTP 端点 + WebSocket 事件契约固化成一份前端可直接照着写代码的 API 规范,作为 Vue 3 前端全页面重写的契约源。

## 1. 背景与动机

### 1.1 用户意图
用户计划在**保留前端技术栈**(Vue 3 + Element Plus + Pinia + Vite)的前提下,**按 HTML 设计模板**(`V5 Hikami-Go 全页面重设计.html`)重写全部 4 个页面(首页/我的主播/回顾/设置)。用户希望"先写后端接口文档,再重写前端"。

### 1.2 现状评估
项目里**已经有相当完整的接口契约**,只是分散且不够详细:

| 已有材料 | 位置 | 详细度 |
|---|---|---|
| 路由表 | `CLAUDE-detail/api-routes.md` | 仅 method + path + 一句话说明,**无请求体/响应体/错误码** |
| TS 类型 | `web/src/api/types.ts` | 较完整的实体类型,但**与后端字段名偶有漂移** |
| API 客户端 | `web/src/api/*.ts` | 按域分 10 个文件,功能化封装 |
| handler 实现 | `internal/handler/server.go`(4154 行,125 处路由) | 字段真相源,但 grep 才能找到 |

**问题**:前端重写时,要逐页核对"这个按钮调哪个接口、字段叫什么",目前要在 4 个来源间反复跳转,且缺请求体/响应体/错误码这些**写代码真正需要的东西**。

### 1.3 方案选择历程(已与用户确认)
- ❌ swag 注解生成:与"后端基本不动"冲突(要补 125 处注解 + 抽取 60+ 结构体重构),用户了解后改选 OpenAPI。
- ✅ **手写 OpenAPI YAML 作为唯一真相源**:不动 Go 代码,半天-1 天产出可用文档,与"后端不动"完全一致。

### 1.4 三个子决策(均已确认)
| 决策点 | 选择 |
|---|---|
| 文件组织 | **B. 主文件 + 按域拆分**(10 个 paths 文件 + 15 个 components/schemas/) |
| gap 处理 | **A. OpenAPI 只描述现状**,模板差异写到独立 `api-gap-analysis.md` |
| 渲染方式 | **B. 静态 HTML 渲染页**(Swagger UI CDN,无需后端改动) |

---

## 2. 总体架构

### 2.1 文件结构

```
docs/api/
├── openapi.yaml                    # 主文件:info/servers/security/tags + $ref 聚合
├── paths/                          # 按域拆分的 path 定义
│   ├── system.yaml                 # 系统与引导(/healthz, /onboarding/*, /health/runtime)
│   ├── channels.yaml               # 频道(/api/channels/*, identify, copy-config)
│   ├── sessions.yaml               # 场次(/api/sessions/*, discover, download, import)
│   ├── tasks.yaml                  # 任务(/api/tasks/*)
│   ├── runtime-stats.yaml          # 运行时与统计(/api/live/*, /api/stats/*, /api/cookies/status)
│   ├── config.yaml                 # 配置(/api/config/*)
│   ├── glossary.yaml               # 术语表(/api/glossary/*, /api/channels/:id/glossary/*)
│   ├── templates.yaml              # 回顾模板(/api/recap/*, /api/channels/:id/recap-template/*)
│   ├── bili-accounts.yaml          # B站账号与 QR 登录(/api/bili/*, /api/cookie-accounts/*)
│   └── secrets.yaml                # 密钥(/api/secrets/*)
├── components/
│   └── schemas/                    # 共享 schema(可复用实体)
│       ├── channel.yaml            # Channel, UpsertChannelInput, IdentifyInput/Result
│       ├── session.yaml            # Session, SessionDetail, SessionFile
│       ├── task.yaml               # Task, TaskStatus, FriendlyError, AutoRetry
│       ├── runtime.yaml            # RuntimeStatus, ToolStatus, Capabilities, ConfigStatus
│       ├── live.yaml               # LiveStatus
│       ├── config-sections.yaml    # PublishConfig, RecapConfig, DashScopeConfig, ASRS3Config, ...
│       ├── glossary.yaml           # GlossaryEntry, GlossaryNote, Candidate
│       ├── templates.yaml          # RecapTemplate, TemplatePreset, ResolvedRecapTemplate
│       ├── discover.yaml           # DiscoverResult, DiscoverPickItem, ExecuteItem
│       ├── bili.yaml               # BiliCookieAccount, QRCodeSession/Poll/Save
│       ├── stats.yaml              # DashboardData, SessionStats, CostStats
│       ├── websocket.yaml          # TaskProgressEvent
│       └── common.yaml             # Error, ListResponse, Secrets/SecretView
├── index.html                      # Swagger UI 静态渲染页(CDN 引入)
└── README.md                       # 文档使用说明(怎么渲染/怎么编辑)
```

**为什么按"后端域"而不是"前端页面"拆**:`Channel`、`Session`、`Task` 这类 schema 被多个页面共用(首页 stats 用 channel、回顾页用 session、主播页用 channel),按页面拆会导致 schema 跨文件重复。按后端域拆符合 API 本身的边界,`$ref` 引用更清晰。

### 2.2 $ref 引用约定

- **path → schema**: `'../components/schemas/channel.yaml#/Channel'`
- **主文件 → path**: `'paths/channels.yaml#/api-channels'`(每个 path 文件顶层用显式 key,如 `/api/channels`、`/api/channels/{id}`)
- **schema → schema**: 同目录 `'./common.yaml#/Error'`
- 所有 path 参数用 `{id}` 风格(OpenAPI 标准),**不是 Go 的 `:id`**

### 2.3 工作流

```
       编辑 docs/api/openapi.yaml + paths/ + components/
                        │
                        │ git 提交(版本化)
                        ▼
       ┌─────────────────────────────────────┐
       │  ① 浏览器打开 docs/api/index.html    │ ← Swagger UI(CDN)
       │     自动 fetch openapi.yaml 渲染     │   人读、可交互试调
       └─────────────────────────────────────┘
                        │
                        │ (可选,前端重写阶段)
                        ▼
       ┌─────────────────────────────────────┐
       │  ② openapi-typescript                │ ← 生成 TS 类型
       │     npx openapi-typescript \         │   替代手写 types.ts
       │       docs/api/openapi.yaml \        │
       │       -o web/src/api/generated.ts    │   (重写时启用,本 spec 不强制)
       └─────────────────────────────────────┘
```

---

## 3. OpenAPI 规范的技术约定

### 3.1 元信息(`openapi.yaml` 头部)

```yaml
openapi: 3.0.3
info:
  title: Hikami-Go API
  version: 1.0.0
  description: |
    B 站直播回顾自动生成服务的后端 HTTP API。
    本规范描述后端现状,不包含 HTML 模板与后端的差异(见 api-gap-analysis.md)。
  contact:
    name: Hikami-Go
    url: https://github.com/用户/hikami-go
servers:
  - url: http://127.0.0.1:6334
    description: 默认本机监听
```

### 3.2 全局安全约定

```yaml
security:
  - AdminToken: []
components:
  securitySchemes:
    AdminToken:
      type: apiKey
      in: header
      name: X-Admin-Token
      description: |
        管理员令牌。当后端 admin_token 配置为空时(loopback 场景)中间件直接放行;
        绑非 loopback 地址时强制要求。
        兼容 Authorization: Bearer <token> 头。401 时响应 {error: "missing or invalid admin token"}。
tags:
  - name: 系统
  - name: 频道
  - name: 场次
  - name: 任务
  - name: 运行时
  - name: 配置
  - name: 术语表
  - name: 回顾模板
  - name: B站账号
  - name: 密钥
  - name: WebSocket
```

### 3.3 统一错误格式

后端 `writeError`(server.go:1507)统一返回,**两类变体**:

```yaml
# components/schemas/common.yaml
Error:
  type: object
  required: [error]
  properties:
    error:
      type: string
      description: 错误消息(可直接展示给用户)
    reason:
      type: string
      description: |
        仅特定错误带此字段:
        - 能力守卫类(409):reason = Capabilities.Reason(说明为何能力不可用)
        - glossary not found/duplicate(404/409):reason = 原始错误
        - recap template not found(404):reason = 原始错误
```

**状态码映射**(摘自调研,完整表写进 `openapi.yaml` 的 `components.responses`):

| 错误 | 状态码 |
|---|---|
| `*.ErrInvalid` / `*.ErrInvalidTask` / `ErrInvalidCandidate` | 400 |
| 未认证 | 401 |
| publisher cookie 过期 / 非专栏所有者 / 内置模板不可改 | 403 |
| `*.ErrNotFound` / `*.ErrTaskNotFound` | 404 |
| QR session 过期 | 410 |
| `*.ErrDuplicate` / `*.ErrInUse` / `*.ErrTaskConflict` / 能力不可用 / 状态不匹配 | 409 |
| 专栏内容被拒 | 422 |
| publisher 限流 | 429 |
| 上游 B 站接口失败 | 502 |
| 其他未映射 | 500 |

### 3.4 时间格式约定

所有时间字段:**RFC 3339 字符串**(`2026-07-04T09:07:39+08:00`,本地时区)。
- 2026-07-04 起统一存本地时区(见 AGENTS.md)
- 历史数据可能为 UTC 无时区格式
- OpenAPI schema:`type: string`, `format: date-time`

### 3.5 只写字段约定(密钥)

配置类端点的密钥字段是**只写不读**的:
- GET 响应:**永不返回明文**,只用 `*_set: boolean` 表示是否已设置(如 `api_key_set`、`password_set`、`access_key_set`)
- PUT 请求:可传明文写入(如 `api_key`、`password`、`access_key_secret`),或用 `clear_key`/`clear_password`/`clear_secret: true` 清除

OpenAPI 处理:在 PUT 请求 schema 里定义这些字段,GET 响应 schema 里**不出现**。在字段 description 里标注"仅写入,响应永不返回明文"。

### 3.6 字段命名

后端 JSON 字段一律 `snake_case`(Go struct json tag),OpenAPI schema 属性名严格对齐:
- `live_room_id`(不是 `liveRoomId`)
- `started_at`(不是 `startedAt`)
- `local_available`(不是 `localAvailable`)

---

## 4. 各域端点清单(写入 paths/ 的内容纲要)

> 每个端点的完整 schema(请求体/响应体/错误码)在实现阶段从调研结论直接填充。下面只列端点清单和关键字段约束。

### 4.1 系统(paths/system.yaml)
- `GET /api/healthz` — 无认证 · `200 {status: "ok"}`
- `GET /api/health/runtime` — `200 runtimeStatusResponse`(嵌入 `RuntimeStatus` + `has_default_download`/`has_default_publish`)
- `GET /api/onboarding/status` — `200 {needed, has_tools, has_keys, has_channels}`
- `POST /api/onboarding/dismiss` — `200 {message: "引导已跳过"}`

### 4.2 频道(paths/channels.yaml)
- `GET /api/channels` — `200 {items: [Channel]}`
- `POST /api/channels` — body `UpsertChannelInput` · `201 Channel`
- `PUT /api/channels/{id}` — body `UpsertChannelInput` · `200 Channel`
- `DELETE /api/channels/{id}` — `204` · 冲突 `409 {error, session_count}`
- `POST /api/channels/identify` — body `{input, uid?, live_room_id?}` · `200 {channel: UpsertInput, source}`
- `POST /api/channels/identify/save` — `200/201 {channel, source, created}`
- `POST /api/channels/{id}/copy-config` — body `{target_channel_id, copy_glossary, copy_template, copy_publish, copy_automation, copy_recap}` · `200` 条件字段(glossary_copied/template_copied/channel_updated)

### 4.3 场次(paths/sessions.yaml)
- `GET /api/sessions` — **无查询参数过滤** · `200 {items: [Session]}`
- `GET /api/sessions/{sid}` — `200 {session, files: [{path, size}]}`
- `DELETE /api/sessions/{sid}` — `204`
- `DELETE /api/sessions/failed` — `200 {deleted: int64}`
- `POST /api/sessions/download` — body `{session_id}` · `202 Task`
- `POST /api/sessions/download-by-url` — body `{channel_id, url}` · `202 Task` · 能力不可用 `409`
- `POST /api/sessions/import` — **multipart/form-data** · 字段 media_file/channel_id/title/started_at?/ended_at?/source_url?/danmaku_file? · `202 Task`
- `POST /api/sessions/discover` — `202 {items: [DiscoverResult]}`
- `POST /api/sessions/discover/preview` — `200 {items: [DiscoverResult]}`(含 `exists` 标记)
- `POST /api/sessions/discover/execute` — body `{items: [DiscoverPickItem]}` · `202 {items: [DiscoverResult]}`
- `POST /api/sessions/{sid}/upload` — `202 Task`
- `POST /api/sessions/{sid}/fetch` — `202 Task`
- `POST /api/sessions/{sid}/publish` — body(发布参数) · `202 Task`
- `POST /api/sessions/{sid}/archive` — `202 Task` · 状态不匹配 `409`
- `POST /api/sessions/{sid}/recap/generate` — `local_available=false` 时 `409`
- `POST /api/sessions/{sid}/recap/regenerate` — 仅 recap_done/published
- `POST /api/sessions/{sid}/recap-partial` — body `{start_time, end_time}`
- `POST /api/sessions/{sid}/recap-with-range` — `recap-partial` 兼容别名
- `GET /api/sessions/{sid}/recap` — `200 {available, markdown, prompt, raw_response, suggested_terms?}`
- `PUT /api/sessions/{sid}/recap/content` — body `{markdown}` · `local_available=false` 拒绝
- `POST /api/sessions/{sid}/glossary/discover` — `local_available=false` 时 `409`

### 4.4 任务(paths/tasks.yaml)
- `GET /api/tasks` — `200 {items: [Task]}`
- `GET /api/tasks/{id}` — `200 {task, friendly_error?, auto_retry?}`(后两者仅 failed 时出现)
- `POST /api/tasks/batch-retry` — body `{task_ids: [string]}` · `200 {retried, tasks}`
- `DELETE /api/tasks/failed` — `200 {deleted: int64}`

### 4.5 运行时与统计(paths/runtime-stats.yaml)
- `POST /api/live/check` — `202 {items: [LiveStatus]}`(注:server.go:964 用 202)
- `GET /api/live/status` — `200 {items: [LiveStatus]}`
- `GET /api/live/{channel_id}/status` — `200 LiveStatus`(直接对象,非 items)
- `GET /api/cookies/status` — `200 {channels: [{channel_id, channel_name, publish_cookie?, download_cookie?}]}`
- `GET /api/stats/overview` — `200 {sessions: SessionStats, task_summary: {status: count}, asr_cost_estimate, asr_hours}`
- `GET /api/stats/cost` — `200 {asr_cost_estimate, asr_hours_estimate, ai_cost_estimate, total_cost_estimate, monthly_breakdown}`
- `GET /api/stats/dashboard` — `200 DashboardData`
- `GET /api/diagnostic/report` — 诊断报告(实现时再细化字段)

### 4.6 配置(paths/config.yaml)
9 组配置端点(publish/recap/dashscope/asr-s3/webdav/archive 各 GET+PUT)+ `GET /api/config/recap/models` + 导出导入。**重点**:每个 PUT 请求 schema 区分"指针字段(presence-aware patch)"和"值字段",密钥字段标"仅写入"。

### 4.7 术语表(paths/glossary.yaml)
全局 + 主播级两套(主播级多 `/channels/{id}` 前缀)。CRUD + 导入导出 + 候选审批 + 批量操作。约 30 个端点。

### 4.8 回顾模板(paths/templates.yaml)
全局(`/api/recap/templates`) + 主播级(`/api/channels/{id}/recap-template`)+ 预设(`/api/recap/presets`)。

### 4.9 B站账号(paths/bili-accounts.yaml)
QR 登录(`/api/bili/login/qrcode/*`) + Cookie Account CRUD(`/api/cookie-accounts/*`)+ 兼容端点(`/api/bili/accounts/*`)。

### 4.10 密钥(paths/secrets.yaml)
- `GET /api/secrets` — `200 {items: [SecretView]}`
- `PUT /api/secrets/{key}` — body `{value}`(空串=删除) · `200 SecretView`
- `DELETE /api/secrets/{key}`

### 4.11 WebSocket(paths/websocket.yaml — 特殊)
OpenAPI 3.0 不原生支持 WebSocket,用**占位 path + extension** 描述:
```yaml
/api/ws:
  get:
    tags: [WebSocket]
    summary: 任务进度推送(WebSocket 升级端点)
    description: |
      gorilla/websocket Upgrade。无需 admin token。
      服务端只 WriteJSON 推送 task_progress 事件,不读客户端消息。
      事件 schema 见 components/schemas/websocket.yaml#/TaskProgressEvent。
      OpenAPI 3.0 不原生支持 WS,本条目仅作文档锚点。
    responses:
      '101':
        description: Switching Protocols(WebSocket 握手成功)
```
事件结构 `TaskProgressEvent` 写进 `components/schemas/websocket.yaml`:
```
type        string  // 恒 "task_progress"
task_id     string
channel_id  string
session_id  string  (omitempty)
status      string  // pending/running/succeeded/failed/cancelled
progress    int
message     string
error       string  (omitempty)
```

---

## 5. 静态渲染页(index.html)

用 Swagger UI CDN,无构建步骤。`docs/api/index.html`:

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <title>Hikami-Go API · Swagger UI</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: './openapi.yaml',          // 相对路径,本地 file:// 直接打开也能用
        dom_id: '#swagger-ui',
        deepLinking: true,
        docExpansion: 'none',
        operationsSorter: 'alpha',
        tagsSorter: 'alpha',
        defaultModelsExpandDepth: 2,
      });
    };
  </script>
</body>
</html>
```

**查看方式**:浏览器直接打开 `docs/api/index.html`(或 `make api-docs` 起本地静态服务器)。
**注意**:Swagger UI fetch 本地 YAML 受 CORS/file 协议限制,推荐加一个轻量 Makefile target 起静态服务器(见 §6)。

---

## 6. Makefile 集成

加 3 个 target(不破坏现有):

```makefile
# API 文档渲染(本地静态服务器,Swagger UI 通过 http 访问避免 file:// CORS 限制)
api-docs:
	@echo "API 文档: http://127.0.0.1:6335"
	@cd docs/api && python3 -m http.server 6335

# 校验 openapi.yaml 语法(swag-cli 或 redocly lint,需 npm i -g @redocly/cli)
api-lint:
	@npx -y @redocly/cli lint docs/api/openapi.yaml

# (可选,前端重写阶段)从 OpenAPI 生成 TS 类型
api-gen-types:
	@npx -y openapi-typescript docs/api/openapi.yaml -o web/src/api/generated.ts
```

`api-gen-types` 是**可选**的,本 spec 不强制启用——前端重写时如果决定用生成类型替代手写 `types.ts` 再启用。

---

## 7. 模板与后端的 gap 文档(api-gap-analysis.md)

**独立于 OpenAPI**,放在 `docs/api/api-gap-analysis.md`。结构:

```markdown
# HTML 模板 vs 后端 API 差异分析

> 为前端全页面重写服务。逐页对照 V5 HTML 模板与后端现状,标注每个差异的处理决策。

## 模板页 1:首页(page-home)
| 模板元素 | 后端端点 | 状态 | 决策 |
|---|---|---|---|
| 直播状态卡片 | GET /api/live/status | ✅ 已有 | 直接用 |
| 最近场次列表 | GET /api/sessions | ✅ 已有(前端过滤) | 前端按 created_at 排序取前 N |
| 录播进度环 | WS task_progress | ✅ 已有 | WS 订阅 |
| (模板某功能) | (无) | ❌ 缺失 | **重写时砍掉**(后端不动) |

## 模板页 2:我的主播(page-streamers)
...

## 模板页 3:回顾(page-reviews)
...

## 模板页 4:设置(page-settings)
...

## 汇总
- 缺失端点:N 个(列清单)
- 多余端点:M 个(后端有但模板没用到,前端可不实现)
- 字段映射差异:K 处
```

**gap 文档的实现方式**:实现 OpenAPI 阶段顺带产出——写完每个域的 path 后,翻一遍 HTML 模板对应板块,把"模板有后端没"的记到 gap 表。这是 OpenAPI 工作的副产品,不另起任务。

---

## 8. 实施范围与不在范围

### ✅ 在范围
1. 创建 `docs/api/` 目录结构(主文件 + 10 个 paths + ~15 个 schemas)
2. 填充全部 ~80 端点的 OpenAPI 定义(基于调研结论,字段精确)
3. 填充全部共享 schema(Channel/Session/Task/RuntimeStatus/各 Config/...)
4. 静态渲染页 `index.html`(Swagger UI CDN)
5. README.md(怎么查看/怎么编辑)
6. Makefile 加 `api-docs` / `api-lint` / `api-gen-types` 三个 target
7. `docs/api/api-gap-analysis.md` 初稿(逐页对照模板)

### ❌ 不在范围
- ❌ 改动任何 Go 代码(后端完全不动)
- ❌ 改动前端代码(`web/src/` 不动)
- ❌ 启用 openapi-typescript 生成(留给前端重写阶段决定)
- ❌ 把 Swagger UI 集成进后端 `/swagger` 路由(用静态 HTML 即可)
- ❌ 实际开始前端重写(那是下一个独立项目)

---

## 9. 验收标准

1. `docs/api/openapi.yaml` + 子文件通过 `npx @redocly/cli lint`(无 error,warning 可接受)
2. `make api-docs` 起服务后,浏览器打开 `http://127.0.0.1:6335` 能看到完整可交互文档
3. **抽查验证**:随机选 5 个端点(覆盖 GET/POST/PUT/DELETE + 多个域),对照 `internal/handler/server.go` 实际实现,字段名/类型/错误码 100% 一致
4. WebSocket 端点有文档锚点,事件 schema 完整
5. `api-gap-analysis.md` 覆盖全部 4 个模板页

---

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| OpenAPI 漂移(后端改了接口文档没更新) | gap 文档 + OpenAPI 作为前端契约源后,改接口必须同步文档;`api-lint` 进 CI 可后续补 |
| 字段调研有遗漏 | 抽查验证(§9.3)+ 前端重写时发现差异即修文档 |
| 单个端点的内联 `gin.H` 返回字段不全 | 调研已基于代码逐个核对;实现时每个 path 都注明 `server.go:行号` 溯源 |
| 80 端点工作量大 | 按域拆分,可分批 PR(system/channels/sessions 先行,其余跟进) |

---

## 11. 后续(本 spec 之后)

本 spec 完成后,用户审核通过 → 进入 `writing-plans` 制定详细实现计划(分批 PR 的粒度、每个 path 文件的填充顺序)→ 然后才开始实际写 YAML。
