package uigui

// settings_page_delegate.go 「外部代理」页（D270）：清单 / 可用性 / 缺省 / 命令行编辑。
// 数据面与动作见 settings_delegate.go。
//
// 页面只有一块内容：代理清单。每行两个层次——上面是身份与状态（名字、可用/已停用/缺省、
// 动作），下面是命令行编辑档。零代理时这块位置给一句「去哪写一条」，而不是留白。

import (
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// delegatePage 外部代理页模块。
type delegatePage struct{}

func (delegatePage) Key() string { return "delegate" }

func (delegatePage) Rows(f *settingsForm) []rowDesc {
	rows := []rowDesc{}
	if f.delegates == nil {
		return append(rows, rowDesc{kind: rowDlgEmpty, text: "未接线（只读占位）"})
	}
	rows = append(rows, sec("清单（config delegate.agents）"), rowDesc{kind: rowDlgNote})
	if len(f.delegateSnap.Agents) == 0 {
		text := "一个外部代理都没有。在 config.json 的 delegate.agents 里写一条（至少要有 name 与 command），" +
			"重启后这里会出现它；没配代理时模型拿不到 delegate* 工具。"
		if f.delegateSnap.Note != "" {
			text = f.delegateSnap.Note
		}
		rows = append(rows, rowDesc{kind: rowDlgEmpty, text: text})
	}
	for i := range f.delegateSnap.Agents {
		rows = append(rows, rowDesc{kind: rowDlgItem, field: i})
	}
	rows = append(rows, rowDesc{kind: rowDlgRefresh})
	if f.delegateStatus != "" {
		rows = append(rows, rowDesc{kind: rowDlgStatus})
	}
	return rows
}

func (delegatePage) Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	case rowDlgItem:
		if r.field < 0 || r.field >= len(f.delegateSnap.Agents) {
			return layout.Dimensions{}, true
		}
		return f.delegateAgentRow(gtx, th, f.delegateSnap.Agents[r.field]), true
	case rowDlgEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowDlgNote:
		l := material.Body2(th, "「可用」只探命令在不在 PATH 上，不做握手——启动时挨个拉起代理是"+
			"拿用户的时间换一张清单，所以标题与版本要等它真的跑过一次才有。"+
			"在这里改的都是 config，**重启生效**：转交管理器按启动时的配置装配。"+
			"要删掉一个代理请改 config.json（这里只改不删）。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowDlgRefresh:
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := f.delegateBtns["refresh"]
				if btn == nil {
					btn = new(widget.Clickable)
					f.delegateBtns["refresh"] = btn
				}
				return paperButton(th, btn, "重新检测", paperBtnSecondary, true)(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), "装在别处之后再扫一遍 PATH；不改任何配置")
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		), true
	case rowDlgStatus:
		l := material.Body2(th, f.delegateStatus)
		if f.delegateErr {
			l.Color = paperOf().danger
		} else {
			l.Color = paperOf().text3
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

func (delegatePage) Events(gtx layout.Context, u *UI, f *settingsForm) {
	if btn := f.delegateBtns["refresh"]; btn != nil {
		for btn.Clicked(gtx) {
			f.syncDelegateSnapshot()
			for _, a := range f.delegateSnap.Agents {
				f.resetDelegateEditor(a.Name, a.CommandLine)
			}
			f.delegateStatus, f.delegateErr = "已重新检测（可用性按 PATH 现算）", false
		}
	}
	for key, btn := range f.delegateBtns {
		if key == "refresh" {
			continue
		}
		if btn.Clicked(gtx) {
			u.dispatchDelegateAction(f, key)
		}
	}
}

// FirstEditor 开窗焦点落第一个代理的命令行（本页唯一的编辑档）。
func (delegatePage) FirstEditor(f *settingsForm) *widget.Editor {
	if len(f.delegateSnap.Agents) == 0 {
		return nil
	}
	return f.delegateEditor(f.delegateSnap.Agents[0].Name)
}
