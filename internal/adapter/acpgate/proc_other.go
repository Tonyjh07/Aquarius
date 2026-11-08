//go:build !windows

package acpgate

import (
	"os"
	"os/exec"
	"syscall"
)

// setupProc 非 Windows：独立进程组，killTree 才能连子进程一起终止。
func setupProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree 终止进程组（连带子进程）；失败回退直接 Kill。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// exitSignal 被信号终止时给出信号名（协议里 exitCode 与 signal 至多一个非空）。
func exitSignal(st *os.ProcessState) string {
	if st == nil {
		return ""
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
