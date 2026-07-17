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
- **启动(开发)**:`make run` 或 `./hikami -config config.yaml`(日志打到 stdout)。
- **启动(生产/systemd)**:`systemctl start hikami`(service 定义在 `/etc/systemd/system/hikami.service`,`Restart=on-failure` 崩溃自愈)。**改完代码必须 `make build-go` 重编 `./hikami` 后 `systemctl restart hikami` 才生效**;`systemctl restart` 不会重新编译。
- **默认监听**:`127.0.0.1:6334`(仅本机,定义在 `internal/config/config.go` 的 `web.listen` 默认值)。
- **访问管理界面**:浏览器打开 http://127.0.0.1:6334 。
- **二进制特点**:前端经 `embedded_web` build tag 内嵌进 `./hikami`(单文件部署,无需额外 web 资源)。

## 日志与状态存储

**事件日志和结构化状态分开存放,排查问题时两者都要看:**

| 位置 | 内容 | 查看方式 |
|------|------|---------|
| **journald**(systemd 收集) | 运行时事件日志(slog JSON 流:任务进度、自动触发链、WARN/ERROR) | `journalctl -u hikami -f`(实时)/ `-n 200`(最近)/ `--since "1 hour ago"` |
| **`hikami.db`**(SQLite) | 结构化状态:session/task/channel 表、时间戳、last_error | `sqlite3 hikami.db "..."` 查具体场次/任务状态 |
| **`logs/hikami-*.log`**(历史) | systemd 部署前的旧运行日志(手动启动时 stdout 重定向产生) | 已停止写入,仅供回溯;`.gitignore` 已忽略 |

> **日志位置说明**:
> - 程序代码里 slog 只输出到 **`os.Stdout`**(`cmd/hikami/main.go`),**自身不写文件**。
> - 生产环境(systemd)经 `StandardOutput=journal` 进 **journald**——这是唯一的实时日志源。
> - 开发环境(手动 `./hikami`/`make run`)日志直接到终端 stdout,需自行 `2>&1 | tee file` 才落盘。
> - `config.logs.{dir,level,format}` 配置项目前只控制日志**级别**和**格式**(`json`/`text`),`dir` 用于建目录但程序不主动写文件——文件落盘靠外层(systemd journal 或手动重定向)。

> **DB 时间字段时区**(2026-07-04 统一):`sessions`/`tasks` 表的用户可见时间字段(`started_at`/`ended_at`/`published_at`/`uploaded_at`/`archived_at`/`created_at`/`updated_at`)统一存本地时区 RFC3339(`2026-07-04T09:07:39+08:00`)。该日期之前的历史数据可能是 UTC 无时区格式,显示会偏移。前端 `formatDateTime` 用 `new Date()` 解析,带时区字符串能正确显示本地时间。

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
- **支撑**:`aiprovider`(AI provider 抽象)、`secrets`、`runtimeconfig`(全局运行时配置覆盖持久化,与 secrets 共享事务)、`fsutil`、`importer`、`biliutil`。

### 模块依赖概览(Mermaid)

> 运行时快照。完整结构图(带 click 跳转)见 [`CLAUDE.md`](./CLAUDE.md);各模块深度说明见对应 `internal/<模块>/CLAUDE.md`。

```mermaid
graph LR
    CMD["cmd/hikami<br/>main + 自动触发链"]
    CMD --> HANDLER["handler<br/>Gin + WebSocket"]
    CMD --> RUNTIME["runtime<br/>工具探测/编排"]
    CMD --> SCHEDULER["scheduler<br/>定时发现/直播检查"]
    CMD --> WORKER["worker<br/>任务池 + Hub"]

    subgraph 来源["来源适配"]
        DISCOVER["discover"]
        DOWNLOAD["download"]
        LIVE_REC["live_record"]
        IMPORTER["importer"]
    end
    subgraph 管道["处理管道"]
        NORMALIZE["normalize"] --> ASR["asr"]
        ASR --> RECAP["recap"]
        RECAP --> UPLOAD["upload"]
        UPLOAD --> PUBLISHER["publisher"]
        PUBLISHER -.状态旁路.-> ARCHIVE["archive"]
    end

    HANDLER --> 管道
    WORKER --> 管道
    DISCOVER --> NORMALIZE
    DOWNLOAD --> NORMALIZE
    LIVE_REC --> NORMALIZE
    IMPORTER --> NORMALIZE

    subgraph 生命周期["生命周期 / 状态"]
        SESSION["session"]
        STATE["state<br/>状态机"]
        CHANNEL["channel"]
    end
    subgraph 配置存储["配置 / 存储"]
        CONFIG["config<br/>+ApplyOverrides"]
        DB[("db / SQLite<br/>v33")]
        SECRETS["secrets"]
        RUNTIMECFG["runtimeconfig"]
    end
    subgraph 支撑["支撑"]
        AIPROV["aiprovider"]
        GLOSSARY["glossary"]
        NOTIFY["notify"]
        BILIUTIL["biliutil"]
        FSUTIL["fsutil"]
    end

    管道 --> SESSION
    SESSION --> STATE
    CHANNEL --> SESSION
    RUNTIMECFG -->|"覆盖基线"| CONFIG
    SECRETS -.共享事务.-> RUNTIMECFG
    RUNTIMECFG --> DB
    SECRETS --> DB
    CONFIG --> DB
    RECAP --> AIPROV
    RECAP --> GLOSSARY
    PUBLISHER --> NOTIFY
    UPLOAD --> FSUTIL
```


## 前端结构(web/src/)

分层架构(详见 `docs/FRONTEND_ARCHITECTURE.md`):

- `api/` — 类型化 HTTP 客户端,**唯一**与后端通信处(新包装器不得含 UI 副作用)。
- `stores/` — Pinia 实体缓存,`loaded`/`byId`/`ensureLoaded()`(inflight 去重)+ `getByIdAfterLoad(id)`。当前 5 个:`channels`、`sessions`、`tasks`、`liveStatus`、`runtime`(运行时状态/能力)。
- `composables/` — 跨域复用 hooks(共 7 个):`useAdminToken`、`useExpertMode`、`usePolling`、`useWebSocket`、`useAppRefreshCoordinator`(WebSocket + 降级轮询 + 终态会话刷新的唯一拥有者)、`useRecapModels`(按厂商分组的推荐回顾模型下拉,全局/主播级复用)、`useDiscoverReplay`(发现回放抽屉可见性 + 执行后刷新,RecapsView/HomeView 共用)。
- `features/` — 按业务域组织(V10 重写核心):
  - `features/recaps/sessionActions.ts` — 两个回顾页入口(行 vs 抽屉)的显式动作矩阵(`UIActionName` 8 个动作,区别于生命周期的 `SessionActionName`);`isReplaySource` 对回放类(download/import)隐藏 publish/edit/remove(归档 upload 保留);覆盖测试 `sessionActions.test.ts`(48 用例)。
  - `features/recaps/components/`、`features/settings/components-v10/`、`features/channel/`、`features/onboarding/`、`features/streamers/`、`features/home/` — 拆分后的子组件与自管理 hooks。设置页由 `SettingsView.vue` 编排为 sidebar + content + 多卡(V10 重写,Phase 5)。
