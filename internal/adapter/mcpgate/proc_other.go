//go:build !windows

package mcpgate

import "os/exec"

// hideStdioWindow 非 Windows 无「控制台窗」语义（D118 仅适用 Windows console 子系统）：
// 子进程照常由平台自己的窗口规则决定，此处不设 SysProcAttr。
func hideStdioWindow(*exec.Cmd) {}
