[根目录](../../CLAUDE.md) > **cmd/hikami**

# cmd/hikami -- CLI 入口

## 模块职责

服务启动入口。负责解析命令行参数、加载配置、初始化 Cookie 文件加密密钥、初始化日志、探测外部依赖、打开数据库、执行迁移、加载 API Key 到环境变量、导入旧版术语表、创建回顾模板 Store、构建并启动任务池、注册所有任务处理器、启动定时调度器、启动 HTTP 服务器，以及优雅关闭。

## 入口与启动

- **入口文件**: `main.go`, `embed.go`
- **构建目标**: `go build ./cmd/hikami` (Makefile: `make build`)
- **运行**: `./hikami -config config.yaml`
- **命令行参数**: `-config` (配置文件路径，默认 `config.yaml`)

## 启动流程

1. 解析 `-config` 参数
2. `config.Load()` 加载并校验配置
3. `biliutil.SetCookieEncryptionKey(cfg.CookieEncryptionKey)` 初始化 Cookie 文件 AES-256-GCM 加密密钥；密钥无效则启动失败，启用时记录 info 日志
4. `slog.SetDefault()` 设置结构化 JSON 日志
5. `cfg.EnsureDirs()` 创建输出目录、日志目录、数据库目录
6. `db.Open()` + `db.Migrate()` 初始化 SQLite（当前 v35，含 recap_templates、bili_cookie_accounts、账号关联列、per-channel recap 配置、sessions.archived_at、channels.auto_recap、runtime_settings 7 段 CHECK、bypass_fail_state、tools CHECK 扩展）
7. **`secrets.NewStore()` + `secretsStore.LoadIntoEnv()`** 将数据库中的 API Key 加载到环境变量
8. **`glossary.NewStore()` + 旧版术语表导入**：如果 `glossary_file` 配置存在且数据库中无全局词条，自动导入文件内容
9. `runtime.Probe()` 探测外部工具（含平台感知安装提示），硬依赖缺失则 `os.Exit(1)`
10. `channel.Bootstrap()` 首次导入 YAML 中的主播配置（含 per-channel 发布字段、source_mode、discover_limit、auto_recap 三态）
11. 构建各模块 Store/Handler 实例，通过依赖注入连接
12. **`recap.NewTemplateStore(db)`** -- 创建回顾模板 Store
13. **`recap.NewHandler(cfg, sessions, states, provider, glossaryStore, templateStore)`** -- recap Handler 注入 glossaryStore 和 templateStore
14. **自动触发链注册（SetOnSuccess 回调，设计 4.1/4.3）**：
    - `normalizeHandler.SetOnSuccess` → 主播 `auto_asr` 为真且 ASR 能力可用 → 自动提交 ASR 任务
    - `asrHandler.SetOnSuccess` → 自动提交 recap 任务（回顾能力 gate 已下沉到 `recap.CreateTask`）
    - `recapHandler.SetOnSuccess` → 主播 `auto_publish` 为真且发布能力可用 → 自动提交 publish 任务
    - `publisherHandler.SetOnSuccess` → `archive.auto_after_publish` 为真 → 自动提交 archive 归档任务（状态旁路）
15. 各模块 `Register(workerPool)` 注册任务处理器（download, import, asr, recap, upload, **archive** `WithBypassFailState()`, publisher `WithBypassFailState()`, live_record）；旁路任务通过注册选项声明，不再依赖硬编码类型特判
16. `workerPool.SetFailSessionStateFn()` 注册任务失败时同步 session 状态的回调（旁路任务仅写 `last_error` 不降级状态）
17. `workerPool.Start()` 启动任务池，自动恢复上次中断任务
18. `scheduler.Start()` 启动 cron 定时调度器（回放发现、直播检查）
19. `handler.NewServer()` 构建 Gin 路由并启动 HTTP 服务（含 secretsStore + glossaryStore + recapTemplates + archives 注入）
20. **关闭协调与主线程阻塞**：`newShutdownCoordinator(httpServer)` 创建幂等关闭协调器（`sync.Once` 保证 HTTP server 只关一次），`runTray(sc, serverURL, logger)` 阻塞主线程:
    - **Windows + systray build tag**:运行托盘消息循环(Win32 消息循环必须在主线程),托盘菜单「打开管理界面/退出」+ 信号 goroutine 兜底;日志写文件(`%LOCALAPPDATA%/Hikami-Go/hikami.log`,失败回退 exe 同目录,见 `initLogFile`)
    - **其他平台/无 systray tag**:阻塞在 `signal.Notify`(SIGINT/SIGTERM),收到信号调 `requestShutdown`;日志写 stdout
    - 任一退出路径都走 `requestShutdown`,**不调 `os.Exit`**,让 main 的 defer 链(sched.Stop → workerPool.Stop → database.Close → logCleanup)按 LIFO 自然执行

