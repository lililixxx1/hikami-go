[根目录](../../CLAUDE.md) > **internal/config**

# internal/config -- 配置加载与验证

## 模块职责

从 YAML 文件加载全局配置，提供类型化的 Go 结构体，包含默认值设置、字段校验、目录创建、日志级别解析和日志格式配置。

## 入口与启动

- **入口文件**: `config.go`
- **核心函数**: `Load(path string) (*Config, error)`

## 对外接口

| 函数/方法 | 说明 |
|-----------|------|
| `Load(path)` | 加载 YAML 配置文件并返回 `*Config` |
| `Config.Validate()` | 校验必填项和数值范围 |
| `Config.EnsureDirs()` | 创建 `output_root`、数据库目录（不再创建 `logs/`——程序只写 stdout 不落盘，`logs.dir` 字段保留仅为向后兼容） |
| `Config.LogLevel()` | 将字符串日志级别映射为 `slog.Level` |

## 关键依赖与配置

- 依赖 `github.com/spf13/viper` 进行配置解析
- 配置结构体: `Config`，包含 Web/Worker/Cron/LiveRecord/Logs/DashScope/ASRTemp/ASRS3/RecapAI/WebDAV/Upload/Downloader/Publish/Notify/BootstrapChannel 等配置块
- 默认值在 `setDefaults()` 中定义
- API Key 等敏感信息通过 `api_key_env` 字段指定环境变量名，运行时通过 `os.Getenv()` 读取

## 数据模型

配置结构体对应 `config.example.yaml` 的所有字段。主要分组：

- **全局**: `output_root`(默认 `hikami-go`,2026-07-08 从 `huizeman` 改名), `db_path`, `log_format`, `ffmpeg`, `ffprobe`, `yt_dlp`, `rclone`(后两者 2026-07-08 起可通过 web `/api/config/tools` 修改,持久化到 runtime_settings tools 段), `cookie_encryption_key`
- **Web**: `web.enabled`（默认 true）, `web.listen`（默认 `:6334`）
- **Worker**: `worker.num`（唯一并发旋钮；原 `worker.live_record_num` 已删除——调度器从不读它，现走共享 `worker.num` 池）
- **Cron**: `cron.discovery`, `cron.live_check`
- **直播录制**: `live_record.*`（9 个子项，含 record_danmaku、generate_asr_audio、segment_minutes、stop_grace_seconds）
- **日志**: `log_format`, `logs.dir`, `logs.level`, `logs.format`
- **ASR**: `dashscope.*`, `asr_temp.*`, `asr_s3.*`
- **AI 回顾**: `recap_ai.*`（provider, api_key_env, base_url, model, timeout_seconds, cli_path, **glossary_file**, enable_summarization, max_continuations）
- **WebDAV**: `webdav.remote`, `webdav.base_path`, `webdav.url`, `webdav.username`, `webdav.password`, `webdav.password_env`
- **上传清理**: `upload.cleanup_policy`（none/temp/generated/all）
- **发布后归档**: `archive.auto_after_publish`、`archive.cleanup_policy`（none/temp/generated/all，默认 none；与 upload.cleanup_policy 解耦）
- **下载后端**: `downloader.backend`（auto/native/ytdlp，默认 auto）
- **发布**: `publish.*`（enabled, mode, category_id, list_id, private_pub, summary_len, **aigc**, **timer_pub_time**）
- **通知**: `notify.*`（enabled, type, webhook_url, bark_url, bark_key, serverchan_key, events）
- **引导**: `bootstrap_channels` 数组

**ASRS3Config 结构体（S3 兼容对象存储）：**

| 字段 | 说明 |
|------|------|
| `endpoint` | S3 端点 URL（如 `https://oss.example.com`） |
| `bucket` | 存储桶名称 |
| `access_key_id` | Access Key ID |
| `access_key_secret` | Access Key Secret（直接值） |
| `access_key_env` | Access Key Secret 环境变量名（优先于直接值） |
| `region` | 区域（可选） |
| `public_url_prefix` | 公开访问 URL 前缀 |
| `use_path_style` | 使用路径风格访问（默认虚拟主机风格） |

