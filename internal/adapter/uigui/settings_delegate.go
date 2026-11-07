package uigui

// 外部代理页数据面（D270）：清单 / 可用性 / 缺省 / 命令行编辑。
//
// 这一页补的是一条**看得见**的路：在它之前，要加一个代理只能手改 config.json 的
// delegate.agents，要停掉某个 delegate 工具得记住 tools.disabled 的写法，而「命令不在
// PATH 上」这种最常见的配错只会在第一次派活时以报错的形式出现。
//
// 三个动作都写 config、**重启生效**：转交管理器是启动期装配的（命令、参数、env、上限），
// 热改它等于在别人的进程还在跑的时候换掉它的身份。所以每个动作的反馈都带一句「重启生效」，
// 而不是假装它立刻就好了。
//
// 与工具页的差别：改配置与探 PATH 都是本地同步读写；只有「测试连接」要起一个真进程，
// 那一件事走旁路 goroutine + 结果通道（与技能/工具页同一套形状），其余动作同步做完。

import (
	"errors"
	"image/color"
	"sort"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// DelegateAdmin 外部代理管理面（装配根实现；线程安全——设置窗 goroutine 调用）。
type DelegateAdmin interface {
	// Snapshot 清单（每次调用都重新探一遍可执行文件，所以「重新检测」= 再取一次）。
	Snapshot() DelegateSnapshot
	// Add 追加一条代理（名字 + 命令行）。名字重名、命令行空、引号未闭合都在这里拦下
	// ——它们都会让下次启动吃 fatal，放进界面拦比让用户改文件好。
	Add(name, commandLine string) error
	// Remove 删除一条代理（缺省代理也允许删：删完没有缺省 = 只剩一条时自动选它）。
	Remove(name string) error
	// Rename 改一个代理的名字。
	Rename(oldName, newName string) error
	// SetCommandLine 改一个代理的命令行（`command args...`，按空白切分；含空白的参数用
	// 双引号）。只认一次改一条，写回 config delegate.agents，重启生效。
	SetCommandLine(name, commandLine string) error
	// SetCommand 只改可执行命令（参数保持不动）。
	SetCommand(name, command string) error
	// SetArgs 只改参数（一行一个；空 = 删掉 args 键）。
	SetArgs(name string, args []string) error
	// SetEnv 改环境变量表（值可写 secret:<名>；空表 = 删掉 env 键）。界面只负责写文本，
	// 不解析也不缓存密钥——那是起进程时的事。
	SetEnv(name string, env map[string]string) error
	// SetWorkdir 改工作目录（绝对路径；空 = 删掉 workdir 键）。
	SetWorkdir(name, dir string) error
	// SetEnabled 启用/停用一个代理（写 enabled；重启生效）。
	SetEnabled(name string, enabled bool) error
	// SetDefault 指定缺省代理（写 default；同一条上唯一的 true，重启生效）。
	SetDefault(name string) error
	// Probe 测一次连通性：起进程 → initialize 握手 → 立刻收掉，回执摊给用户看。
	// **会起一个真进程**，最长十几秒：调用方必须在旁路 goroutine 里跑，不能占用界面循环。
	Probe(name string) (DelegateProbe, error)
}

// DelegateProbe 一次连通性探测的回执（设置窗「测试连接」显示的就是这些字段）。
type DelegateProbe struct {
	ProtocolVersion int
	Title           string
	Version         string
	AuthMethods     []string // 对方支持的登录方式（已登录也照报，不代表当前没登录）
	LoadSession     bool
	PromptImages    bool
	PromptAudio     bool
	PromptEmbedded  bool
	McpHTTP         bool
	McpSSE          bool
}

// DelegateSnapshot 外部代理页的快照。
type DelegateSnapshot struct {
	Agents []DelegateAgentState
	// Secrets 已存密钥的名字（env 值写 `secret:<名>` 时的候选；取不到给空）。
	// 只有名字，值永不出库（§9）。
	Secrets []string
	// Note 配置层的一句话（零代理时给「去哪写一条」，读不出配置时说原因）。
	Note string
}

// DelegateAgentState 一个代理一行。
type DelegateAgentState struct {
	Name        string   // 配置里的名字（delegate 工具 agent 参数的取值）
	CommandLine string   // `command args...` 的展示形态（改这里就是改它）
	Command     string   // 字段级编辑：命令
	Args        []string // 字段级编辑：参数（一行一个）
	Env         map[string]string
	WorkDir     string
	Title       string // initialize 报的 agentInfo.title；还没转过为空
	Version     string
	Available   bool // 命令在 PATH 上（只探存在，不握手）
	Enabled     bool
	Default     bool
	Note        string // 不可用的原因（用户面一句话）
}

// delegateBadges 状态徽标：可用/不可用、已停用、缺省。只画需要的那几个。
func (f *settingsForm) delegateBadges(gtx layout.Context, th *material.Theme, st DelegateAgentState) layout.Dimensions {
	type badge struct {
		text string
		col  color.NRGBA
	}
	badges := make([]badge, 0, 3)
	switch {
	case !st.Enabled:
		badges = append(badges, badge{i18n.T("ui.settings.dlg.badge_off"), paperOf().text3})
	case st.Available:
		badges = append(badges, badge{i18n.T("ui.settings.dlg.badge_ok"), paperOf().ok})
	default:
		badges = append(badges, badge{i18n.T("ui.settings.dlg.badge_bad"), paperOf().danger})
	}
	if st.Default {
		badges = append(badges, badge{i18n.T("ui.settings.dlg.badge_default"), paperOf().text3})
	}
	children := make([]layout.FlexChild, 0, len(badges))
	for _, b := range badges {
		b := b
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperMicroSp(th), b.text)
			l.Color = b.col
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, l.Layout)
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}

