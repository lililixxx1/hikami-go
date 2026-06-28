[根目录](../../CLAUDE.md) > **internal/scheduler**

# internal/scheduler -- 定时任务调度

## 模块职责

使用 robfig/cron 库调度周期性任务：回放发现和直播状态检查。在服务启动时创建并注册 cron 任务，优雅关闭时停止。

## 入口与启动

- **入口文件**: `scheduler.go`
- **核心类型**: `Scheduler`

## 对外接口

| 方法 | 说明 |
|------|------|
| `New(cfg, discoverManager, liveManager)` | 创建 Scheduler，注册 cron 任务 |
| `Start()` | 启动 cron 调度器 |
| `Stop()` | 优雅停止 cron 调度器 |

## 关键依赖与配置

- `github.com/robfig/cron/v3`: cron 表达式解析和调度
- `cron.discovery`: 回放发现的 cron 表达式（默认 `@every 20m`）
- `cron.live_check`: 直播检查的 cron 表达式（默认 `@every 30s`）
- 依赖 `discover.Manager` 执行回放发现
- 依赖 `live_record.Manager` 执行直播检查和自动录制

## 任务说明

| Cron 任务 | 配置项 | 默认值 | 说明 |
|-----------|--------|--------|------|
| 回放发现 | `cron.discovery` | `@every 20m` | 遍历所有主播发现新回放 |
| 直播检查 | `cron.live_check` | `@every 30s` | 检查所有主播直播状态，自动开始录制 |

## 相关文件清单

- `scheduler.go` -- 唯一源文件

## 变更记录 (Changelog)

| 日期 | 操作 | 说明 |
|------|------|------|
| 2026-05-17 | 更新 | Scheduler 持有 ctx/cancel 实现上下文传播；cron 任务使用 scheduler context 替代 context.Background()；Stop 优雅关闭先 cancel() 再 cron.Stop() |
| 2026-05-01 | 新建 | 首次生成模块文档 |
