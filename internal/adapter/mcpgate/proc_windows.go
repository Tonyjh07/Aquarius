//go:build windows

package mcpgate

import (
	"os/exec"
	"syscall"
)

// createNoWindow CREATE_NO_WINDOW（0x08000000）：console 子系统子进程不创建控制台窗。
const createNoWindow = 0x08000000

// hideStdioWindow stdio MCP server 子进程不新开可见控制台（D118）：server 长驻，
// D114 双击启动 FreeConsole 后父进程无控制台可继承 → Windows 为每个 server 新建一个
// 控制台窗、活多久开多久（server I/O 全走 stdio 管道，窗口纯属噪音）。
// 只挂 CREATE_NO_WINDOW、不挂 HideWindow：不误伤 server 自己拉起的 GUI 子窗。
func hideStdioWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
