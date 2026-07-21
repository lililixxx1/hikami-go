[根目录](../../CLAUDE.md) > **internal/session**

# internal/session -- 场次 CRUD

## 模块职责

管理场次（session）的创建、查询、去重和删除。支持三种来源类型的场次创建：直播录制、回放下载、手动导入。每种来源有独立的 ID 生成规则和 slug 格式。直播场次支持失败后重试（重置状态），下载场次支持幂等去重。

## 入口与启动

- **入口文件**: `session.go`
- **核心类型**: `Store`

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewStore(db)` | 创建 Store 实例 |
| `CreateLive(ctx, input)` | 创建直播场次；同 `(channel, 分钟槽)` UNIQUE 冲突时返回包装了 `ErrAlreadyLive` 的错误（不再自动复用/重置已有 session） |
| `CreateDownload(ctx, input)` | 创建下载场次，幂等（返回 `(session, created, error)`） |
| `CreateImport(ctx, input)` | 创建导入场次 |
| `List(ctx)` | 列出所有场次（按创建时间倒序） |
| `Get(ctx, id)` | 按 ID 获取场次 |
| `GetBySource(ctx, channelID, sourceType, sourceID)` | 按来源获取场次 |
| `ActiveLiveForChannel(ctx, channelID)` | 获取主播当前活跃的直播场次（状态为 recording/discovered/downloading/importing） |
| `Delete(ctx, id)` | 删除单个场次（未找到返回 ErrNotFound） |
| `DeleteFailed(ctx)` | 批量删除所有失败场次，返回删除数量 |
| `SetLocalAvailable(ctx, sessionID, available)` | 标记本地产物是否可用（上传清理后置 `false`、Fetch 取回后置回 `true`；未找到返回 ErrNotFound） |
| `SetArchivedAt(ctx, sessionID, archivedAt)` | 标记场次已归档到 WebDAV 的时间戳。归档任务不推进 session 主状态（保持 `published`），仅写 `archived_at` 并清空 `last_error`（归档失败后重试成功场景）；未找到返回 ErrNotFound |
| `GetStats(ctx)` | 返回 `SessionStats`（总场次/回顾数、按月计数、Top 主播排行），用于 `/api/stats/overview`、`/api/stats/cost` |
| `GetDashboardStats(ctx)` | 返回 `DashboardData`（按月/按主播/成本趋势/弹幕 Top/回顾/发布计数），单次查询聚合；`handler.handleStatsDashboard` 复用本方法（`a651fec`），避免循环内逐条查库在 `SetMaxOpenConns(1)` 下自死锁 |
| `ResetFailedSession(ctx, sessionID)` | **2026-07-21 新增**。把 ASR 失败的 session 从 `failed` 重置回 `media_ready`，作为 UI/API 的「重置失败场次」恢复入口。5 个守卫（session 存在 / status=failed / local_available=1 / current_task 类型=asr / 无 pending/running task）合并进 UPDATE 的 WHERE 子句，SQLite 单连接下原子执行（消除 check-then-act 竞态）。RowsAffected=0 时二次查询区分 `ErrNotFound`/`ErrSessionNotFailed`/`ErrActiveTaskExists`。**不删任何 task**（保留历史供审计），**保留 publish_target/published_at**。4 个哨兵：`ErrResettableConditionFailed`（统一覆盖 5 守卫）、`ErrSessionNotFailed`、`ErrActiveTaskExists`、`ErrInvalidResetState` |

## 关键依赖与配置

- 依赖 `internal/state` 获取场次初始状态常量和状态值
- 唯一约束去重: `(channel_id, source_type, source_id)` 和 `(channel_id, slug)`

## 数据模型

**Session 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string PK | 格式: `{channelID}_{sourceType}_{slugTime}` |
| `slug` | string | 目录名格式: `live_20060102_150405` / `{bv号}` / `import_20060102_150405` |
| `channel_id` | string FK | 主播 ID |
| `source_type` | string | `live_record` / `download` / `import` |
| `source_id` | string | 来源标识（BV号、live-xxx、import-xxx） |
| `title` | string | 场次标题（各类型有默认值） |
| `status` | string | 聚合状态（由 state 模块管理） |
| `local_available` | bool | 本地目录是否可用（上传 `all` 清理策略删除本地目录后置 `false`，`Fetch` 取回成功后置回 `true`；驱动 glossary/recap/publisher 守卫） |
| `started_at`, `ended_at` | string | 时间范围 |
| `uploaded_at`, `published_at` | string | 上传/发布时间 |
| `archived_at` | string | 归档到 WebDAV 的时间戳（archive 任务成功后写入，不推进主状态） |
| `publish_target` | string | 发布目标标识（如 `draft:12345` 或 dynID） |
| `last_error` | string | 最近失败原因 |

**ID 生成规则：**
- 直播: `{channelID}_live_{YYYYMMDD_HHMMSS}`
- 下载: `{channelID}_download_{sanitized_sourceID}`
- 导入: `{channelID}_import_{YYYYMMDD_HHMMSS}`

**默认标题：**
- 直播: `B站直播`
- 下载: 使用 `sourceID`
- 导入: `手动导入`

**CreateLive 幂等与冲突处理（`d7a1346` 下播竞态修复后）：**
- 正常插入成功即返回新 session。
- 约束冲突（`isConstraintViolation`：消息含 "constraint failed"/"UNIQUE constraint"）时，用一次 `Get(id)` 查目标 session 是否真实存在来区分 UNIQUE 与 FK：
  - **目标 session 存在 → 同 `(channel, 分钟槽)` UNIQUE 命中**：返回 `fmt.Errorf("%w: %s", ErrAlreadyLive, id)`，**不再复用或重置**。
  - **目标 session 不存在 → 其它约束（如 FK：channel 不存在）**：原样返回底层错误。
- **历史行为变更（重要）：** 旧版会在冲突且已有记录为 `failed` 时把它重置回 `discovered` 复用——这是下播竞态的放大器（live_check 把 failed 拉回 discovered 后，新录制任务把状态污染到 recording），已移除。竞态现靠同槽 UNIQUE 精确防护，不依赖频道级白名单扩展（后者会致该频道发布过一场后永久禁录，已由 codex 审核回退）。

**`ErrAlreadyLive`：** 哨兵错误（`"live session already exists for this slot"`），标识同槽重复创建。`live_record.Start` 将其映射为 `ErrAlreadyRecording`，使 cron 的 `CheckAndStartAll` 走既有兜底分支静默返回。

## 测试与质量

- `session_test.go`: 共 49 个测试函数，覆盖：
  - `CreateLive`: 成功创建、默认标题、默认时间、缺少 channel_id 拒绝、无效 room_id 拒绝、**同槽 UNIQUE 冲突返回 `ErrAlreadyLive`（不再复用/重置）**、**FK（channel 不存在）错误不被误判为已存在**、ID 格式
  - **`ResetFailedSession`（2026-07-21 新增 9 个测试）**：成功重置 / status 非 failed / local_available=0 / 非 ASR 失败（current_task 类型不符）/ 存在 pending/running task / session 不存在 / 空 taskID / task 已删除 / **v7 原子守卫（`TestResetFailedSession_ActiveTaskAtomicGuard` 验证 active task 检查合并进 WHERE 子句的原子性）**
  - `CreateDownload`: 成功创建、重复去重、缺少 source_id 拒绝、默认标题、slug 回退
  - `CreateImport`: 成功创建、默认标题、有/无结束时间
  - `Get` / `GetBySource`: 成功 / 未找到
  - `List`: 空列表、创建时间倒序
  - `ActiveLiveForChannel`: 有活跃 / 无活跃
  - `SetLocalAvailable` / `SetArchivedAt`: 成功设置 / 未找到
  - `GetStats` / `DeleteFailed` 等统计与批量删除路径
  - `sanitizeSlug`: 多种输入验证

## 相关文件清单

- `session.go` -- 唯一源文件（含 `ErrAlreadyLive` 哨兵、`isConstraintViolation` helper、`ResetFailedSession` 方法 + 4 个 reset 错误哨兵）
- `session_test.go` -- 单元测试（49 个测试函数）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-07-21 | 功能/BUG 修复 | **新增 `ResetFailedSession` 方法**(branch `fix/bug-fix-2026-07-20`,commit `61f3989` v6 + `add3b51` v7)。**触发**:ASR 任务失败后 session 进入 `failed` 状态,重提 ASR 返回 `409 status must be media_ready`,**无任何 UI/API 恢复入口**——只能直接改 DB + 重启服务。**v6**:5 守卫(session 存在 / status=failed / local_available=1 / current_task 类型=asr / 无 pending/running task)+ UPDATE 的 WHERE status='failed' 二次校验 + RowsAffected 检查;不删 task(保留审计);保留 publish_target/published_at。**v7 修订**(r20 HIGH):把 active task 检查从独立 SELECT 合并到 UPDATE 的 WHERE 子句(`NOT EXISTS pending/running task`),SQLite 单连接下原子执行,消除 check-then-act 竞态;RowsAffected=0 时二次查询区分 ErrNotFound/ErrSessionNotFailed/ErrActiveTaskExists。4 个错误哨兵:`ErrResettableConditionFailed`(统一覆盖 5 守卫)/`ErrSessionNotFailed`/`ErrActiveTaskExists`/`ErrInvalidResetState`。新增 9 个测试(8 个守卫路径 + 1 个 v7 原子守卫),session_test.go 40→49。 |
| 2026-06-27 | 修复 | **下播竞态导致非法状态转换**（`d7a1346`）：`CreateLive` 同 `(channel, 分钟槽)` UNIQUE 冲突时改为返回包装了 `ErrAlreadyLive`（新增哨兵 `"live session already exists for this slot"`）的错误，**移除旧的 failed→discovered 自动重置复用逻辑**（该复用是 live_check 误触发复用旧 session 的放大器，把 failed 拉回 discovered 后新录制任务污染状态机到 recording）。新增 `isConstraintViolation` helper，对 `constraint failed`/`UNIQUE constraint` 错误用 `Get(id)` 查存在性区分 UNIQUE 与 FK（避免 FK 错误被误判为已存在）。`live_record.Start` 将 `ErrAlreadyLive` 映射为 `ErrAlreadyRecording` 让 cron 静默兜底。竞态现靠同槽 UNIQUE 精确防护，不依赖频道级白名单扩展（后者致该频道永久禁录，已由 codex 审核回退）。session_test.go 38→40 |
| 2026-06-17 | 更新 | 新增 `SetLocalAvailable(ctx, sessionID, available)`：上传 `all` 清理策略删除本地目录后置 `false`、`Fetch` 取回成功后置回 `true`，驱动 glossary/recap/publisher 守卫 |
| 2026-05-17 | 修复 | GetStats 返回 (StatsOverview, error) 正确传播所有查询错误，不再静默忽略 |
| 2026-05-03 | 更新 | 新增 Delete、DeleteFailed 方法 |
| 2026-05-02 | 更新 | CreateLive 支持失败重试、新增 SetPublishTarget、新增 session_test.go（22 个用例） |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
