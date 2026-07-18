//go:build windows

package executil

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW 是 Windows CreateProcess 的 dwCreationFlags 位，
// 抑制为子进程分配新控制台（从而避免黑色窗口闪现）。
// 标准库 syscall 未导出该常量（仅含部分 CreationFlags），此处按 Win32 头文件值声明。
// 参考：https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags
const CREATE_NO_WINDOW = 0x08000000

// HideWindow 配置 cmd 在运行时不弹出可见的控制台窗口。
//
// 仅 Windows 有效。当父进程用 -H windowsgui（GUI 子系统、无控制台）构建时必需：
// 在该模式下派生控制台子进程会让 Windows 为子进程新建一个控制台，表现为黑色窗口闪现。
// CREATE_NO_WINDOW 抑制该分配，同时保持 stdin/stdout/stderr pipe 行为不变。
//
// cmd.SysProcAttr 已设置时也安全调用：仅 OR 进该 flag，不覆盖既有配置。
// 非 Windows 平台为 no-op（见 exec_other.go）。
//
// 与 cmd.Cancel（如 ffmpeg 录制的 SIGTERM 优雅停止）正交，互不影响：
// cmd.Cancel 走 Go 运行时的 cmd.Process.Signal，与 Windows 进程创建标志独立。
//
// 调用约定：在 cmd.Start/Run/Output/CombinedOutput 之前调用即可。
func HideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= CREATE_NO_WINDOW
}
