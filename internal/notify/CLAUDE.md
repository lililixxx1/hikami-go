[根目录](../../CLAUDE.md) > **internal/notify**

# internal/notify -- 通知事件系统

## 模块职责

封装业务事件通知。模块提供事件常量、通知发送接口、事件过滤和异步发送逻辑，供 worker、live_record、recap、publisher、scheduler 等模块集成。

## 入口与启动

- **入口文件**: `notify.go`, `factory.go`, `webhook.go`, `bark.go`, `serverchan.go`
- **核心类型**: `Manager`, `Notifier`

## 对外接口

| 函数/方法 | 说明 |
|-----------|------|
| `NewManager(notifier, events)` | 创建通知管理器，`events` 为允许发送的事件列表 |
| `NewNotifierFromConfig(type, webhookURL, barkURL, barkKey, serverChanKey)` | 按配置创建具体 Notifier |
| `Manager.ShouldSend(eventType)` | 检查事件是否启用 |
| `Manager.Send(ctx, eventType, title, body)` | 异步发送通知，失败只记录日志 |
| `NoopManager` | 通知未启用时的空实现 |

**Notifier 接口：**

```go
type Notifier interface {
    Send(ctx context.Context, title, body string) error
}
```

## 事件常量

| 常量 | 值 | 触发位置 |
|------|----|----------|
| `EventRecordStart` | `record_start` | `live_record.Manager.HandleTask` 录制开始 |
| `EventRecordStop` | `record_stop` | `live_record.Manager.Stop` 手动停止 |
| `EventTaskFailed` | `task_failed` | `worker.Pool.fail` 任务失败 |
| `EventRecapDone` | `recap_done` | `recap.Handler.HandleTask` 回顾生成完成 |
| `EventPublishDone` | `publish_done` | `publisher.Handler.HandleTask` 草稿/发布完成 |

## 配置

`internal/config` 中的 `NotifyConfig`：

| 字段 | 说明 |
|------|------|
| `enabled` | 是否启用通知 |
| `type` | 通知类型，默认 `webhook` |
| `webhook_url` | 通用 Webhook 地址 |
| `bark_url` / `bark_key` | Bark 通知配置 |
| `serverchan_key` | ServerChan SendKey |
| `events` | 允许发送的事件列表 |

默认事件列表包含 `task_failed`、`record_start`、`record_stop`、`recap_done`、`publish_done`。

## 集成点

- `cmd/hikami/main.go`：根据配置创建 `notify.Manager`，未启用时使用 `notify.NoopManager`；注入 workerPool、recapHandler、publisherHandler、liveManager 和 scheduler。
- `worker.Pool.fail`：任务失败后发送 `EventTaskFailed`。
- `live_record.Manager`：录制开始发送 `EventRecordStart`，手动停止发送 `EventRecordStop`。
- `recap.Handler`：回顾生成完成发送 `EventRecapDone`。
- `publisher.Handler`：发布或保存草稿完成发送 `EventPublishDone`。
- `handler.Server`：`POST /api/notify/test` 用于测试通知配置。

## 设计约束

- `Manager.Send` 内部起 goroutine 异步发送，不阻塞业务主流程。
- `ShouldSend` 对 nil manager、nil notifier 和未启用事件返回 false。
- 新增通知事件必须先定义常量，再加入配置默认值或文档说明，调用点只传事件常量。

## 相关文件清单

- `notify.go` -- 事件常量、Notifier 接口、Manager、NoopManager
- `webhook.go` -- 具体 Notifier 创建和发送实现
- `factory.go` -- NotifierFromConfig 工厂函数
- `bark.go` -- Bark 通知实现
- `serverchan.go` -- ServerChan 通知实现

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-05-17 | 更新 | 集成 worker 通知管理器；新增 notify_test.go 和 sender_test.go 测试文件 |
| 2026-05-15 | 初始化 | 新建通知事件系统文档，记录 record_start/record_stop/task_failed/recap_done/publish_done 事件、配置项和 worker/live_record/recap/publisher 集成点 |
