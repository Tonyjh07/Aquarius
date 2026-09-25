//go:build !windows

package jobproc

import (
	"errors"
	"os/exec"
	"syscall"
)

// setupProc 平台进程属性：独立进程组，killTree 才能连子进程一起终止。
func setupProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree 终止任务进程组（连带子进程）；失败回退直接 Kill。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("jobproc: process not started")
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
