[根目录](../../CLAUDE.md) > **internal/worker**

# internal/worker -- 后台任务池、WebSocket 广播与友好错误映射

## 模块职责

提供 goroutine 任务池、任务 CRUD、Hub 广播和 WebSocket 进度推送。所有异步任务（下载、录制、标准化、ASR、回顾、上传、发布等）都通过本模块调度和执行。提供友好错误映射，将原始错误信息转换为中文消息和操作建议。

## 入口与启动

- **入口文件**: `worker.go`, `task.go`, `hub.go`, `errors.go`
- **核心类型**: `Pool`, `Store`, `Hub`, `Task`, `FriendlyError`

## 对外接口

### Pool（任务池）

| 方法 | 说明 |
|------|------|
| `NewPool(store, hub, workerCount, cfg)` | 创建任务池 |
| `Register(taskType, handler, opts...)` | 注册任务处理器，可传 `WithBypassFailState()` 标记为状态旁路任务（失败不降级主状态，设计 4.3） |
| `bypassFailState(taskType)` | 查询某任务类型是否注册为旁路任务（替代原先对 upload/archive 的硬编码类型特判） |
| `Task.BypassFailState` / `CreateInput.BypassFailState` | 任务实例级 bypass（v34 加列 `bypass_fail_state`）：重新生成回顾等非推进型任务置 true，失败不降级主状态；`syncSessionState` 取 `task.BypassFailState \|\| 类型级`（OR） |
| `Start(ctx, workerCount)` | 启动 worker goroutine，恢复中断任务 |
| `Stop()` | 停止所有 worker |
| `Enqueue(ctx, input)` | 创建并排队任务 |
| `Retry(ctx, id)` | 重试失败任务（attempt+1） |
| `BatchRetry(ctx, ids)` | 批量重试多个失败任务 |
| `Cancel(ctx, id)` | 取消 pending/running 任务 |
| `SetFailSessionStateFn(fn)` | 设置任务失败时同步 session 状态的回调（fn 新增 `bypassState bool` 参数，旁路任务仅写 `last_error` 不降级状态） |
| `SetNotifyManager(m)` | 注入通知管理器，任务失败发送 `task_failed` |
| `Store()` / `Hub()` | 获取底层 Store/Hub |

### Store（任务 CRUD）

| 方法 | 说明 |
|------|------|
| `Create(ctx, input)` | 创建任务 |
| `List(ctx)` | 列出所有任务 |
| `Get(ctx, id)` | 获取任务 |
| `ActiveBySessionAndType(ctx, sessionID, type)` | 查找活跃任务（防止重复提交） |
| `MarkRunning/MarkSucceeded/MarkFailed` | 状态更新 |
| `UpdateProgress(ctx, id, progress, message)` | 更新进度（0-100） |
| `ListRunning(ctx)` | 列出所有 running 状态任务 |
| `ResetToPending(ctx, id)` | 重置为 pending（用于恢复） |
| `Delete(ctx, id)` | 删除单个任务（未找到返回 ErrTaskNotFound） |
| `DeleteByStatus(ctx, status)` | 按状态批量删除，返回删除数量 |
| `DeleteBySession(ctx, sessionID)` | 删除指定场次的所有任务 |
| `DeleteByFailedSessions(ctx)` | 删除所有失败场次关联的任务 |
| `RecoverRunning(ctx)` | 将所有 running 状态任务标记为 failed |
| `Retry(ctx, id)` | 重试失败任务 |
| `RecentFailedTasks(ctx, limit)` | 返回最近 N 条失败任务（用于诊断报告） |
| `TaskSummary(ctx)` | 返回按状态分组的任务计数（用于诊断报告） |

### Hub（广播）

| 方法 | 说明 |
|------|------|
| `Run()` | 启动 Hub 事件循环 |
| `Subscribe()` | 返回事件 channel |
| `Unsubscribe(ch)` | 取消订阅 |
| `Broadcast(task)` | 广播任务状态变更 |
| `Stop()` | 停止 Hub |

