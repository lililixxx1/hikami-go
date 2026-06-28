# 测试策略

> 本文件由根 CLAUDE.md 拆分而来，作为 AI 上下文补充文档。
>
> 最后同步：2026-06-25 | Go 测试文件 61 个 | Go 测试函数 880 | 前端测试文件 4 个 | 前端测试用例 90
>
> 注：计数统一采用 `grep -c "^func Test"` 顶级函数口径（前端为 `it/test` 块）。各模块测试文件数与历史口径可能因 build tag / 子测试而异。

## 测试策略

- `internal/handler/server_test.go`：集成测试（52 个测试函数），使用内存 SQLite 和 fake 依赖测试 API 路由，覆盖主播 CRUD、主播识别保存、直播状态检查、录制启停、运行时健康（含 ASR 详情）、ASR/Recap/Upload/Fetch/Publish 能力拒绝、opus 编辑/删除能力守卫、任务 CRUD、配置导出/导入、stats/dashboard、archive 端点。
- `internal/handler/auth_test.go`：单元测试（5 个测试函数），覆盖 admin token 认证中间件（X-Admin-Token/Bearer、ConstantTimeCompare、缺失/错误 token 拒绝、公开路由放行）。
- `internal/channel/channel_test.go`：单元测试（54 个测试用例），覆盖主播 Store CRUD、Bootstrap、识别输入解析（URL/UID/直播间）、B 站 API 识别、mergeIdentified 合并策略、per-channel 配置。
- `internal/channel/identify_test.go`：单元测试（5 个测试用例），覆盖识别输入规范化、直播间/UID 识别、Cookie 查找策略。
- `internal/worker/worker_test.go`：单元+集成测试（33 个测试用例），覆盖任务 Store CRUD、生命周期、重试、取消、进度更新、恢复策略、Pool 注册与执行、Hub 广播与取消订阅、旁路任务（WithBypassFailState）。
- `internal/worker/task_test.go`：单元+集成测试（5 个测试用例），覆盖 Task Store 生命周期、重试、取消、ActiveBySessionAndType、RecoverRunning。
- `internal/glossary/glossary_test.go`：单元+集成测试（31 个测试用例），覆盖 CRUD、合并逻辑、Prompt 导出、Markdown/JSON 导入导出、批量删除/切换、频道作用域隔离。
- `internal/glossary/candidate_store_test.go`：单元+集成测试（12 个测试用例），覆盖候选 CRUD、审批、评分、批量操作。
- `internal/glossary/discovery_test.go`：单元+集成测试（14 个测试用例），覆盖 AI 术语发现、分块、prompt、候选合并、解析结果校验、时间戳处理。
- `internal/recap/recap_test.go`：单元+集成测试（62 个测试用例），覆盖 CreateTask 前置条件、LocalProvider、HandleTask 全流程（含校正产物验证）、弹幕分析、Prompt 构建（含模板渲染）、SessionMetadata 读取、FormatDanmakuStats/appendDanmakuStats、术语校正转写（buildCorrectionRules/correctTextWithRules/correctedTranscriptForPrompt）、ensureFinalAddressSection、续写逻辑。
- `internal/recap/template_test.go`：单元+集成测试（25 个测试用例），覆盖 TemplateStore CRUD（GetGlobal/GetByChannel/Upsert/Delete/ListGlobal）、Resolve 合并逻辑（全局覆盖内置、主播覆盖全局、部分覆盖、禁用回退、ExtraVars 合并、`__builtin__` 标记回退）、RenderTemplate 变量插值（标准变量、自定义变量、空值/nil 处理、数值变量）。
- `internal/recap/test_recap_main_test.go`：集成测试（1 个测试用例），使用真实 AI API 端到端回顾生成（需手动运行 `go test -run TestGenerateRecapFromRealData -v -timeout 10m`）。
- `internal/recap/test_recap_0329_test.go`：集成测试（1 个测试用例），使用真实 AI API 端到端回顾生成。
- `internal/state/state_test.go`：单元+集成测试（12 个测试用例），覆盖所有合法转换、非法转换拒绝、task_failed 全状态可达、Apply 事务持久化、失败错误写入、时间戳设置、ApplyWithPublishTarget 同事务写 publish_target、ApplyRevertPublish 发布回退。
- `internal/asr/asr_test.go`：单元测试（37 个测试用例），覆盖 CreateTask 前置条件、LocalTranscriber、DashScope 模型/请求体/请求模式、转写结果提取、SRT 生成、退避重试、任务恢复。
- `internal/asr/danmaku_correction_test.go`：单元测试（1 个测试用例），覆盖弹幕时间校正。
- `internal/asr/temp_server_test.go`：单元测试（10 个测试用例），覆盖 localPath 路径安全、Publish 成功/取消/不存在、Delete 成功/已删除/空父目录清理、MountHandler HTTP 文件服务。
- `internal/asr/s3_publisher_test.go`：单元测试（7 个测试用例），覆盖 S3Publisher 构建、上传、删除、配置校验。
- `internal/asr/public_ip_test.go`：单元测试（8 个测试用例），覆盖公网 IP 检测（多服务回退、超时、私有 IP 过滤）。
- `internal/publisher/md2opus_test.go`：单元测试（20 个测试用例），覆盖 Markdown 到 Opus 格式转换的各种场景（代码块、表格、链接、标题、列表等）。
- `internal/publisher/publisher_test.go`：单元+集成测试（23 个测试用例），覆盖 resolvePublishConfig 合并逻辑、mapBiliError 错误映射、HandleTask 集成测试（CookieAccountStore、NotifyManager、InvalidStatus、UploadedStatus 等场景）。
- `internal/publisher/bilibili_opus_test.go`：单元测试（5 个测试用例），覆盖 BiliOpusClient 的 SaveDraft/PublishOpus/DeleteDraft/uploadCoverWithURL HTTP 交互。
- `internal/scheduler/scheduler_test.go`：单元测试（13 个测试用例），覆盖 cron job 注册计数、空 spec 不注册、Start/Stop 生命周期、磁盘使用告警（低/高/边界值）、Cookie 过期告警（已过期/即将过期/正常）、nil notifyManager 不 panic。
- `internal/notify/notify_test.go`：单元测试（9 个测试用例），覆盖 NewManager、ShouldSend、nil manager/notifier 安全、异步发送、事件过滤、NoopManager。
- `internal/notify/sender_test.go`：单元测试（3 个测试用例），覆盖 WebhookNotifier/BarkNotifier/ServerChanNotifier 发送和错误处理、NewNotifierFromConfig 工厂函数。
- `internal/normalize/normalize_test.go`：单元+集成测试（68 个测试用例），覆盖 JSONL/XML 弹幕解析、多 P 弹幕合并、文件操作、元数据构建、HandleTask 全流程。
- `internal/session/session_test.go`：单元测试（32 个测试用例），覆盖三种来源场次创建、幂等去重、失败重试、查询、状态更新、统计、发布目标设置、Slug 清理、时间窗口查询（直播/下载）。
- `internal/live_record/bilibili_test.go`：单元测试（3 个测试用例），覆盖 B 站直播状态和流选择。
- `internal/live_record/ffmpeg_test.go`：单元测试（4 个测试用例），覆盖 HTTP pipe + Headers 传递、FFmpeg 参数构建、.part 文件保护、优雅停止等待期。
- `internal/live_record/manager_test.go`：集成测试（16 个测试用例），覆盖录制启停、任务执行写入原始产物、重连录制分片拼接、Cookie 查找（主播/Bootstrap 优先级）、流选择（混合/纯音频/回退/必须音频）、CheckAndStartAll 跳过 replay_only、redactURL 脱敏、Stop 幂等、健康检查生命周期、setActive/clearActive。
- `internal/live_record/danmaku_test.go`：单元测试（11 个测试用例），覆盖弹幕消息解包（普通/zlib/brotli 压缩）、弹幕内容解析（文本/用户/颜色/时间偏移）、getDanmuInfo Cookie 传递与空 Cookie、-352 重试成功/-352 全失败降级/-352 getConf 旧版 API 回退、buildAuthBody UID/protover 设置。
- `internal/discover/discover_test.go`：单元+集成测试（5 个测试用例）。
- `internal/download/download_test.go`：单元+集成测试（20 个测试用例），覆盖单 P 和多 P 下载、错误处理。
- `internal/importer/importer_test.go`：单元测试（15 个测试用例），覆盖 multipart 导入、弹幕导入、ffmpeg 转码。
- `internal/upload/upload_test.go`：单元测试（25 个测试用例），覆盖上传逻辑、清理策略。
- `internal/upload/webdav_copier_test.go`：单元测试（10 个测试用例），覆盖 joinWebDAVPath、pathDir、relativeTarget、isWebDAVNotExist。
- `internal/biliutil/cookie_test.go`：单元测试（1 个测试用例），覆盖 Cookie 加载和请求头生成。
- `internal/biliutil/cookie_crypto_test.go`：单元测试（10 个测试用例），覆盖 Cookie 加密密钥校验、AES-256-GCM 加解密往返、明文 passthrough、错误密钥和截断数据。
- `internal/biliutil/cookie_account_test.go`：单元测试（14 个测试用例），覆盖 Cookie Account Store CRUD、路径穿越防护、CreateImported、ClearAll。
- `internal/biliutil/cookie_writer_test.go`：单元测试（3 个测试用例），覆盖 Cookie 文件写入。
- `internal/biliutil/login_test.go`：单元测试（3 个测试用例），覆盖 QR 码登录流程。
- `internal/biliutil/wbi_test.go`：单元测试（13 个测试用例），覆盖 WBI 签名（置换表、密钥提取、参数排序、MD5 计算、缓存机制、错误处理）。
- `internal/secrets/secrets_test.go`：单元测试（8 个测试用例），覆盖存取、覆盖、删除、列表、环境变量加载、掩码、校验。
- `internal/db/migrate_test.go`：单元测试（8 个测试用例），覆盖迁移幂等性和核心表创建。
- `internal/config/config_test.go`：单元测试（12 个测试用例），覆盖配置加载、校验、默认值（含端口 :6334）、ASRS3Config。
- `internal/runtime/probe_test.go`：单元测试（1 个测试用例），覆盖 ASR 模型和请求模式探测。
- `internal/runtime/health_test.go`：单元测试（8 个测试用例），覆盖健康检查、磁盘使用、Cookie 过期检查。
- `internal/runtime/ffmpeg_resolver_test.go`：单元测试（15 个测试用例），覆盖 safeJoin 路径穿越防护、executableFile 校验、extractArchive 解压、extractZip/extractTgz 穿越拦截、cachedResolution 缓存、并发安全。
- `internal/aiprovider/result_test.go`：单元测试（5 个测试用例），覆盖 GenerateResult 构建和字段访问。

## 前端测试

- `web/src/features/recaps/sessionActions.test.ts`：单元测试（41 个测试用例），覆盖 `getRowActions`/`getDrawerActions`（各状态下可见动作）、`canFetchLocal`（local_available 守卫）、`decideRetry`/`isRetryable`/`retryHint`（重试决策）、`primaryActionType`、`UI_ACTION_REASON`（禁用原因）。从 RecapsView 视图抽出的纯函数。
- `web/src/utils/format.test.ts`：单元测试（17 个测试用例），覆盖格式化工具函数。
- `web/src/utils/friendlyStatus.test.ts`：单元测试（13 个测试用例），覆盖状态友好化映射。
- `web/src/utils/lifecycle.test.ts`：单元测试（19 个测试用例），覆盖生命周期工具函数。