## 自动触发链

`cmd/hikami` 通过各模块的 `SetOnSuccess(func(ctx, task))` 回调串联起完整的「来源 → 标准化 → ASR → 回顾 → 发布 → 归档」自动链，避免分散在各 handler 内的硬编码触发：

```text
normalize.SetOnSuccess --(auto_asr)--> asr.CreateTask
asr.SetOnSuccess -------------------> recap.CreateTask     (能力 gate 已下沉到 recap.CreateTask)
recap.SetOnSuccess --(auto_publish)-> publisher.CreateTask
publisher.SetOnSuccess -(auto_after_publish)-> archive.CreateTask  (状态旁路任务)
```

每段回调内：获取主播配置 → 检查对应能力（`runtimeStatus.Capabilities.*`）→ 调用下一阶段 `CreateTask`；失败仅记录警告，不阻断主流程，已成功阶段不会被回滚。归档段从 `published` 出发，是「状态旁路任务」——不调用 `states.Apply`、不发 Event，成功仅写 `archived_at`。

**回放类不自动发布（2026-07-02，`e9cb624`）**：`recap.SetOnSuccess` 回调在提交 publish 任务前，先查 `sessionStore.Get(task.SessionID)`；若 `session.SourceType == "download"` 或 `"import"`（即回放类来源），**直接 return 跳过自动发布**——只有录播（`live_record`）才会随 `auto_publish` 自动发B站。手动 API `POST /api/sessions/:sid/publish` 不受此限制（由前端按 source_type 隐藏动作覆盖）。覆盖 recap 重跑场景（重跑仍按 source_type 判断）。

## 旧版术语表迁移

启动时检查 `recap_ai.glossary_file` 配置：
1. 如果配置路径存在且文件内容非空
2. 且数据库中全局术语条目数为 0
3. 自动调用 `glossaryStore.ImportMarkdown` 导入文件内容
4. 导入失败仅记录警告，不阻止启动

## 对外接口

本模块无对外接口，是纯启动入口。

## 关键依赖

- 所有 `internal/*` 子模块均在此组装
- 外部依赖: Gin、gorilla/websocket、Viper、modernc.org/sqlite、robfig/cron/v3
- `embed.go`: 使用 `//go:embed all:webdist` 嵌入前端构建产物

## 常见问题 (FAQ)

**Q: 如何添加新模块？**
A: 在 `main.go` 中导入新模块包，创建实例并调用 `Register(workerPool)` 注册任务处理器，按需传入 `handler.NewServer()`。

**Q: 启动失败 "required tool ffmpeg is unavailable"？**
A: 确保 `ffmpeg` 和 `ffprobe` 在 PATH 中可执行。这是硬依赖。ToolStatus.InstallHint 提供了平台感知的安装提示。

**Q: 前端如何嵌入？**
A: `make web-build` 将 `web/dist` 复制到 `cmd/hikami/webdist/`，`embed.go`（`//go:build embedded_web`）通过 Go embed 机制嵌入。`make build-go` / `make build` 自动加 `-tags embedded_web`，且构建后用 `strings ./hikami | grep 'webdist/'` 静态自检前端是否真嵌入（漏 tag 会 fail）。`make build-go-api` 不带 tag，编译出纯 API 二进制（无前端，启动时 main.go 打 WARN 并降级到 API-only fallback 页）。**CI release**（`.github/workflows/release.yml`）的 TAGS 始终含 `embedded_web`（embed_ffmpeg 仅 Windows 完整版叠加）——此前漏 tag 曾导致 4 个发布矩阵前端被静默丢弃（`1781937` 修复）。

**Q: 旧版 glossary_file 配置如何处理？**
A: 已标记为 deprecated。启动时自动导入到数据库，后续通过 Web API 管理。

**Q: cookie_encryption_key 配置错误会怎样？**
A: 启动阶段调用 `biliutil.SetCookieEncryptionKey` 校验密钥。非空密钥必须是 64 位 hex（32 字节）；格式错误会记录错误并终止启动，避免后续 Cookie 文件写入不可预期格式。

