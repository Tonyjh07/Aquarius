//go:build windows

package jobproc

import (
	"errors"
	"os/exec"
	"strconv"
)

// setupProc 平台进程属性：Windows 无进程组设定，
// 整树终止由 killTree 的 taskkill /T 承担。
func setupProc(cmd *exec.Cmd) { _ = cmd }

// killTree 终止任务及其全部子进程：taskkill /T /F；失败回退直接 Kill。
func killTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("jobproc: 进程未启动")
	}
	err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	if err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
