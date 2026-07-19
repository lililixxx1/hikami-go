# API 路由与通知事件

> 本文件由根 CLAUDE.md 拆分而来，作为 AI 上下文补充文档。

## 路由表

### 系统与引导 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/onboarding/status` | 引导向导状态（needed/has_tools/has_keys/has_channels） |
| POST | `/api/onboarding/dismiss` | 跳过引导向导 |

### 统计与诊断 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/diagnostic/report` | 生成诊断报告（含磁盘使用、Cookie 过期警告、最近失败任务、任务摘要） |
| GET | `/api/stats/overview` | 总览统计 |
| GET | `/api/stats/cost` | 费用估算统计 |
| GET | `/api/stats/dashboard` | 专家模式统计仪表板（月度场次、主播排名、ASR 费用趋势） |

### 配置 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/config/publish` | 获取全局发布配置 |
| PUT | `/api/config/publish` | 更新全局发布配置 |
| GET | `/api/config/recap` | 获取回顾 AI 配置（base_url/model/max_tokens/max_continuations/timeout_seconds） |
| PUT | `/api/config/recap` | 更新回顾 AI 配置（同步 RuntimeStatus.RecapModel） |
| GET | `/api/config/recap/models` | 推荐回顾模型列表（按厂商分组的快捷选项，模型名仍支持自由输入） |
| GET | `/api/config/dashscope` | 获取 DashScope 配置（api_key_env 空时回退 EffectiveAPIKeyEnv 默认值） |
| PUT | `/api/config/dashscope` | 更新 DashScope 配置（字段校验 + key env 改名的 secrets 迁移，同步 RuntimeStatus） |
| GET | `/api/config/asr-s3` | 获取 ASR S3 配置（access_key_env 空时回退 EffectiveAccessKeyEnv 默认值） |
| PUT | `/api/config/asr-s3` | 更新 ASR S3 配置（字段校验 + secret 改名的 secrets 迁移，同步 RuntimeStatus） |
| GET | `/api/config/archive` | 获取归档配置（auto_after_publish / cleanup_policy） |
| PUT | `/api/config/archive` | 更新归档配置（cleanup_policy 取值校验 none/temp/generated/all） |
| GET | `/api/config/webdav` | 获取 WebDAV 配置（url/username/base_path/remote/password_env/password_set） |
| PUT | `/api/config/webdav` | 更新 WebDAV 配置（支持原生 WebDAV URL 和 rclone remote，同步 RuntimeStatus） |
| GET | `/api/config/export` | 全量配置导出（JSON 附件下载，含 6 个全局配置段 recap_ai/publish/webdav/asr_s3/dashscope/archive + Secrets/Channels/Glossary/Templates/BiliAccounts；WebDAV/ASR S3 用专用 DTO 剔除明文密钥） |
| POST | `/api/config/import` | 全量配置导入（?strategy=merge/overwrite）：6 段配置 + secrets 在同一事务持久化到 runtime_settings，commit 成功后才提交内存 cfg 与进程 env；持久化前复用各 update handler 的段内校验，非法值 400 不落盘 |

### 来源/下载/导入 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/sessions/discover` | 为所有已配置 `replay_source_url` 的主播自动发现回放并排队下载（一步式：发现+下载） |
| POST | `/api/sessions/discover/preview` | 两步式发现·第一步：列出所有频道可发现的回放，不建场次不入队；每条返回 `exists` 标记（是否已建过 download 场次），供前端标注「已处理」 |
| POST | `/api/sessions/discover/preview-by-url` | URL 驱动独立发现预览（2026-07-19 解耦新增）：body `{url, cookie_file?, title_prefix?}`，不绑定主播表，结果归 `_unassigned`；cookie 留空时自动使用默认登录账号（v3 拆双 helper：URL 模式走 `resolveURLCookie`，账号池 cookie 加密场景自动解密 + 写明文临时文件给 yt-dlp） |
| POST | `/api/sessions/discover/execute` | 两步式发现·第二步：body `{items: ExecuteItem[]}`（前端勾选项），按列表建 download 场次并入队；不重跑 yt-dlp，复用 `CreateDownload` 幂等去重 |
| POST | `/api/sessions/download` | 按 `session_id` 重跑下载任务 |
| POST | `/api/sessions/download-by-url` | 按视频链接（BV 号等）+ `channel_id` 创建下载场次并入队；受 `ReplayDownload` 能力守卫；同 BV 重复返回 409 |
| POST | `/api/sessions/import` | multipart 上传本地媒体文件导入场次 |
| POST | `/api/sessions/:sid/upload` | 上传归档到 WebDAV/S3 |
| POST | `/api/sessions/:sid/fetch` | 从 WebDAV 取回本地目录（取回后 `local_available` 置 true） |
| POST | `/api/sessions/:sid/publish` | 发布 B 站专栏 |
| POST | `/api/sessions/:sid/archive` | 手动归档已发布场次到 WebDAV（自动归档失败时的手动重试入口；状态必须为 published；错误 archive.ErrSessionNotReady/ErrArchiveMissing/ErrConfigMissing→409） |