### 友好错误映射（errors.go）

| 函数 | 说明 |
|------|------|
| `GetFriendlyError(taskType, rawError)` | 将原始错误映射为中文友好消息和操作建议 |

**FriendlyError 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `message` | string | 中文友好消息 |
| `suggestion` | string | 建议操作 |

**错误映射覆盖范围：**
- ffmpeg 相关：音频处理失败/工具未安装
- 网络相关：连接失败/超时/DNS 错误/连接重置
- Cookie/认证：过期/无效/未登录
- API 限流：频率超限
- ASR/DashScope：配额不足/Key 无效/服务未开通/文件过大
- 发布：审核未通过/稿件重复/频率限制
- yt-dlp/rclone：工具未安装/视频不可用/需要登录/传输失败
- 通用：磁盘空间不足/进程被终止/命令执行失败
- 按任务类型提供默认友好消息

**自动重试判断：**

| 函数 | 说明 |
|------|------|
| `ShouldAutoRetry(cfg, taskType, attempt)` | 根据 worker 配置判断是否应自动重试 |

## 数据模型

**Task 结构体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string PK | `task_` + 32位 hex |
| `channel_id` | string FK | 主播 ID |
| `session_id` | string FK | 场次 ID（可为空） |
| `type` | string | 任务类型 |
| `status` | string | pending/running/succeeded/failed/cancelled |
| `payload` | string | JSON 参数 |
| `progress` | int | 0-100 |
| `message` | string | 当前进度描述 |
| `error` | string | 错误信息（失败时） |
| `attempt` | int | 尝试次数，从 1 开始 |

数据库中另有 `usage_metadata` 字段（TEXT, 默认 `{}`），对应 tasks 表的 usage_metadata 列（v22 迁移）。

## 任务恢复策略

启动时 `recoverRunning` 根据任务类型执行不同的恢复策略：

| 任务类型 | 恢复策略 |
|----------|----------|
| `asr_poll` | 重新入队执行（更新 attempt 计数） |
| `upload` | 重新入队执行 |
| `live_record` | 检查 ffmpeg 进程是否仍在运行（PID 解析），不存活则标记 failed |
| 其他 | 标记 failed，允许用户通过 API 重试 |

恢复失败时通过 `FailSessionStateFunc` 同步更新 session 状态为 failed。

## 关键设计决策

- 任务 ID 使用 `crypto/rand` 生成，避免冲突。
- 队列容量为 `workerCount * 4`，满时启动临时 goroutine 排队。
- Hub 使用 fan-out 模式，每个 subscriber 独立 channel，容量 16。
- 任务状态转换通过 `MarkRunning/MarkSucceeded/MarkFailed` 方法控制，`MarkSucceeded` 要求当前状态为 `running`。
- Pool 持有 `notifyMgr`，任务失败时发送 `notify.EventTaskFailed`。
- handler 层在任务详情 API（`GET /api/tasks/:id`）中返回 `friendly_error` 字段和 `auto_retry` 信息。
- **状态旁路任务**（设计 4.3）：任务类型在 `Register` 时用 `WithBypassFailState()` 声明旁路属性，存于 `registeredHandler{bypassFailState bool}`。worker 失败路径据此调用 `SetFailSessionStateFn(..., bypassState)`，旁路任务（如 `archive`）仅写 `last_error`、**不**降级主状态。另有**任务实例级 bypass**（`Task.BypassFailState`，DB v34 `bypass_fail_state` 列）：重新生成回顾等非推进型任务置 true，`syncSessionState` 取 `task.BypassFailState || 类型级`（OR），失败同样不降级；这取代了原先 `nonRetryableTypes` 集合 + `cmd/hikami` 对 `upload`/`archive` task.Type 的硬编码特判。
- **双重降级收敛**：各业务 handler 内冗余的 `Apply(EventTaskFailed)` 已移除，失败状态降级统一由 worker 在失败路径处理，避免重复降级。

