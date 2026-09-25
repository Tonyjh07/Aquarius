//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setupAcceptCmd unix：独立进程组，便于整组回收（npx → node 孙进程同组）。
func setupAcceptCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killAcceptCmd 杀整个验收 server 进程组（SIGKILL 负 pgid）。
func killAcceptCmd(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
