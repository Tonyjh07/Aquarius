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
		return errors.New("process not started")
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// alive 探活（D262）：按 pid 发信号 0。EPERM 说明进程在、只是不归我们管，仍算活着。
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
