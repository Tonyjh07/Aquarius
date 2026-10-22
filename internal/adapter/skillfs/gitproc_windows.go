//go:build windows

// git 子进程的窗口抑制（安装/更新会拉起 git，失败时也别让控制台黑框闪一下）。
package skillfs

import (
	"os/exec"
	"syscall"
)

// createNoWindow CREATE_NO_WINDOW（无控制台窗口）。
const createNoWindow = 0x08000000

// setupGitProc Windows：挂 CREATE_NO_WINDOW。
func setupGitProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