- `components/ui/` — **V10 自建组件库**(Phase 6):19 个 H* 组件(HInput/HSelect/**HCombobox**/HButton/HCheckbox/HSwitch/HDialog/HDrawer/HTable/HCard/HPill/HProgress/HEmpty/HDescriptions/HCollapse/HTextarea/HToast + ConfirmHost;2026-07-15 新增 HCombobox)+ HMessage/HConfirm/HToast 命令式基础设施,`design-tokens.css` 锁定 token。已移除 Element Plus。15 个组件有单测保护。
- `components/` — 其他共享/展示组件;`components/shared/` **不得**自取 store。
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

- 2026-07-15(二):**回顾模型支持手动输入 + HCombobox 组件**(`e17fa9c` + merge `797a8e4`,codex 审核 2 轮 APPROVED)。回顾模型选择原先用 `HSelect`(原生 `<select>`),只能从预设选、无法手动输入;后端预设却堆 8 个覆盖面仍不足。**改动**:① 新增 `web/src/components/ui/HCombobox.vue`(input + 原生 datalist 组合框,可输入任意模型名 + 下拉快捷选项,`clearable` 清空回路用于主播级"留空跟随全局",渐进增强旧浏览器降级为普通 input),H* 组件库 16→19(含 ConfirmHost/HToast 统计口径);② `RecapCardV10.vue`(设置页)与 `StreamerDrawer.vue`(主播抽屉)两处回顾模型字段改用 HCombobox;③ 后端 `internal/handler/server.go` 的 `recommendedRecapModels` 常量精简到 DeepSeek 2 个(flash + pro),`TestGetRecapModels` 改精确集合+顺序断言。**测试**:新增 `HCombobox.test.ts`(11 用例:v-model/旧值回显/datalist 绑定/同页双实例 id 唯一性/disabled/clearable/slot/size),前端 161→180 测试、24→26 文件。后端 27 包全过、type-check/build/gofmt 通过。文档:handler/web CLAUDE.md + 本文件 changelog。

- 2026-07-15(二):**4 个调查问题修复**(`a1a595d`,codex CLI 计划+执行审核 APPROVED)。基于 4 个调查文档(ffmpeg-location/弹幕/z-index/二维码竞态),经代码核实统一实施。① **download: yt-dlp `--ffmpeg-location` 注入**(`download.go`):`ytDlpArgs()` 原只处理 `--cookies`,未把 `YTDLPDownloader.FFmpeg` 字段转为 `--ffmpeg-location`,致 yt-dlp 后处理(`-x` 提取音频为 m4a)找不到 hikami 解析的 ffmpeg;重写注入 `--ffmpeg-location <dir>` + 新增 `ffmpegLocationDir()` helper(裸命令名/空值返回空保持 PATH 回退)。② **download: 单 P 弹幕抓取缺失**:`downloadSingleP` 原无弹幕抓取,normalize 阶段弹幕为空、回顾显示 0 条;新增 `singlePCid()` helper(bvid→view API→Pages[0].CID),成功后调 `fetchDanmakuShared` 写 `raw/danmaku.xml`(与 native 单 P / yt-dlp 多 P 对齐),失败不阻断。③ **ui: RecapDrawerV10 面板 z-index 缺失**:面板用 `recap-drawer-panel` class 但该 class 全前端无 CSS 定义 → `position:static` 被遮罩盖住;class 改为 `drawer rtl open recap-drawer-panel` 复用 ui.css 的 fixed+z-index:101。④ **channel: B站扫码二维码首次不显示**:真实根因(修正调查文档竞态归因)是 `watch(visible)` 缺 `immediate:true`(组件 v-if 挂载、visible 初值即 true,watch 默认只在变化时触发 → startLogin 永不调用);主修复加 `{ immediate: true }`,补充防御 `renderQRCode` 加 `await nextTick()`。**测试**:download +7(ffmpegLocationDir 跨平台 + TestSinglePCidNoBvid 降级),download 49→56(运行时 60)。**验证**:后端 27 包全过、前端 vitest 172 全过、gofmt/vet/build 通过。文档:download/web CLAUDE.md + 本文件 changelog。

- 2026-07-14(一):**Windows 系统托盘 + 隐藏控制台 + 文件日志**(`ad34a15`)。为 Windows 桌面用户优化:双击 exe 后无控制台黑窗、托盘图标可打开管理界面/退出、日志自动落盘。**改动**:① `cmd/hikami/` 新增 `tray_windows.go`(`//go:build windows && systray`,基于 `fyne.io/systray` 实现托盘图标+菜单「打开管理界面/退出」)、`tray_other.go`(`//go:build !windows || !systray` 等价占位,两文件 build tag 互斥保证 `runTray`/`shutdownCoordinator`/`initLogFile` 在所有平台可编译)、`trayicon.go`+`trayicon.ico`(`//go:embed` 托盘图标字节);② **关闭流程重构为 `shutdownCoordinator`**(sync.Once 幂等):原 main.go 内联的 SIGINT/SIGTERM 监听 + HTTP Shutdown 抽到协调器,托盘「退出」/信号都走 `requestShutdown`,关 HTTP 后调 `systray.Quit()` 让 `systray.Run()` 返回、main 继续 defer 链(LIFO:sched.Stop→workerPool.Stop→database.Close→logCleanup),**不调 os.Exit** 保证 defer 执行;③ **桌面模式文件日志**:`initLogFile` 在 Windows+systray 下优先写 `%LOCALAPPDATA%/Hikami-Go/hikami.log`(用户可写目录),失败回退 exe 同目录便携模式,其他平台返回 stdout(main 启动时调 `initLogFile()` 拿 logWriter+logCleanup);④ Makefile 新增 `build-windows-desktop`/`-lite` target(`-tags 'embed_ffmpeg,embedded_web,systray'` + `-ldflags='-H windowsgui -s -w'`,`CGO_ENABLED=0`);⑤ CI `.github/workflows/release.yml` windows 矩阵新增 `desktop: true` 变体(产物名带 `-desktop` 后缀)。依赖:`go.mod` 新增 `fyne.io/systray v1.12.2`(transitive godbus/dbus/v5 等)。**验证**:27 包全过、go vet 通过。文档:cmd/hikami CLAUDE.md + 本文件 changelog。

