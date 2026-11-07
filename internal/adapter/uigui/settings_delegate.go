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
// 与工具页的差别：这里的动作是本地读写（改一段 JSON + 一次 PATH 探测），同步做完即可，
// 不进异步通道——没有网络、没有握手。

import (
	"image/color"
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
	// SetRoots 改根集（绝对路径；空 = 删掉 roots 键）。本版只落盘，执行侧在 A2 才读。
	SetRoots(name string, roots []string) error
	// SetEnabled 启用/停用一个代理（写 enabled；重启生效）。
	SetEnabled(name string, enabled bool) error
	// SetDefault 指定缺省代理（写 default；同一条上唯一的 true，重启生效）。
	SetDefault(name string) error
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
	Roots       []string
	Title       string // initialize 报的 agentInfo.title；还没转过为空
	Version     string
	Available   bool // 命令在 PATH 上（只探存在，不握手）
	Enabled     bool
	Default     bool
	Note        string // 不可用的原因（用户面一句话）
}

// syncDelegateSnapshot 取一次清单（开窗、动作之后、以及「重新检测」按钮）。
//
// **不重置编辑档**：用户的输入在 widget.Editor 里，每帧重建就会把正在敲的字抹掉。
// 编辑档按名字 get-or-create，只有保存成功之后才把它的文本对齐回配置里的值。
func (f *settingsForm) syncDelegateSnapshot() {
	if f.delegates == nil {
		return
	}
	f.delegateSnap = f.delegates.Snapshot()
}

// delegateEditor 取（或建）某个代理的命令行编辑档。
func (f *settingsForm) delegateEditor(name string) *widget.Editor {
	if f.delegateEditors == nil {
		f.delegateEditors = map[string]*widget.Editor{}
	}
	ed := f.delegateEditors[name]
	if ed == nil {
		ed = &widget.Editor{SingleLine: true}
		f.delegateEditors[name] = ed
	}
	return ed
}

// resetDelegateEditor 把编辑档对齐回配置里的命令行（保存成功、或「重新检测」之后）。
func (f *settingsForm) resetDelegateEditor(name, line string) {
	ed := f.delegateEditor(name)
	if ed.Text() != line {
		ed.SetText(line)
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

// delegateAgentRow 一个代理一块：名字 + 状态 + 动作；下面一行是命令行编辑档。
func (f *settingsForm) delegateAgentRow(gtx layout.Context, th *material.Theme, st DelegateAgentState) layout.Dimensions {
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
								label := i18n.T("ui.common.disable")
								if !st.Enabled {
									label = i18n.T("ui.common.enable")
								}
								return layout.Inset{Right: unit.Dp(6)}.Layout(gtx,
									f.delegateButton("on:"+st.Name, label, paperBtnSecondary, th))
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								if st.Default {
									return layout.Dimensions{}
								}
								return f.delegateButton("def:"+st.Name,
									i18n.T("ui.settings.dlg.set_default"), paperBtnSecondary, th)(gtx)
							}),
						)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return fieldRow(gtx, th, i18n.T("ui.settings.dlg.cmd_label"),
							editorBox(th, f.delegateEditor(st.Name), i18n.T("ui.settings.dlg.cmd_hint")))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return f.delegateButton("save:"+st.Name, i18n.T("ui.common.save"), paperBtnPrimary, th)(gtx)
						})
					}),
				)
			})
		}),
	)
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

// dispatchDelegateAction 按钮分发（key = "<动作>:<名字>"）。动作同步做完、立即刷新快照：
// 写的是本地 config，失败也要让用户马上看到原因。
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
	case "save":
		line := strings.TrimSpace(f.delegateEditor(name).Text())
		if line == "" {
			f.delegateStatus, f.delegateErr = i18n.T("ui.settings.dlg.cmd_empty"), true
			return
		}
		err = f.delegates.SetCommandLine(name, line)
		status = i18n.Tf("ui.settings.dlg.cmd_saved", "name", name)
	default:
		return
	}
	if err != nil {
		f.delegateStatus, f.delegateErr = i18n.Tf("ui.common.failed", "err", err.Error()), true
		return
	}
	f.delegateStatus, f.delegateErr = status, false
	f.syncDelegateSnapshot()
	for _, a := range f.delegateSnap.Agents {
		if a.Name == name {
			f.resetDelegateEditor(a.Name, a.CommandLine)
		}
	}
	if f.win != nil {
		f.win.Invalidate()
	}
}
