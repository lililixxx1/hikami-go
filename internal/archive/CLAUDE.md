[根目录](../../CLAUDE.md) > **internal/archive**

# internal/archive -- 发布后 WebDAV 归档（状态旁路任务）

## 模块职责

在专栏发布成功后，将场次目录复制到 WebDAV 远端并按策略清理本地文件。与 `internal/upload`（手动上传，从 `asr_done`/`recap_done` 出发，成功后推进到 `uploaded`）**根本差异**在于：

> **archive 是「状态旁路任务」**——它从 `published` 出发，成功后**不**调用 `states.Apply`、不发任何 Event，场次主状态全程保持 `published`，仅写 `archived_at` 时间戳。

为保证「不改 session.Status」这一约束，`HandleTask` 自身不调 `states.Apply`；失败兜底则由 `worker` 的状态旁路机制处理——`Register` 时声明 `worker.WithBypassFailState()`，worker 在失败路径据此调用 `SetFailSessionStateFn(..., bypassState=true)`，`cmd/hikami` 的回调收到后仅写 `last_error`，否则 `EventTaskFailed` 的全局特判会把 `published` 降级为 `failed`，丢失 UI 已发布入口（设计 4.3，状态旁路任务设计；原计划文档已随仓库重建清理）。

## 入口与启动

- **入口文件**: `archive.go`
- **任务类型常量**: `TaskType = "archive"`
- **核心类型**: `Handler`（通过 `NewHandler` 创建，`Register` 注册到 worker Pool）

## 对外接口

| 方法 | 说明 |
|------|------|
| `NewHandler(cfg, sessions, states, copier, deleter)` | 创建 Handler。`copier`/`deleter` 复用 `upload.NewConfiguredCopier` / `NewConfiguredDeleter` 工厂产物，保证归档与手动上传走同一套 WebDAV/rclone 后端 |
| `Register(pool)` | 注册 `archive` 任务处理器到 worker Pool |
| `CreateTask(ctx, pool, sessionID)` | 校验前置条件并入队归档任务 |
| `HandleTask(ctx, task, reporter)` | 执行归档：复制本地目录到 WebDAV → 写 `archived_at`（清 `last_error`）→ 清理 |

**错误哨兵：**

| 错误 | 含义 | HTTP 映射 |
|------|------|-----------|
| `ErrSessionNotReady` | 状态不是 `published` | 409 |
| `ErrArchiveMissing` | 本地场次目录不存在或非目录 | 409 |
| `ErrConfigMissing` | WebDAV 远端未配置 | 409 |