- 2026-07-16(三):**3 个调查文档问题修复**(用户提供的 `/home/lioi/文档/investigations/` 下 3 个调查,codex CLI + code-reviewer 子代理多轮审核)。**核实阶段发现**:问题 2(术语校正)的调查文档误标"已修复",实际本仓库代码未修复(改动停在另一台 Windows 机器 `C:\Users\Administrator\Desktop\ccc\hzm`),3 个问题全部待修复。**修复**:
  ① **术语校正词边界缺失**(后端,严重度中):`glossary_correction.go`/`transcript_correction.go` 两处 `strings.ReplaceAll` 纯子串匹配,含 ASCII 字母数字的 term 嵌在更长单词里时被误替换(AI 嵌 MAIL/277 嵌 123277456),静默损坏转写文本+回顾正文。新增 `replaceTermBoundaryAware`/`hasAlphanumeric`/`isASCIIAlphanumeric`(对含 `[A-Za-z0-9]` 的 term 强制词边界,纯 CJK 回落 ReplaceAll 零回归;`term==""` 提前返回防文本损坏),位置B 顺带修正 applied 记录准确性。+4 测试(recap_test.go 72→76)。
  ② **ResolvedTemplate 缺 json tag**(后端,严重度中):`template.go:57-63` 4 字段无 tag → Go 用 PascalCase 序列化 → 前端按 snake_case 访问得 undefined,主播级模板「跟随全局」预览全空。补 `json:"snake_case"` tag,同步 OpenAPI spec 4 文件(从 PascalCase 改回 snake_case)、重新生成 `generated.ts`、清理误导注释。+1 测试(template_test.go 26→27)。
  ③ **TemplateCardV10「添加变量」无效**(前端,严重度中):`kvRows` writable computed 读写环 + setter 过早丢弃空 key,点「+ 添加变量」后新行立即被销毁。改为独立 ref + 保存时 flush;`:key` 从数组索引改稳定 id。codex 3 轮审核收敛出额外 BLOCKING:成功/失败协议(composable 的 loadData/save/importTemplateFile 返回 `Promise<boolean>`,组件仅在成功时 sync/关对话框/emit;写入成功但重拉失败也视为失败)、导入同步(import handler 后 sync 避免覆盖)、预设不 sync(applyPreset 不动 extraVars)。+8 测试(web 161→180、24→26 文件)。
  **审核**:codex CLI 对修复 2/3 APPROVED、修复 1 经 3 轮 NEEDS_CHANGES 收敛后 code-reviewer 子代理 APPROVED;codex 渠道前期持续 503(模型 gpt-5.6-sol 无可用 distributor),后期恢复完成修复1的3轮交互审核。**验证**:后端 27 包全过、前端 type-check/vitest/build 全过、gofmt/vet 通过、redocly 7 warnings 同基线。文档:recap/web CLAUDE.md + 本文件 changelog + 调查文档状态更正。

