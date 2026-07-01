# Repository Guidelines

> **ZCode Agent 运行时上下文**。ZCode 在每个任务启动时读取**两处** `AGENTS.md`(据官方文档,仅两级、**不**逐级合并子目录):
> ① `~/.zcode/AGENTS.md`(用户全局,本机当前为空)、② `<repo>/AGENTS.md`(工作区,本文件)。两者存在时先追加全局、再追加工作区,工作区指令为当前任务的主源。
> 本文件聚焦"Agent 工作时最常需要的信息":命令、约定、结构与边界。
> 详细的架构图、模块逐一解析、数据流见根目录 [`CLAUDE.md`](./CLAUDE.md)(人类可读的完整参考;ZCode 仅在 onboarding 时把 CLAUDE.md 作为一次性迁移源,运行时不持续读取)。

## 项目一句话

Hikami-Go 是一个 Go 1.25 后端 + 内嵌 Vue 3 管理界面的 B 站直播回顾自动生成服务。入口 `cmd/hikami/`,后端包在 `internal/`,前端在 `web/`。

## 常用命令(Make 封装,优先使用)

| 命令 | 用途 |
|------|------|
| `make build` | 构建前端 → 嵌入 `web/dist` → 编译 `./hikami`(完整产物) |
| `make build-go` | 仅编译 Go 二进制(`./cmd/hikami`,含 `embedded_web` tag) |
| `make run` | `go run ./cmd/hikami -config config.yaml` |
| `make test` | `go test ./...`(全部后端单测) |
| `make fmt` | `gofmt -w cmd internal` |
| `make tidy` | 更新 go.mod |
| `make web-dev` | 启动 Vite 前端开发服务器(`web/`) |
| `make web-build` | 安装前端依赖并产出嵌入用 UI 包 |

## 直接命令(不依赖 Make 时)

```bash
# 编译(无需任何环境变量前缀,普通用户即可)
go build -tags embedded_web -o ./hikami ./cmd/hikami

# 全量测试
go test ./...

# 单模块测试(示例)
go test ./internal/recap/...

# 前端
cd web && npm install && npm run build      # 构建
cd web && npm run type-check                 # 类型检查
cd web && npx vitest run                     # 单测
```

> **关于 Go 环境**:本机 Go 1.25 已正确配置(`GOPATH=/home/<user>/go`,`GOCACHE` 默认)。
> **直接运行 `go build` / `go test` 即可,不需要 `HOME=` / `GOPATH=` / `GOMODCACHE=` 前缀。**
> (此前文档中 `HOME=/root ...` 的前缀仅适用于特定沙箱/CI 环境,在本工作区会导致命令失败。)

## 启动与运行

- **首次运行**:`cp config.example.yaml config.yaml`,按需编辑(最小配置仅需 `output_root`)。
- **启动**:`make run` 或 `./hikami -config config.yaml`。
- **默认监听**:`127.0.0.1:6334`(仅本机,定义在 `internal/config/config.go` 的 `web.listen` 默认值)。
- **访问管理界面**:浏览器打开 http://127.0.0.1:6334 。
- **二进制特点**:前端经 `embedded_web` build tag 内嵌进 `./hikami`(单文件部署,无需额外 web 资源)。

## 外部运行时依赖

| 工具 | 是否必需 | 说明 |
|------|----------|------|
| `ffmpeg` / `ffprobe` | **必需** | 启动时探测,缺失则启动失败 |
| `yt-dlp` | 可选 | 回放下载 / 多 P 降级 / 发现已知主播;缺失仅降级对应能力(见 `runtime.Probe`) |
| `rclone` | 可选 | 当 WebDAV/ASR 临时目录无原生后端时的回退;缺失仅降级 |

## 后端结构(internal/)

核心包(完整模块索引与 Mermaid 结构图见 `CLAUDE.md`):