// metaLine 第二行：不可用原因 + 代理自报的标题版本。
func (st DelegateAgentState) metaLine() string {
	parts := make([]string, 0, 2)
	if st.Note != "" {
		parts = append(parts, st.Note)
	}
	if st.Title != "" || st.Version != "" {
		name := strings.TrimSpace(st.Title)
		if name == "" {
			name = i18n.T("ui.settings.dlg.no_title")
		}
		if st.Version != "" {
			name += " " + st.Version
		}
		parts = append(parts, name)
	} else {
		parts = append(parts, i18n.T("ui.settings.dlg.never_ran"))
	}
	return strings.Join(parts, " · ")
}

// delegateForm 一条代理的字段编辑档（展开后才可见；按名字 get-or-create）。
//
// 与快照的关系：**快照是事实，档是草稿**。展开、保存成功、重新检测这三个时点把档对齐
// 回快照；其余帧不动它——每帧 SetText 会打断正在敲的字。
type delegateForm struct {
	name    widget.Editor // 名字（改名也在这里改：保存时先改名再改别的字段）
	cmd     widget.Editor
	args    widget.Editor // 多行：一行一个参数
	env     widget.Editor // 多行：一行一个 KEY=VALUE
	workdir widget.Editor
}

// newDelegateForm 新建一条字段档（只设单行/多行属性；文本由 loadDelegateForm 填）。
func newDelegateForm() *delegateForm {
	f := &delegateForm{}
	for _, ed := range []*widget.Editor{&f.name, &f.cmd, &f.workdir} {
		ed.SingleLine = true
	}
	return f
}

// delegateProbeOutcome 一次「测试连接」的结果（旁路 goroutine → 通道 → 界面循环）。
type delegateProbeOutcome struct {
	name string
	info DelegateProbe
	err  error
}

// syncDelegateSnapshot 取一次清单（开窗、动作之后、以及「重新检测」按钮）。
//
// **不重置草稿**：用户的输入在 widget.Editor 里，每帧重建就会把正在敲的字抹掉。
// 只有展开、保存成功、重新检测这三个时点才把草稿对齐回配置里的值。
func (f *settingsForm) syncDelegateSnapshot() {
	if f.delegates == nil {
		return
	}
	f.delegateSnap = f.delegates.Snapshot()
}

