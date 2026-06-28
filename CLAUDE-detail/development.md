# 运行开发与工程规范

> 本文件由根 CLAUDE.md 拆分而来，作为 AI 上下文补充文档。

## 运行与开发

### 构建

```bash
make build        # 前端构建 + Go 编译，生成 ./hikami 二进制
make build-go     # 仅 Go 编译
make web-build    # 仅前端构建
make web-dev      # 前端开发模式 (Vite dev server)
make run          # go run ./cmd/hikami -config config.yaml
make test         # go test ./...
make fmt          # gofmt -w cmd internal
make tidy         # go mod tidy
```

### 跨平台构建

```bash
make build-linux-amd64    # Linux x86_64
make build-linux-arm64    # Linux ARM64
make build-darwin-arm64   # macOS ARM64
```

### 配置

1. 复制 `config.example.yaml` 为 `config.yaml`。
2. 必填项：`output_root`、`db_path`。
3. 硬依赖：`ffmpeg`、`ffprobe` 必须在 PATH 中可执行。
4. 按需依赖：`yt-dlp`（回放发现/下载）、`rclone`（ASR 临时公开/WebDAV 上传）。
5. AI 能力：配置 `dashscope` 和 `asr_temp` 启用 ASR；配置 `recap_ai` 启用回顾生成。
6. 发布能力：配置 `publish` 和主播 `cookie_file` 启用 B 站专栏发布。支持 per-channel 发布配置（分区/文集/可见性/原创/Aigc/定时发布/封面/话题）。
7. Cookie 分离与账号池：`cookie_file` 用于发布，`download_cookie_file` 用于下载/识别/录制；Cookie Account 可设置默认下载/发布账号，主播级账号优先于全局默认，再回退旧 cookie 文件。支持 QR 码登录获取 Cookie。Cookie Account Cookie 文件路径受 `ValidateCookiePath` 防穿越校验。
8. Cookie 静态加密：可配置顶层 `cookie_encryption_key`（64 位 hex，32 字节）启用 AES-256-GCM；为空时禁用。已加密文件格式为 `HIKAMI_V1` magic + nonce + ciphertext/tag，旧明文文件保持兼容读取。
9. 自动化：主播 `auto_record` 控制自动录制，`auto_asr` 控制自动 ASR，`auto_publish` 控制自动发布。
10. 来源模式：主播 `source_mode` 控制回放发现和直播录制行为（both/live_only/replay_only/live_first/replay_first）。`discover_limit` 限制每次发现新场次数。
11. 环境变量：`DASHSCOPE_API_KEY`、`AI_API_KEY` 可从环境读取，也可通过 Web 设置页面存储到数据库。
12. 术语表：通过 Web 设置页面或 `/api/glossary` 端点管理，支持全局和主播级别。`recap_ai.glossary_file` 为旧配置（deprecated），启动时自动导入到数据库。AI 术语发现自动在回顾生成后执行，候选通过 `/api/glossary/candidates` 端点审核。
13. 回顾模板：通过 Web 设置中心第 5 分区"回顾模板"或 `/api/recap/templates` 端点管理全局模板。主播详情页第 6 标签管理主播级模板。支持 system prompt、输出格式、粉丝称呼、自定义变量（JSON）配置。内置 5 个模板预设。
14. 日志格式：`log_format` 或 `logs.format` 支持 `json`/`text`，默认 `json`；`log_format` 为空时回退 `logs.format`。
15. 通知事件：`notify.events` 默认包含 `task_failed`、`record_start`、`record_stop`、`recap_done`、`publish_done`。
16. 新手引导：首次启动时检查工具/Key/主播配置，自动显示引导向导；可跳过。
17. 转写摘要：`recap_ai.enable_summarization` 启用长直播转写文本压缩（超过 30000 字自动触发）。
18. 回顾续写：`recap_ai.max_continuations` 控制最大续写次数（默认 0），AI 输出被截断时自动续写。可被主播级 `max_continuations` 覆盖。
19. Per-channel 回顾模型：`channels.recap_model` 非空时覆盖全局 `recap_ai.model`。
20. 回顾配置端点：`GET/PUT /api/config/recap` 在线更新 base_url/model/max_tokens/max_continuations/timeout_seconds。

