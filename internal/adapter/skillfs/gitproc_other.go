//go:build !windows

// git 子进程的平台差异（非 Windows 无需挂窗口抑制）。
package skillfs

import "os/exec"

// setupGitProc 非 Windows：无操作。
func setupGitProc(*exec.Cmd) {}