**Q: 自动发布如何触发？**
A: 回顾任务成功后，`recapHandler.SetOnSuccess` 回调检查主播的 `auto_publish` 配置和发布能力可用性，自动提交发布任务；**但回放类（download/import）来源的回顾不自动发布**（仅录播 live_record 会），由回调内查 session.SourceType 判断。发布成功后再由 `publisherHandler.SetOnSuccess` 按 `archive.auto_after_publish` 决定是否自动归档。详见「自动触发链」章节。

**Q: 回顾模板如何初始化？**
A: DB 迁移 v21-v23 自动创建 `recap_templates` 表、唯一索引并插入内置默认模板（system_prompt='__builtin__', is_default=1）。无需手动初始化。

**Q: Windows 系统托盘 / 无控制台模式如何构建？**
A: 加 `-tags systray` 编译托盘代码（`tray_windows.go`，依赖 `fyne.io/systray`），`-ldflags='-H windowsgui'` 隐藏控制台窗口。Makefile 提供 4 个 Windows target：`build-windows-amd64`（完整 ffmpeg，控制台版，`-tags embed_ffmpeg,embedded_web`）、`build-windows-amd64-lite`（无 ffmpeg，控制台版）、`build-windows-desktop`（完整 ffmpeg + 托盘 + 无控制台，`-tags 'embed_ffmpeg,embedded_web,systray'` + `-H windowsgui`）、`build-windows-desktop-lite`（无 ffmpeg + 托盘 + 无控制台）。CI release.yml 的 windows 矩阵新增 `desktop: true` 变体，产物名带 `-desktop` 后缀。托盘模式下 stdout 可能不可写，`initLogFile` 自动改写 `%LOCALAPPDATA%/Hikami-Go/hikami.log`（失败回退 exe 同目录便携模式）。Linux/macOS 走 `tray_other.go` 占位，`initLogFile` 返回 stdout 不写文件。

## 相关文件清单

