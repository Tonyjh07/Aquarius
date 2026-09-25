//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

// setupAcceptCmd Windows：进程树由 taskkill /T 收尾，无需额外组属性。
func setupAcceptCmd(*exec.Cmd) {}

// killAcceptCmd 杀整棵验收 server 进程树（taskkill /T 覆盖 npx → node 链）。
func killAcceptCmd(cmd *exec.Cmd) {
	_ = exec.Command("taskkill", "/T", "/F",
		"/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
