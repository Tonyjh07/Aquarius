//go:build !windows

// 非 Windows 外壳面（降级，D111③）：无托盘、无全局热键、无原生右键菜单——启动时一次性
// 发降级提示（不静默装死）；文件选择框改走桌面环境命令（zenity/kdialog、macOS osascript）。
package platform

import (
	"os/exec"
	"runtime"
	"strings"
)

// StartShell 非 Windows 无托盘/全局热键：发一次降级提示，让用户知道呼出与常驻不在
// （D111 修订④「缺失项发一次性降级 notice」）。
func (p *Plat) StartShell() {
	p.Notice(unsupported("系统托盘与全局呼出快捷键"))
}

// ReloadHotkey 无全局热键可重注册（改配置不报错，只是不生效）。
func (p *Plat) ReloadHotkey() {}

// ShutdownShell 无托盘/热键需注销。
func (p *Plat) ShutdownShell() {}

// DropTray 无托盘图标可清。
func (p *Plat) DropTray() {}

// PostMenu 无原生菜单：发降级提示（右键菜单在非 Windows 缺失，D111 修订④）。
func (p *Plat) PostMenu(MenuKind) { p.Notice(unsupported("原生右键菜单")) }

// SubclassCloseToHide 无 WM_CLOSE 拦截概念：关窗即销毁（由窗口管理器与 Gio 处理）。
func (p *Plat) SubclassCloseToHide(Handle) {}

// RequestFileDialog 请求文件选择（非 Windows）：外部对话框本身就是阻塞调用（Windows 侧
// 由 shell 线程承担模态泵），此处交独立 goroutine，不卡 Gio 帧循环。
func (p *Plat) RequestFileDialog() { go p.showFileDlg() }

// showFileDlg 呈现文件选择框（独立 goroutine）：选中回传宿主，取消/失败静默或提示。
func (p *Plat) showFileDlg() {
	path, ok := pickFileCommand()
	if !ok {
		p.Notice(unsupported("文件选择框（Linux 需 zenity 或 kdialog）"))
		return
	}
	if path == "" || p.host == nil {
		return // 用户取消
	}
	p.host.FilePicked(path)
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

// CloseWindow 非 Windows 无原生关闭消息：Gio 侧由调用方走窗口自身关闭路径
// （次窗真关闭语义不变，见 uigui/winmgr.go）。
func (p *Plat) CloseWindow(Handle) {}

// FocusWindow 非 Windows 无原生聚焦能力（窗口管理器决定）。
func (p *Plat) FocusWindow(Handle, RunFunc) {}