- **入口/编排**:`cmd/hikami`(main)、`handler`(Gin 路由 + WebSocket)、`runtime`(启动编排 + 能力探测)、`config`、`db`(SQLite)。
- **生命周期**:`session`(场次状态机)、`state`、`scheduler`(调度)、`worker`(任务执行)、`channel`、`live_record`、`discover`。
- **业务流水线**:`download` → `asr`(语音转写)→ `recap`(AI 回顾生成)→ `glossary`(术语表)→ `normalize` → `upload` → `publisher`(发布)→ `archive` → `notify`(通知)。
- **支撑**:`aiprovider`(AI provider 抽象)、`secrets`、`fsutil`、`importer`、`biliutil`。

## 前端结构(web/src/)

分层架构(详见 `docs/FRONTEND_ARCHITECTURE.md`):

- `api/` — 类型化 HTTP 客户端,**唯一**与后端通信处(新包装器不得含 UI 副作用)。
- `stores/` — Pinia 实体缓存,`loaded`/`byId`/`ensureLoaded()`(inflight 去重)+ `getByIdAfterLoad(id)`。
- `composables/` — 跨域复用 hooks:`useAdminToken`、`useExpertMode`、`usePolling`、`useWebSocket`、`useAppRefreshCoordinator`(WebSocket + 降级轮询 + 终态会话刷新的唯一拥有者)。
- `features/` — 按业务域组织(本次重构核心):
  - `features/recaps/sessionActions.ts` — 两个回顾页入口(行 vs 抽屉)的显式动作矩阵(`UIActionName` 8 个动作,区别于生命周期的 `SessionActionName`);覆盖测试 `sessionActions.test.ts`。
  - `features/recaps/components/`、`features/settings/components/`、`features/channel/`、`features/onboarding/` — 拆分后的子组件与自管理 hooks。
- `components/` — 共享/展示组件;`components/shared/` **不得**自取 store。
- `views/` — 薄路由壳:数据加载分发、store 编排、动作处理;业务 UI 委托给 `features/`。

## 编码规范

- **Go**:包名小写,文件可用 snake_case,导出标识符用 PascalCase,测试 `*_test.go`。提交前 `gofmt`。偏好聚焦的小包,仅在降低耦合处用接口。
- **前端**:Vue 组件 PascalCase,清晰 TS 模块名,沿用既有 Element Plus + Pinia 模式。
- **提交**:遵循 Conventional Commits,如 `feat(recap): ...`、`fix(runtime): ...`、`style: ...`,scope 对应包或区域(`ui`、`recap`、`scheduler`)。

## 测试约定

- 后端:Go 标准 `testing`,测试置于所测包旁,命名为 `TestXxx`,分支行为用表驱动。PR 前 `make test`。
- 前端:`cd web && npm run type-check`;单测 `cd web && npx vitest run`;改动路由/导入/Vite 配置后跑 `npm run build`。

## 安全与配置

**禁止提交**:`config.yaml`、cookies、API keys、生成的数据库(`*.db`)、本地输出目录(`data/`、`logs/`)。
使用 `config.example.yaml`(最小)或 `config.full.example.yaml`(完整)作为模板。

## ZCode Skills 与扩展能力

ZCode 运行时对**每个目录根**同时扫描两个 skill 源(逆向 `~/.zcode/server/agents/glm/zcode.cjs` 的 `WWt`/`GWt` 解析器确认):
- `<root>/.zcode/skills/`(`source="zcode"`)
- `<root>/.agents/skills/`(`source="agents"`)← **本项目使用的路径**

二者均生效并合并。本项目在 `.agents/skills/`(本地 vendored,**已 `.gitignore`**)放了 43 个 Go Skill(`samber/cc-skills-golang`),全局 `~/.zcode/skills/` 另有 46 个(多 `codex-review`、`find-skills`、`pdf` 三个通用 skill)。本会话开头的 `system-reminder` 里两套会成对列出,是正常的去重合并结果,不是重复。