| 方法 | 说明 |
|------|------|
| `SecretResolved()` | 优先取 access_key_env 对应环境变量，回退到 access_key_secret 字段 |
| `Configured()` | endpoint + bucket + access_key_id + secret + public_url_prefix 均非空 |

**ASRTempConfig 结构体（支持本地 HTTP 临时音频服务）：**

| 字段 | 说明 |
|------|------|
| `rclone_remote` | rclone 远端名称（传统模式） |
| `base_path` | 远端基础路径 |
| `public_base_url` | 公开访问基础 URL |
| `cleanup_after_success` | ASR 成功后清理临时文件 |
| `enabled` | 启用本地 HTTP 文件服务 |
| `listen` | 本地 HTTP 服务监听地址 |
| `local_dir` | 本地音频存储目录 |

| 方法 | 说明 |
|------|------|
| `NativeConfigured()` | enabled + local_dir + public_base_url 均非空 |
| `RcloneConfigured()` | rclone_remote 非空 |

**WebDAVConfig 结构体（支持原生 WebDAV）：**

| 字段 | 说明 |
|------|------|
| `remote` | rclone 远端名称（传统模式） |
| `base_path` | 远端基础路径 |
| `url` | WebDAV 服务 URL（原生模式） |
| `username` | WebDAV 用户名 |
| `password` | WebDAV 密码 |
| `password_env` | WebDAV 密码环境变量名 |

| 方法 | 说明 |
|------|------|
| `PasswordResolved()` | 优先取 password_env 对应环境变量，回退到 password 字段 |
| `NativeConfigured()` | URL 非空 |
| `RcloneConfigured()` | remote 非空 |

**日志格式配置：**

| 字段 | 说明 |
|------|------|
| `log_format` | 顶层日志格式，默认 `json`。`text` 时 main.go 使用 `slog.NewTextHandler`，其他值按 JSON 处理 |
| `logs.format` | 日志格式兼容字段，默认 `json`。当 `log_format` 为空时作为回退 |

**CookieEncryptionKey 字段：**

| 字段 | 说明 |
|------|------|
| `cookie_encryption_key` | Cookie 文件静态加密密钥。为空时禁用；非空时必须是 64 位 hex（32 字节），启动后传入 `biliutil.SetCookieEncryptionKey`，用于 AES-256-GCM 加密扫码登录写入的 Cookie 文件，并解密已有 `HIKAMI_V1` 格式 Cookie 文件。 |

**DownloaderConfig 结构体（回放下载后端选择）：**

| 字段 | 说明 |
|------|------|
| `backend` | 下载后端：`auto`（默认，native 优先，遇不支持回退 yt-dlp）、`native`、`ytdlp` |

| 方法 | 说明 |
|------|------|
| `NativeConfigured()` | backend 为空、`auto` 或 `native` 时返回 true |
| `YTDLPConfigured()` | backend 为 `ytdlp` 时返回 true |

**ArchiveConfig 结构体（发布成功后归档到 WebDAV）：**

与 `UploadConfig` 的手动上传路径解耦：归档任务不推进 session 主状态（保持 `published`），仅写 `archived_at`；删除策略用独立的 `archive.cleanup_policy`，不复用 `upload.cleanup_policy`。

| 字段 | 说明 |
|------|------|
| `auto_after_publish` | 发布成功后自动归档到 WebDAV（默认 false） |
| `cleanup_policy` | 归档成功后删除范围（none/temp/generated/all，默认 none） |

**Effective\* 默认值方法族（统一各消费者默认值）：**

为消除各业务模块各自维护「字段为空时的默认值」导致的不一致，新增一组 `Effective*` 方法作为统一默认值来源（`afbc9b7`），各消费者（handler/runtime/recap/asr 等）改为调用这些方法而非自行判空：

