[根目录](../../CLAUDE.md) > **internal/state**

# internal/state -- 场次聚合状态机

## 模块职责

管理场次的聚合状态转换。业务模块不得直接修改 `sessions.status`，必须通过本模块的 `Apply` 方法提交事件。状态机保证转换的合法性和一致性。

## 入口与启动

- **入口文件**: `state.go`
- **核心类型**: `Store`

## 对外接口

| 方法/函数 | 说明 |
|-----------|------|
| `NewStore(db)` | 创建 Store 实例 |
| `Store.Apply(ctx, sessionID, event, taskID, errorMessage)` | 提交事件并更新状态（事务） |
| `Next(current, event)` | 纯函数：计算下一个状态（无副作用） |

## 状态与事件

**状态常量：**

| 状态 | 说明 |
|------|------|
| `discovered` | 已发现，等待来源处理 |
| `downloading` | 回放下载中 |
| `recording` | 直播录制中 |
| `importing` | 手动导入中 |
| `media_ready` | 原始媒体就绪 |
| `asr_submitted` | ASR 已提交 |
| `asr_done` | ASR 完成 |
| `recap_done` | 回顾生成完成 |
| `uploaded` | 已上传 |
| `published` | 已发布 |
| `failed` | 失败（可恢复） |

**事件常量：**

| 事件 | 触发模块 |
|------|----------|
| `download_started/succeeded` | download |
| `live_record_started/succeeded` | live_record |
| `import_started/succeeded` | importer |
| `normalize_succeeded` | normalize |
| `asr_submitted` / `asr_succeeded` | asr |
| `recap_succeeded` | recap |
| `upload_succeeded` | upload |
| `publish_succeeded` | publisher |
| `task_failed` | 所有模块 |

## 转换表

```
discovered  --download_started--> downloading
discovered  --live_record_started--> recording
discovered  --import_started--> importing
downloading --download_succeeded--> downloading (保持，等待 normalize)
downloading --normalize_succeeded--> media_ready
recording   --live_record_succeeded--> recording (保持，等待 normalize)
recording   --normalize_succeeded--> media_ready
importing   --import_succeeded--> importing (保持，等待 normalize)
importing   --normalize_succeeded--> media_ready
media_ready --asr_submitted--> asr_submitted
asr_submitted --asr_succeeded--> asr_done
asr_done    --recap_succeeded--> recap_done
asr_done    --upload_succeeded--> uploaded
recap_done  --upload_succeeded--> uploaded
recap_done  --publish_succeeded--> published
uploaded    --publish_succeeded--> published
任何状态    --task_failed--> failed
failed      --normalize_succeeded/asr_submitted/asr_succeeded/recap_succeeded/upload_succeeded/publish_succeeded--> 对应状态
```

## 关键设计决策

- `task_failed` 事件可从任何状态触发，转换到 `failed`。
- 来源成功事件（`download_succeeded` 等）只将状态保持在来源处理中，需要 `normalize_succeeded` 才能推进到 `media_ready`。
- `Apply` 在事务中执行，先读取当前状态，校验转换合法性，再更新。
- `failed` 状态可恢复到后续管道节点（normalize_succeeded/asr_submitted 等），支持失败重试后的状态恢复。
- `Apply` 对不同事件设置不同的时间戳：`task_failed` 写入 `last_error`，`upload_succeeded` 写入 `uploaded_at`，`publish_succeeded` 写入 `published_at`。

## 测试与质量

- `state_test.go`: 12 个测试用例，覆盖：
  - `TestNextValidTransitions`: 所有合法转换（含 published→uploaded 回退出口）
  - `TestNextInvalidTransition`: 非法转换拒绝
  - `TestTaskFailedFromAnyState`: task_failed 从所有状态可达
  - `TestNullable`: nullable 辅助函数
  - `TestApplySuccess`: Apply 事务持久化
  - `TestApplySessionNotFound`: 不存在的 sessionID
  - `TestApplyInvalidTransition`: 非法转换在 Apply 中拒绝
  - `TestApplyTaskFailedSetsError`: task_failed 写入 last_error
  - `TestApplyUploadSucceededSetsTimestamp`: upload_succeeded 设置 uploaded_at
  - `TestApplyPublishSucceededSetsTimestamp`: publish_succeeded 设置 published_at
  - `TestApplyWithPublishTarget`: 同事务写 publish_target（JSON 结构）
  - `TestApplyRevertPublish`: 发布回退（published→uploaded，清空 publish_target，保留 published_at）

## 相关文件清单

- `state.go` -- 唯一源文件
- `state_test.go` -- 单元+集成测试（12 个用例）

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-06-21 | 增量同步 | 测试计数校正：10→12（补 `TestApplyWithPublishTarget` 同事务写 publish_target、`TestApplyRevertPublish` 发布回退）。新增 `EventPublishReverted`/`ApplyRevertPublish` + `transitions[StatusPublished]` 出口（published→uploaded，清空 publish_target，保留 published_at），支撑专栏删除/编辑流程 |
| 2026-05-04 | 重大更新 | failed 状态恢复支持（接受所有管道事件）、新增 state_test.go（10 个用例） |
| 2026-04-29 | 初始化 | 首次生成模块文档 |