- `main.go` -- 启动流程、Cookie 加密密钥初始化、依赖组装、`newShutdownCoordinator` 创建 + `runTray` 主线程阻塞、自动触发链（normalize→asr→recap→publish→archive 的 SetOnSuccess 回调；recap→publish 回调按 session.SourceType 拦截回放类自动发布）、archive Handler 创建与 WithBypassFailState 注册、旧版术语表导入、回顾模板 Store 创建
- `embed.go` -- 前端静态文件嵌入（`//go:build embedded_web`，`//go:embed all:webdist`）；`embed_none.go` 为无 tag 时的空占位（API-only 构建）
- `tray_windows.go` -- **Windows 系统托盘实现**（`//go:build windows && systray`）：基于 `fyne.io/systray`，托盘菜单「打开管理界面/退出」+ 信号 goroutine 兜底；`shutdownCoordinator`(sync.Once 幂等关闭，关 HTTP 后调 `systray.Quit()` 让 `systray.Run()` 返回、main 走 defer 链)；`initLogFile` 桌面模式日志写 `%LOCALAPPDATA%/Hikami-Go/hikami.log`(失败回退 exe 同目录便携模式)
- `tray_other.go` -- **非 Windows / 无 systray tag 的等价占位**（`//go:build !windows || !systray`）：`shutdownCoordinator` 结构/逻辑与 `tray_windows.go` 一致但不调 `systray.Quit`(无托盘)；`runTray` 阻塞在 `signal.Notify`；`initLogFile` 返回 stdout(不写文件)。两文件通过 build tag 互斥，保证 main.go 的 `runTray`/`newShutdownCoordinator`/`initLogFile` 符号在所有平台可编译
- `trayicon.go` + `trayicon.ico` -- 托盘图标 ICO 字节(`//go:embed trayicon.ico` → `iconBytes`，仅 windows&&systray 编译)

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-14 | 功能 | **Windows 系统托盘 + 隐藏控制台 + 文件日志**（`ad34a15`）：① 新增 `tray_windows.go`（`//go:build windows && systray`），基于 `fyne.io/systray` 实现托盘图标 + 菜单「打开管理界面/退出」，`openBrowser` 打开 `serverURL`；新增 `tray_other.go`（`//go:build !windows \|\| !systray`）作等价占位，两文件通过 build tag 互斥保证 `runTray`/`shutdownCoordinator`/`initLogFile` 在所有平台可编译；新增 `trayicon.go` + `trayicon.ico`（`//go:embed` 托盘图标字节）。② **关闭流程重构为 `shutdownCoordinator`**（`sync.Once` 幂等）：原 main.go 内联的 SIGINT/SIGTERM 监听 + HTTP Shutdown 抽到协调器，托盘菜单「退出」/信号都走 `requestShutdown`，关 HTTP 后调 `systray.Quit()` 让 `systray.Run()` 返回、main 继续 defer 链（sched.Stop → workerPool.Stop → database.Close → logCleanup LIFO）；**不调 `os.Exit`** 保证 defer 执行。③ **桌面模式文件日志**：`initLogFile` 在 Windows+systray 下优先写 `%LOCALAPPDATA%/Hikami-Go/hikami.log`（用户可写目录），失败回退 exe 同目录便携模式；其他平台返回 stdout。main.go 启动时调 `initLogFile()` 拿 `logWriter` + `logCleanup`。④ Makefile 新增 `build-windows-desktop`/`build-windows-desktop-lite` target（`-tags 'embed_ffmpeg,embedded_web,systray'` + `-ldflags='-H windowsgui -s -w'`），`CGO_ENABLED=0` 纯 Go 编译。⑤ CI release.yml 的 windows 矩阵新增 `desktop: true` 变体（产物名带 `-desktop` 后缀，加 `systray` tag + `-H windowsgui`）。依赖：`go.mod` 新增 `fyne.io/systray v1.12.2`（transitive：`godbus/dbus/v5` 等）。 |
| 2026-07-02 | 功能 | **回放类回顾不自动发布B站**（`e9cb624`）：`recapHandler.SetOnSuccess` 回调在提交 publish 任务前，先查 `sessionStore.Get(task.SessionID)`，若 `session.SourceType` 为 `download`/`import`（回放类）则 `return` 跳过自动发布——只有录播（live_record）随 `auto_publish` 自动发B站。手动 `POST /api/sessions/:sid/publish` 不受此限制（由前端按 source_type 隐藏动作）。覆盖 recap 重跑场景。配合前端 RecapsView 拆「录播/回放」子 tab |
| 2026-06-27 | 修复 | **release 二进制漏带 embedded_web 导致前端缺失**（`1781937`）：CI release 工作流的 TAGS 此前仅 Windows 完整版带 `embed_ffmpeg`，其余矩阵 `TAGS=""` 致 `//go:build embedded_web` 的 `embed.go` 被排除、前端静默丢失。修复：TAGS 始终含 `embedded_web`（embed_ffmpeg 仅叠加）；main.go 的 webFS 降级分支补醒目 WARN（"binary built without -tags embedded_web"），漏 tag 在启动日志立即暴露；Makefile `build-go` 追加 `strings ./hikami \| grep 'webdist/'` 静态自检，构建后立即可验证前端是否嵌入。同步修正本文件 DB 迁移版本引用 31→32 |
| 2026-06-23 | 功能 | 自动触发链加固（设计 4.1/4.3）：串联完整 `normalize→asr→recap→publish→archive` 的 `SetOnSuccess` 回调；新增 `archiveHandler` 创建、注入 `handler.NewServer` 并 `Register(workerPool, worker.WithBypassFailState())`；publisher 回顾能力 gate 下沉到 `recap.CreateTask`；旁路任务失败经 `SetFailSessionStateFn(..., bypassState)` 仅写 `last_error` 不降级状态（替代 task.Type 硬编码特判）；DB 迁移版本 27→31 |
| 2026-05-23 | 安全更新 | 启动流程新增 `biliutil.SetCookieEncryptionKey(cfg.CookieEncryptionKey)`，在目录创建前初始化 Cookie 文件 AES-256-GCM 加密密钥 |
| 2026-05-17 | 增量更新 | DB 迁移版本引用从 21 修正为 27（含 recap_templates、bili_cookie_accounts、账号关联列） |
| 2026-05-14 | 更新 | 新增 recapTemplateStore 创建和注入（NewTemplateStore + NewHandler 增加 templateStore 参数）；DB 迁移增至 v21 |
| 2026-05-08 | 更新 | 新增 recapHandler.SetOnSuccess 自动发布回调 |
| 2026-05-07 | 更新 | 新增 glossaryStore 初始化和旧版术语表自动导入 |
| 2026-05-04 | 更新 | 新增 secretsStore 初始化和 LoadIntoEnv 调用 |
| 2026-05-03 | 更新 | 新增自动 ASR 回调、SetFailSessionStateFn 注册 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