| 方法 | 默认值（字段为空时） |
|------|----------------------|
| `RecapAIConfig.EffectiveProvider()` | `"openai"`（兼容 OpenAI 协议） |
| `RecapAIConfig.EffectiveBaseURL()` | `"https://api.openai.com/v1"` |
| `RecapAIConfig.EffectiveModel()` | `"gpt-4o-mini"` |
| `RecapAIConfig.EffectiveAPIKeyEnv()` | `"AI_API_KEY"` |
| `DashScopeConfig.EffectiveAPIKeyEnv()` | `"DASHSCOPE_API_KEY"` |
| `ASRS3Config.EffectiveAccessKeyEnv()` | `"ASR_S3_ACCESS_KEY_SECRET"` |

**BootstrapChannel 结构体（含来源模式字段）：**

| 字段 | 说明 |
|------|------|
| `source_mode` | 来源模式：both（默认）/live_only/replay_only/live_first/replay_first |
| `discover_limit` | 每次回放发现最大新建场次数（0 = 不限制，默认 0） |
| `auto_recap` | `*bool` 三态：标准化/ASR 完成后是否自动提交回顾。YAML/config 未显式设置（`nil`）时由 `channel.resolveAutoRecap(nil, false)` 兜底为**默认关**（2026-07-06 反转,原默认开）；显式 `true`/`false` 直接采用（设计 4.1，对应 db migrate v32 的 `channels.auto_recap` 列,默认 0） |

（其余字段同 Channel 结构体，详见 internal/channel 模块文档。）

**RecapAIConfig glossary_file 字段：**

| 字段 | 说明 |
|------|------|
| `glossary_file` | ASR 术语校正表文件路径（Markdown 格式）。**已 deprecated**：术语表已迁移到数据库管理，此配置仅用于首次启动时自动导入。 |

## 测试与质量

- `config_test.go`: 34 个测试用例，覆盖：
  - 默认值: TestLoad_DefaultValues（Web/Worker/DashScope/RecapAI 全部默认值验证）
  - 校验: TestValidate_MissingOutputRoot、TestValidate_MissingDbPath、TestValidate_Success、TestValidate_WorkerNumZero、TestValidate_PublishModeInvalid、TestValidate_DownloaderBackend、**TestValidate_ArchiveCleanupPolicy**（archive.cleanup_policy 合法值校验）
  - 日志级别: TestLogLevel_Default、TestLogLevel_Explicit（6 种输入映射）
  - 默认设置: TestSetDefaults_WebListen、TestSetDefaults_DashScope
  - 日志格式: TestLogFormat（json 默认 / text 显式）
  - 覆盖: TestLoad_ExplicitOverrides（Web.Listen / RecapAI.Model / RecapAI.MaxTokens）
  - 下载后端 helper: TestDownloaderConfigHelpers、TestNativeConfigured_RequiresPassword
  - **Effective\* 默认值**：TestRecapAIEffectiveDefaults、TestDashScopeEffectiveAPIKeyEnv、TestASRS3EffectiveAccessKeyEnv、TestEffectivePasswordEnv_DefaultFallback、TestEffectivePassword_Managed*（true 不回退 / false 回退 Yaml）、TestEffectiveAccessKey_ManagedDoesNotFallBack
  - **ApplyOverrides（runtimeconfig 持久化覆盖）**：TestApplyOverrides_OverridesPublishFields、_MissingSectionRetainsBaseline、_EmptyObjectRetainsBaseline、_CorruptJSONSkippedNotFatal、_DoesNotFreezeHiddenRecapFields、_InjectsWebDAVTombstone、**_OverridesToolsFields / _ToolsPresenceAware / _ToolsEmptyStringClears（2026-07-08 新增 tools 段：全覆盖 / nil 保留基线 / 空串清空）**
  - **向后兼容**：TestLoadConfigBackcompatLiveRecordNumRemoved（旧配置含已删的 `worker.live_record_num` 字段，viper 静默忽略不报错）