### 场次与回顾 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/sessions/:sid/recap/generate` | 生成完整回顾（`local_available=false` 时返回 409，提示先 Fetch） |
| POST | `/api/sessions/:sid/recap/regenerate` | 重新生成整场回顾（覆盖本地 md，不碰 B站；仅 recap_done/published；任务带 BypassFailState，失败不降级主状态） |
| POST | `/api/sessions/:sid/recap-partial` | 按 `start_time`/`end_time` 生成指定时间段回顾 |
| POST | `/api/sessions/:sid/recap-with-range` | `recap-partial` 兼容别名 |
| GET | `/api/sessions/:sid/recap` | 获取回顾内容，包含 `suggested_terms` 术语建议 |
| PUT | `/api/sessions/:sid/recap/content` | 更新回顾 Markdown 内容（`local_available=false` 时拒绝，避免重建孤立目录） |
| POST | `/api/sessions/:sid/glossary/discover` | 对指定场次执行 AI 术语发现（`local_available=false` 时返回 409，提示先 Fetch） |

### 任务 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tasks/:id` | 任务详情（含 friendly_error 和 auto_retry 信息） |
| POST | `/api/tasks/batch-retry` | 批量重试失败任务 |
| DELETE | `/api/tasks/failed` | 批量删除失败任务 |

### 主播与频道 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/channels/:id/copy-config` | 复制主播配置到其他主播 |
| POST | `/api/channels/:id/discover/preview` | 回放发现预览 |
| GET | `/api/cookies/status` | 所有主播 Cookie 状态检查（含过期检测） |

### Cookie Account 与 QR Login API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/bili/login/qrcode` | 创建 B 站 QR 码登录会话 |
| GET | `/api/bili/login/qrcode/:session_id` | 轮询 QR 码登录状态 |
| POST | `/api/bili/login/qrcode/:session_id/save` | 将扫码 Cookie 保存到主播下载/发布 Cookie 配置 |
| POST | `/api/bili/login/qrcode/:session_id/save-account` | 将扫码 Cookie 保存为全局 Cookie Account |
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

### 回顾模板与预设 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/recap/templates` | 列出全局回顾模板 |
| PUT | `/api/recap/templates` | 新增/更新全局回顾模板 |
| GET | `/api/recap/templates/export` | 导出全局回顾模板 JSON |
| POST | `/api/recap/templates/import` | 导入全局回顾模板 JSON |
| GET | `/api/recap/presets` | 列出内置回顾模板预设（粉丝向精修/正式详实/粉丝向/简洁摘要/弹幕聚焦） |
| GET | `/api/channels/:id/recap-template` | 获取主播回顾模板（含全局/主播/合并结果） |
| PUT | `/api/channels/:id/recap-template` | 新增/更新主播回顾模板 |
| DELETE | `/api/channels/:id/recap-template` | 删除主播回顾模板（回退到全局） |
| GET | `/api/channels/:id/recap-template/export` | 导出主播回顾模板 JSON |
| POST | `/api/channels/:id/recap-template/import` | 导入主播回顾模板 JSON |

### 术语表 API

| 方法 | 路径 | 说明 |
|------|------|------|
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
| GET | `/api/glossary/candidates` | 列出全局术语发现候选（支持 ?status=pending/approved/rejected/all） |
| POST | `/api/glossary/candidates/:cid/approve` | 审批通过候选（自动写入 glossary_entries） |
| POST | `/api/glossary/candidates/:cid/reject` | 拒绝候选 |
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
| GET | `/api/channels/:id/glossary/candidates` | 列出主播术语发现候选 |
| POST | `/api/channels/:id/glossary/candidates/batch-approve` | 批量审批通过主播术语候选 |
| POST | `/api/channels/:id/glossary/candidates/batch-reject` | 批量拒绝主播术语候选 |

### 通知与测试 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/notify/test` | 测试通知配置（发送测试通知） |

### 通知事件

| 事件 | 触发位置 | 说明 |
|------|----------|------|
| `record_start` | `live_record.Manager.HandleTask` | 直播录制开始 |
| `record_stop` | `live_record.Manager.Stop` | 手动停止直播录制 |
| `task_failed` | `worker.Pool.fail` | 任务执行失败 |
| `recap_done` | `recap.Handler.HandleTask` | 回顾生成完成 |
| `publish_done` | `publisher.Handler.HandleTask` | 专栏草稿/发布完成 |
