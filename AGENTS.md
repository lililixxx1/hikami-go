# Repository Guidelines

> **ZCode Agent 运行时上下文**。ZCode 在每个任务启动时读取本文件(以及 `~/.zcode/AGENTS.md`)。
> 本文件聚焦"Agent 工作时最常需要的信息":命令、约定、结构与边界。
> 详细的架构图、模块逐一解析、数据流见根目录 [`CLAUDE.md`](./CLAUDE.md)(人类可读的完整参考,各模块目录另有本地 `CLAUDE.md`)。

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

- 2026-06-28:依据 ZCode 文档规范重写。修正错误的 Go 环境指令(移除 `/root` 前缀要求);删除不存在的 `.agents/skills/` Go Skills 死引用;补充启动/端口/调试命令与运行时依赖表;明确 AGENTS.md(ZCode 运行时)与 CLAUDE.md(详尽人类参考)的分工。
