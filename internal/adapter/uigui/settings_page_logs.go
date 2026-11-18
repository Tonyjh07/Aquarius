package uigui

// settings_page_logs.go 「日志」页（v0.9.0）：运行日志（§7.9）的界面查看口。
//
// 背景：双击启动没有控制台，日志只写在 `<profile>/logs/aquarius-YYYY-MM-DD.log` 里。
// 出问题时用户拿不到那一行——要么去翻文件管理器里一个记不住的路径，要么从终端重启
// 一遍把问题复现出来。这一页只做三件事：列出目录里的那几份、点一份看末尾、给一个
// 「打开目录」。
//
// 只 tail：整份读进内存既慢又会把一大片旧内容铺到窗口里，而这个页面的用途是「刚才
// 那一轮发生了什么」。行数与读取窗口由装配根注入的读侧（port.LogReader）兜住，页面只负责呈现。
//
// 页序契约：插在「高级」与「关于」之间（两者都是排查/信息面），加页只动
// settings_page.go 的 settingsPages 与本文件。

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// logTailLines 一次查看的行数上限（大约一屏半到两屏）：再长就滚不动了，而请求出问题
// 时真正要看的是最末那几十行。
const logTailLines = 400

// logsPage 日志页模块。
type logsPage struct{}

func (logsPage) Key() string { return "logs" }

func (logsPage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{sec(i18n.T("ui.page.logs.section_files"))}
	if f.logs == nil {
		return append(rows, rowDesc{kind: rowLogEmpty, text: i18n.T("ui.common.unwired")})
	}
	rows = append(rows, rowDesc{kind: rowLogNote})
	if len(f.logsFiles) == 0 {
		rows = append(rows, rowDesc{kind: rowLogEmpty, text: i18n.T("ui.page.logs.empty")})
	}
	for i := range f.logsFiles {
		rows = append(rows, rowDesc{kind: rowLogFile, field: i})
	}
	rows = append(rows, rowDesc{kind: rowLogAction})
	if f.logStatus != "" {
		rows = append(rows, rowDesc{kind: rowLogStatus})
	}
	return rows
}

func (logsPage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowLogNote:
		l := material.Body2(th, i18n.T("ui.page.logs.note"))
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowLogEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowLogFile:
		return f.logFileRow(gtx, th, r.field), true
	case rowLogAction:
		return f.logActionRow(gtx, th), true
	case rowLogStatus:
		l := material.Body2(th, f.logStatus)
		l.Color = paperOf().text3
		if f.logErr {
			l.Color = paperOf().danger
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

func (logsPage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	for f.logRefresh.Clicked(gtx) {
		f.loadLogFiles()
		f.logStatus, f.logErr = "", false
	}
	for f.logOpenDir.Clicked(gtx) {
		f.openLogsDir(u)
	}
	// 按文件名分派：按钮只在本页渲染，未渲染的 Clicked 恒假。
	for name, btn := range f.logsBtns {
		for btn.Clicked(gtx) {
			f.openLogTail(u, name)
		}
	}
}

// OnEnter 进入本页时取一次文件清单（去重：切回来不必重读目录）。
func (logsPage) OnEnter(u *UI, f *settingsForm) {
	if f.logs == nil || f.logsLoaded {
		return
	}
	f.loadLogFiles()
}

// FirstEditor 本页没有可手输的字段，开窗不抢焦点。
func (logsPage) FirstEditor(*settingsForm) *widget.Editor { return nil }

// openLogsDir 用文件管理器打开日志目录（双击启动的用户不知道这个路径长什么样）。
func (f *settingsForm) openLogsDir(u *UI) {
	f.logStatus, f.logErr = "", false
	if u.plat == nil {
		f.logErr = true
		f.logStatus = i18n.T("ui.page.logs.no_opener")
		return
	}
	if err := u.plat.OpenPath(f.logs.Dir()); err != nil {
		f.logErr = true
		f.logStatus = i18n.Tf("ui.page.logs.dir_failed", "err", err)
		return
	}
	f.logStatus = i18n.T("ui.page.logs.dir_opened")
}

// logFileButton 按文件名 get-or-create 的点击件。
func (f *settingsForm) logFileButton(name string) *widget.Clickable {
	if f.logsBtns == nil {
		f.logsBtns = map[string]*widget.Clickable{}
	}
	btn := f.logsBtns[name]
	if btn == nil {
		btn = new(widget.Clickable)
		f.logsBtns[name] = btn
	}
	return btn
}

// loadLogFiles 重取目录清单。读不出来不算错误弹窗：列表位置给一行说明，页面其余部分照常。
func (f *settingsForm) loadLogFiles() {
	f.logsLoaded = true
	files, err := f.logs.List()
	if err != nil {
		f.logsFiles = nil
		f.logErr = true
		f.logStatus = i18n.Tf("ui.page.logs.list_failed", "err", err)
		return
	}
	f.logsFiles = files
}

// openLogTail 读一份的末尾并铺进只读次窗（与 `/raw`、`/diag` 同一个窗口）。
func (f *settingsForm) openLogTail(u *UI, name string) {
	f.logStatus, f.logErr = "", false
	text, err := f.logs.Tail(name, logTailLines)
	if err != nil {
		f.logErr = true
		f.logStatus = i18n.Tf("ui.page.logs.read_failed", "err", err)
		return
	}
	if text == "" {
		f.logErr = true
		f.logStatus = i18n.Tf("ui.page.logs.empty_file", "name", name)
		return
	}
	u.showTextView(port.TextViewEvent{
		Title: i18n.Tf("ui.page.logs.view_title", "name", name),
		Text:  text,
	})
	f.logStatus = i18n.Tf("ui.page.logs.opened", "name", name, "lines", logTailLines)
}

// logFileRow 一份文件：名字靠左，大小与最后写入时刻靠右；整行可点。
func (f *settingsForm) logFileRow(gtx layout.Context, th *material.Theme, idx int) layout.Dimensions {
	if idx < 0 || idx >= len(f.logsFiles) {
		return layout.Dimensions{}
	}
	file := f.logsFiles[idx]
	btn := f.logFileButton(file.Name)
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, file.Name).Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(th, logFileMeta(file))
						l.Color = paperOf().text3
						return l.Layout(gtx)
					})
				}),
			)
		})
	})
}

// logActionRow 页级动作：刷新 + 打开目录（与「关于」页的项目主页同一套按钮）。
func (f *settingsForm) logActionRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.logRefresh, i18n.T("ui.page.logs.refresh"), paperBtnSecondary, true)(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.logOpenDir, i18n.T("ui.page.logs.open_dir"), paperBtnSecondary, f.logs != nil)(gtx)
			})
		}),
	)
}

// logFileMeta 右侧那行：大小 + 最后写入日期。
//
// 日期只到「月-日 时:分」：列表按时间倒序，年份在这一屏里没有分辨作用，写了反而挤掉文件名。
func logFileMeta(file port.LogFile) string {
	return i18n.Tf("ui.page.logs.file_meta",
		"size", logFileSize(file.Size),
		"at", file.Mod.Format("01-02 15:04"))
}

// logFileSize 人类可读大小（与导出回执同一套进位，只到 MB：日志单文件上限 4 MiB）。
func logFileSize(n int64) string {
	switch {
	case n >= 1<<20:
		return i18n.Tf("ui.page.logs.size_mb", "n", float64(n)/(1<<20))
	case n >= 1<<10:
		return i18n.Tf("ui.page.logs.size_kb", "n", float64(n)/(1<<10))
	}
	return i18n.Tf("ui.page.logs.size_bytes", "n", n)
}