## 常见问题 (FAQ)

**Q: 如何添加新配置项？**
A: 在 `Config` 结构体中添加字段和 `mapstructure` tag，在 `setDefaults()` 中设置默认值，按需在 `Validate()` 中添加校验。

**Q: WebDAV 配置如何选择后端？**
A: 优先使用原生 WebDAV（`webdav.url` 非空时使用 gowebdav 库）；未配置 URL 则回退到 rclone（需 `webdav.remote` 非空且 rclone 可用）。

**Q: ASR 临时音频配置如何选择后端？**
A: 三级优先级：优先使用本地 HTTP 服务（`asr_temp.enabled=true` + `local_dir` + `public_base_url` 均非空）；其次使用 S3 兼容存储（`asr_s3` 配置完整）；最后回退到 rclone（需 `asr_temp.rclone_remote` 非空）。

**Q: ASR S3 配置如何设置？**
A: 在 YAML 中添加 `asr_s3` 配置块，包含 endpoint、bucket、access_key_id、access_key_secret（或 access_key_env）、public_url_prefix。可选 region 和 use_path_style。

**Q: 默认监听端口是多少？**
A: `web.listen` 默认值为 `:6334`（从 `:8080` 变更），可在 YAML 中显式覆盖。

## 相关文件清单

- `config.go` -- 唯一源文件
- `config_test.go` -- 配置加载与验证测试（34 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-13 | 配置变更 | **`EnsureDirs()` 不再创建 `logs/` 空目录**（`f39c44d`）：程序只把 slog 日志写到 stdout（`main.go` 的 slog handler 绑 `os.Stdout`），从不落盘文件，`logs/` 空目录是历史遗留。`EnsureDirs()` 删掉 `MkdirAll(c.Logs.Dir)` 那段（保留 `OutputRoot` + DB 目录创建）；`LogsConfig.Dir` 字段和 `logs.dir` 配置项保留仅为向后兼容（老 config.yaml 里有这个 key 不会报错），当前无任何代码读取它。Linux 生产环境经 systemd `StandardOutput=journal` 进 journald，Windows 手动运行需 `> run.log 2>&1` 重定向。无新增测试（`EnsureDirs` 无断言 `Logs.Dir`）。测试计数不变（34）。 |
| 2026-07-08 | 功能 | **新增 `tools` 配置段（yt-dlp/rclone 路径 web 可编辑）**（`dfe7d23`）：第 7 个 runtimeconfig 段——`ToolsSectionDTO`（YTDLP/Rclone 指针，presence-aware）+ `ApplyOverrides` tools case + `GET/PUT /api/config/tools` handler。设计决策：只暴露 yt-dlp/rclone 不含 ffmpeg/ffprobe（后者 required=true，改错路径下次启动 fatal、web 不可达无法纠正）。DB v35 迁移：runtime_settings 表 CHECK 白名单 `+tools`（SQLite 不支持改 CHECK，走标准表重建：建 v35 临时表→INSERT 复制→DROP 旧表→RENAME，6 段旧数据无损回灌）。新增 3 测试（ToolsOverrides/PresenceAware/EmptyStringClears），config 31→34。 |
| 2026-07-06 | 配置变更 | **删除 `worker.live_record_num` 死配置项**（异常 #5，`3ae2435`）：`WorkerConfig.LiveRecordNum` 字段、`setDefaults` 的 `worker.live_record_num` 默认值（原 2）、`Validate` 的非负校验全部移除。根因：调度器从不读这个字段，录制任务走共享的 `worker.num` 任务池，留着误导用户以为可调录制并发。向后兼容：viper 默认忽略未知字段，旧配置文件含此字段会被静默忽略；新增 `TestLoadConfigBackcompatLiveRecordNumRemoved` 验证。测试计数 30→31（本提交 +1；自上次文档以来还累计了 10 个 ApplyOverrides/EffectivePassword/Managed 回退测试，文档从 19→31） |
| 2026-06-24 | 文档补注 | 补登 `BootstrapChannel.auto_recap`（`*bool` 三态,默认经 `channel.resolveAutoRecap(nil,true)` 兜底为开,对应 db v32 的 `channels.auto_recap` 列）。本字段实际随 `5fadea4` 引入,代码与 channel 默认值逻辑此前已在 channel 模块记录,本次补齐 config 侧描述;测试计数无变化（仍 19） |
| 2026-06-23 | 功能/重构 | (1) 新增 `ArchiveConfig` 结构体（`auto_after_publish`、`cleanup_policy`），与 `UploadConfig` 手动上传路径解耦——归档不推进 session 主状态、用独立的 cleanup_policy；`Validate` 校验 cleanup_policy 取值（TestValidate_ArchiveCleanupPolicy）。(2) 新增 `Effective*` 默认值方法族（`afbc9b7`）：RecapAIConfig.EffectiveProvider/BaseURL/Model/APIKeyEnv、DashScopeConfig.EffectiveAPIKeyEnv、ASRS3Config.EffectiveAccessKeyEnv，统一各消费者「字段为空时的默认值」来源，消除各模块自行判空的不一致。测试 15→19 |
| 2026-06-18 | 配置变更 | 新增 `DownloaderConfig`（`downloader.backend`，默认 `auto`，合法值 auto/native/ytdlp）和 NativeConfigured/YTDLPConfigured helper；Validate 校验后端取值 |
| 2026-06-05 | 重大更新 | 新增 ASRS3Config 结构体（Endpoint/Bucket/AccessKeyID/AccessKeySecret/AccessKeyEnv/Region/PublicURLPrefix/UsePathStyle），支持 S3 兼容对象存储作为 ASR 临时音频发布后端；SecretResolved() 优先环境变量回退直接值，Configured() 校验必要字段非空 |
| 2026-06-03 | 配置变更 | `web.listen` 默认值从 `:8080` 变更为 `:6334` |
| 2026-06-03 | 重大更新 | ASRTempConfig 新增 Enabled/Listen/LocalDir 字段和 NativeConfigured() 方法，支持本地 HTTP 临时音频服务；WebDAVConfig 新增 URL/Username/Password/PasswordEnv 字段和 PasswordResolved()/NativeConfigured()/RcloneConfigured() 方法，支持原生 WebDAV 操作 |
| 2026-06-01 | 测试补充 | 新增 `config_test.go`（12 用例）：默认值验证、必填校验、日志级别映射、默认设置、日志格式、显式覆盖 |
| 2026-05-23 | 安全更新 | Config 新增 `CookieEncryptionKey` / `cookie_encryption_key` 顶层配置，用于 Cookie 文件 AES-256-GCM 静态加密 |
| 2026-05-17 | 更新 | 无直接变更，本轮改动涉及其他模块集成 |
| 2026-05-15 | 更新 | 新增顶层 log_format 与 logs.format 配置，默认 json，main.go 按 json/text 选择 slog handler；新增 NotifyConfig，默认事件包含 task_failed/record_start/record_stop/recap_done/publish_done |
| 2026-05-14 | 更新 | BootstrapChannel 新增 source_mode 和 discover_limit 字段 |
| 2026-05-08 | 更新 | PublishConfig 新增 Aigc/TimerPubTime 字段；BootstrapChannel 新增 11 个发布相关字段 |
| 2026-05-07 | 更新 | glossary_file 标记为 deprecated、LiveRecordConfig 新增 generate_asr_audio/segment_minutes |
| 2026-05-04 | 更新 | RecapAIConfig 新增 glossary_file 字段 |
| 2026-05-03 | 更新 | 新增 UploadConfig、PublishConfig 子结构体、BootstrapChannel 新增 auto_record/auto_asr |
| 2026-05-02 | 更新 | BootstrapChannel 新增 download_cookie_file 字段 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
