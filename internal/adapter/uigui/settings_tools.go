package uigui

// 工具与 MCP 管理页数据面（S6/D143）：消费方定义接口与快照类型，装配根实现。
//
// 并发口径（同 §15.7）：快照经原子槽读取（次窗 goroutine 无锁读），动作**一律异步**
// ——`Host.Enable` 拨号上限 15s、信任/能力确认弹窗在主窗，次窗同步调必冻窗；动作在
// 旁路 goroutine 执行，结果经通道回灌，完成后 Invalidate 重绘（快照 Revision 已递增）。

import (
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// ToolsAdmin 工具与 MCP 管理面（装配根实现；线程安全——次窗 goroutine 调用）。
type ToolsAdmin interface {
	// Snapshot 原子快照（含 Revision，任何变更递增）。
	Snapshot() ToolsSnapshot
	// SetBuiltinEnabled 内置工具启停（热生效；写回 config tools.disabled）。未知名报错。
	SetBuiltinEnabled(name string, enabled bool) error
	// SetPluginEnabled 插件启停（Enable 含信任门与 capability 授权流——会弹主窗确认）。
	SetPluginEnabled(name string, enabled bool) error
	// GrantCap 手动预授权一个能力（免一次确认；省确认不等于绕过风险，UI 需标注）。
	GrantCap(name string, cap string) error
	// RevokeGrant 撤销一个能力：同时停用该插件并清除授权（再启用即重走确认）。
	RevokeGrant(name string, cap string) error
	// RevokeTrust 撤销插件目录来源插件的启动信任（清指纹 + 断开）；config 来源报错。
	RevokeTrust(name string) error
	// Logs 插件调用日志：写临时文件并打开系统编辑器展示（复用 /memory 的 pickEditor）；
	// name 空 = 各插件最近调用摘要。返回状态文案。
	Logs(name string) (string, error)
}

// ToolsSnapshot 工具与 MCP 页的原子快照。
type ToolsSnapshot struct {
	Revision int64
	Builtins []BuiltinToolState // 内置工具（含禁用态）
	Plugins  []PluginToolState  // MCP 插件（config / 插件目录两来源）
}

// BuiltinToolState 单个内置工具。
type BuiltinToolState struct {
	Name    string // 工具名（与 Spec 一致；契约）
	Label   string // 中文词表（展示）
	Enabled bool   // 禁用集之外 = 可见/可用
}

// PluginToolState 单个插件。
type PluginToolState struct {
	Name         string
	Source       string // "config" | "plugins"
	Transport    string // "stdio" | "streamable-http"
	Status       string // plugin.Status 字符串
	Enabled      bool
	Risk         string
	Capabilities []string
	Granted      []string // 已授权能力
	Trusted      bool     // source=plugins 且已确认启动信任指纹（撤销信任入口显示条件）
	Restarts     int
	LastErr      string
	Calls        int
	Errors       int
	LastMS       int64
}

// toolActionResult 工具页异步动作结果（回灌状态行）。
type toolActionResult struct {
	status string
	err    error
}

// toolsAsync 工具页动作统一异步执行（S6/D143）：do 在旁路 goroutine 运行，返回
// 完成文案与错误；结果经通道回灌，完成即 Invalidate 重绘。任何路径都不阻塞次窗。
func (u *UI) toolsAsync(f *settingsForm, do func() (string, error)) {
	f.toolWait++
	go func() {
		status, err := do()
		f.toolRes <- toolActionResult{status: status, err: err}
		if f.win != nil {
			f.win.Invalidate()
		}
	}()
}

// drainToolResults 取尽异步动作结果（帧内非阻塞）：回灌状态行（错误带文案）。
func (f *settingsForm) drainToolResults() {
	for {
		select {
		case r := <-f.toolRes:
			f.toolWait--
			f.toolsStatus, f.toolsErr = r.status, r.err != nil
			if r.err != nil {
				f.toolsStatus = i18n.Tf("ui.common.failed", "err", r.err.Error())
			}
		default:
			return
		}
	}
}

// syncToolsSnapshot 工具页原子快照同步（Revision 变了才重建行与按钮；同历史窗口径）。
// 帧首调用一次（settingsFrame）；测试可直接调以铺好行表。
func (f *settingsForm) syncToolsSnapshot() {
	if f.tools == nil {
		return
	}
	cur := f.tools.Snapshot()
	if cur.Revision != f.toolsRev {
		f.toolsRev = cur.Revision
		f.toolsSnap = cur
	}
}

// dispatchToolAction 工具页按钮分发（key = 动作签名，见各构建处）。按钮只在
// 「工具与 MCP」页渲染；快照取上帧同步值（1 帧滞后可接受）。一律异步。
func (u *UI) dispatchToolAction(f *settingsForm, key string) {
	snap := f.toolsSnap
	switch {
	case strings.HasPrefix(key, "b:"): // 内置工具启停
		name := strings.TrimPrefix(key, "b:")
		for _, b := range snap.Builtins {
			if b.Name == name {
				enabled := b.Enabled
				u.toolsAsync(f, func() (string, error) {
					if err := f.tools.SetBuiltinEnabled(name, !enabled); err != nil {
						return "", err
					}
					return i18n.T("ui.settings.tool.builtin_updated"), nil
				})
				return
			}
		}
	case strings.HasPrefix(key, "p:"):
		// key = "p:<name>:<action>"；action 可为 "toggle"/"logs"/"trust"/"cap:<能力>"。
		// 按**首个 ':'** 切出 name 与整段 action——能力名里的 ':' 不能当分隔符（曾致 cap 按钮失效）。
		rest := strings.TrimPrefix(key, "p:")
		i := strings.Index(rest, ":")
		if i < 0 {
			return
		}
		name, act := rest[:i], rest[i+1:]
		switch act {
		case "toggle":
			target := true
			for _, p := range snap.Plugins {
				if p.Name == name {
					target = !p.Enabled
				}
			}
			u.toolsAsync(f, func() (string, error) {
				if err := f.tools.SetPluginEnabled(name, target); err != nil {
					return "", err
				}
				return i18n.T("ui.settings.tool.plugin_updated"), nil
			})
		case "logs":
			u.toolsAsync(f, func() (string, error) { return f.tools.Logs(name) })
		case "trust":
			u.toolsAsync(f, func() (string, error) {
				if err := f.tools.RevokeTrust(name); err != nil {
					return "", err
				}
				return i18n.T("ui.settings.tool.trust_revoked"), nil
			})
		default: // "cap:<cap>"：已授权 = 撤销；未授权 = 手动预授权
			if !strings.HasPrefix(act, "cap:") {
				return
			}
			capName := strings.TrimPrefix(act, "cap:")
			granted := false
			for _, p := range snap.Plugins {
				if p.Name == name {
					for _, g := range p.Granted {
						if g == capName {
							granted = true
						}
					}
				}
			}
			if granted {
				u.toolsAsync(f, func() (string, error) {
					if err := f.tools.RevokeGrant(name, capName); err != nil {
						return "", err
					}
					return i18n.T("ui.settings.tool.cap_revoked"), nil
				})
			} else {
				u.toolsAsync(f, func() (string, error) {
					if err := f.tools.GrantCap(name, capName); err != nil {
						return "", err
					}
					return i18n.T("ui.settings.tool.cap_granted"), nil
				})
			}
		}
	}
}

// toolActionButton 工具页动作按钮（get-or-create 按 key 缓存；文案按帧给定）。
func (f *settingsForm) toolActionButton(gtx layout.Context, th *material.Theme, key, label string) layout.Dimensions {
	btn := f.toolBtns[key]
	if btn == nil {
		btn = new(widget.Clickable)
		f.toolBtns[key] = btn
	}
	return material.Button(th, btn, label).Layout(gtx)
}

// builtinToolRow 内置工具行：中文词表 + 工具名 + 后果提示 + 启停按钮。
func (f *settingsForm) builtinToolRow(gtx layout.Context, th *material.Theme, b BuiltinToolState) layout.Dimensions {
	hint := ""
	switch b.Name {
	case "think":
		hint = i18n.T("ui.settings.tool.hint.think")
	case "context_compact":
		hint = i18n.T("ui.settings.tool.hint.compact")
	}
	action := i18n.T("ui.common.disable")
	if !b.Enabled {
		action = i18n.T("ui.common.enable")
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body1(th, b.Label)
								return l.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(th, b.Name)
								l.Color = tok().textDim
								return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return l.Layout(gtx)
								})
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if hint == "" {
							return layout.Dimensions{}
						}
						l := material.Body2(th, hint)
						l.Color = tok().textMuted
						return l.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return f.toolActionButton(gtx, th, "b:"+b.Name, action)
		}),
	)
}