// delegateFormFor 取（或建）某个代理的字段档。
func (f *settingsForm) delegateFormFor(name string) *delegateForm {
	if f.delegateForms == nil {
		f.delegateForms = map[string]*delegateForm{}
	}
	df := f.delegateForms[name]
	if df == nil {
		df = newDelegateForm()
		f.delegateForms[name] = df
	}
	return df
}

// loadDelegateForm 把字段档对齐回快照里的那一条。
func (f *settingsForm) loadDelegateForm(st DelegateAgentState) {
	df := f.delegateFormFor(st.Name)
	df.name.SetText(st.Name)
	df.cmd.SetText(st.Command)
	df.args.SetText(strings.Join(st.Args, "\n"))
	df.env.SetText(formatDelegateEnv(st.Env))
	df.workdir.SetText(st.WorkDir)
}

// renameDelegateForm 改名后把档与展开态迁到新名字上（旧档丢掉：名字是 map 的键）。
func (f *settingsForm) renameDelegateForm(oldName, newName string) {
	if df := f.delegateForms[oldName]; df != nil {
		delete(f.delegateForms, oldName)
		f.delegateForms[newName] = df
	}
	if f.delegateOpen == oldName {
		f.delegateOpen = newName
	}
	if f.delegateConfr == oldName {
		f.delegateConfr = ""
	}
	if out, ok := f.delegateProbe[oldName]; ok {
		delete(f.delegateProbe, oldName)
		out.name = newName
		f.delegateProbe[newName] = out
	}
	if f.delegateBusy[oldName] {
		delete(f.delegateBusy, oldName)
		f.delegateBusy[newName] = true
	}
}

// delegateButton 动作按钮（get-or-create 按 key）。
func (f *settingsForm) delegateButton(key, label string, kind paperBtnKind, th *material.Theme) layout.Widget {
	btn := f.delegateBtns[key]
	if btn == nil {
		btn = new(widget.Clickable)
		f.delegateBtns[key] = btn
	}
	return paperButton(th, btn, label, kind, true)
}

// delegateAgentRow 一个代理的头部：名字 + 状态徽标 + 第二行脚注；右侧是动作。
//
// 动作按「改身份 / 改状态」分组：启停与设缺省是常用开关，放前面；删除放在最后，
// 且要过一道行内确认（它改的是配置里的一条，没有撤销）。
func (f *settingsForm) delegateAgentRow(gtx layout.Context, th *material.Theme, st DelegateAgentState) layout.Dimensions {
	confirming := f.delegateConfr == st.Name
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return layout.Inset{Right: unit.Dp(8)}.Layout(gtx,
										material.Body1(th, st.Name).Layout)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return f.delegateBadges(gtx, th, st)
								}),
							)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Label(th, paperMicroSp(th), st.metaLine())
							l.Color = paperOf().text3
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if confirming {
									return layout.Dimensions{}
								}
								label := i18n.T("ui.common.disable")
								if !st.Enabled {
									label = i18n.T("ui.common.enable")
								}
								return layout.Inset{Right: unit.Dp(6)}.Layout(gtx,
									f.delegateButton("on:"+st.Name, label, paperBtnSecondary, th))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if st.Default || confirming {
									return layout.Dimensions{}
								}
								return layout.Inset{Right: unit.Dp(6)}.Layout(gtx,
									f.delegateButton("def:"+st.Name,
										i18n.T("ui.settings.dlg.set_default"), paperBtnSecondary, th))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if confirming {
									return layout.Dimensions{}
								}
								label := i18n.T("ui.settings.dlg.edit")
								act := "open:"
								if f.delegateOpen == st.Name {
									label = i18n.T("ui.settings.dlg.collapse")
									act = "close:"
								}
								return layout.Inset{Right: unit.Dp(6)}.Layout(gtx,
									f.delegateButton(act+st.Name, label, paperBtnSecondary, th))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if !confirming {
									return f.delegateButton("del:"+st.Name,
										i18n.T("ui.settings.dlg.remove"), paperBtnDanger, th)(gtx)
								}
								return f.delegateButton("delok:"+st.Name,
									i18n.T("ui.settings.dlg.remove_confirm"), paperBtnDanger, th)(gtx)
							}),
						)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !confirming {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), i18n.Tf("ui.settings.dlg.remove_hint", "name", st.Name))
				l.Color = paperOf().danger
				return l.Layout(gtx)
			})
		}),
	)
}