## 测试与质量

- `worker_test.go`: 33 个测试用例，覆盖：
  - Store CRUD: Create（成功、缺 channel_id、缺 type、默认 payload）、Get（未找到）、List（空、排序）
  - Store 生命周期: pending->running->succeeded、pending->failed、running->failed、非 running 不能 MarkSucceeded
  - Store 高级: 非 failed 不能 Retry、UpdateProgress（成功、越界）、ActiveBySessionAndType（有/无）、ResetToPending
  - Store 恢复: RecoverRunning（running->failed）、recoverRunning（default 类型标记 failed）
  - Pool: RegisterAndRun（任务执行到 succeeded）、Retry（失败后重试成功）、Cancel（pending 取消）、**Register + WithBypassFailState 旁路任务失败不降级**、**bypassFailState 查询**
  - Hub: SubscribeBroadcast（广播接收）、Unsubscribe（取消订阅）、StopClosesChannels
  - Helpers: parsePIDFromMessage、isProcessAlive

- `task_test.go`: 5 个测试用例，覆盖：
  - Task Store 生命周期: pending->running->progress->succeeded
  - RetryFailedTask: failed->pending（attempt+1）
  - CancelPendingTask: pending->cancelled
  - ActiveBySessionAndType: pending/running 可找到，succeeded 不可找到
  - RecoverRunning: running->failed

## 相关文件清单

- `worker.go` -- Pool 实现、worker 循环、任务恢复、session 状态同步、BatchRetry、SetNotifyManager、**Register/WithBypassFailState/bypassFailState（状态旁路任务元数据，设计 4.3）**
- `task.go` -- Task 结构体、Store 实现、SQL 常量、PID 解析工具、RecentFailedTasks、TaskSummary
- `hub.go` -- Hub 广播实现
- `errors.go` -- 友好错误映射（GetFriendlyError、FriendlyError、errorMapping、friendlyErrorMappings）
- `worker_test.go` -- 单元+集成测试（33 个用例）
- `task_test.go` -- 单元+集成测试（5 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-23 | 功能 | 自动触发链加固（设计 4.3）：`Register(taskType, handler, opts...)` 新增可变选项，`WithBypassFailState()` 标记状态旁路任务（`registeredHandler.bypassFailState`）；`bypassFailState(taskType)` 查询旁路属性，替代原先对 upload/archive 的硬编码 task.Type 特判；`FailSessionStateFunc` 签名新增 `bypassState bool` 参数，旁路任务仅写 `last_error` 不降级主状态。各业务 handler 内冗余的 `Apply(EventTaskFailed)` 移除，失败降级统一由 worker 处理。worker_test.go 30→33（新增 Register/WithBypassFailState/bypassFailState 用例），worker 总测试数 36→38 |
| 2026-05-15 | 增量更新 | 发现并记录遗漏文件 errors.go（GetFriendlyError 友好错误映射、FriendlyError 类型、30+ 条正则错误模式映射）；Pool 新增 BatchRetry 批量重试、SetNotifyManager 通知注入；Store 新增 RecentFailedTasks/TaskSummary 统计方法；handler 层任务详情 API 返回 friendly_error 和 auto_retry |
| 2026-05-07 | 更新 | 新增 task_test.go（5 个测试用例），worker_test.go 测试用例计数更新为 30 |
| 2026-05-04 | 重大更新 | 新增 worker_test.go（25+ 个测试用例），覆盖 Store CRUD、生命周期、Pool 执行、Hub 广播 |
| 2026-05-03 | 更新 | 新增 Delete、DeleteByStatus、DeleteBySession、DeleteByFailedSessions 方法、SetFailSessionStateFn |
| 2026-05-01 | 更新 | 完善任务恢复策略：asr_poll/upload 重入队、live_record 进程检测 |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
