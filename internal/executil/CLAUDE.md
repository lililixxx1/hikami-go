[根目录](../../CLAUDE.md) > **internal/executil**

# internal/executil -- 子进程窗口隐藏辅助

> 2026-07-18 新增。源自 [`docs/子进程闪窗问题分析.md`](../../docs/子进程闪窗问题分析.md)。

## 模块职责

提供 `HideWindow(cmd)` 辅助，在 Windows 桌面模式（`-H windowsgui` 编译）下抑制派生子进程时黑色控制台窗口闪现；非 Windows 平台为 no-op。零外部依赖（仅标准库 `os/exec`，Windows 额外 `syscall`）。

## 入口与启动

- **入口文件**: `exec_windows.go`（Windows 实现）、`exec_other.go`（非 Windows no-op stub）
- **零依赖**：参照 `fsutil`/`biliutil` 的"聚焦小包"惯例，不导入任何 `internal/*` 包（避免 import cycle；曾考虑放 `internal/runtime/`，但 `runtime/probe.go` 已导入 `internal/asr`，而 `asr/dashscope.go` 是调用点之一，会成环）。
- **build constraint 互斥**：`//go:build windows` 与 `//go:build !windows` 保证单平台单实现。

## 对外接口

| 函数 | 说明 |
|------|------|
| `HideWindow(cmd *exec.Cmd)` | Windows：`cmd.SysProcAttr.CreationFlags |= CREATE_NO_WINDOW (0x08000000)`，nil 时新建 SysProcAttr；非 Windows：空函数 |

## 关键设计决策

- **`CREATE_NO_WINDOW` 包内声明**：Go 标准库 `syscall` 未导出该常量（仅含部分 CreationFlags），按 Win32 头文件值 `0x08000000` 声明（参考 [Process Creation Flags](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)）。
- **OR 进 flag、不覆盖**：`cmd.SysProcAttr` 已设置时只 OR 进 `CREATE_NO_WINDOW`，保留既有 CreationFlags（如调用方已设的其它位）。
- **与 pipe 兼容**：`CREATE_NO_WINDOW` 仅抑制控制台分配，**不影响** stdin/stdout/stderr 重定向。所有调用点的 `CombinedOutput()` / `StdinPipe()` / `Stdout=&buf` 行为不变。
- **与 `cmd.Cancel` 正交**：`cmd.Cancel`（如 `live_record/ffmpeg.go` 录制的 SIGTERM 优雅停止）走 Go 运行时 `cmd.Process.Signal`，与 Windows 进程创建标志独立，互不影响。
- **非 Windows no-op**：空函数体，零运行时开销，跨平台编译无需调用方加 build constraint。

## 调用约定

任何新增 `exec.Command` / `exec.CommandContext` 派生外部进程的代码，都应在 `cmd.Start/Run/Output/CombinedOutput` **之前**调用 `executil.HideWindow(cmd)`，确保桌面模式下不闪窗。

```go
cmd := exec.CommandContext(ctx, command, args...)
executil.HideWindow(cmd)   // ← 加这一行
// ...（Stdin/Stdout/Cancel 等原有配置保留）
if err := cmd.Start(); err != nil { ... }
```

## 覆盖范围（2026-07-18 首批落地，18 处调用点 / 11 个生产文件）

| 文件 | 用途 |
|------|------|
| `cmd/hikami/main.go` | `openBrowser` 三分支（open/cmd/xdg-open）共享一处插入 |
| `internal/normalize/normalize.go` | ffmpeg normalize |
| `internal/importer/importer.go` | ffmpeg 导入转码 |
| `internal/download/download.go` | yt-dlp list/单P/多P + ffprobe + ffmpeg concat（5 处） |
| `internal/live_record/ffmpeg.go` | ffmpeg 录制长子进程（`cmd.Cancel = SIGTERM`，正交） |
| `internal/live_record/manager.go` | `runFFmpegConcat` var + `RecordWithProcessStart`（2 处） |
| `internal/upload/upload.go` | rclone copy/delete（2 处） |
| `internal/asr/dashscope.go` | rclone copyto + deletefile（2 处） |
| `internal/discover/discover.go` | yt-dlp --flat-playlist |
| `internal/recap/claude_cli.go` | claude CLI |
| `internal/recap/codex_cli.go` | codex CLI |

> **测试代码（`*_test.go`）不加 helper**：测试在开发者本地跑，闪窗无所谓；且测试桩（如 `RecordWithProcessStart`、`runFFmpegConcat` 是 var）多替换了真实 exec 调用，加 helper 无意义。

## 变更记录

- **2026-07-18（四）**：新增包 + 18 处调用点改造。源自用户反馈"软件在后台运行时会有终端窗口突然闪现又关闭"（仅 Windows 桌面模式）。详见 [`docs/子进程闪窗问题分析.md`](../../docs/子进程闪窗问题分析.md)。
