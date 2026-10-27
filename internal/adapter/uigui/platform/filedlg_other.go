//go:build !windows

// 通用文件框（非 Windows 降级）：单选打开与保存都走桌面环境命令（zenity / kdialog /
// osascript）。外部命令本身就是阻塞调用，故交独立 goroutine，不卡 Gio 帧循环。
package platform

import (
	"os/exec"
	"runtime"
	"strings"
)

// RequestOpenFile 请求单选文件框（非 Windows）：交独立 goroutine。
func (p *Plat) RequestOpenFile(req OpenRequest) {
	go p.showOpenFile(req)
}

// RequestSaveFile 请求保存框（非 Windows）：交独立 goroutine。
func (p *Plat) RequestSaveFile(req SaveRequest) {
	go p.showSaveFile(req)
}

// showOpenFile 呈现单选文件框：选中 → Host.FilePicked；无可用命令 → 一次性降级提示。
func (p *Plat) showOpenFile(req OpenRequest) {
	path, ok := openFileCommand(req)
	if !ok {
		p.noticeOnce(&p.dlgNotice, unsupported("文件选择框（Linux 需 zenity 或 kdialog）"))
		return
	}
	if path == "" || p.host == nil {
		return // 取消
	}
	p.host.FilePicked(req.Purpose, path)
}

// showSaveFile 呈现保存框：选定落点 → Host.FilePicked。
func (p *Plat) showSaveFile(req SaveRequest) {
	path, ok := saveFileCommand(req)
	if !ok {
		p.noticeOnce(&p.dlgNotice, unsupported("另存为对话框（Linux 需 zenity 或 kdialog）"))
		return
	}
	if path == "" || p.host == nil {
		return
	}
	p.host.FilePicked(req.Purpose, path)
}

// openFileCommand 按平台挑一条可用命令选单个文件；ok=false = 无命令可用。
func openFileCommand(req OpenRequest) (string, bool) {
	title := req.Title
	filter := firstFilter(req.Filters)
	switch runtime.GOOS {
	case "darwin":
		script := `POSIX path of (choose file`
		if title != "" {
			script += ` with prompt "` + title + `"`
		}
		out, err := exec.Command("osascript", "-e", script+")").Output()
		if err != nil {
			return "", true // 命令存在但取消/报错：视同取消
		}
		return strings.TrimSpace(string(out)), true
	default:
		var cmds [][]string
		if zf := zenityFilter(req.Filters); zf != "" {
			cmds = append(cmds, []string{"zenity", "--file-selection", zf, "--title=" + orDefault(title, "选择文件")})
		} else {
			cmds = append(cmds, []string{"zenity", "--file-selection", "--title=" + orDefault(title, "选择文件")})
		}
		cmds = append(cmds, []string{"kdialog", "--getopenfilename", ".", kdialogFilter(filter)})
		return runPickCommand(cmds)
	}
}

// saveFileCommand 按平台挑一条可用命令取保存落点；ok=false = 无命令可用。
func saveFileCommand(req SaveRequest) (string, bool) {
	title := req.Title
	switch runtime.GOOS {
	case "darwin":
		name := req.Name
		if name == "" {
			name = "未命名" + req.DefExt
		}
		script := `POSIX path of (choose file name with name "` + name + `"`
		if title != "" {
			script += ` with prompt "` + title + `"`
		}
		out, err := exec.Command("osascript", "-e", script+")").Output()
		if err != nil {
			return "", true
		}
		return strings.TrimSpace(string(out)), true
	default:
		name := req.Name
		if name == "" {
			name = "未命名" + req.DefExt
		}
		cmds := [][]string{
			{"zenity", "--file-selection", "--save", "--confirm-overwrite",
				"--filename=" + name, "--title=" + orDefault(title, "导出会话")},
			{"kdialog", "--getsavefilename", name, kdialogFilter(firstFilter(req.Filters))},
		}
		return runPickCommand(cmds)
	}
}

// runPickCommand 依次试给定的命令（zenity → kdialog）：第一条存在的那个负责呈现；
// 它取消/失败即视同取消，不再回落第二条（免得同一个动作弹两次框）。
func runPickCommand(cmds [][]string) (string, bool) {
	for _, cmd := range cmds {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			continue
		}
		out, err := exec.Command(cmd[0], cmd[1:]...).Output()
		if err != nil {
			return "", true
		}
		return strings.TrimSpace(string(out)), true
	}
	return "", false
}

// firstFilter 第一项过滤器（没给 = 所有文件）。
func firstFilter(filters []FileFilter) FileFilter {
	if len(filters) == 0 {
		return FileFilter{Label: "所有文件 (*.*)", Pattern: "*.*"}
	}
	return filters[0]
}

// zenityFilter zenity 的 --file-filter 参数（`显示名 | 通配符`）；空 = 不加。
func zenityFilter(filters []FileFilter) string {
	if len(filters) == 0 {
		return ""
	}
	f := filters[0]
	if f.Label == "" {
		return "--file-filter=" + f.Pattern
	}
	return "--file-filter=" + f.Label + " | " + f.Pattern
}

// kdialogFilter kdialog 的过滤器参数（`通配符 | 显示名`）。
func kdialogFilter(f FileFilter) string {
	if f.Label == "" {
		return f.Pattern
	}
	return f.Pattern + " | " + f.Label
}

// orDefault 空串取缺省值（对话框标题不能为空）。
func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