**API 端点：**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/sessions/:sid/archive` | 手动归档（自动归档失败时的手动重试入口） |
| GET | `/api/config/archive` | 获取归档配置 |
| PUT | `/api/config/archive` | 更新归档配置 |

## 关键设计决策

- **不推进主状态**：`HandleTask` 全程不调 `states.Apply`、不发 Event，`session.Status` 保持 `published`；归档完成仅 `sessions.SetArchivedAt`（同时清空历史 `last_error`，覆盖「归档失败后重试成功」场景）。
- **不加入 worker 自动重试**（`worker.go` 的 `nonRetryableTypes` 含 `archive`）：归档失败通常是 WebDAV 不可达/目录竞态，立即重试大概率仍失败；与 `publish` 同策略，失败后由用户手动 `POST /api/sessions/:sid/archive` 重试。注意 `nonRetryableTypes`（不自动重试）与「状态旁路任务」（失败不降级状态）是**两套独立机制**，archive 同时满足两者。
- **失败兜底走 worker 旁路机制**（设计 4.3）：`Register` 声明 `worker.WithBypassFailState()`；worker 失败路径据此在 `SetFailSessionStateFn(..., bypassState=true)` 透传旁路标志，`cmd/hikami` 的回调据此仅写 `last_error` 不降级状态，取代原先对 `archive`/`upload` task.Type 的硬编码特判。
- **与 upload 互斥**：`CreateTask` 拒绝同 session 活跃的 `upload` 任务——两者操作同一场次目录与同一 WebDAV 目标，并发复制/删除会竞态（一方 `cleanup=all` 删目录时另一方可能复制到一半失败）。反之 upload 侧亦对 archive 做对称互斥检查。
- **WebDAV 后端复用而非重写**：直接复用 `upload.Copier` / `upload.Deleter` 接口与 `NewConfiguredCopier` / `NewConfiguredDeleter` 工厂（native WebDAV 或 rclone 由 `WebDAVConfig.NativeConfigured()` 自动选择）。
- **清理复用共享函数**：归档成功后调用 `upload.CleanupSession(...)`（派发到 `cleanupTempShared`/`cleanupGeneratedShared`/`cleanupAllShared`），以 `guardStatus=published` 区分守卫态（upload 调用时传 `uploaded`）；`cleanupAllShared` 在执行前再次确认状态仍为 `guardStatus`，覆盖「归档期间 session 状态被并发变更而偏离 guardStatus」的竞态。
- **后端类型启动时固定**：`nativeWebDAV` 与 `copier`/`deleter` 在 `NewHandler` 时按启动时 `cfg.WebDAV` 固定；运行时改 WebDAV 后端类型需重启服务（与 `upload.Handler` 同一既有约束，属架构级限制）。

## 自动归档触发

`cmd/hikami/main.go` 在 `publish` 任务的 `EventPublishSucceeded` 回调中，按 `archive.auto_after_publish` 决定是否自动 `archiveHandler.CreateTask`：

- 仅当 WebDAV 能力可用时触发（否则 `slog.Warn` 跳过）；
- 自动归档提交失败仅记日志，不阻断已完成的发布。

## 清理策略

归档成功后根据 `archive.cleanup_policy` 执行清理（枚举与 `upload.cleanup_policy` 同）：

| 策略 | 说明 |
|------|------|
| `none` | 不清理（默认） |
| `temp` | 删除 ASR 临时公开音频（`asr/audio.public.json`）和远端临时对象（`asr_temp.rclone_remote` + 路径） |
| `generated` | 删除可重新生成的中间文件（`asr/` 目录），保留 `raw/`、`package/`、`recap/` |
| `all` | 再次确认仍为 `published` 后删除整个本地场次目录，置 `local_available=false`；再次编辑专栏需先 `Fetch` 取回 |

清理是 best-effort：归档（复制）已成功，删除失败仅记 `slog` 不阻断。`all` 策略下若 `RemoveAll` 成功而 `SetLocalAvailable(false)` 失败，会出现「目录已删但 UI 仍认为 `local_available=true`」的中间态，依赖各处 `local_available` 守卫在读目录时报错兜底。

## 目标路径计算

`archiveTarget` 镜像 `upload.uploadTarget` 的 native/rclone 分支，避免混配时路径错误：

- **native WebDAV**：`{channelID}/{slug}`（gowebdav 相对远端根）
- **rclone**：`{rclone_remote}{base_path}/{channelID}/{slug}`

## 关键依赖与配置

- 复用接口：`upload.Copier`、`upload.Deleter`（不重写 WebDAV 逻辑）
- 依赖：`internal/config`（`cfg.Archive`、`cfg.WebDAV`、`cfg.OutputRoot`、`cfg.ASRTemp`）、`internal/session`（`SetArchivedAt`、`SetLocalAvailable`、`Get`）、`internal/state`（`StatusPublished`）、`internal/upload`（`Copier`/`Deleter`/`TaskType`）、`internal/worker`（`Pool`/`Task`/`Reporter`/`Store`）
- 配置块 `archive`（见 `internal/config` 的 `ArchiveConfig`）：`auto_after_publish`（bool，默认 false）、`cleanup_policy`（none/temp/generated/all，默认 none）

## 测试与质量

- `archive_test.go`: 13 个测试用例，覆盖：
  - **CreateTask**: 成功（`TestCreateTaskSuccess`）、错误状态（`TestCreateTaskWrongStatus`，非 published）、目录缺失（`TestCreateTaskDirMissing`）、WebDAV 未配置（`TestCreateTaskWebDAVNotConfigured`）、活跃 archive 冲突（`TestCreateTaskActiveArchiveConflict`）、活跃 upload 互斥（`TestCreateTaskActiveUploadConflict`）
  - **HandleTask**: 成功且不推进状态（`TestHandleTaskSuccessDoesNotAdvanceState`）、复制失败返回错误（`TestHandleTaskCopyFailureReturnsErr`）、错误状态（`TestHandleTaskWrongStatus`）、`all` 清理删目录 + 置 `local_available=false`（`TestHandleTaskCleanupAllRemovesDirAndSetsLocalAvailable`）、`all` 清理在状态回退时跳过（`TestHandleTaskCleanupAllSkipsWhenStatusReverted`）、`generated` 清理仅删 asr 目录（`TestHandleTaskCleanupGeneratedRemovesAsrOnly`）
  - **目标路径**: rclone 分支（`TestArchiveTargetRclone`）、native 分支（`TestArchiveTargetNative`）

## 相关文件清单

- `archive.go` -- Handler、`CreateTask`、`HandleTask`、状态旁路逻辑、清理策略（cleanupTemp/cleanupGenerated/cleanupAll）、目标路径计算
- `archive_test.go` -- 单元测试（13 个用例）

## 变更记录 (Changelog)

