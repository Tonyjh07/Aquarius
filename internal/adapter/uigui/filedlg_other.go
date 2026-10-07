//go:build !windows

// 附件文件选择框（非 Windows 降级，D111③）：无 Win32 commdlg，改走桌面环境命令——
// Linux 用 zenity/kdialog，macOS 用 osascript；命令缺失或环境无桌面 = 向转写区投一条
// 降级提示（不静默失败）。与 Windows 侧同契约：选中 → attachMsg 投回事件循环。
package uigui

import (
	"os/exec"
	"runtime"
	"strings"
)

// requestFileDlg 请求文件选择（非 Windows）：外部对话框本身就是阻塞调用（Windows 侧
// 由 shell 线程承担模态泵），此处交独立 goroutine，不卡 Gio 帧循环。
func (u *UI) requestFileDlg() {
	go u.showFileDlg()
}

// showFileDlg 呈现文件选择框（独立 goroutine）：选中投 attachMsg，取消/失败按提示收尾。
func (u *UI) showFileDlg() {
	path, ok := pickFileCommand()
	if !ok {
		u.post(sayMsg{text: "当前平台未找到可用的文件选择框（Linux 需 zenity 或 kdialog），请直接输入附件路径。"})
		return
	}
	if path == "" {
		return // 用户取消
	}
	u.post(attachMsg{path: path})
}

// pickFileCommand 按平台挑一条可用命令取文件路径；ok=false = 无可用命令。
// 返回 path 为空 = 用户取消（命令成功但未选）。
func pickFileCommand() (path string, ok bool) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e",
			`POSIX path of (choose file with prompt "选择附件")`).Output()
		if err != nil {
			return "", true // 命令存在但用户取消/报错：视同取消，不再降级
		}
		return strings.TrimSpace(string(out)), true
	default:
		for _, cmd := range [][]string{
			{"zenity", "--file-selection", "--title=选择附件"},
			{"kdialog", "--getopenfilename", ".", "*"},
		} {
			if _, err := exec.LookPath(cmd[0]); err != nil {
				continue
			}
			out, err := exec.Command(cmd[0], cmd[1:]...).Output()
			if err != nil {
				return "", true // 取消/失败：视同取消（避免回落第二条命令再弹一次）
			}
			return strings.TrimSpace(string(out)), true
		}
		return "", false
	}
}
