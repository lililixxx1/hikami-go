[根目录](../../CLAUDE.md) > **internal/handler**

# internal/handler -- HTTP API 与 WebSocket 端点

## 模块职责

Gin HTTP 服务器，注册所有 REST API 路由和 WebSocket 端点。负责请求解析、参数校验、调用业务模块、错误映射和响应序列化。支持嵌入式 Vue SPA 前端。内部维护运行时状态的代际校验机制（`configGen` + `runtimeMu`），保证配置并发更新不会发生乱序覆盖。

## 入口与启动

- **入口文件**: `server.go`
- **核心类型**: `Server`

## 对外接口

**API 路由表：**

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/` | 首页 HTML（无嵌入前端时）或 SPA 入口 |
| GET | `/ws` | WebSocket 进度推送 |
| GET | `/api/healthz` | 健康检查 |
| GET | `/api/health/runtime` | 运行时工具与能力状态（含 InstallHint） |
| GET | `/api/channels` | 主播列表 |
| GET | `/api/channels/:id` | 单个主播详情（2026-07-08 新增；`Store.Get` 已存在,补路由注册） |
| POST | `/api/channels/identify` | 识别 B 站主播 |
| POST | `/api/channels/identify/save` | 识别并保存 |
| POST | `/api/channels` | 创建主播 |
| PUT | `/api/channels/:id` | 更新主播（含 per-channel 发布配置、source_mode、discover_limit） |
| DELETE | `/api/channels/:id` | 删除主播 |
| POST | `/api/channels/:id/copy-config` | 复制主播配置到其他主播 |
| POST | `/api/bili/login/qrcode` | 创建 B 站 QR 码登录会话 |
| GET | `/api/bili/login/qrcode/:session_id` | 轮询 QR 码登录状态 |
| POST | `/api/bili/login/qrcode/:session_id/save` | 保存 QR 码登录 Cookie 到主播配置 |
| POST | `/api/bili/login/qrcode/:session_id/save-account` | 保存 QR 码登录 Cookie 为 Cookie Account |
| DELETE | `/api/bili/login/qrcode/:session_id` | 删除 QR 码登录会话 |
| GET | `/api/cookie-accounts` | 列出 Cookie Account |
| POST | `/api/cookie-accounts` | 创建 Cookie Account |
| PUT | `/api/cookie-accounts/:id` | 更新 Cookie Account |
| DELETE | `/api/cookie-accounts/:id` | 删除 Cookie Account |
| POST | `/api/cookie-accounts/:id/default-download` | 设置默认下载账号 |
| POST | `/api/cookie-accounts/:id/default-publish` | 设置默认发布账号 |
| GET | `/api/bili/accounts` | Cookie Account 兼容列表端点 |
| PUT | `/api/bili/accounts/:id` | Cookie Account 兼容更新端点 |
| DELETE | `/api/bili/accounts/:id` | Cookie Account 兼容删除端点 |
| GET | `/api/bili/topics/search` | B 站话题搜索（`searchBiliTopics`，发布时话题建议;2026-07-06 起经 `biliCreativeGet` 共享 client + 风控对抗头） |
| GET | `/api/bili/series/list` | B 站文集列表（`listBiliSeries`,发布时合集选择;2026-07-06 起经 `biliCreativeGet` 共享 client + 风控对抗头;2026-07-20 新增可选 `?channel_id=` query,主播抽屉 per-channel 文集下拉用该主播的 publish_account_id 拉取对应账号下的文集,支持不同主播不同发布账号;与 `publisher.resolvePublishCookie` 走相同 ResolveCookie 三级链,保证「下拉看到的文集」=「发布时用的账号文集」） |
| POST | `/api/live/check` | 检查并自动开始录制 |
| GET | `/api/live/status` | 所有主播直播状态 |
| GET | `/api/live/:channel_id/status` | 单个主播状态 |
| POST | `/api/live/:channel_id/record/start` | 手动开始录制 |
| POST | `/api/live/:channel_id/record/stop` | 手动停止录制 |
| POST | `/api/sessions/discover` | 发现回放（一步式：发现全部并下载） |
| POST | `/api/sessions/discover/preview` | 发现回放预览（两步式·第一步：列出可发现项，不建场次不入队，每条带 `exists` 标记） |
| POST | `/api/sessions/discover/execute` | 发现回放执行（两步式·第二步：按勾选的 entry 列表建 download 场+入队） |
| GET | `/api/sessions` | 场次列表 |
| GET | `/api/sessions/:sid` | 场次详情+文件列表 |
| DELETE | `/api/sessions/failed` | 批量删除失败场次（含关联任务） |
| DELETE | `/api/sessions/:sid` | 删除单个场次（含关联任务） |
| POST | `/api/sessions/download` | 下载指定场次（按 session_id 重跑） |
| POST | `/api/sessions/download-by-url` | 按视频链接（BV 号等）+ 主播 ID 创建下载场次并入队，受 `ReplayDownload` 能力守卫；同 BV 重复返回 409 |
| POST | `/api/sessions/import` | multipart 导入 |
| POST | `/api/sessions/:sid/asr/submit` | 提交 ASR |
| POST | `/api/sessions/:sid/recap/generate` | 生成回顾 |
| POST | `/api/sessions/:sid/recap-partial` | 生成指定时间段回顾 |
| POST | `/api/sessions/:sid/recap-with-range` | 指定时间段回顾兼容端点 |
| GET | `/api/sessions/:sid/recap` | 获取回顾内容（含 suggested_terms） |
| PUT | `/api/sessions/:sid/recap/content` | 更新回顾 Markdown 内容 |
| POST | `/api/sessions/:sid/upload` | 上传归档 |
| POST | `/api/sessions/:sid/fetch` | 从 WebDAV 取回 |
| POST | `/api/sessions/:sid/publish` | 发布 B 站专栏 |
| POST | `/api/sessions/:sid/archive` | 手动归档已发布场次到 WebDAV（自动归档失败时的手动重试入口，archive 模块） |
| GET | `/api/tasks` | 任务列表 |
| POST | `/api/tasks/batch-retry` | 批量重试失败任务 |
| GET | `/api/tasks/:id` | 任务详情（含 friendly_error） |
| POST | `/api/tasks/:id/retry` | 重试任务 |
| POST | `/api/tasks/:id/cancel` | 取消任务 |
| DELETE | `/api/tasks/failed` | 批量删除失败任务 |
| DELETE | `/api/tasks/:id` | 删除单个任务 |
| POST | `/api/notify/test` | 测试通知配置（发送测试通知） |
| GET | `/api/secrets` | 列出 API Key 状态 |
| PUT | `/api/secrets/:key` | 更新 API Key |
| GET | `/api/config/publish` | 获取全局发布配置 |
| PUT | `/api/config/publish` | 更新全局发布配置 |
| GET | `/api/config/recap` | 获取回顾 AI 配置 |
| PUT | `/api/config/recap` | 更新回顾 AI 配置 |
| GET | `/api/config/recap/models` | 推荐回顾模型列表（下拉快捷选项；2026-07-15 起精简到 DeepSeek 2 个 flash/pro，前端改用 HCombobox 支持手动输入任意模型名） |
| GET | `/api/config/dashscope` | 获取 DashScope 配置（含 EffectiveAPIKeyEnv 兜底默认值） |
| PUT | `/api/config/dashscope` | 更新 DashScope 配置（字段校验 + key 环境变量改名的 secrets 迁移） |
| GET | `/api/config/asr-s3` | 获取 ASR S3 配置（含 EffectiveAccessKeyEnv 兜底默认值） |
| PUT | `/api/config/asr-s3` | 更新 ASR S3 配置（字段校验 + secret 改名的 secrets 迁移） |
| GET | `/api/config/archive` | 获取归档配置（auto_after_publish / cleanup_policy） |
| PUT | `/api/config/archive` | 更新归档配置（cleanup_policy 取值校验） |
| GET | `/api/config/tools` | 获取外部工具路径（yt_dlp / rclone） |
| PUT | `/api/config/tools` | 更新外部工具路径（presence-aware；保存后 refreshRuntimeStatus 重新 Probe） |
| GET | `/api/config/webdav` | 获取 WebDAV 配置 |
| PUT | `/api/config/webdav` | 更新 WebDAV 配置 |
| GET | `/api/config/export` | 全量配置导出（JSON 附件下载） |
| POST | `/api/config/import` | 全量配置导入（?strategy=merge/overwrite） |
| GET | `/api/diagnostic/report` | 诊断报告 |
| GET | `/api/stats/overview` | 总览统计 |
| GET | `/api/stats/cost` | 成本统计 |
| GET | `/api/stats/dashboard` | 统计仪表板 |
| POST | `/api/channels/:id/discover/preview` | 回放发现预览 |
| GET | `/api/cookies/status` | Cookie 状态检查 |
| GET | `/api/onboarding/status` | 引导向导状态 |
| POST | `/api/onboarding/dismiss` | 跳过引导向导 |
| GET | `/api/recap/templates` | 列出全局回顾模板 |
| PUT | `/api/recap/templates` | 新增/更新全局回顾模板 |
| GET | `/api/recap/templates/export` | 导出全局回顾模板 JSON |
| POST | `/api/recap/templates/import` | 导入全局回顾模板 JSON |
| GET | `/api/recap/presets` | 列出内置回顾模板预设 |
| GET | `/api/channels/:id/recap-template` | 获取主播回顾模板（含全局/主播/合并结果） |
| PUT | `/api/channels/:id/recap-template` | 新增/更新主播回顾模板 |
| DELETE | `/api/channels/:id/recap-template` | 删除主播回顾模板 |
| GET | `/api/channels/:id/recap-template/export` | 导出主播回顾模板 JSON |
| POST | `/api/channels/:id/recap-template/import` | 导入主播回顾模板 JSON |
| GET | `/api/glossary/entries` | 列出全局术语条目 |
| POST | `/api/glossary/entries` | 新增/更新全局术语条目 |
| DELETE | `/api/glossary/entries/:eid` | 删除全局术语条目 |
| GET | `/api/glossary/note` | 获取全局术语表备注 |
| PUT | `/api/glossary/note` | 更新全局术语表备注 |
| POST | `/api/glossary/import/markdown` | 导入全局术语表 Markdown |
| POST | `/api/glossary/import/json` | 导入全局术语表 JSON |
| GET | `/api/glossary/export/json` | 导出全局术语表 JSON |
| POST | `/api/glossary/entries/batch-delete` | 批量删除全局术语条目 |
| POST | `/api/glossary/entries/batch-toggle` | 批量启停全局术语条目 |
| POST | `/api/glossary/entries/:eid/toggle` | 单条启停全局术语条目 |
| GET | `/api/channels/:id/glossary/entries` | 列出主播术语条目 |
| POST | `/api/channels/:id/glossary/entries` | 新增/更新主播术语条目 |
| DELETE | `/api/channels/:id/glossary/entries/:eid` | 删除主播术语条目 |
| GET | `/api/channels/:id/glossary/note` | 获取主播术语表备注 |
| PUT | `/api/channels/:id/glossary/note` | 更新主播术语表备注 |
| POST | `/api/channels/:id/glossary/import/markdown` | 导入主播术语表 Markdown |
| POST | `/api/channels/:id/glossary/import/json` | 导入主播术语表 JSON |
| GET | `/api/channels/:id/glossary/export/json` | 导出主播术语表 JSON |
| POST | `/api/channels/:id/glossary/entries/batch-delete` | 批量删除主播术语条目 |
| POST | `/api/channels/:id/glossary/entries/batch-toggle` | 批量启停主播术语条目 |
| POST | `/api/channels/:id/glossary/entries/:eid/toggle` | 单条启停主播术语条目 |

## 错误映射

`writeError` 函数通过 `errors.Is` 将业务错误映射到 HTTP 状态码：

| 错误 | HTTP 状态码 |
|------|-------------|
| `ErrInvalid` / `ErrInvalidTask` | 400 |
| `ErrChannelNotRecordable` | 400 |
| `ErrNotFound` / `ErrTaskNotFound` / `glossary.ErrNotFound` / `recap.ErrTemplateNotFound` | 404 |
| `ErrTemplateBuiltIn` | 403 |
| `ErrDuplicate` / `ErrInUse` / `ErrTaskConflict` / `glossary.ErrDuplicate` / `biliutil.ErrAccountUIDDuplicate` / `biliutil.ErrInvalidCookiePath` | 409 |
| `ErrSessionNotReady` / `ErrNotLive` / `ErrNotRecording` / 能力不可用 | 409 |
| `archive.ErrSessionNotReady` / `archive.ErrArchiveMissing` / `archive.ErrConfigMissing` | 409 |
| `ErrQRLoginSessionExpired` | 410 |
| `ErrContentRejected` | 422 |
| `ErrRateLimited` | 429 |
| `ErrBilibiliAPI` / `ErrBiliLoginUpstream` / `ErrCookieMissing` | 502 |
| 其他 | 500 |

## 关键设计决策

- Gin 设置为 ReleaseMode
- WebSocket 使用 `checkWebSocketOrigin` 校验 Origin 头：同源或 localhost 允许，非同源拒绝并记录 warn 日志
- 场次详情接口额外返回文件列表（遍历本地目录）
- 导入接口使用 `multipart/form-data`，必填 `channel_id`、`title`、`media_file`
- 嵌入前端时使用 SPA 模式：静态文件直接返回，非 `/api/` 和 `/ws` 路径回退到 `index.html`
- 删除场次时先删除关联任务再删除场次
- 回顾模板端点分为全局（`/api/recap/templates`）和主播级别（`/api/channels/:id/recap-template`）
- 主播回顾模板端点返回三部分：global（全局模板）、channel（主播模板）、resolved（合并结果）
- Server 依赖注入包含 `recapTemplates *recap.TemplateStore`、`cookieAccounts *biliutil.CookieAccountStore`、可选 `notifyMgr`、以及 `archives *archive.Handler`（发布后归档）
- **配置端点统一模式**（recap/dashscope/asr-s3/archive）：GET 返回响应结构体（敏感字段回显为 `*_key_env`，密钥本身不回显，环境变量名为空时回退到 `Effective*` 默认值）；PUT 在 `publishMu` 写锁内修改 `s.cfg.*` → bumpConfigGen → refreshRuntimeStatus 刷新能力。带密钥的配置（dashscope/asr-s3/recap）额外实现 **key 环境变量改名的 secrets 迁移**：旧 env 名下已存的 secret 迁移到新 env 名（同时 `os.Setenv`/`os.Unsetenv`），清空为默认值时反向迁移；环境变量名校验拒绝非法 Go 标识符。配置更新点的并发安全经代际校验机制保护（见下节）。
- **publish.private_pub 规范化**(2026-07-08):全局配置段 PUT 把 `private_pub=0` 规范化为 viper 默认 `2`(公开),非法值(非 0/1/2)仍 400。理由:全局段没有"继承上层"语义(区别于频道级 `PublishPrivatePub` 用 0 表示"继承全局"),全局 0 无意义;且 publisher.go:62 的 fallback 把频道级 0 回落到本全局值,若全局也是 0 会原样发给 B 站专栏 API 导致发布失败。规范化既保证 GET/PUT round-trip 幂等,又堵住 publisher 收到 0 的路径(bug 报告 #2 根因;codex 审核指出的 BLOCKING)。
- **glossary import/json 双格式**(2026-07-08):handler `handleImportJSON` 把 raw body 传给 `glossary.ImportJSON`,后者先试 `GlossaryExport` 对象、失败回退裸数组 `[]GlossaryItem`。非法 JSON 经 `glossary.ErrInvalidJSON` 哨兵在 `writeError` 映射为 400(原走通用 500)。
- Cookie Account API 同时保留 `/api/bili/accounts` 兼容端点（list/update/delete）
- Cookie Account 创建/更新时调用 `biliutil.ValidateCookiePath` 校验 Cookie 文件路径在允许目录内，防止路径穿越
- 统计仪表板通过 `session.Store.DB()` 直接执行聚合查询，返回月度场次、主播排名和 ASR 费用趋势
- 通知测试端点 `POST /api/notify/test` 通过注入的 `notifyMgr` 发送测试通知
- 术语表端点分为全局（`/api/glossary/entries`、`/api/glossary/note`）和主播级别（`/api/channels/:id/glossary/entries`、`/api/channels/:id/glossary/note`），均支持 CRUD + 导入导出 + 批量操作
- 配置导出（`GET /api/config/export`）聚合所有配置为单个 JSON 附件：**6 个全局配置段**（recap_ai/publish/webdav/asr_s3/dashscope/archive，全段指针化，`omitempty` 统一 presence 语义）+ Secrets 实际值 + Channels 全量 + Glossary/Templates 全局+主播 + BiliAccounts 元数据（不含 Cookie 文件）。WebDAV/ASR S3 用专用导出 DTO（`WebDAVExportSection`/`ASRS3ExportSection`）**剔除明文密钥字段**（Password/AccessKeySecret），密钥统一走 Secrets 段，避免导出文件泄漏
- 配置导入（`POST /api/config/import?strategy=merge/overwrite`）两阶段原子化：**阶段一**把 6 段配置 + secrets 绑进同一 `runtimeconfig.WithTx` 事务（overwrite 用 `secrets.ClearTx` 清旧密钥），任一失败回滚返回 500、内存 cfg 与进程 env 不变；commit 成功后才提交内存 cfg + 进程 env。**阶段二**（仅 overwrite，核心事务成功后）清 glossary/templates/cookies。**持久化前复用各正式 update handler 的段内校验**（`validateImportedSections`：provider 白名单/URL/枚举/负数/env 名/timer 范围），非法值返回 400 不落盘，避免污染 `runtime_settings` 导致启动失败
- 配置导入的 WebDAV/ASR S3 managed tombstone：先回填 env 字段再用 effective env 判定 managed，覆盖 overwrite 下 env 改名且 bundle 缺新 secret 的场景（旧 env→新 env 改名时，必须用新 env 名查 bundle.Secrets）
- 配置导入时 BiliAccounts 仅导入元数据（UID/昵称/默认标记），需重新扫码登录恢复 Cookie
- 导入的 Secrets 自动 `os.Setenv` 使运行时立即生效；overwrite 策略先 `os.Unsetenv` 旧 env keys 再 set 新值，避免残留
- **两步式发现端点**（2026-07-02）：`discoverPreviewAll`（`POST /api/sessions/discover/preview`）调 `discoveries.PreviewAll` 返回 `{items: Result[]}`（200，每条带 `exists`）；`discoverExecute`（`POST /api/sessions/discover/execute`）解析 `{items: ExecuteItem[]}` 调 `discoveries.Execute` 返回 202。旧的 `discoverSessions`（`/api/sessions/discover`，一步式建场+入队）保留为抽屉「全部下载」回退

## 运行时状态代际校验（并发安全）

`Server` 内部通过 `configGen atomic.Uint64` + `runtimeMu sync.RWMutex` + `publishMu sync.RWMutex` 协调运行时状态（`*runtime.Status`）的并发更新与读取，修复了配置并发更新时 Probe 结果乱序覆盖的竞态。

**核心 helpers：**

| 方法 | 说明 |
|------|------|
| `currentRuntimeStatus()` | 在 `runtimeMu` 读锁下返回最新 `*runtime.Status`，所有 capability handler 读取状态均走此入口（不再直接访问 `s.runtimeStatus` 字段） |
| `setRuntimeStatus(status)` | 写锁下替换 `runtimeStatus` |
| `configSnapshot() (config.Config, uint64)` | 在 `publishMu` 读锁下返回 cfg 副本和当前 `configGen`（代际号） |
| `bumpConfigGen() uint64` | `runtimeMu` 写锁下 `configGen.Add(1)`，返回新代际号 |
| `refreshRuntimeStatus(cfgSnapshot, gen)` | 重新 `runtime.Probe(&cfgSnapshot)`，写锁下比对 `configGen.Load() > gen` 则丢弃过期快照（stale discard） |

**调用约定：**

- 所有配置更新点在 `publishMu` 写锁内修改 `s.cfg.*` 后调用 `bumpConfigGen()` 拿到新代际，再调用 `refreshRuntimeStatus(cfgSnapshot, gen)`：
  - `handleImportConfig`：6 段配置 + secrets 在同一 `runtimeconfig.WithTx` 事务内持久化（commit 成功后）→ 提交内存 cfg + 进程 env → `bumpConfigGen()` + `refreshRuntimeStatus`（cfgSnapshot 在事务前算好，避免持锁调 configSnapshot 与当前 Lock 冗余）
  - `updateSecret`：写入/删除 + `os.Setenv`/`os.Unsetenv` 后 `bumpConfigGen` + `configSnapshot` + `refreshRuntimeStatus`
  - `updatePublishConfig` / `updateRecapConfig` / `updateWebDAVConfig`：`bumpConfigGen` 后 `refreshRuntimeStatus`（统一消除原局部 clone 写回与 Probe 的双路径竞态）
- 各 capability handler（`submitASR`/`generateRecap`/`generateRecapPartial`/`uploadSession`/`fetchSession`/`publishSession`/`webDAVAvailable`）通过 `currentRuntimeStatus()` 读取最新状态，避免读取陈旧快照
- `runtimeHealth` 通过 `currentRuntimeStatus()` 返回当前状态

## 测试与质量

- `server_test.go`: 集成测试（使用内存 SQLite 和 fake 依赖）。覆盖范围：
  - 主播 CRUD（创建、列表、**单频道详情 GET（2026-07-08 新增路由）**、无效 body 拒绝、识别保存幂等性）
  - B 站 QR 码登录保存 Cookie 到主播
  - 直播状态检查、录制启停、重复录制拒绝
  - 运行时健康检查（含 ASR 模型和请求模式详情）
  - 推荐回顾模型列表（2026-07-15 起精简到 DeepSeek 2 个；`TestGetRecapModels` 精确集合+顺序断言）
  - ASR/Recap/Upload/Fetch/Publish 能力不可用拒绝
  - 任务 CRUD（创建、获取、取消、列表）
  - 配置导出/导入（merge/overwrite，7 段含 MCP）—— 详见 `config_export_test.go` 的 17 个回归用例
  - **回顾配置端点**（9 个）：getRecapConfig 响应、provider/baseURL/model/key 更新、空值兜底默认值（EffectiveProvider/BaseURL/Model）、key 生命周期、env 改名迁移 secret、非法 env 名拒绝、env 清空为默认值反向迁移、并发字段更新
  - **DashScope 配置端点**（9 个）：getDashScopeConfig 响应、字段更新、key 生命周期、非法 URL/负数 speaker_num/非法 env 名拒绝、env 改名迁移、env 清空为默认值迁移、并发 env 改名
  - **ASR S3 配置端点**（9 个）：getASRS3Config 响应、字段更新、secret 入库、key 生命周期、非法 endpoint/非法 public_url_prefix/非法 env 名拒绝、env 改名迁移、env 清空为默认值迁移
  - **配置代际校验**：`TestImportConfigRefreshesRuntimeStatus`（导入后状态刷新）、`TestRefreshRuntimeStatusAllowsNilStatus`（nil 状态短路）、`TestRefreshRuntimeStatusDiscardsStaleGeneration`（过期快照丢弃）、`TestUpdatePublishConfigRefreshesRuntimeStatusWithProbe`（Probe 后能力反映新配置）、`TestConcurrentConfigUpdatesRefreshLatestRuntimeStatus`（并发 PUT publish/recap 后最终状态一致）

- `auth_test.go`: 5 个测试用例，覆盖 admin token 认证中间件（X-Admin-Token / Bearer、`subtle.ConstantTimeCompare`、缺失/错误 token 拒绝、公开路由放行）。

## 相关文件清单

- `server.go` -- 路由注册、API 处理函数、错误映射、统计仪表板、术语表端点（CRUD+导入导出+批量操作）、回顾内容端点、局部回顾端点、Cookie Account（含 ValidateCookiePath 校验）、全局发布配置、WebDAV/DashScope/ASR-S3/archive 配置端点、回顾模板导入导出、QR 码登录端点、onboarding 引导端点、通知测试端点、诊断报告、WebSocket Origin 校验、配置导出/导入、archiveSession 端点、recap/regenerate 重新生成端点、两步式发现端点（discoverPreviewAll/discoverExecute）、**2026-07-21 新增 `POST /api/sessions/:sid/reset` 端点**（ASR 失败 reset 入口，调 `session.ResetFailedSession`，`writeSessionDetail` helper 抽取复用，writeError 4 个新 case 全部 409 Conflict）、运行时状态代际校验（`configGen`/`runtimeMu`/`configSnapshot`/`bumpConfigGen`/`refreshRuntimeStatus`/`currentRuntimeStatus`）
- `auth.go` -- admin token 认证中间件（X-Admin-Token / Bearer、`subtle.ConstantTimeCompare`）
- `config_export.go` -- 全量配置导出/导入处理器（ConfigExportBundle 7 段指针化含 MCP + WebDAVExportSection/ASRS3ExportSection/**MCPExportSection** 密钥剔除投影 DTO、handleExportConfig、handleImportConfig 两阶段事务化、validateImportedSections 持久化前校验、channelToUpsertInput；**2026-07-23 新增 MCP 段**:mcpToExport/mcpFromExport/mcpServerSecretKey 三个 helper,MCP 密钥(Builtin.BraveAPIKey/TavilyAPIKey + Servers Headers Authorization)随 Secrets 走,server 鉴权头用「下标+名」双键 `MCP_SERVER_{idx}_{NAME}_AUTHORIZATION` 防归一化碰撞）
- `config_export_test.go` -- 配置导出/导入回归测试（17 个用例：密钥不泄漏含 MCP、可缺段含 MCP omitempty、持久化到 DB 含 MCP、非法值拒绝、env 改名 tombstone、回滚、overwrite 替换 secrets、旧备份兼容、**2026-07-23 新增 MCP round-trip + 同名碰撞双键 + 旧 bundle 零回归**）
- `server_test.go` -- 集成测试（71 个测试函数，表驱动子测试展开后运行时用例数更多；含 fake 依赖实现 + 测试辅助函数、stats/dashboard 死锁回归测试、2026-07-20 新增 `TestListBiliSeries_WithChannelID_*` 三个用 `cookieCapturingRT` 截获请求 Cookie + 覆盖 `server.biliCreativeClient` 让硬编码 B 站 URL 走桩、**2026-07-21 新增 `TestResetSession_*` 四个**：Success/NotFound/StatusNotFailed/NonASRFailure，验证 reset 端点的成功路径与 3 个 409 错误分支）。handler 包函数口径总数 94（server_test 71 + config_export_test 17 + auth_test 5）。
- `auth_test.go` -- admin token 认证中间件测试（5 个测试函数）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-23 | Bug 修复 | **MCP 配置段纳入配置备份导入导出**(branch 主线,qoder 计划审核 Ready with fixes + 执行后复审)。**触发**:用户实测「配置备份」导出 JSON 不含 `mcp` 字段,换机器后 MCP 配置需手动重建(`docs/MCP配置导入导出缺失问题分析.md`)。**根因**:`ConfigExportBundle` 只有 6 段,MCP 段(2026-07-22 6 phase 新增)引入时漏更新 `config_export.go`。**方案**:用户选定**投影 DTO + 密钥走 Secrets**(仿 WebDAV/ASRS3 范式)。**改动**(`config_export.go` 单文件):① 新增 `MCPExportSection`/`mcpServerExport`/`mcpBuiltinExport` 投影 DTO;② 3 helper `mcpToExport`/`mcpServerSecretKey`/`mcpFromExport`(密钥 Builtin.BraveAPIKey/TavilyAPIKey + Servers Headers Authorization 随 Secrets 走,server 鉴权头双键 `MCP_SERVER_{idx}_{NAME}_AUTHORIZATION` 防碰撞);③ `ConfigExportBundle` 加 `MCP *MCPExportSection`(指针+omitempty);④ 导出填充 + 导入恢复(走 `MCPSectionDTO` 与 PUT 同构)+ manager.Reload 热重载。**测试**:config_export_test 11→**17**(+6:密钥不泄漏/omitempty/round-trip 完全可逆/同名碰撞双键/merge 持久化+密钥回填/旧 bundle 零回归)。handler 函数口径 87→**94**。**qoder 计划审核**(Qwen3.8-Max-Preview,Ready with fixes):3 Important(同名碰撞→双键方案;缺 round-trip 测试→新增;Headers nil vs {}→仅按需分配)+ 3 Minor 全部采纳。零回归:旧 bundle 无 mcp 段不碰 MCP(测试钉死)。 |
| 2026-07-21 | BUG 修复 | **新增 `POST /api/sessions/:sid/reset` 端点**(branch `fix/bug-fix-2026-07-20`,commit `61f3989` v6)。**触发**:配合 session `ResetFailedSession` 的「ASR 失败可 reset」恢复链——ASR 任务失败后 session 进入 `failed`,重提 ASR 返回 `409 status must be media_ready`,**无任何 UI/API 恢复入口**。**改动**:① 新增 `POST /api/sessions/:sid/reset` 路由 + `resetSession` handler,调 `session.ResetFailedSession`;② 抽取 `writeSessionDetail(w, s)` helper 统一 session 详情响应(消除 reset/getSession 等多处重复的 JSON 序列化);③ writeError 新增 4 个 case 全部映射 409 Conflict(`ErrResettableConditionFailed`/`ErrSessionNotFailed`/`ErrActiveTaskExists`/`ErrInvalidResetState`)。**测试**:handler +4(`TestResetSession_Success`/`_NotFound`/`_StatusNotFailed`/`_NonASRFailure`,覆盖成功路径与 3 个 409 错误分支),server_test.go 67→71,handler 函数口径 83→87。 |
| 2026-07-20 | 功能 | **listBiliSeries 加 ?channel_id= query**(branch `fix/streamer-publish-fields-2026-07-19`,codex 4 轮计划审核收敛)。**根因**:`listBiliSeries` 只用全局默认发布账号,主播抽屉 per-channel 文集下拉无法拉到该主播发布账号下的文集。**改动**:① 新增私有 helper `resolvePublishCookieForChannel(ctx, channelID)` 统一走 `CookieAccountStore.ResolveCookie(ctx, null, publishAccountID, "publish", fallback)` 三级链(与 `publisher.go:382` 完全一致);② channel_id 非空时取 channel 的 PublishAccountID(若 nil 走 level 2 默认)+ CookieFile(level 3 fallback);③ channel_id 空/缺席时完全等价现状(全局默认账号);④ listBiliSeries 内联的 `GetDefaultPublish + LoadCookie` 删除改调 helper。**配套**:`newTestServerWithDB(t)` 拆分(返回 `(*Server, *sql.DB)` 供需要 DB 引用的测试用)+ `newTestServerWithCookieAccounts(t, cookieDir)`(注入 cookieAccounts store,codex r17c MEDIUM #5)。**测试**:handler +3(`TestListBiliSeries_WithChannelID_UsesChannelPublishAccount`/`_NoAccount_FallsBackToDefault`/`_WithoutChannelID_UnchangedBehavior`),用自定义 `cookieCapturingRT RoundTripper` 截获请求 Cookie(codex r17b/r17d:httptest.Server.Client 不会自动接管硬编码 B 站 URL,需覆盖 `server.biliCreativeClient = &http.Client{Transport: rt}`)。 |
| 2026-07-19 | 功能 | **主播管理 ↔ 回顾管理·回放 解耦**(branch `fix/decouple-recap-replay-2026-07-18`,codex 计划 v1/v2 审核 + 代码审核)。**放开 channel_id 必填**:`downloadSessionByURL` / `importSession` 空 channel_id 时自动回退 `channel.UnassignedID`(不再 400);title 仍必填。**新端点** `POST /api/sessions/discover/preview-by-url`(body `{url, cookie_file?, title_prefix?}`,调 `discover.Preview`)。**List 调用方迁移**:`listChannels`/`discoverPreviewAll`/`handleOnboardingStatus`/`handleCookieStatus`/`config_export.go` 5 处改用 `channels.ListVisible`(过滤占位)。**测试**:handler 函数口径 75→80(+5:DownloadByURLNoChannelFallsBackToUnassigned/ImportSessionNoChannelFallsBackToUnassigned/ImportSessionStillRejectsEmptyTitle/DiscoverPreviewByURL/ListChannelsExcludesUnassigned)。 |
| 2026-07-18 | 文档 | **测试计数口径统一为函数口径**（`/init-project` 增量核对）：根 CLAUDE.md 模块索引 `handler` 行 98→75（对齐全表惯例 `grep -c "^func Test"` 函数口径，见 normalize 2026-06-21 校正先例）。handler CLAUDE.md `server_test.go` 条目运行时用例数 92→98（实测 `go test -v | grep "=== RUN"`，旧值 92 过时），并补注函数口径总数 75。代码与测试零改动，纯文档校正。07-15 changelog 的"运行时口径 75→98"保留为历史事实记录。 |
| 2026-07-15 | 优化 | **回顾模型预设精简 + 支持手动输入**（`e17fa9c`）：`recommendedRecapModels` 常量从 8 个（DeepSeek/OpenAI/其他 三组）精简到 **DeepSeek 2 个**（`deepseek-v4-flash` 快速 + `deepseek-v4-pro` 默认）。理由：前端原用 `HSelect`（原生 `<select>`）只能从预设选，后端却堆 8 个预设覆盖面仍不足；改为前端新增 `HCombobox`（input + datalist 组合框）支持手动输入任意模型名 + 下拉快捷选项，预设只需保留最常用的 2 个 DeepSeek。handler/路由/`RecapModelOption` 结构体不动，仅改常量内容。`TestGetRecapModels` 从"非空 + 含特定项"改为**精确集合 + 顺序断言**（锁死 2 个 DeepSeek）+ 反向断言已移除的 6 个模型（gpt-4o/gpt-4o-mini/qwen-plus/qwen-turbo/qwen-max/claude-sonnet-4）不再出现。handler 测试运行时口径 75→98（server_test.go 函数数仍 59，增量来自表驱动子测试展开 + 本轮断言细化）。codex 审核 2 轮 APPROVED。 |
| 2026-07-08 | 功能 + Bug 修复 | **① tools 配置端点**（`dfe7d23`）：新增 `GET/PUT /api/config/tools`（yt-dlp/rclone 路径 web 可编辑），参照 archive 模式，保存后 `refreshRuntimeStatus` 重新 Probe，前端 `onSaved` 重拉 runtime。handler 测试 +1（ToolsConfigRoundTrip）。② **Bug 报告核实修复**（branch `fix/bug-report-2026-07-08`）：glossary `ImportJSON` 双格式 fallback（对象→裸数组）+ `ErrInvalidJSON`→400；publish `private_pub` 全局段 `0` 规范化为默认 `2`（堵 publisher 收到 0 路径）；补 `GET /api/channels/:id` 路由（Store.Get 已存在仅未注册）。新增 7 测试（glossary 3 + handler 4 含 publish round-trip 幂等）。handler 测试总数 66→75（server_test.go 50→59）。 |
| 2026-07-05 | 功能 | **配置备份导入持久化到 runtime_settings**（`6a2bb18`）：导出 bundle 的 6 个全局配置段（recap_ai/publish/webdav/asr_s3/dashscope/archive）全段指针化统一 presence 判断；WebDAV/ASR S3 改用专用导出 DTO（`WebDAVExportSection`/`ASRS3ExportSection`）剔除明文密钥。导入路径两阶段事务化：阶段一把 6 段 + secrets 绑进同一 `runtimeconfig.WithTx`（overwrite 用新增 `secrets.ClearTx`），commit 成功后才提交内存 cfg 与进程 env；持久化前 `validateImportedSections` 复用各 update handler 的段内校验，非法值 400 不落盘。修正 webdav/asr_s3 的 managed tombstone（先回填 env 字段再用 effective env 判定）。新增 `config_export_test.go`（11 个回归用例）。handler 测试总数 55→66 |
| 2026-07-02 | 功能 | **两步式发现端点**（`83ef024`）：新增 `POST /api/sessions/discover/preview`（`discoverPreviewAll`，调 `discoveries.PreviewAll`，200 返回带 `exists` 标记的预览结果）和 `POST /api/sessions/discover/execute`（`discoverExecute`，解析 `{items: ExecuteItem[]}` 调 `discoveries.Execute`，202）。旧的 `POST /api/sessions/discover`（一步式）保留为抽屉「全部下载」回退 |
| 2026-06-24 | 修复 | **stats/dashboard 单连接自死锁修复**（`a651fec`）：`handleStatsDashboard` 此前循环内逐条 `db.QueryContext` 查库，在 `SetMaxOpenConns(1)` 下与未关闭的 rows 共用唯一连接，导致确定性超时。重构为复用 `session.GetDashboardStats`（单次查询），删除仅旧 handler 使用的 `sessionColumnExists` 及 `database/sql` import。新增带超时 ctx 的死锁回归测试（`TestStatsDashboardRouteDoesNotDeadlock`）与空库测试，server_test.go 50→52，handler 总测试数 55→57。HomeView 表格绑定对齐到新契约字段 `session_count`/`asr_hours` |
| 2026-06-23 | 功能 | (1) 新增归档端点：`POST /api/sessions/:sid/archive`（调用 `archives.CreateTask`，错误经 writeError 映射 archive.ErrSessionNotReady/ErrArchiveMissing/ErrConfigMissing→409）、`GET/PUT /api/config/archive`（auto_after_publish / cleanup_policy，PUT 校验 cleanup_policy 取值）。Server 依赖注入新增 `archives *archive.Handler`。(2) 新增 DashScope 配置端点（`GET/PUT /api/config/dashscope`）：响应回显 api_key_env（空则 EffectiveAPIKeyEnv 兜底），PUT 在 publishMu 锁内改字段→bumpConfigGen→refreshRuntimeStatus，含 key env 改名 secrets 迁移。(3) 新增 ASR S3 配置端点（`GET/PUT /api/config/asr-s3`）：同模式，secret 字段经 secrets store 管理，含 env 改名迁移。(4) 回顾配置端点补 EffectiveProvider/BaseURL/Model 空值兜底。新增 27 个测试函数（DashScope 9 + ASR S3 9 + recap 默认值/迁移 9）；server_test.go 22→50，handler 总测试数 27→55 |
| 2026-06-21 | 增量同步 | 测试计数校正：server_test.go 20→22（含 opus edit/remove 能力守卫用例），新增 auth_test.go（5 个，admin token 中间件）。handler 测试总数 24→27（grep 口径） |
| 2026-06-17 | 功能 | 新增 `POST /api/sessions/download-by-url`：按视频链接（BV 号等）+ 主播 ID 触发下载，受 `ReplayDownload` 能力守卫，错误经 writeError 自动映射（`worker.ErrTaskConflict`→409、`session.ErrInvalid`→400）；`handleUpdateRecapContent`、`discoverSessionGlossary` 加 `local_available` 守卫（本地已清理时拒绝并提示先 Fetch） |
| 2026-06-14 | 并发安全 | handler 修复配置并发更新时 Probe 结果乱序覆盖的竞态：Server 新增 `runtimeMu sync.RWMutex` + `configGen atomic.Uint64` 代际号 + helpers `currentRuntimeStatus`/`setRuntimeStatus`/`refreshRuntimeStatus(cfgSnapshot, gen)`/`configSnapshot()`/`bumpConfigGen()`；`refreshRuntimeStatus` Probe 完成后比对 `configGen.Load() > gen`，过期快照丢弃；所有配置更新点（handleImportConfig/updateSecret/updatePublishConfig/updateRecapConfig/updateWebDAVConfig）在 publishMu 写锁内 bump 后调用 refreshRuntimeStatus；updatePublishConfig 统一走 refreshRuntimeStatus（消除原局部 clone 写回与 Probe 双路径竞态）；各 capability handler（submitASR/generateRecap/uploadSession/fetchSession/publishSession/webDAVAvailable）改用 currentRuntimeStatus() 读取。新增 5 个测试函数：TestImportConfigRefreshesRuntimeStatus、TestRefreshRuntimeStatusAllowsNilStatus、TestRefreshRuntimeStatusDiscardsStaleGeneration、TestUpdatePublishConfigRefreshesRuntimeStatusWithProbe、TestConcurrentConfigUpdatesRefreshLatestRuntimeStatus。handler 测试数 15→20 |
| 2026-06-13 | 新增端点 | 新增 GET /api/config/recap/models 返回推荐回顾模型列表（recommendedRecapModels 常量，按 DeepSeek/OpenAI/其他 分组，含 deepseek/gpt/qwen/claude 共 8 个）；新增 RecapModelOption 类型与 getRecapModels handler；模型名仍支持自由输入，列表仅为前后端共享的快捷选项；新增 TestGetRecapModels 测试 |
| 2026-06-03 | 增量扫描 | 新增 `config_export.go`（ConfigExportBundle 导出结构体、handleExportConfig/handleImportConfig 处理器）；新增 GET /api/config/export 和 POST /api/config/import 路由；新增 GET/PUT /api/config/webdav 路由；导入支持 merge/overwrite 策略，overwrite 时调用 secrets.Clear/glossary.ClearAll/recapTemplates.ClearCustom/cookieAccounts.ClearAll；导入 BiliAccounts 使用 cookieAccounts.CreateImported 跳过路径校验；相关文件清单新增 config_export.go |
| 2026-05-18 | 清理 | 移除回顾内容 API 的 `bilibili` 字段和更新回顾时写 `_bilibili.txt` 的逻辑 |
| 2026-05-17 | 安全修复 | WebSocket Origin 校验：`CheckOrigin: true` 替换为 `checkWebSocketOrigin`（校验 Origin 头与 Host 匹配，localhost 互联允许，非同源拒绝）；Cookie Account 创建/更新时调用 `ValidateCookiePath` 路径穿越防护；错误映射新增 `biliutil.ErrInvalidCookiePath`（409） |
| 2026-05-17 | 增量更新 | 路由表补全：展开术语表基础 CRUD 端点（entries/note 共 10 个全局 + 10 个主播级）、新增 POST /api/notify/test 测试通知端点、补全 onboarding 端点、recap presets 端点、channels copy-config 端点、bili/accounts 兼容端点、tasks batch-retry 端点 |
| 2026-05-15 | 重大更新 | 新增 GET /api/stats/dashboard；新增 POST /api/sessions/:sid/recap-partial 与 recap-with-range；新增 Cookie Account 6 个 REST 端点和 QR Login save-account；新增模板导入导出端点（/api/recap/templates/export/import、/api/channels/:id/recap-template/export/import）；术语表 JSON/Markdown 导入导出端点已注册；回顾内容返回 suggested_terms；Server 注入 cookieAccounts 和 notifyMgr |
| 2026-05-14 | 重大更新 | 新增回顾模板端点（GET/PUT /api/recap/templates 全局 CRUD + GET/PUT/DELETE /api/channels/:id/recap-template 主播 CRUD）；新增 recap.ErrTemplateNotFound/ErrTemplateBuiltIn 错误映射；Server 依赖注入新增 recapTemplates；新增 QR 码登录端点（POST/GET/POST/DELETE /api/bili/login/qrcode）；新增 biliutil 错误映射 |
| 2026-05-12 | 更新 | 新增 `GET/PUT /api/config/publish` 全局发布配置端点 |
| 2026-05-08 | 更新 | 错误映射新增 ErrPublishNotEnabled（409） |
| 2026-05-07 | 重大更新 | 大幅扩展术语表端点、新增回顾内容查看端点、新增 glossary 错误映射 |
| 2026-05-04 | 更新 | 新增 /api/secrets、/api/glossary 端点 |
| 2026-05-03 | 更新 | 新增 delete 端点、publisher 错误映射 |
| 2026-05-01 | 更新 | 新增集成测试、发布端点、SPA 前端支持 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