### 默认监听

`config.go` 中 `web.listen` 默认 `127.0.0.1:6334`（loopback；绑外网时 `Validate` 强制要求 `web.admin_token`）。`config.example.yaml` 示例值已同步为 `127.0.0.1:6334`。

## 编码规范

- 单一 Go module (`hikami-go`)，所有业务代码在 `internal/` 下。
- 配置以 SQLite 为主来源，YAML 只负责全局配置和首次引导。
- 主播隔离：所有路径、任务、状态、锁必须携带 `channel_id`。
- 原始层不可覆盖：`raw/` 只保存原始输入，后续产物写入 `asr/`、`package/`、`recap/`。
- 原子写入：标准产物先写临时文件，校验成功后 rename 替换。
- 外部工具封装为接口（`Downloader`、`AudioRecorder`、`Transcriber`、`Copier`、`OpusClient`、`OpusCoverUploader`、`URLSigner` 等），便于测试。
- 状态转换只由 `internal/state` 模块执行，业务模块不得直接散写 session 状态。
- 错误定义在各模块中，handler 层通过 `errors.Is` 映射到 HTTP 状态码。
- 友好错误映射：`worker/errors.go` 的 `GetFriendlyError` 将原始错误信息映射为中文友好消息和操作建议；handler 层在任务详情 API 中返回 `friendly_error` 字段。
- B 站 Cookie 相关工具统一在 `internal/biliutil` 中，其他模块通过重导出（publisher）或直接引用使用。
- B 站 WBI URL 签名在 `internal/biliutil/wbi.go` 中，通过 `URLSigner` 接口抽象。
- B 站统一 User-Agent 常量在 `internal/biliutil/ua.go` 中定义（`BiliUserAgent`）。
- Cookie 分离：发布用 `cookie_file`，下载/识别/录制用 `download_cookie_file`，两者在 Channel 和 BootstrapChannel 中独立配置。支持 QR 码登录获取 Cookie。
- Cookie Account：`CookieAccountStore` 管理全局和主播级 Cookie，统一通过 `ResolveCookie` 按 channel override、global default、legacy fallback 优先级查找；新增账号必须写入 `bili_cookie_accounts`，不得绕过 Store 直接拼 SQL。Cookie 文件路径通过 `ValidateCookiePath` 校验防止路径穿越。
- Cookie Writer：扫码登录保存 Cookie 时使用 `WriteNetscapeCookieFile` 原子写入 `.tmp` 后 rename，文件权限默认 `0600`，目录权限默认 `0700`，启用 `cookie_encryption_key` 时写入 AES-256-GCM 密文，不得提交 Cookie 文件。
- Cookie 加密：`internal/biliutil/cookie_crypto.go` 管理进程级 AES-256-GCM 密钥，`SetCookieEncryptionKey` 只接受空字符串或 64 位 hex；`LoadCookie` 和 `CheckCookieExpiry` 必须经 `decryptCookieFile` 读取，`WriteNetscapeCookieFile` 必须经 `encryptCookieFile` 写入。
- Cookie 过期检查：`biliutil.CheckCookieExpiry` 解析 SESSDATA 过期时间；`runtime.CheckCookieExpiry` 批量检查所有主播 Cookie 状态。
- AI Provider 返回类型：`internal/aiprovider.GenerateResult` 包含 `Content`、`Raw`、`FinishReason`，被 recap 和 glossary 模块共用。Provider 接口返回 `GenerateResult` 而非 `(string, string, error)` 元组。
- 通知事件：`internal/notify` 定义事件常量（`EventRecordStart`/`EventRecordStop`、`EventTaskFailed`、`EventRecapDone`、`EventPublishDone`），业务模块通过 `SetNotifyManager` 注入 `notify.Manager`；发送异步且不得阻塞主流程。
- 日志格式：`config.LogFormat` 支持 `json`（默认）和 `text` 两种格式；启动时根据 `log_format`/`logs.format` 选择 `slog.NewJSONHandler` 或 `slog.NewTextHandler`，新增日志字段保持结构化 key-value。
- 弹幕时间校正：ASR 完成后基于 segments 校正弹幕时间戳并生成 `package/danmaku.json`；recap 优先使用校正后的弹幕，新增弹幕处理逻辑不得覆盖原始时间信息。
- ASR 重试：DashScope HTTP 请求支持指数退避重试，429/5xx/网络错误最多重试 3 次，业务语义错误不重试。
- B 站专栏格式：代码块和表格转为文本保留，不再跳过；新增 Markdown 转 Opus 规则需优先保持内容可读性。
- API Key 管理：数据库存储优先，环境变量作为备选，启动时 `secrets.LoadIntoEnv` 加载。`_onboarding_dismissed` 等 meta 键使用下划线前缀。
- 术语表管理：数据库存储（`glossary_entries` + `glossary_meta` + `glossary_candidates` 表），全局+主播级别合并，`glossary_file` 配置已 deprecated。AI 术语发现（`glossary/discovery.go`）在回顾完成后自动执行，候选通过审核队列管理。
- 回顾模板管理：数据库存储（`recap_templates` 表），全局+主播级别合并，`__builtin__` 标记表示使用内置默认值。Provider 接口签名包含 `systemPrompt` 参数。内置 5 个模板预设定义在 `recap/presets.go` 中。
- 回顾续写：AI 输出 `finish_reason` 为 `length`/`max_tokens` 时自动续写，最大续写次数由 `recap_ai.max_continuations` 和 `channels.max_continuations` 控制。
- Per-channel 回顾配置：`channels.recap_model` 覆盖全局模型，`channels.max_continuations` 覆盖全局续写次数。通过 `recapOptions` 在 HandleTask 中读取。
- 发布配置：全局 `publish` + per-channel 覆盖，`resolvePublishConfig` 合并逻辑；字段级 fallback（-1/0 表示跟随全局）。
- 来源模式：`source_mode` 控制 discover 和 live_record 行为，`discover_limit` 限制每次发现新场次数。
- 磁盘检查：`runtime.CheckDiskUsage` 提供跨平台磁盘使用信息，`disk_unix.go` 使用 syscall.Statfs，`disk_windows.go` 使用 GetDiskFreeSpaceEx。
- WebSocket 安全：handler/server.go 的 `checkWebSocketOrigin` 校验 Origin 头，仅允许同源或 localhost 连接，不再使用 `CheckOrigin: true`。
- 流 URL 脱敏：`live_record/manager.go` 的 `redactURL` 在写入元数据时去除 URL 的 query/fragment/user 部分。
- 敏感文件权限：`live_record/manager.go` 写入 `live.raw.json` 时使用 `0600` 权限。
- 任务池取消：`worker.Pool` 维护 `running map[string]context.CancelFunc`，Cancel 时调用 cancel 函数终止任务 goroutine。`ShouldAutoRetry` 已导出供 handler 层共用。
- Scheduler 生命周期：`Scheduler` 持有 `ctx/cancel`，`Stop` 调用 `cancel()` 取消所有 cron job 的上下文。
- 健康检查生命周期：`live_record.Manager.StartHealthCheck` 支持重复调用（先 StopHealthCheck），`StopHealthCheck` 通过 context cancel 停止后台 goroutine。
- 前端组件按功能域组织：`components/channel/`、`components/session/`、`components/task/`、`components/layout/`、`components/onboarding/`。
- 前端工具函数：`utils/lifecycle.ts` 集中管理生命周期步骤映射和动作元数据，被多个视图和组件复用。`utils/friendlyStatus.ts` 提供友好状态标签和进度映射。
- 前端组合函数：`composables/useChannelHealth.ts` 提供主播自动化风险检测，`composables/useExpertMode.ts` 提供专家模式切换（localStorage 持久化），`composables/useWebSocket.ts` 提供 WebSocket 进度推送。