**调用方式**:**用户在 chat 里用 `$skill-name` 触发 skill**(`$` 是 Skill 触发符;`/` 留给 Command,二者在 `/` 命令面板里分两组显示)。Agent 内部则通过 Skill 工具调用。仅可调用列表中或用户显式 `$<name>` 提及的 skill,禁止凭训练记忆臆造。

> 例:用户输入 `$obscura 抓取 https://example.com` → ZCode 把该 skill 传给 agent,agent 遵循其指令工作。

**本项目常用的几个 Skill**:
- `golang-how-to` / `golang-code-style` / `golang-naming` — 写代码前查规范
- `golang-lint` / `golang-testing` / `golang-stretchr-testify` — 提交前 `gofmt` + 测试
- `golang-context` / `golang-concurrency` — 后端 `session`/`worker`/流水线大量依赖 context
- `codex-review` — 代码审查(全局 skill)
- `obscura` — Rust 无头浏览器(抓 JS 渲染页 / web 工具限流时的 fallback);已配置为全局 MCP server(`~/.zcode/v2/config.json` 的 `mcp.servers.obscura`),CLI 也能直接用 `obscura fetch <url>`

**尚未启用的 ZCode 扩展**(本仓库当前未配置,如需可后补):
- 自定义 slash 命令:`~/.zcode/commands/*.md`(用户级)或项目目录(工作区级),`/command-name` 调用
- Plugin:`.zcode-plugin/plugin.json`,可捆绑 skill + command + MCP + hook + LSP
- Output Styles 与 `hooks/hooks.json`

## 关键文件索引(遇到问题先看这些)

| 需求 | 入口文件 |
|------|----------|
| 启动流程 | `cmd/hikami/main.go` → `internal/runtime/` |
| 配置项定义与默认值 | `internal/config/config.go` |
| HTTP/WebSocket 路由 | `internal/handler/` |
| 场次状态机 | `internal/session/` |
| AI 回顾生成 | `internal/recap/` |
| 完整 API 路由表 | `CLAUDE-detail/api-routes.md` |
| 数据流详解 | `docs/data-flow.md` |
| 业务流程 | `docs/BUSINESS_FLOW.md` |
| 前端架构 | `docs/FRONTEND_ARCHITECTURE.md` |
| 各模块深度说明 | 根 `CLAUDE.md` + 各 `internal/<模块>/CLAUDE.md` |

## 变更记录

- 2026-06-29(二):用 Obscura 抓取 ZCode 官方文档(`/en/docs/skill`、`mcp-services`、`agents`、`commands`、`plugin`)核对,据官方表述修正两处:**① Skill 触发符为 `$`(用户 chat 输入 `$skill-name`),不是 `/`(`/` 是 Command 的触发符);② AGENTS.md 只读全局 + 工作区两级,不逐级合并子目录**。补充 Obscura(全局 MCP server + Skill)的集成说明。
- 2026-06-29:ZCode 运行时适配修正。逆向 `zcode.cjs` 确认 ZCode 对每个目录根**同时**扫描 `.zcode/skills` 与 `.agents/skills` 两个源并合并,因此本仓库 `.agents/skills/`(43 个 Go Skill,本地 vendored 且 `.gitignore`)与全局 `~/.zcode/skills/`(46 个)均生效;新增"ZCode Skills 与扩展能力"小节说明调用方式与未启用的扩展;修正上次把 `.agents/skills` 当"死引用删除"的错误结论。联动修正 `.gitignore`(移除误入的 `引用格式`、移除与"已提交 AGENTS.md"矛盾的 `AGENTS.md` 忽略规则)。
- 2026-06-28:依据 ZCode 文档规范重写。修正错误的 Go 环境指令(移除 `/root` 前缀要求);补充启动/端口/调试命令与运行时依赖表;明确 AGENTS.md(ZCode 运行时)与 CLAUDE.md(详尽人类参考)的分工。
