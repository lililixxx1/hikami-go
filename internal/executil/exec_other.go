//go:build !windows

package executil

import "os/exec"

// HideWindow 在非 Windows 平台为 no-op。背景与原理见 exec_windows.go。
func HideWindow(cmd *exec.Cmd) {}