## AI 使用指引

- 本项目是 Go 单体服务，修改时注意 `internal/` 下各模块的职责边界。
- 状态机转换表在 `internal/state/state.go` 的 `transitions` 变量中定义。
- 任务类型常量在各模块中定义（如 `download.TaskType`、`normalize.TaskType`）。
- 数据库迁移在 `internal/db/migrate.go` 中，新增表需追加到 `migrations` 切片。当前 32 个版本。
- 外部工具交互全部通过接口抽象，mock 时实现对应接口即可。
- API 路由注册在 `internal/handler/server.go` 的 `routes()` 方法中。
- 配置结构体在 `internal/config/config.go` 中，新增配置项需同时更新 `setDefaults`。
- B 站 Cookie 解析在 `internal/biliutil/cookie.go` 中，publisher 通过 `cookie.go` 重导出保持接口兼容。
- B 站 Cookie 静态加密在 `internal/biliutil/cookie_crypto.go` 中，配置入口为顶层 `cookie_encryption_key`；启动时 `cmd/hikami/main.go` 调用 `biliutil.SetCookieEncryptionKey`，密钥为空禁用，64 位 hex 启用 AES-256-GCM。
- B 站 Cookie 过期检查在 `internal/biliutil/cookie.go` 的 `CheckCookieExpiry` 函数中，解析 SESSDATA 过期时间。
- B 站 WBI 签名在 `internal/biliutil/wbi.go` 中，`WBISigner` 实现了 `URLSigner` 接口，密钥从 nav API 获取并缓存 1 小时。
- B 站统一 User-Agent 在 `internal/biliutil/ua.go` 中定义为常量 `BiliUserAgent`。
- B 站发布相关接口和类型定义在 `internal/publisher/bilibili_opus.go` 中。
- Markdown 到 B 站 Opus 格式转换在 `internal/publisher/md2opus.go` 中，代码块和表格需转为文本保留。
- `internal/aiprovider/result.go` 定义 `GenerateResult`（Content/Raw/FinishReason），被 recap 和 glossary 的 Provider 接口共用。Provider 接口返回 `aiprovider.GenerateResult` 而非 `(string, string, error)` 元组。
- `internal/recap` 已从单文件 `recap.go` 拆分为模块化架构：`handler.go`（公共 API + 续写 + per-channel 配置 + 术语发现）、`prompt.go`（PromptSection 管道）、`filter.go`（时间范围过滤）、`provider_openai.go`（OpenAI Provider）、`provider_util.go`（Provider 接口+工具函数）、`danmaku_analysis.go`（弹幕分析子函数）、`segmentation.go`（话题驱动分段）、`transcript_summarizer.go`（转写摘要器）、`glossary_correction.go`（术语兜底）、`transcript_correction.go`（回顾前术语校正转写）。`recap.go` 已删除。
- Provider 接口签名：`Generate(ctx, systemPrompt, prompt, sessionInfo) (aiprovider.GenerateResult, error)`，返回 `GenerateResult` 包含 Content/Raw/FinishReason。
- 回顾模板预设定义在 `internal/recap/presets.go` 中（`BuiltinPresets` 变量，5 个预设），通过 `GET /api/recap/presets` 暴露。
- 友好错误映射在 `internal/worker/errors.go` 中，`GetFriendlyError` 按正则匹配原始错误返回中文消息和建议。
- Cookie 查找策略：优先使用 `CookieAccountStore.ResolveCookie`，按主播账号覆盖、全局默认账号、旧版 cookie 文件回退解析；识别、录制、发布不得各自维护独立优先级。
- Cookie 路径穿越防护：`biliutil.ValidateCookiePath` 校验 Cookie 文件路径在允许目录内，`handler/server.go` 在创建/更新 Cookie Account 时调用。
- Cookie 加密兼容性：`decryptCookieFile` 仅在检测到 `HIKAMI_V1` magic 时解密，旧明文 Netscape Cookie 文件继续透传解析；更换或丢失密钥会导致已加密 Cookie 文件无法读取。
- 自动 ASR：normalize 成功后回调检查主播 `auto_asr` 配置，在 `cmd/hikami/main.go` 中注册。
- 自动发布：recap 成功后回调检查主播 `auto_publish` 配置，在 `cmd/hikami/main.go` 中注册。
- 自动录制：`live_record/manager.go` 的 `CheckAndStartAll` 检查主播 `auto_record` 配置。
- API Key 管理：`internal/secrets` 模块提供 SQLite 存储，启动时加载到环境变量，Web 设置页面通过 `/api/secrets` 端点管理。
- 术语表：`internal/glossary` 模块提供全局+主播级别术语管理，`handler.go` 在生成 prompt 时通过 `glossaryStore.ExportForPrompt` 注入。Web 端通过 `/api/glossary/entries` 和 `/api/channels/:id/glossary/entries` 端点管理。
- AI 术语发现：`internal/glossary/discovery.go` 提供 `Discoverer`（分块转写文本、调用 AI 提取候选、合并到 candidate store），`candidate_store.go` 提供候选 CRUD/审批/评分。回顾完成后 `recap.Handler` 自动触发发现。手动触发通过 `POST /api/sessions/:sid/glossary/discover`。
- 回顾模板：`internal/recap/template.go` 提供 `TemplateStore`（CRUD + Resolve 合并）。`render.go` 提供 `RenderTemplate` 变量插值引擎。`danmaku_stats.go` 提供 `FormatDanmakuStats` 和 `appendDanmakuStats` 程序化弹幕统计生成。Handler 持有 `templateStore`，在 `HandleTask` 中通过 `Resolve` 获取最终模板。
- 弹幕分析：`internal/recap/danmaku.go` 提供 `analyzeDanmaku` 函数，优先读取 ASR 校正后的 `package/danmaku.json`，分析弹幕密度峰值、代表性弹幕、关键词统计，注入到回顾 prompt 中。`danmaku_analysis.go` 提供多因子评分、突发时刻检测、话题聚类等子函数。
- 时间段回顾：`POST /api/sessions/:sid/recap-partial` 按 `start_time`/`end_time` 过滤 SRT/VTT 与弹幕，兼容别名 `/api/sessions/:sid/recap-with-range`。过滤逻辑在 `filter.go` 中。
- 回顾内容查看：`GET /api/sessions/:sid/recap` 返回 markdown、prompt、raw_response、suggested_terms。
- 回顾内容编辑：`PUT /api/sessions/:sid/recap/content` 更新回顾 Markdown。
- 转写摘要：`recap/transcript_summarizer.go` 提供 `TranscriptSummarizer`，超过 30000 字自动压缩为精简摘要+关键引用+话题列表。通过 `recap_ai.enable_summarization` 配置启用。
- 话题驱动分段：`recap/segmentation.go` 检测 SRT 静音间隔和弹幕密度变化，为长直播（>30 分钟）生成分段建议注入 prompt。
- 回顾前术语校正转写：`recap/transcript_correction.go` 在 HandleTask 读取 transcript 后执行术语校正，生成 `transcript.corrected.txt` 和 `transcript.correction.json`。全量回顾优先使用 `segments.json` 生成带时间戳校正版。
- 回顾续写：`recap/handler.go` 检测 `finish_reason` 为 `length`/`max_tokens` 时自动续写。`shouldContinueRecap`、`buildContinuationPrompt`、`appendContinuation`、`dropDuplicateLeadingHeading` 管理续写逻辑。
- Per-channel 回顾配置：`recap/handler.go` 的 `recapOptions` 从 channel 读取 `recap_model` 和 `max_continuations`，覆盖全局配置。`withRecapModel`/`recapModelFromContext` 通过 context 传递模型。
- 回顾 AI 配置端点：`GET/PUT /api/config/recap` 在线更新 base_url/model/max_tokens/max_continuations/timeout_seconds，同步 RuntimeStatus.RecapModel。
- 发布配置合并：`publisher.resolvePublishConfig` 将主播级配置与全局配置合并，支持字段级 fallback（-1/0 表示跟随全局）。新增 Aigc（AI 辅助创作声明）、TimerPubTime（定时发布）、CoverURL（自定义封面）、Topics（话题标签）字段。
- 封面图上传：`OpusCoverUploader` 接口提供 `UploadCover` 方法（`uploadCoverWithURL` 支持 base URL 注入用于测试），优先使用 session recap 目录的 cover 图片，fallback 到 channel 配置的 `publish_cover_url`。
- 全局发布配置端点：`GET/PUT /api/config/publish` 读取和更新全局发布设置。
- 来源模式：`discover.go` 的 `DiscoverAll` 检查 `source_mode == "live_only"` 跳过回放发现。`DiscoverChannel` 检查 `discover_limit` 限制新建场次数。
- 外部工具安装提示：`runtime/probe.go` 的 `ToolStatus` 新增 `InstallHint` 字段，`getInstallHint` 根据 OS 返回安装命令。
- 磁盘使用检查：`runtime/disk_unix.go` 和 `runtime/disk_windows.go` 提供跨平台实现，通过 `CheckDiskUsage` 调用。诊断报告 API 包含磁盘使用信息。
- Cookie 过期检查：`runtime/health.go` 的 `CheckCookieExpiry` 遍历所有启用主播的 Cookie 文件，返回 7 天内过期或已过期的警告。
- 任务批量重试：`worker.Pool.BatchRetry` 支持批量重试多个失败任务；`POST /api/tasks/batch-retry` 端点暴露此能力。
- 任务摘要统计：`worker.Store.TaskSummary` 返回按状态分组的任务计数；`worker.Store.RecentFailedTasks` 返回最近 N 条失败任务。
- 任务取消：`worker.Pool.running` 维护 `map[string]context.CancelFunc`，`Cancel` 方法调用 cancel 函数终止任务 goroutine 并从 map 中清理。
- 自动重试去重：`worker.ShouldAutoRetry` 已导出为公共函数（含 `nonRetryableTypes` 集合），handler 层和 Pool 共用。`nonRetryableTypes`（archive/publish 不自动重试）与「状态旁路任务」是**两套独立机制**：前者控制是否自动重试，后者由 `worker.Register(type, h, opts...)` 的 `WithBypassFailState()` 选项声明，控制失败时是否降级 session 主状态（旁路任务仅写 `last_error`）。
- 新手引导：handler 的 `handleOnboardingStatus` 检查工具/Key/主播，`handleOnboardingDismiss` 通过 secrets 存储跳过状态。
- 主播配置复制：`POST /api/channels/:id/copy-config` 端点复制主播配置到其他主播。
- 通知测试：`POST /api/notify/test` 端点发送测试通知，用于验证通知配置是否正确。
- WebSocket Origin 校验：`handler/server.go` 的 `checkWebSocketOrigin` 校验 Origin 头与 Host 匹配，localhost 互联允许，非同源拒绝。
- 前端生命周期工具：`web/src/utils/lifecycle.ts` 定义 6 个生命周期步骤（source/media/asr/recap/upload/publish），提供状态到步骤映射、下一步动作计算、能力禁用原因、显示数据格式化。
- 前端友好状态：`web/src/utils/friendlyStatus.ts` 提供状态到友好标签/颜色/进度的映射和状态分组过滤。
- 前端专家模式：`web/src/composables/useExpertMode.ts` 提供 localStorage 持久化的专家模式切换。
- 前端当前主路由为 `HomeView`、`StreamersView`、`RecapsView`、`SettingsView`；旧 `/live`、`/dashboard`、`/sessions`、`/tasks`、`/channels` 路径通过 router 重定向。
- 前端首页（`HomeView`）专家模式调用 `/api/stats/dashboard` 展示月度场次、主播排名和费用趋势。
- 前端回顾页（`RecapsView`）承载场次列表、生命周期动作、局部回顾和 `suggested_terms` 一键添加术语。
- 前端设置中心（`SettingsView`）管理系统能力、API Key、回顾 AI 配置（RecapConfig 在线编辑）、全局发布、全局术语、回顾模板和 B 站 Cookie Account。
- 前端回顾模板编辑器（`RecapTemplateEditor`）支持 global/channel 两种作用域，可编辑 system prompt、输出格式、粉丝称呼、自定义变量，并支持导入/导出。
- 前端新手引导（`OnboardingWizard`）三步引导新用户完成工具检查、API Key 设置和首个主播添加。
