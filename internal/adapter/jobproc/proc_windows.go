//go:build windows

package jobproc

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// setupProc 平台进程属性：
//   - cmd.exe /c：Go 的 argv 转义（EscapeArg）把 `"` 写成 `\"`，而 cmd.exe 不认
//     反斜杠转义，含引号的命令会被静默改写（echo "a b" → \"a b\"、
//     powershell -Command "..." 拿到错误参数）。对 cmd /c <整条命令行> 这一形态
//     改用 SysProcAttr.CmdLine 原始命令行直传，引号/重定向语义保持模型原文。
func setupProc(cmd *exec.Cmd) {
	if raw, ok := cmdRawLine(cmd); ok {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: raw}
	}
}

// cmdRawLine 构造 cmd.exe 原始命令行；非 "cmd /c <单条命令行>" 形态返回 ok=false
// （回退 Go 默认 argv 拼装——多段参数无内嵌引号时默认拼装本就正确）。
func cmdRawLine(cmd *exec.Cmd) (string, bool) {
	if len(cmd.Args) != 3 {
		return "", false
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(cmd.Args[0])), ".exe")
	if base != "cmd" || (cmd.Args[1] != "/c" && cmd.Args[1] != "/C") {
		return "", false
	}
	if strings.Contains(cmd.Args[0], `"`) {
		return "", false // 可执行路径含引号：罕见，回落默认拼装
	}
	exe := cmd.Args[0]
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"` // 路径含空格时按 Windows 惯例加引号
	}
	return exe + " " + cmd.Args[1] + " " + cmd.Args[2], true
}

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
