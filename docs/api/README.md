# Hikami-Go API 文档

手写 [OpenAPI 3.0.3](https://spec.openapis.org/oas/v3.0.3) 规范,描述后端 HTTP API 现状(约 80 个端点 + WebSocket 事件契约),作为 Vue 3 前端全页面重写的契约源。

## 查看

**推荐:本地静态服务器**
```bash
make api-docs
```
浏览器打开 http://127.0.0.1:6335(Swagger UI 通过 CDN 渲染)。

**直接打开** `index.html`:需允许 `file://` fetch,推荐用上面的服务器方式。

## 校验

```bash
make api-lint
```
使用 [@redocly/cli](https://redocly.com/docs/cli/) 检查 OpenAPI 语法。首次运行 npx 会下载依赖。

## 生成前端类型(前端重写阶段)

```bash
make api-gen-types
```
用 [openapi-typescript](https://github.com/drwpow/openapi-typescript) 把 `openapi.yaml` 生成 `web/src/api/generated.ts`。

## 文件结构

```
docs/api/
├── openapi.yaml                    # 主文件,含 info/security/tags + 全部端点 paths(内联) + components 引用
├── components/
│   └── schemas/                    # 共享 schema 按域拆分(此处 $ref 无需 JSON Pointer 转义)
│       ├── common.yaml             # Error / ErrorWithSessionCount / OkResponse / DeletedCountResponse
│       ├── runtime.yaml            # RuntimeStatus / Capabilities / ...
│       ├── websocket.yaml          # TaskProgressEvent
│       ├── channel.yaml            # Channel / UpsertChannelInput / Identify*
│       ├── session.yaml            # Session / SessionDetail / Recap*
│       ├── discover.yaml           # DiscoverResult / DiscoverExecuteRequest
│       ├── task.yaml               # Task / TaskDetailResponse
│       ├── live.yaml               # LiveStatus
│       ├── stats.yaml              # SessionStats / DashboardData / ...
│       ├── config-sections.yaml    # 6 组 config Request/Response
│       ├── glossary.yaml           # GlossaryEntry / Candidate / 状态机请求
│       ├── templates.yaml          # RecapTemplate / ResolvedTemplate(snake_case)
│       ├── bili.yaml               # QRCode* / BiliCookieAccount
│       └── secrets.yaml            # SecretView
├── index.html                      # Swagger UI 渲染页(CDN)
├── api-gap-analysis.md             # HTML 模板(V10)vs 后端接口的差异
└── README.md                       # 本文件
```

## 真相源约定

| 源 | 作用 |
|----|------|
| `internal/handler/server.go` + 各业务包 Go struct 的 `json:"..."` tag | **唯一真相源** |
| `web/src/api/types.ts` | 交叉验证(有漂移,不直接抄) |

字段类型映射(Go → OpenAPI):

| Go | OpenAPI |
|---|---|
| `string` | `type: string` |
| `int` / `int64` | `type: integer, format: int64` |
| `bool` | `type: boolean` |
| `float64` | `type: number, format: double` |
| `time.Time` | `type: string, format: date-time` |
| `*T` 带 `omitempty` | 字段缺席(不写 nullable,不进 required) |
| `*T` 无 omitempty | 字段必出现,值可为 null(写 `nullable: true`) |

## 结构决策(对 spec §2.1 的执行期修正)

原 spec(`docs/superpowers/specs/2026-07-07-后端接口OpenAPI文档-design.md`)计划把 `paths/` 按域拆分成 10 个文件,但 OpenAPI 3.0 跨文件 path 引用需要 JSON Pointer 转义(`/`→`~1`,如 `'paths/system.yaml#/~1api~1healthz'`),可读性差且易错。

本实现改为 **`openapi.yaml` 单文件 paths 内联,仅 schema 跨文件**。权衡:主文件较大(~2000 行),但 `$ref` 只用于引 schema,路径简单可靠。schema 跨文件安全(`$ref: 'components/schemas/channel.yaml#/Channel'`,无特殊字符)。

## 关键陷阱速查

- **WebSocket 路径是 `/ws` 非 `/api/ws`**(`server.go:275`);OpenAPI 3.0 不原生支持 WS,用 `101 Switching Protocols` 响应作文档锚点
- **ResolvedTemplate 字段名 snake_case**(`system_prompt`/`user_format`/`fan_name`/`extra_vars`,2026-07-16 已给 `recap.ResolvedTemplate` 补 json tag;此前 PascalCase 为历史遗留,已修复)
- **`POST /api/sessions/import` 是 multipart/form-data**,非 JSON
- **`PUT /api/secrets/{key}` 传 `{value:""}` 删除密钥**,无独立 DELETE 端点
- **`auto_recap` 三态语义**:`UpsertChannelInput.auto_recap` 是 `*bool`,字段缺席=nil=默认 false(`channel.go:resolveAutoRecap`)
- **配置 patch 语义**:6 组 config 的请求 schema 全字段可选(presence-aware),传哪个改哪个,未传保持原值
- **QR session 过期 → 410 Gone**(非 404)
- **兼容端点** `/api/bili/accounts/*` 标 `deprecated: true`(别名转发到 `/api/cookie-accounts/*`)