// delegateFieldRow 展开区的一行字段：定宽标签 + 控件（单行或多行共用）。
func (f *settingsForm) delegateFieldRow(gtx layout.Context, th *material.Theme, label string, ed *widget.Editor, hint string, h int) layout.Dimensions {
	box := func(gtx layout.Context) layout.Dimensions {
		return editorBox(th, ed, hint)(gtx)
	}
	if h > 0 {
		box = func(gtx layout.Context) layout.Dimensions {
			return editorBoxH(th, ed, hint, h)(gtx)
		}
	}
	return fieldRow(gtx, th, label, box)
}

// delegateSaveRow 展开区的动作行：保存 + 测试连接。
func (f *settingsForm) delegateSaveRow(gtx layout.Context, th *material.Theme, name string) layout.Dimensions {
	probeLabel := i18n.T("ui.settings.dlg.test_conn")
	busy := f.delegateBusy[name]
	if busy {
		probeLabel = i18n.T("ui.settings.dlg.testing")
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx,
				f.delegateButton("save:"+name, i18n.T("ui.common.save"), paperBtnPrimary, th))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if busy {
				// 在途：按钮换成纯文字，不给人连点的机会（一次探测要起一个进程）。
				l := material.Label(th, paperMicroSp(th), probeLabel)
				l.Color = paperOf().text3
				return l.Layout(gtx)
			}
			return f.delegateButton("probe:"+name, probeLabel, paperBtnSecondary, th)(gtx)
		}),
	)
}

// delegateProbeRow 上一次探测的回执：成功摊开回执，失败给一句话 + 原因。
func (f *settingsForm) delegateProbeRow(gtx layout.Context, th *material.Theme, name string) layout.Dimensions {
	out, ok := f.delegateProbe[name]
	if !ok {
		return layout.Dimensions{}
	}
	if out.err != nil {
		l := material.Label(th, paperMicroSp(th), i18n.Tf("ui.settings.dlg.test_failed", "err", out.err.Error()))
		l.Color = paperOf().danger
		return l.Layout(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperMicroSp(th), i18n.Tf("ui.settings.dlg.test_ok",
				"title", probeTitle(out.info), "version", out.info.Version,
				"proto", strconv.Itoa(out.info.ProtocolVersion)))
			l.Color = paperOf().ok
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if caps := delegateCapLine(out.info); caps != "" {
				l := material.Label(th, paperMicroSp(th), caps)
				l.Color = paperOf().text3
				return l.Layout(gtx)
			}
			return layout.Dimensions{}
		}),
	)
}

// probeTitle 标题缺省时用一句占位（有的代理不报 agentInfo.title）。
func probeTitle(p DelegateProbe) string {
	if strings.TrimSpace(p.Title) == "" {
		return i18n.T("ui.settings.dlg.no_title")
	}
	return p.Title
}

// delegateCapLine 能力清单的人话：只列对方申报为 true 的项，一个都没有就不出这一行。
func delegateCapLine(p DelegateProbe) string {
	var parts []string
	add := func(on bool, key string) {
		if on {
			parts = append(parts, i18n.T(key))
		}
	}
	add(p.LoadSession, "ui.settings.dlg.cap_load")
	add(p.PromptImages, "ui.settings.dlg.cap_image")
	add(p.PromptAudio, "ui.settings.dlg.cap_audio")
	add(p.PromptEmbedded, "ui.settings.dlg.cap_embedded")
	add(p.McpHTTP, "ui.settings.dlg.cap_mcp_http")
	add(p.McpSSE, "ui.settings.dlg.cap_mcp_sse")
	if len(parts) == 0 {
		if len(p.AuthMethods) > 0 {
			return i18n.Tf("ui.settings.dlg.cap_auth", "methods", strings.Join(p.AuthMethods, i18n.T("ui.common.list_sep")))
		}
		return i18n.T("ui.settings.dlg.cap_none")
	}
	line := i18n.Tf("ui.settings.dlg.cap_list", "caps", strings.Join(parts, i18n.T("ui.common.list_sep")))
	if len(p.AuthMethods) > 0 {
		line += " · " + i18n.Tf("ui.settings.dlg.cap_auth", "methods", strings.Join(p.AuthMethods, i18n.T("ui.common.list_sep")))
	}
	return line
}