// pluginStatusLabel plugin.Status → 展示名（工具页；未知原样露出状态串）。
// **必须是函数**：语言在 main 里才定，包级 map 存下的会永远是本机 locale 那套。
func pluginStatusLabel(status string) string {
	switch status {
	case "disabled":
		return i18n.T("ui.common.disable")
	case "ready":
		return i18n.T("ui.settings.tool.status.ready")
	case "connecting":
		return i18n.T("ui.settings.tool.status.connecting")
	case "failed":
		return i18n.T("ui.settings.tool.status.failed")
	case "restarting":
		return i18n.T("ui.settings.tool.status.restarting")
	case "revoked":
		return i18n.T("ui.settings.tool.status.revoked")
	case "crashed":
		return i18n.T("ui.settings.tool.status.crashed")
	}
	return status
}

// pluginToolRow 插件行：名称/状态/来源传输/能力授权/动作按钮（启停、日志、撤销信任、
// 逐能力撤销或预授权）。撤销信任仅 source=plugins 且已信任时显示。
func (f *settingsForm) pluginToolRow(gtx layout.Context, th *material.Theme, p PluginToolState) layout.Dimensions {
	source := i18n.T("ui.settings.tool.source.plugins")
	if p.Source == "config" {
		source = "config"
	}
	status := pluginStatusLabel(p.Status)
	meta := i18n.Tf("ui.settings.tool.meta", "source", source, "transport", orDashCN(p.Transport))
	if p.Restarts > 0 {
		meta += i18n.Tf("ui.settings.tool.meta.restarts", "n", strconv.Itoa(p.Restarts))
	}
	if p.Calls > 0 {
		meta += i18n.Tf("ui.settings.tool.meta.calls", "n", strconv.Itoa(p.Calls))
		if p.Errors > 0 {
			meta += i18n.Tf("ui.settings.tool.meta.errors", "n", strconv.Itoa(p.Errors))
		}
	}
	action := i18n.T("ui.common.disable")
	if !p.Enabled {
		action = i18n.T("ui.common.enable")
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, p.Name).Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, "["+status+"]")
					l.Color = tok().textDim
					return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return l.Layout(gtx)
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, meta)
			l.Color = tok().textMuted
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(p.Capabilities) == 0 {
				return layout.Dimensions{}
			}
			var caps []string
			for _, c := range p.Capabilities {
				mark := i18n.T("ui.settings.tool.cap.ungranted")
				for _, g := range p.Granted {
					if g == c {
						mark = i18n.T("ui.settings.tool.cap.granted")
					}
				}
				caps = append(caps, c+"("+mark+")")
			}
			l := material.Body2(th, i18n.Tf("ui.settings.tool.caps_prefix", "list", strings.Join(caps, ", ")))
			l.Color = tok().textMuted
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceStart}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.toolActionButton(gtx, th, "p:"+p.Name+":toggle", action)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.toolActionButton(gtx, th, "p:"+p.Name+":logs", i18n.T("ui.settings.tool.logs"))
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if p.Source != "plugins" || !p.Trusted {
						return layout.Dimensions{}
					}
					return f.toolActionButton(gtx, th, "p:"+p.Name+":trust", i18n.T("ui.settings.tool.revoke_trust"))
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(p.Capabilities) == 0 {
				return layout.Dimensions{}
			}
			children := make([]layout.FlexChild, 0, len(p.Capabilities))
			for _, c := range p.Capabilities {
				capName := c
				granted := false
				for _, g := range p.Granted {
					if g == capName {
						granted = true
					}
				}
				label := i18n.Tf("ui.settings.tool.grant_prefix", "cap", capName)
				if granted {
					label = i18n.Tf("ui.settings.tool.revoke_prefix", "cap", capName)
				}
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return f.toolActionButton(gtx, th, "p:"+p.Name+":cap:"+capName, label)
					})
				}))
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.LastErr == "" {
				return layout.Dimensions{}
			}
			l := material.Body2(th, p.LastErr)
			l.Color = tok().textError
			return l.Layout(gtx)
		}),
	)
}

// orDashCN 空值显示为 "-"。
func orDashCN(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
