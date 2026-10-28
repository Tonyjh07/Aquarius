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
	// 导入侧不看档位（认格式靠内容），-1 = 没有这回事。
	p.host.FilePicked(req.Purpose, path, -1)
}

// showSaveFile 呈现保存框：选定落点 → Host.FilePicked。
//
// 档位恒报 -1：zenity / kdialog / osascript 只回路径，回不了用户在「文件类型」里选了哪
// 一档（zenity 的 --file-filter 在保存框里只是筛显与补扩展名，不回报选中的那项）。宿主
// 据此按落点扩展名回落——这是本平台能给出的最好依据，写进 DESIGN §5.16。
func (p *Plat) showSaveFile(req SaveRequest) {
	path, ok := saveFileCommand(req)
	if !ok {
		p.noticeOnce(&p.dlgNotice, unsupported("另存为对话框（Linux 需 zenity 或 kdialog）"))
		return
	}
	if path == "" || p.host == nil {
		return
	}
	p.host.FilePicked(req.Purpose, path, -1)
}

// openFileCommand 按平台挑一条可用命令选单个文件；ok=false = 无命令可用。
func openFileCommand(req OpenRequest) (string, bool) {
	title := req.Title
	switch runtime.GOOS {
	case "darwin":
		script := `POSIX path of (choose file`
		if title != "" {
			script += ` with prompt ` + appleScriptString(title)
		}
		out, err := exec.Command("osascript", "-e", script+")").Output()
		if err != nil {
			return "", true // 命令存在但取消/报错：视同取消
		}
		return strings.TrimSpace(string(out)), true
	default:
		args := []string{"--file-selection", "--title=" + orDefault(title, "选择文件")}
		args = append(args, zenityFilters(req.Filters)...)
		cmds := [][]string{
			append([]string{"zenity"}, args...),
			{"kdialog", "--getopenfilename", ".", kdialogFilters(req.Filters)},
		}
		return runPickCommand(cmds)
	}
}

// saveFileCommand 按平台挑一条可用命令取保存落点；ok=false = 无命令可用。
//
// 保存框**不设覆盖确认**（D246）：导出是「再存一份」，同名时命令面会加 -2 后缀并在回执里
// 写清真落到哪个文件。对话框里先问一句「要覆盖吗」，答「是」之后文件仍然存成 -2，是个
// 无效选择题；zenity 的 --confirm-overwrite 也已被标为 DEPRECATED。
func saveFileCommand(req SaveRequest) (string, bool) {
	title := req.Title
	switch runtime.GOOS {
	case "darwin":
		script := `POSIX path of (choose file name with name ` + appleScriptString(saveDefaultName(req))
		if title != "" {
			script += ` with prompt ` + appleScriptString(title)
		}
		out, err := exec.Command("osascript", "-e", script+")").Output()
		if err != nil {
			return "", true
		}
		return strings.TrimSpace(string(out)), true
	default:
		cmds := [][]string{
			zenitySaveArgs(req),
			{"kdialog", "--getsavefilename", saveDefaultName(req), kdialogFilters(req.Filters)},
		}
		return runPickCommand(cmds)
	}
}

// saveDefaultName 保存框预填的文件名：调用方给了就用，否则按缺省扩展名造一个。
func saveDefaultName(req SaveRequest) string {
	if req.Name != "" {
		return req.Name
	}
	return "未命名" + extWithDot(req.DefExt)
}

// zenitySaveArgs zenity 保存框的参数表（单列出来，测试与实现读同一份）。
func zenitySaveArgs(req SaveRequest) []string {
	return []string{"zenity", "--file-selection", "--save",
		"--filename=" + saveDefaultName(req),
		"--title=" + orDefault(req.Title, "导出会话")}
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

// zenityFilters zenity 的 --file-filter 参数：**每一档一条 flag**，档内多个模式用空格分隔
// （zenity 的写法是 `显示名 | 模式1 模式2`，不是 Windows 的分号）。
//
// 只传第一档、或把 `*.aqx;*.json` 原样交给 zenity，都会得到一个筛不出任何文件的伪模式——
// 用户界面不筛文件，而对话框自己不报错。空表不传，zenity 的缺省就是全部文件。
func zenityFilters(filters []FileFilter) []string {
	out := make([]string, 0, len(filters))
	for _, f := range filters {
		pat := zenityPattern(f.Pattern)
		if pat == "" {
			continue
		}
		if f.Label == "" {
			out = append(out, "--file-filter="+pat)
			continue
		}
		out = append(out, "--file-filter="+f.Label+" | "+pat)
	}
	return out
}

// zenityPattern 分号分隔的通配符 → 空格分隔（zenity 认多个模式的口径）。
func zenityPattern(pattern string) string {
	var parts []string
	for _, seg := range strings.Split(pattern, ";") {
		if seg = strings.TrimSpace(seg); seg != "" {
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, " ")
}

// kdialogFilters kdialog 的过滤器参数（`模式1 模式2 | 显示名`）；空表给「所有文件」。
//
// kdialog 只收一个过滤器串：多档之间用换行分隔（`A | *.a\nB | *.b`），档内模式同样用空格。
func kdialogFilters(filters []FileFilter) string {
	if len(filters) == 0 {
		filters = []FileFilter{{Label: "所有文件", Pattern: "*.*"}}
	}
	var segs []string
	for _, f := range filters {
		pat := zenityPattern(f.Pattern)
		if pat == "" {
			continue
		}
		if f.Label == "" {
			segs = append(segs, pat)
			continue
		}
		segs = append(segs, pat+" | "+f.Label)
	}
	return strings.Join(segs, "\n")
}

// appleScriptString 把字符串写成 AppleScript 字面量（转义反斜杠与引号）。
//
// 直接拼进双引号的 `with prompt "…"`：标题里有一个引号，整段脚本就语法错误（命令存在、
// 执行失败，被当成「用户取消」静默吞掉）。
func appleScriptString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// extWithDot 扩展名补点（SaveRequest.DefExt 不带点，与 Win32 的 lpstrDefExt 同口径）。
func extWithDot(ext string) string {
	if ext == "" || strings.HasPrefix(ext, ".") {
		return ext
	}
	return "." + ext
}

// orDefault 空串取缺省值（对话框标题不能为空）。
func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