// formatDelegateEnv 环境变量表 → 多行文本（按键排序：同配置每次展开看到同一顺序）。
func formatDelegateEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+env[k])
	}
	return strings.Join(lines, "\n")
}

// parseEnvText 多行 KEY=VALUE → 表。空行与 `#` 开头的注释忽略；没有 `=` 的行按行号报错
// ——静默跳过等于把用户写的变量悄悄丢掉。
func parseEnvText(text string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, errors.New(i18n.Tf("cmd.delegate.err.env_line", "line", i+1))
		}
		out[k] = v // 值不 trim：前后空白可能是密钥或参数的一部分
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// lines 多行文本 → 去空白、去空行的切片（args 用）。
func lines(text string) []string {
	out := []string{}
	for _, line := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dispatchDelegateAction 按钮分发（key = "<动作>:<名字>"）。
//
// 除「测试连接」外都是同步的本地写：改一段 config + 立即重取快照，失败也马上报到状态行。
// 探测要起真进程，走旁路 goroutine，结果经 delegateRes 回灌（见 consumeDelegateProbes）。
func (u *UI) dispatchDelegateAction(f *settingsForm, key string) {
	if f.delegates == nil {
		return
	}
	act, name, ok := strings.Cut(key, ":")
	if !ok {
		return
	}
	var err error
	status := ""
	switch act {
	case "on":
		enabled := false
		for _, a := range f.delegateSnap.Agents {
			if a.Name == name {
				enabled = !a.Enabled
			}
		}
		err = f.delegates.SetEnabled(name, enabled)
		if enabled {
			status = i18n.Tf("ui.settings.dlg.enabled", "name", name)
		} else {
			status = i18n.Tf("ui.settings.dlg.disabled", "name", name)
		}
	case "def":
		err = f.delegates.SetDefault(name)
		status = i18n.Tf("ui.settings.dlg.set_default_done", "name", name)
	case "open":
		f.delegateOpen = name
		f.delegateConfr = ""
		for _, a := range f.delegateSnap.Agents {
			if a.Name == name {
				f.loadDelegateForm(a)
			}
		}
		return
	case "close":
		f.delegateOpen = ""
		f.delegateConfr = ""
		return
	case "del":
		f.delegateConfr = name
		return
	case "delok":
		f.delegateConfr = ""
		err = f.delegates.Remove(name)
		status = i18n.Tf("ui.settings.dlg.removed", "name", name)
		if err == nil {
			delete(f.delegateForms, name)
			delete(f.delegateProbe, name)
			if f.delegateOpen == name {
				f.delegateOpen = ""
			}
		}
	case "save":
		err, status = f.saveDelegateForm(name)
	case "probe":
		f.startDelegateProbe(name)
		return
	case "add":
		addName := strings.TrimSpace(f.delegateAddN.Text())
		addCmd := strings.TrimSpace(f.delegateAddC.Text())
		if addName == "" {
			f.delegateStat, f.delegateErr = i18n.T("cmd.delegate.err.name_required"), true
			return
		}
		if addCmd == "" {
			f.delegateStat, f.delegateErr = i18n.T("ui.settings.dlg.cmd_empty"), true
			return
		}
		err = f.delegates.Add(addName, addCmd)
		status = i18n.Tf("ui.settings.dlg.added", "name", addName)
		if err == nil {
			f.delegateAddN.SetText("")
			f.delegateAddC.SetText("")
		}
	default:
		return
	}
	if err != nil {
		f.delegateStat, f.delegateErr = i18n.Tf("ui.common.failed", "err", err.Error()), true
		return
	}
	f.delegateStat, f.delegateErr = status, false
	f.syncDelegateSnapshot()
	// 保存成功后把草稿对齐回配置（名字可能改过，按新名字重取）。
	if act == "save" {
		for _, a := range f.delegateSnap.Agents {
			if a.Name == f.delegateOpen {
				f.loadDelegateForm(a)
			}
		}
	}
	if f.win != nil {
		f.win.Invalidate()
	}
}

// saveDelegateForm 保存一条的整块字段：先全部本地解析与校验，再按序写。
//
// 「先校验后写」是这一段唯一的顺序要求：一次保存会落五六个键，写到一半才因为环境变量
// 有一行写错而停下，盘上就留下一份只改了一半的配置。解析全部前置之后，失败时一个键
// 都没动。
func (f *settingsForm) saveDelegateForm(name string) (error, string) {
	df := f.delegateFormFor(name)
	newName := strings.TrimSpace(df.name.Text())
	if newName == "" {
		return errors.New(i18n.T("cmd.delegate.err.name_required")), ""
	}
	command := strings.TrimSpace(df.cmd.Text())
	if command == "" {
		return errors.New(i18n.T("cmd.delegate.err.command_required")), ""
	}
	env, err := parseEnvText(df.env.Text())
	if err != nil {
		return err, ""
	}
	args := lines(df.args.Text())
	workdir := strings.TrimSpace(df.workdir.Text())

	// 写：改名先做，后面几个动作都按新名字写。
	cur := name
	if newName != name {
		if err := f.delegates.Rename(name, newName); err != nil {
			return err, ""
		}
		f.renameDelegateForm(name, newName)
		cur = newName
	}
	if err := f.delegates.SetCommand(cur, command); err != nil {
		return err, ""
	}
	if err := f.delegates.SetArgs(cur, args); err != nil {
		return err, ""
	}
	if err := f.delegates.SetEnv(cur, env); err != nil {
		return err, ""
	}
	if err := f.delegates.SetWorkdir(cur, workdir); err != nil {
		return err, ""
	}
	return nil, i18n.Tf("ui.settings.dlg.saved", "name", cur)
}

// startDelegateProbe 起一次探测（旁路 goroutine + 结果通道）。
//
// 同一代理不并发探测两次：一次探测要起一个进程，连点没有意义，只会有两个进程互不相关
// 地回执（后到的会盖掉先到的）。
func (f *settingsForm) startDelegateProbe(name string) {
	if f.delegateBusy == nil {
		f.delegateBusy = map[string]bool{}
	}
	if f.delegateBusy[name] {
		return
	}
	f.delegateBusy[name] = true
	f.delegateWait++
	f.delegateStat, f.delegateErr = i18n.Tf("ui.settings.dlg.testing_name", "name", name), false
	go func() {
		info, err := f.delegates.Probe(name)
		f.delegateRes <- delegateProbeOutcome{name: name, info: info, err: err}
	}()
}

// consumeDelegateProbes 收探测结果（帧首调用；没有结果就立即返回，不阻塞界面循环）。
func (f *settingsForm) consumeDelegateProbes() bool {
	if f.delegates == nil {
		return false
	}
	changed := false
	for {
		select {
		case out := <-f.delegateRes:
			if f.delegateWait > 0 {
				f.delegateWait--
			}
			delete(f.delegateBusy, out.name)
			if f.delegateProbe == nil {
				f.delegateProbe = map[string]delegateProbeOutcome{}
			}
			f.delegateProbe[out.name] = out
			if out.err != nil {
				f.delegateStat, f.delegateErr = i18n.Tf("ui.settings.dlg.test_failed", "err", out.err.Error()), true
			} else {
				f.delegateStat, f.delegateErr = i18n.Tf("ui.settings.dlg.test_ok",
					"title", probeTitle(out.info), "version", out.info.Version,
					"proto", strconv.Itoa(out.info.ProtocolVersion)), false
			}
			f.syncDelegateSnapshot() // 握手学到的标题版本顺手进清单第二行
			changed = true
		default:
			return changed
		}
	}
}
