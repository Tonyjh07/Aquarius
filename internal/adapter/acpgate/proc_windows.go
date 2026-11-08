//go:build windows

package acpgate

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow CREATE_NO_WINDOW（0x08000000）：console 子系统子进程不创建控制台窗。
const createNoWindow = 0x08000000

// setupProc Windows：压制控制台窗（同 mcpgate / jobproc，D118）并让子进程自成一组，
// 终止时 taskkill /T 才连它自己拉起的子进程一起收。
func setupProc(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// killTree 终止进程及其全部子进程：taskkill /T /F；失败回退直接 Kill。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// exitSignal Windows 上没有「被哪个信号杀掉」这一说：被 taskkill 结束也只留一个退出码。
func exitSignal(*os.ProcessState) string { return "" }