- 2026-07-14(一):**`/init-project` 增量文档同步**(无代码改动,纯文档漂移修复)。核对 `4a79b44`(HEAD,上次文档终点)→ 当前,无新 commit,但三路并行审计发现文档与代码的多处漂移(根 `CLAUDE.md` 模块索引停在 2026-07-06、多个模块 CLAUDE.md 测试计数过时、config CLAUDE.md 的 `EnsureDirs` 描述与代码矛盾)。**修复 10 个文档文件**:① **根 `CLAUDE.md`** 模块索引 13 行测试计数全量刷新(config 31→34、discover 10→16、download 48→49、glossary 63→68、handler 66→75、live_record 72→89、recap 94→100、runtimeconfig 8→9、secrets 8→9、session 39→40、worker 41→42、archive 14→13、web 97→161)+ 描述更新(handler 补 tools/glossary 双格式/GET channels、discover 补 title_prefix raw-title、live_record 补 #10/#11/P2 哨兵、db v33→v35)+ 补 4 个 changelog 段(07-07/07-08/07-13);② **config CLAUDE.md**:`EnsureDirs()` 描述从"创建 output_root、logs.dir、数据库目录"改为"创建 output_root、数据库目录(不再创建 logs/)"(事实修正)+ 补 07-08 tools 段 + 07-13 logs/ 移除两行 changelog + 测试 31→34;③ **discover CLAUDE.md**:补 title_prefix 匹配在原始标题(CleanReplayTitle 之前)的说明 + 07-13/07-11 两行 changelog + 测试 10→16;④ **runtime CLAUDE.md**:平台表 windows 源从"BtbN"改为"裁剪版嵌入"+ manifest 路径修复细节补入 changelog;⑤ **db CLAUDE.md**:补 v33(runtime_settings)/v34(bypass_fail_state)/v35(tools CHECK 扩展)三行 changelog(正文已有但 changelog 停在 v32);⑥ **handler CLAUDE.md**:server_test 50→59 + 补 07-08 tools+bug-report changelog + 测试 66→75;⑦ **live_record CLAUDE.md**:bilibili 11→13、manager 38→51、anomaly9 8→10 + changelog 72→87 修正为 72→89(执行复审 +2);⑧ **archive CLAUDE.md**:14→13;⑨ **web/CLAUDE.md**:149→161、23→24 文件;⑩ **recap/glossary/download/runtimeconfig/session/worker CLAUDE.md**:各 per-file 测试计数刷新。**覆盖率**:28 个模块 CLAUDE.md + 根 CLAUDE.md + AGENTS.md 全部审计,0 缺失文档。**验证**:全项目 `go test ./...` 27 包全过(0 fail)、`cd web && npx vitest run` 24 文件 161 测试全过。

- 2026-07-13(日):**嵌入裁剪版 ffmpeg,减小 Windows 单文件体积**。用户反馈"软件依赖 ffmpeg,包体积大,可不可以打小一点的包"。**先核实代码再动手**(关键,避免误解):① 默认 `make build`/`build-go`(Linux/macOS)**不含 ffmpeg**,二进制只嵌前端,ffmpeg 走运行时 `exec` 调系统命令,Linux 服务器本就不会体积暴涨(`apt install ffmpeg` 即可);② 体积暴涨只发生在 `build-windows-amd64`(`-tags embed_ffmpeg,embedded_web`,嵌完整 BtbN gpl 版 ffmpeg ~80MB);③ 项目**已有完整的 ffmpeg 解析链路**(`ResolveFFmpeg` 三级:系统查找→嵌入→下载兜底)+ `embed_ffmpeg` build tag,改动面很小。**ffmpeg 实际用量调查**(决定裁剪清单):录制(`live_record`)+ 分段合并(`manager.go`)+ download 多P 合并**全走 `-c:a copy` 流复制,零编码器**;仅 normalize 需 mp3 encoder、importer 需 aac encoder;demuxer 需 flv(直播)/concat(合并)/mov/mp3 等、muxer 需 mov(m4a)/mp3。完整 gpl 版 80MB 里绝大部分是 h264/h265/vp9/av1 视频编解码(用不到),裁剪后预计 8-12MB(≈1/8)。**改动**:① 新增 `scripts/build-ffmpeg-minimal.sh`(Docker `gcc:13-bookworm`+MinGW-w64 交叉编译 Windows x64 静态,`--disable-everything` 后白名单启用本项目所需模块,`--disable-asm` 规避 MinGW 汇编坑,产物 `internal/runtime/assets/ffmpeg.zip`);② 新增 `scripts/verify-ffmpeg-minimal.sh`(6 用例逐条复刻代码真实参数:ffprobe 时长/normalize 转码/importer 转码/concat 合并/pipe 录制/FLV 录制,全绿才算产物合格);③ 新增 `scripts/README-ffmpeg-build.md`(用法/许可证/裁剪清单与代码出处对应表/不支持什么);④ `ffmpeg_manifest.go` Version `master-latest`→`embedded-minimal-7.x`(新缓存目录隔离旧完整版,升级用户重新解包裁剪版),补裁剪版注释;⑤ `.gitignore` 第 22 行 `internal/runtime/assets/` 整目录忽略 → 改为白名单放行 `ffmpeg.zip`/`LICENSE.txt`(入库让 Windows 构建开箱即用),加 `.tmp/ffmpeg-build/` 忽略;⑥ Makefile 新增 `build-ffmpeg-minimal`/`verify-ffmpeg-minimal` target,`build-windows-amd64` 注释更新为裁剪版 + 体积提示,lite 版(不嵌)保留兜底。**未改任何解析逻辑**(`ResolveFFmpeg`/`installEmbeddedFFmpeg`/`probe.go` 原样复用,裁剪版只是替换嵌入的 zip 内容)。**验证**:全项目编译通过、27 包 `go test` 全过、`go vet` 通过、`.gitignore` 白名单用 `git check-ignore` 逐条验证、脚本 `bash -n` 语法检查通过。**用户需自行运行一次 `make build-ffmpeg-minimal`**(Docker 环境)产出 `ffmpeg.zip` 放进 assets,否则 `make build-windows-amd64` 因 embed 失败报错(与现状一致,非回归)。文档:`internal/runtime/CLAUDE.md` + 本文件 changelog。

- 2026-07-13(日):**修复 Windows exe 双击闪退(manifest 路径与裁剪版 zip 结构不匹配)**。用户反馈双击 `hikami-windows-amd64.exe` 一闪而过。**核实(命令行实跑 + 源码追溯双确认,详见 `docs/exe闪退问题与修复.md`)**:裁剪版 ffmpeg 本身经 3 轮验证全绿(`docs/验证报告.md` PASS=7 FAIL=0),问题不在 ffmpeg。根因是上次裁剪版提交时**只换了 `assets/ffmpeg.zip` 内容、没同步改 `ffmpeg_manifest.go` 的路径**——manifest 的 `windows-amd64.FFmpegPath` 写死的是 BtbN 完整版目录结构 `ffmpeg-master-latest-win64-gpl-shared/bin/ffmpeg.exe`,而裁剪版 zip 顶层直接是 `bin/ffmpeg.exe` → embedded 解包成功但 `finalizeInstall` 按 manifest 找不到 ffmpeg → 兜底 `downloadAndInstallFFmpeg` 又指向 BtbN 完整版(联网失败/路径仍不符)→ 启动 health check fatal → `exit(1)`(console 程序窗口关太快看起来像"闪退")。**改动**:`ffmpeg_manifest.go` 的 `windows-amd64` 段三处——`FFmpegPath`→`bin/ffmpeg.exe`、`FFprobePath`→`bin/ffprobe.exe`、`ArchiveURL` 删除(留空防误下 80MB 完整版,`downloadAndInstallFFmpeg:135` 对空 URL 有显式保护兜底报错而非 panic);`linux-*` 不动(走系统 ffmpeg)。`Version` 保持 `embedded-minimal-7.x`(上次已改)。**验证**:`runtime`/`config` 包 `go test` 通过、`go vet` 通过、`-tags 'embed_ffmpeg,embedded_web'` 交叉编译 Windows exe(21M,符合嵌裁剪版预期)、用户 Windows 实机验证 exe 正常启动并监听端口。**教训**:换 zip 内容时必须同步 manifest 的 `FFmpegPath`/`FFprobePath`(解包后按此路径找二进制),只改 Version 不够。

- 2026-07-13(日):**不再创建空的 logs/ 目录**。用户反馈 Windows 运行后同目录冒出空 `logs/` 文件夹、里面没文件。**核实(全项目 grep 互证)**:程序只把 slog 日志写到 `os.Stdout`(`main.go:65/67` 的 TextHandler/JSONHandler 绑 stdout),全项目无任何 `OpenFile/os.Create` 写日志文件;`logs/` 空目录是 `config.go:EnsureDirs()` 启动时 `MkdirAll(c.Logs.Dir)` 建的——`logs.dir` 配置项是历史遗留(早期版本可能写过文件),当前只 `level`/`format` 起作用。**改动**:`EnsureDirs()` 删掉建 `logs/` 那段(保留 `LogsConfig.Dir` 字段和 `logs.dir` 默认值,纯向后兼容老 config.yaml,避免反序列化报错),加注释说明程序不落盘、日志靠 stdout(Linux systemd→journald、Windows 手动运行需 `> run.log 2>&1` 重定向)。**验证**:`config` 包 `go test` 通过(无断言 `Logs.Dir`)、Windows exe 重编。

- 2026-07-08(三):**配置默认值 + 设置页 UI 三处修复**(branch `fix/config-and-ui-2026-07-08`,Claude CLI 计划审核 v1 NEEDS_CHANGES → v2 APPROVED + 执行后复审)。用户反馈 4 问题,逐一核实成立。① **output_root 默认值 huizeman→hikami-go**:`config.go` SetDefault + `config.full.example.yaml`(output_root + webdav.base_path)+ `docs/DESIGN.md`。已配 config.yaml 的用户不受影响(SetDefault 仅在未设 key 时生效);recap 测试 fixture 里的 `"huizeman"` ChannelID/路径与默认值无关不改。② **设置页 sidebar 不随滚动**:`settings-v10.css` 的 `.sidebar` 加 `position:sticky;top:0;align-self:flex-start;max-height:calc(100vh - var(--topbar-h))`,配合已有 `overflow-y:auto` 实现钉顶 + 自身超长独立滚动。滚动容器是 `AppLayout.vue` 的 `.main-content`(`overflow-y:auto`),sticky 相对它吸附。③ **yt-dlp/rclone 路径 web 可编辑(新增 tools 配置段)**:新增第 7 个 runtimeconfig 段——`ToolsSectionDTO`(YTDLP/Rclone 指针,presence-aware)+ `ApplyOverrides` tools case + `GET/PUT /api/config/tools` handler(参照 archive 模式,保存后 `refreshRuntimeStatus` 重新 Probe,前端 `onSaved` 重拉 runtime)。**DB v35 迁移**:runtime_settings 表 CHECK 约束白名单 `+tools`(SQLite 不支持改 CHECK,走标准表重建:建 v35 临时表→INSERT 复制→DROP 旧表→RENAME,6 段旧数据无损回灌)。**设计决策(只暴露 yt-dlp/rclone 不含 ffmpeg/ffprobe)**:后者 required=true,改错路径下次启动 fatal、web 不可达无法纠正,运维代价高;前者改错仅降级不崩,web 仍可修正。前端:`settings.ts` 加 getToolsConfig/updateToolsConfig + ToolsConfig 内联类型(generated.ts 尚未含此端点,过渡期手写),`ToolsCardV10.vue` 由纯展示重写为可编辑表单(上半 yt_dlp/rclone 输入框 + 保存,下半保留只读探测结果表),`SettingsView.vue` 加 `@saved` + `:key` reload。④ **确认框出现在左下角**:**根因是 DOM 结构与 CSS 不匹配**——HDialog.vue 里 `.dialog-overlay` 与 `.dialog` 是**兄弟节点**,overlay 的 `display:flex;align-items:center;justify-content:center` 只作用于子元素,dialog 作为兄弟不受影响,其 `position:relative` 无偏移 → 跟随文档流跑到左下。修复:模板改为**嵌套**(`.dialog` 作为 `.dialog-overlay` 子元素)+ overlay 改用 `@click.self="close"`(codex v1 必改:`@click.self` 比 `@click.stop` 更健壮——`.self` 修饰符精确匹配 `event.target===currentTarget`,杜绝 dialog 内 body/footer 空白区点击冒泡误关)。**测试**:config +3(ToolsOverrides/PresenceAware/EmptyStringClears)、handler +1(ToolsConfigRoundTrip 含 presence-aware + runtime_settings 持久化断言)、HDialog +2(点内容不关 / 嵌套结构断言)。前端 149→151、config 31→45、handler 95、db 9。27 包全过、type-check/build/vitest 通过、redocly lint 通过(7 warnings 同基线)。文档:openapi(+tools path+schema)、handler/config/db CLAUDE.md、本文件 changelog。

- 2026-07-08(三):**Bug 报告核实修复**(branch `fix/bug-report-2026-07-08`,复审人 Claw 的 5 条报告逐条核实 + 可复现测试验证 + Claude CLI codex-review 两轮)。**核实结论(不照单全收)**:报告 5 条中真实 3 条、不成立 1 条、夸大定性 1 条。① **Bug #1(glossary JSON 导入,修)**:报告声称"服务崩溃/HTTP 000"**不成立**(实测返回 500,Gin 有 `Recovery()` 不崩),但真实 Bug 存在——前端 `importGlobalJSON` 发裸数组 `[{...}]`、后端 `ImportJSON` 只收 `GlossaryExport` 对象 → 导入永久失败。修复:`ImportJSON` 双格式 fallback(对象→裸数组,范式对齐 `recap/template.go`),新增 `ErrInvalidJSON` 哨兵经 `writeError` 映射为 **400**(原 500)。② **Bug #2(publish round-trip,修 + codex 复审收敛)**:报告归因"topic_name 非法 UTF-8"**不成立**(默认空串,脏数据在该用户 DB),真实根因是全局 `private_pub` 校验拒绝 `0`,而 GET 默认/未配置状态返回 0 → round-trip 失败。**初版放宽接受 0/1/2,codex 审核标 BLOCKING**(指出 publisher.go:62 把频道级 0 回落全局值,若全局也是 0 会原样发给 B 站 API),**改为规范化**:全局段 PUT 把 `0` 规范化为 viper 默认 `2`(公开),既保证 round-trip 幂等又堵住 publisher 收到 0 的路径。③ **Bug #3(channels PUT 非部分更新)**:属实但**设计如此**(仅 auto_recap 三态,全字段替换),改 PATCH 语义风险大,**文档说明不改代码**。④ **Bug #4(缺 GET /api/channels/:id,修)**:`Store.Get` 已存在仅未注册路由,补 `getChannel` handler + 路由。⑤ **Bug #5(cover_url 默认 /home/cc)**:**不成立**,仓库无此默认值(仅无关测试路径),用户 DB 脏数据。**改动**:glossary + handler 2 包、OpenAPI(`channels/{id}` 补 get operation、publish private_pub 描述补规范化语义、glossary import 描述补双格式)、文档 4 文件。**测试**:新增 7 测试(glossary 3 + handler 4),其中 publish 测试 v2 覆盖 0→2 规范化 + 1/2 直收 + 非法拒绝 + GET 回读 round-trip 幂等。27 包全过、build 通过、redocly lint 通过(7 warnings 同基线)。**codex 审核**:v1 NEEDS_CHANGES(Bug #2 publisher fallback BLOCKING + Bug #1/#4 APPROVED)→ v2(规范化修复)。

- 2026-07-08(三):**Vue 3 前端 V10 全页面重写完成(Phase 0-6)**(branch `feat/remove-element-plus`)。基于 OpenAPI 契约(openapi-typescript 生成 `generated.ts`)重写全部 4 个视图(HomeView/RecapsView/StreamersView/SettingsView)+ 设计系统。**设计系统**:自建 V10 组件库 `web/src/components/ui/`(16 个 H* 组件 + HMessage/HConfirm/HToast 命令式基础设施)+ `design-tokens.css` 锁定 token,完全移除 Element Plus。**Phase 6(本轮,4 commits)**:① 迁移剩余 8 个 EP 业务组件(GlossaryEditor/RecapTemplateEditor/ImportSessionDrawer/DownloadByURLDrawer/DiscoverResultDrawer/OnboardingWizard/ChannelIdentifyDialog/BiliQRCodeLoginDialog)到 H* 原语(el-*→H*,@element-plus/icons-vue→inline SVG,el-upload→native file input),删除死代码 TaskProgressBar.vue;② main.ts 删除 ElementPlus 注册 + 删除 ep-theme-bridge.css + `npm uninstall element-plus`;③ 删除手写 `api/types.ts`(549 行),39 个 import 全部迁移到 `api/types-derived.ts`(从 generated.ts 派生;补齐 Capabilities.reason 必填、ConfigStatus.glossary_* 字段、配置类型 Response+Request 写字段合并、ResolvedRecapTemplate snake_case 等兼容性);④ 文档同步(FRONTEND_ARCHITECTURE 技术栈/红线、api-gap-analysis P0/P1 标 ✅、web/CLAUDE.md、本文件)。**验证**:149 测试通过(含 sessionActions 48 + 14 个 UI 组件单测)、type-check 通过、build 通过、bundle 体积大幅下降(EP ~600KB gz 移除)。**关键约束**:业务逻辑(API 调用/表单校验/emit 契约/composable)全部不变,纯 UI 原语替换;types-derived 保留与运行时数据形态一致的兼容定义(Capabilities/ConfigStatus/ResolvedRecapTemplate 等)。

- 2026-07-07(一):**后端接口 OpenAPI 文档落地**(`docs/api/`,branch `feat/api-openapi-doc`)。手写 OpenAPI 3.0.3 YAML 规范,**121 个端点 + WebSocket 事件契约**,作为 Vue 3 前端全页面重写(V10 模板)的契约源。**对 spec §2.1 的执行期修正**:原计划 paths/ 按域拆分需 JSON Pointer 转义(`/`→`~1`),改为 **openapi.yaml 单文件 paths 内联 + 仅 schema 跨文件**(14 个 `components/schemas/*.yaml`)。产物:`openapi.yaml`(主文件,paths 内联)+ 14 个 schema + `index.html`(Swagger UI 5 CDN)+ `api-gap-analysis.md`(V10 模板 vs 后端 4 页逐元素对照)+ `README.md` + Makefile 3 target(`api-docs`/`api-lint`/`api-gen-types`)。11 个 task 分 3 批次(T0-T3 骨架+系统+频道+场次 / T4-T6 任务+运行时+配置 / T7-T11 术语+模板+B站+密钥+gap),打 tag `api-batch-1`/`api-batch-2`。**关键陷阱如实记录**:① `/ws` 路径无 `/api` 前缀;② `ResolvedTemplate` 字段名 **PascalCase**(SystemPrompt 等,源码无 json tag 历史遗留);③ `POST /api/sessions/import` 是 multipart/form-data;④ **`PUT /api/secrets/{key}` 传空串删除,无 DELETE 端点**;⑤ `auto_recap` 三态(`*bool`);⑥ 6 组 config 全 patch 语义(全字段指针,无 required);⑦ QR session 过期→**410**(非 404);⑧ 兼容端点 `/api/bili/accounts/*` 标 `deprecated: true`;⑨ 配置密钥只写(GET 永不返回明文,只有 `*_set` boolean);⑩ `TaskDetailResponse` 条件字段(friendly_error/auto_retry 仅 failed 出现)。redocly lint 全程通过(7 warnings,均 info-license 或未来用的未引用 component)。gap 分析核心结论:模板与 API 整体契合度高,P0 阻塞点是 **Session/Task 缺 channel_name 字段** + **listSessions 不支持 channel_id/source/search 过滤** + **模板无 WebSocket 连接代码**(进度列静态),前端重写需先补这三项。

- 2026-07-07(二):**录播稳定性 异常 #10/#11 + P2 落地**(合并 `plans/archive/plan-录播稳定性-异常10-11.md`,codex 审核计划 **v1→v4 四轮收敛 APPROVED** + 执行后复审 **v1→v3 三轮 APPROVED**)。三项修复,6 个 commit on branch `fix/live-record-anomaly-10-11-p2`(`7c9ae23`/`a35cbbb`/`ce4b0ba`/`6801b26`/`603ae90`/`fcbdbaa`),共 17 个新测试,全 `internal/live_record/...` 通过。① **异常 #10(重连死循环,P0)**:`decideAfterRecord` default 分支(探测出错 + wantErr!=nil)不再"attempt anyway",改返回新决策 `afterRecordProbeFailReconnect`,由重连循环用独立预算 `probeErrorBudget=1` 控制;耗尽时**校验有效音频**(`hasRecordedAudio`):有 → 成功收尾保留,无 → 失败路径(避免空音频送 normalize 污染回顾,codex v1 Critical #1)。配套 4 测试(无音频失败/有音频成功/瞬时抖动恢复/AutoReconnect=false)。② **异常 #11(0 字节僵尸 + NotGrowing,P1)**:`fileSizes`/`failCount`/`zeroSizeStreak`/`abortReason` 四字段聚合到 `map[string]*healthStats`,`checkRecordingHealth` 拆 `checkOneChannelHealth` 分发 + `applyHealthStat` 锁内闭包(**消除循环内 `defer m.mu.Unlock()` 自锁**,codex v1 Critical #2);0 字节文件连续 2 次检测 → `ErrZeroByteStalled` + 取消;`failCount>=3`(文件曾增长后停滞)→ `ErrRecordingNotGrowing` + 取消。**HandleTask peekAbortReason 收尾逻辑(codex impl 复审 Important 修正)**:任一 abort + `hasRecordedAudio`(前序有有效分段)→ 覆盖 err=nil 走**成功**收尾保留(0 字节重连分段不应丢前序有效音频);无有效分段 → 走**失败**路径(0 字节→ErrZeroByteStalled、不增长→NotGrowing,不送 normalize 污染回顾)。`markAbort` 只 Cancel,`peekAbortReason` 只读不 `clearActive`(codex v1 Important #1)。配套 4+2 测试(含 impl 复审新增的"前序有效音频保留"确定性测试,用 `secondFileReady` channel 同步)。③ **P2(HTTP 412/403/429 风控冷却)**:新哨兵 `ErrHTTPRiskControl`,`getJSON` 经 `httpStatusError` 识别风控码(5xx 不冷却);`CheckLive` 对 HTTP 风控单次重试(同 -352 范式);`isRiskControlError` helper 统一识别;**全部 6 个 CheckLive 调用点 + GetStream 经 selectStream 入口**(首次 + 重连 3 处,抽 `handleSelectStreamRiskControl`)覆盖冷却或快速失败:checkOne/Check/Start/preflight(不乐观放行)/decideAfterRecord(独立副作用,不改 #10 决策,codex v3 P1)/首次 selectStream(codex v2 P1);preflight 风控移出乐观放行(网络抖动仍乐观)。**决策 F 改名**:`applyCooldown352`→`applyCooldownRiskControl` 等(语义泛化覆盖所有风控)。配套 7 测试 + 新 `newStatusSequenceServer` helper(支持 status code,替 `newSequenceServer`)。**codex 计划审核**:v1 NEEDS_CHANGES → v2 → v3 → **v4 APPROVED**。**codex 执行后复审**:v1 NEEDS_CHANGES(0字节 abort 丢前序音频 Important + 2 Minor)→ v2 NEEDS_CHANGES(测试时序不确定)→ **v3 APPROVED**(无新 blocking)。文档同步:本文件 changelog + `internal/live_record/CLAUDE.md` + 调查总结标注三项已修 + 计划状态 APPROVED。测试计数 live_record 72→89(+17)。

- 2026-07-06(日):**auto_recap 默认值反转 + -352 剩余端点加固**(合并 `plans/archive/plan-recap-default-and-risk-hardening.md`,codex 审核计划 v3 APPROVED + 执行后复审 2 轮)。① **auto_recap 默认 true→false**:`channel.go` Create/Bootstrap 两处 `resolveAutoRecap(input.AutoRecap, true)` → `false`;`channel_test.go` `TestAutoRecapRoundTrip`/`TestBootstrapAutoRecapDefault` 两处断言反转;`resolveAutoRecap` 注释同步。**迁移 v32 `DEFAULT 1` 保留不改**(codex 执行审核 P1 发现:原计划改 DEFAULT 0 会因 SQLite ADD COLUMN 用 DEFAULT 回填已有行,静默关闭旧库升级用户的已有主播,违背"已有主播不受影响";正确做法是只改应用层 fallback,迁移 DEFAULT 保持 1 保护升级路径)。**消费方 `main.go:250` `if !ch.AutoRecap` 不变**,新建主播默认不自动回顾,需手动打开开关;已有主播不受影响(Update 走 existing)。② **-352 P1 `biliutil/video.go`**:`VideoClient` 改**指针接收者** + 加 `buvids`/`signers`/`newSigner`/`signersMu` 字段(懒初始化在锁保护下),`Fetch` 注入 buvid(`InjectBuvids`)+ WBI 签名(`signerForCookie` 按原始 cookie 缓存,对齐 `live_record/bilibili.go:103-115` v3 修正)+ `setBiliHeaders` 补 `Origin`;`SetBuvidStore`/`SetSignerFactory` 测试注入桩;新增 `NewWBISignerWithHTTPClient`(codex 执行审核 P2 发现:默认 signer 用独立 client 绕过配置的 transport,nav 请求在测试/代理环境失效——让 signer 沿用 VideoClient.HTTPClient)。4 处生产调用点(`download.go:404/504`、`native.go:83/108/159`)配套——变量构造可寻址无需改调用点,`FetchVideoInfo` 包级函数 + 8 处测试复合字面量改 `(&VideoClient{...}).Fetch`。`NativeDownloader` 加 `ViewBuvids`/`ViewSignerFactory` 可选注入字段。③ **-352 P2 `handler/server.go`**:抽 `biliCreativeGet` 共享 client helper(替代 `searchBiliTopics`/`listBiliSeries` 各自内联 `&http.Client{}` 裸调),补 Referer/Origin(本地常量 `biliCreativeReferer`,因 `biliutil.biliReferer` 包私有不可见);`defaultPublishCookieHeader` helper 共用账号解析;业务码判断(-101/非 0)留在端点。本次 P2 不加 buvid/WBI(创作类端点带 cookie+头通常足够,P2 非核心路径,观察)。**测试配套**:`video_test.go` 加 `stubSigner`/`handleAntiRisk`(spi 空响应降级);`native_test.go` 加 `nativeHandleAntiRisk`(3 个 view 测试 mock 放行 spi/nav)。**验证**:`go test ./...` 全过(asr 偶发 SQLITE_BUSY flaky 与本改动无关,重跑过)。文档同步:channel/db/config/biliutil/handler/download 的 CLAUDE.md + 本文件 changelog。

- 2026-07-06(日):**录播稳定性 9 个异常修复**(录播稳定性专项,`3ae2435` + `f13c854`)。**异常 #1~#8**:① `worker.recoverRunning` 恢复重启后孤儿 pending 任务(只入队不递增 attempt,超限 MarkFailed + syncSessionState 同步 session 状态,解除 `discovered→ActiveLiveForChannel 误判 active→scheduler 死锁跳过`的死锁);② `CheckAndStartAll` 的 checkOne 对 CheckLive/Start 失败打 WARN 不再静默吞;③ `BilibiliClient` 注入共享 `BuvidStore` + WBI 签名 + Referer/Origin 加固 CheckLive/GetStream 的 -352(降级容错);④ **删除 `worker.live_record_num` 死配置项**(调度器从不读它,`worker.num` 为唯一并发旋钮,旧配置含此字段被 viper 静默忽略);⑤ 重连循环重构:按 err 类型分支(selectStream 失败走 maxReconnect / CDN 瞬时错误走独立 `cdnRetryBudget=5` + 指数退避),两类重试均不调 CheckLive 避免 `live:false` 抖动误判;⑥ 健康检测 activeRecord 加 `CurrentOutputPath`,切换分段时重置 fileSizes/failCount 基线;⑦ `globLatestAudio` 兜底 Adopt 重启接管时恢复 CurrentOutputPath;⑧ `updateCurrentOutputPath` 切分分段同步更新。**异常 #9**:**scheduler 批量 CheckLive 触发 -352 频率风控**——CheckLive 检测 -352 后 `RefreshKeys`(按 baseCookie 选 signer)+ `buvids.Invalidate(baseCookie)` 失效指纹缓存重试一次,仍 -352 返回新哨兵 `ErrRiskControl352`(`live_record/types.go`);checkOne/Check 用 `errors.Is` 识别哨兵触发频道级阶梯冷却(5/10/20m,`cooldown352Until`/`cooldownStep`),冷却期跳过该频道 CheckLive,成功后重置;CheckLive 前 0~800ms jitter 摊开并发突发;抽 `ensureStartAllowed`+`startWithInfo`,checkOne 透传已得 info 省掉二次 `getInfoByRoom`。新增 `biliutil.BuvidStore.Invalidate`(配套 4 测试)。**部署验证**(61min 实战,3 路录制):CheckLive 成功率 95%(3470/3645),B站 -352 实为周期性脉冲(8~9min/波),修复前雪崩式硬打致 100% 持续失败,修复后脉冲期冷却兜住、脉冲间即恢复,0 ERROR。文档同步:live_record/biliutil/worker/config 的 CLAUDE.md + 根 CLAUDE.md/AGENTS.md changelog。测试计数:live_record 36→72、biliutil 80→84、worker 38→41、config 19→31。
- 2026-07-05(六):**修复识别主播 -352 风控 + buvid 风控对抗下沉共享**。① **根因**:用户反馈"添加主播识别不了,显示网络错误"。systematic-debugging 定位为 `internal/channel/identify.go` 的 `getJSON` 发 `getInfoByRoom` 时 UA 用 `"Mozilla/5.0 Hikami-Go"`、无 Referer/Origin、无 buvid/WBI → B 站 -352 风控(前端"网络错误"是误导,实为后端 500)。② **关键洞察(探针实测)**:buvid 注入是 -352 对抗的**必要但不充分**条件——`getInfoByRoom` 还需 WBI 签名(buvid only 仍 -352,buvid + WBI → 200 code=0,这正是 `live_record/danmaku` 的 `getDanmuInfo` 用 `WBISigner` 的原因)。③ **修复**:新增 `internal/biliutil/buvid.go`(`BuvidStore` 按 cookie 24h 缓存 buvid3/buvid4,nil-safe + `InjectBuvids` **replace 语义**剔除旧同名 cookie);identify 注入 buvid + 用按 cookie 缓存的 `WBISigner` 做 WBI 签名 + 改浏览器 UA/Referer/Origin。④ **下沉重构(行为等价)**:`publisher` 和 `live_record/danmaku` 的本地 buvid 实现删除改用共享 `BuvidStore`,消除两份重复。⑤ **验证**:后端 27 包全过(biliutil 69→80、channel 59→61、publisher 66→67);strace 确认测试无对外网络;手动 `curl identify 924973` → 200 返回 UID 1401928(火西肆)。⑥ **codex 审核**:计划 v1 NEEDS_CHANGES 4 条 → v2 全落实 APPROVED;执行后再次 codex 审核实际代码。文档同步:biliutil/channel/publisher/live_record CLAUDE.md + 根 CLAUDE.md/AGENTS.md changelog。
- 2026-07-05(六):**配置备份导入持久化 + B站账号卡片区分登录态**(`6a2bb18` + `a449d7e`)。① **配置备份导入持久化到 `runtime_settings`**:此前导入只改内存 cfg + 进程 env、重启即丢;现改两阶段事务化。**导出**:bundle 的 6 个全局段(recap_ai/publish/webdav/asr_s3/dashscope/archive)全指针化 + `omitempty` 统一 presence 判断;WebDAV/ASR S3 用专用导出 DTO(`WebDAVExportSection`/`ASRS3ExportSection`)剔除明文密钥(Password/AccessKeySecret),密钥统一走 Secrets 段。**导入**:阶段一把 6 段 + secrets 绑进同一 `runtimeconfig.WithTx` 事务(overwrite 用新增 `secrets.ClearTx`),commit 成功后才提交内存 cfg + 进程 env;阶段二(仅 overwrite)清 glossary/templates/cookies。**持久化前校验** `validateImportedSections` 复用各 update handler 的段内约束,非法值 400 不落盘。修正 webdav/asr_s3 的 managed tombstone(先回填 env 再用 effective env 判定,覆盖 overwrite 下 env 改名场景)。新增 `config_export_test.go`(11 用例),handler 测试总数 55→66。② **B站账号卡片区分登录态**:`BiliAccountsCard.vue` 对 `cookie_file` 为空的账号(备份导入的元数据)显示灰色「未登录」标签 + 卡片 `opacity: 0.6` 置灰,避免误读为已登录。③ 文档同步:handler/secrets 的 CLAUDE.md、api-routes、根 CLAUDE.md(模块索引 handler 55→66 + 设计说明 + changelog)、web/CLAUDE.md、本文件 changelog。
- 2026-07-04(六):**运维 + 代码三处修复**。① **回归 systemd 部署**:停掉 7/3 起手动 `./hikami`(stdout 重定向 `logs/*.log`)的进程,`systemctl start hikami` 走 journald;service 配 `Restart=on-failure` 崩溃自愈。**重要:`systemctl restart` 不会重新编译,改代码后必须先 `make build-go` 再 restart**。日志查看从 `tail -f logs/*.log` 改为 `journalctl -u hikami -f`;`logs/` 目录停止写入(历史文件保留,`.gitignore` 已忽略)。② **DB 时间统一本地时区**:`sessions` 表用 `time.Now().Format(RFC3339)` 存本地时区,但 `tasks` 表 + `state.go` 的 `published_at`/`uploaded_at` 用 SQLite `datetime('now')` 存 **UTC**(无视系统时区),前端 `new Date()` 把无时区字符串当本地时间,显示早 8h。修复:`worker/task.go`+`state/state.go`+`session/session.go` 共 16 处 `datetime('now')` 改 `nowRFC3339()`(本地时区),`SetArchivedAt` 去掉 `.UTC()`;新数据生效,历史 UTC 数据不迁移。③ **自动发布跳过补日志**:`main.go` recap→publish 回调原 `if err != nil || !ch.AutoPublish { return }` 静默,拆为 get channel 失败打 WARN、`auto_publish` 关闭打 INFO,便于排查"自动发布为何没触发"(诊断 7/3 漏自动发布用此)。codex-review(pppzzz)APPROVED,报告 `reviews/main--r34.md`。
- 2026-07-03(四):**移除专栏删除/编辑 + 新增重新生成回顾**。① 砍掉 `removeOpus`/`editOpus`(删B站专栏)——B站内容只能手动去 B站管理。连带清理 4 处死代码:`state.ApplyRevertPublish`、`EventPublishReverted`、`transitions[StatusPublished]` 出口(published 改为终态)、`session.SetPublishTarget`。② 新增「重新生成回顾」:`POST /api/sessions/:sid/recap/regenerate` → `recap.CreateRegenTask`(覆盖本地 md 不碰 B站)。**任务实例级 bypass**:`worker.Task`/`CreateInput` 加 `BypassFailState bool`(DB v34 加列 `bypass_fail_state`),`syncSessionState` 改 OR 逻辑(实例级 || 类型级),失败仅写 `last_error` 不降级 published/recap_done。`main.go` onSuccess 回调对 published 早退。前端 `UIActionName` 8→6、`RecapDrawer` 加硬编码「重新生成」按钮。后端 26 包全过、前端 vitest 97。文档:api-routes(-2+1)、state/session/handler/publisher/archive/worker/db/recap 的 CLAUDE.md、web/CLAUDE.md、根 CLAUDE.md 同步。
- 2026-07-03(四):`/init-project` 增量更新。核对 `d45695f`(上次文档)→ `be509b6`(HEAD)区间,代码改动仅前端 3 文件(后端零改动),集中于一处未同步的 UI 重构:**设置页折叠分组**(`af9df47` + `be509b6`)。① `web/CLAUDE.md` 目录树补登遗漏的 `DashScopeSettingsCard.vue`/`ASRS3SettingsCard.vue`(实为 9 `.vue`,此前文档仅列 7);`views/SettingsView.vue` 章节由"5 分区平铺"重写为"4 折叠分组"(`el-collapse`:总览/流水线配置/账号与备份/高级),详述三处状态卡合并为单个总览卡、API 密钥空壳卡删除(密钥改由各子卡内联管理)、`scrollToSection` 跨分组先展开再滚动的 ~320ms 过渡等待、ASR 能力项 `CapActionType` section/hint 分流。② 根 `AGENTS.md` 前端结构小节 `features/settings/components/` 行补一句 9 卡 + 4 折叠分组要点。③ **修正测试计数口径**:`vitest run` 运行时实为 100(此前文档写 96),`sessionActions.test.ts` 运行时 51(静态 47,因 `describe.each(['download','import'])` 将 6 个回放类用例 ×2 展开);本轮在 `web/CLAUDE.md` 测试状态小节同时标注运行时/静态两数,消除歧义。26 个 internal 模块 + cmd + web 的 28 份 CLAUDE.md 面包屑齐全,本轮无新增模块、无后端改动。
- 2026-07-02(四):`/init-project` 增量更新(跟随 `83ef024` 发现回放两步式 + `e9cb624` 回放类不自动发布)。① `composables/` 计数 6→7(新增 `useDiscoverReplay`,发现回放抽屉可见性 + 执行后刷新);`features/recaps/sessionActions.ts` 补 `isReplaySource` 说明(回放类隐藏 publish/edit/remove)。② 同步更新 5 处模块文档:`internal/discover/CLAUDE.md`(新增 `PreviewAll`/`Execute`/`ExecuteItem`/`Result.Exists`/`annotateExists` + 2 端点,测试 5→10)、`internal/handler/CLAUDE.md`(+2 路由)、`cmd/hikami/CLAUDE.md`(recap→publish 回调按 source_type 拦截回放类)、`web/CLAUDE.md`(录播/回放子 tab + 两步式抽屉,Vitest 90→96,静态口径;`describe.each` 运行时展开实为 94→100,见 2026-07-03 条)、`CLAUDE-detail/api-routes.md`(+2 路由)。③ 根 `CLAUDE.md` 精简模块索引同步 discover(测试 5→10)/web(测试 90→96,静态口径)两行 + 新增本轮 changelog。28 个模块级 CLAUDE.md 面包屑齐全,本轮无新增模块。
- 2026-07-01(二):`/init-project` 增量更新。**新增 Mermaid 模块依赖概览图**(本节"模块依赖概览",运行时自包含、无需跳转 CLAUDE.md);后端"支撑"组补 `runtimeconfig`(全局运行时配置覆盖持久化,与 secrets 共享 `*sql.Tx`)。配合新增的 `internal/runtimeconfig/CLAUDE.md`(26 个 internal 模块 + `cmd/hikami` + `web` 文档齐),并修正 `internal/db/CLAUDE.md` 漂移(schema v32→v33、补 v31/v32/v33 迁移、业务表数 9→10,另含 `schema_migrations` 账本)。注:DB schema 现为 v33(`runtime_settings` 表),`TestMigrateCreatesAllTables` 的 `expected` 清单**已**纳入该表(见 `internal/db/migrate_test.go:69`)。
- 2026-07-01(二):`/init-project` 复核修正。核对代码树与文档漂移:① 前端 `composables/` 实为 6 个(补 `useRecapModels`),`stores/` 实为 5 个(补 `runtime`);② 修正 `docs/DOCUMENTATION_INDEX.md` —— 删除指向**已不存在**的 `OPUS_DRAFT_EMPTY_CONTENT_INVESTIGATION.md` / `WEB_OPTIMIZATION_SUGGESTIONS.md` 两行,补登 `docs/archive/investigations/前端兜底页-embedded_web构建标签缺失.md`,更新索引日期;③ 清理本文件结构性重复(全文正文被意外粘贴两遍,合并去重)。模块级 27 份 `CLAUDE.md`(26 包 + cmd + web)均含导航面包屑,本轮无新增,验证一致。
- 2026-06-29(二):用 Obscura 抓取 ZCode 官方文档(`/en/docs/skill`、`mcp-services`、`agents`、`commands`、`plugin`)核对,据官方表述修正两处:**① Skill 触发符为 `$`(用户 chat 输入 `$skill-name`),不是 `/`(`/` 是 Command 的触发符);② AGENTS.md 只读全局 + 工作区两级,不逐级合并子目录**。补充 Obscura(全局 MCP server + Skill)的集成说明。
- 2026-06-29(二):ZCode 运行时适配修正。逆向 `zcode.cjs` 确认 ZCode 对每个目录根**同时**扫描 `.zcode/skills` 与 `.agents/skills` 两个源并合并,因此本仓库 `.agents/skills/`(43 个 Go Skill,本地 vendored 且 `.gitignore`)与全局 `~/.zcode/skills/`(46 个)均生效;新增"ZCode Skills 与扩展能力"小节说明调用方式与未启用的扩展;修正上次把 `.agents/skills` 当"死引用删除"的错误结论。联动修正 `.gitignore`(移除误入的 `引用格式`、移除与"已提交 AGENTS.md"矛盾的 `AGENTS.md` 忽略规则)。
- 2026-06-28(一):依据 ZCode 文档规范重写。修正错误的 Go 环境指令(移除 `/root` 前缀要求);补充启动/端口/调试命令与运行时依赖表;明确 AGENTS.md(ZCode 运行时)与 CLAUDE.md(详尽人类参考)的分工。
