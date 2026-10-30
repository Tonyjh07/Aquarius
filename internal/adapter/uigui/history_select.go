package uigui

// history_select.go 左栏批量选择态（D249，§5.16）：进选择态后每行左侧出复选框、底部换成
// 动作条，导 / 删都按勾中的一组会话走一条命令。
//
// 为什么不做「导出全部会话」单列入口：全选就是整库。两个入口做同一件事，只会让「到底该点
// 哪个」变成一次猜测（契约里已经这么定）；选择态同时管住导出与删除两条批量动作。
//
// 四条口径：
//   - 勾选按**会话 ID** 记（不按行序）：列表是内核快照的投影，删一个会话、或另一窗切了会话，
//     行序就可能整体上移——按行序记会让勾选跳到别的会话上；
//   - 动作两条都只投命令：导出 = 保存框（只一档 aqx）→ `/export --ids <id>… --path <落点>`，
//     删除 = `/rmconv <id>…`（一次确认与回执都在内核）；
//   - ID 按**左栏顺序**给：命令里的写序就是归档清单顺序，勾选顺序不参与语义；
//   - **整行可点**：复选框是自绘图形而不是 `widget.Bool`——嵌在行点击件里的子控件收不到
//     指针事件（`histLeftColumn` 里对「✕」的实测记录），真塞一个进去会是个点不动的框。
//     所以「点行 = 勾选/取消勾选」，勾选框只是这一行的状态图示。
//
// 壳里依旧只拿路径与 ID，不读会话、不删文件（铁律 5）。

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// histSelBoxDp 选择态行内勾选框边长（dp）。行内左侧**恒**让出这么多，勾选不引起重排。
const histSelBoxDp unit.Dp = 14

// enterSelMode 进选择态（缺省一个都不勾：勾是用户的动作，不替他决定删/导什么）。
func (st *historyState) enterSelMode() {
	st.selMode = true
	st.sel = nil
	st.selPending = nil
}

// exitSelMode 退出选择态并清掉勾选（导完 / 删完 / 取消 / Esc）。
func (st *historyState) exitSelMode() {
	st.selMode = false
	st.sel = nil
}

// setSel 记一项勾选（false = 取消）。
func (st *historyState) setSel(id conversation.ID, on bool) {
	if !on {
		delete(st.sel, id)
		return
	}
	if st.sel == nil {
		st.sel = make(map[conversation.ID]bool, len(st.sessions))
	}
	st.sel[id] = true
}

// selCount 勾了几个。
func (st *historyState) selCount() int {
	n := 0
	for i := range st.sessions {
		if st.sel[st.sessions[i].ID] {
			n++
		}
	}
	return n
}

// selIDs 勾中的会话 ID，**按左栏顺序**给（命令里的写序即归档清单顺序；勾选顺序不参与语义）。
func (st *historyState) selIDs() []string {
	out := make([]string, 0, len(st.sel))
	for i := range st.sessions {
		if id := st.sessions[i].ID; st.sel[id] {
			out = append(out, string(id))
		}
	}
	return out
}

// selAll 全选；已经全选时再点 = 全不选。一颗钮管两头：这样「全选」不会变成没有回退方向的
// 死路，而多一颗「全不选」只是同一件事的第二个入口。
func (st *historyState) selAll() {
	n := st.selCount()
	if n > 0 && n == len(st.sessions) {
		st.sel = nil
		return
	}
	st.sel = make(map[conversation.ID]bool, len(st.sessions))
	for i := range st.sessions {
		st.sel[st.sessions[i].ID] = true
	}
}

// drainSelKeys 选择态下的键盘事件：Esc 退出（与遮罩同口径的「先撤退」直觉）。
//
// 选择态不是模态层（不铺 scrim、背景照常用），所以不夺焦点，只挂一条**不限焦点**的 Esc
// 过滤器：本次帧在历史窗里跑，键盘事件落到本窗时就该由这里接住；主窗是另一个窗口、另一个
// event 源，收不到这条。模态层在屏时不走这条（dispatch 见门即返回，Esc 归模态层自己）。
func (st *historyState) drainSelKeys(gtx layout.Context) {
	if !st.selMode {
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			st.exitSelMode()
			return
		}
	}
}

// requestBatchExport 批量导出的保存框：**只一档 aqx**（其余格式的容器装不下多会话），
// 预填 `aquarius-backup.aqx`（与命令面 `--all` 的缺省名同源，见 port.MultiExportStem）。
//
// 勾中的 ID 先存进 selPending：对话框是异步的，等落点回来时用户可能已经退出选择态
// （Esc 之类）——那时 sel 已经清空，不该把「一次点击」变成一条空命令。
func (st *historyState) requestBatchExport(u *UI) {
	ids := st.selIDs()
	if len(ids) == 0 {
		return
	}
	def, ok := exportFormatAt(0)
	if !ok || def.Name != "aqx" {
		return // 格式表变了（第一档不再是 aqx）：宁可什么都不发生，也不投一条猜出来的格式
	}
	st.selPending = ids
	u.plat.RequestSaveFile(platform.SaveRequest{
		Purpose:     platform.PurposeExchangeBatchExport,
		Title:       "导出选中的会话",
		Name:        port.MultiExportStem + def.Ext,
		DefExt:      strings.TrimPrefix(def.Ext, "."),
		Filters:     []platform.FileFilter{{Label: def.Label + " (*" + def.Ext + ")", Pattern: "*" + def.Ext}},
		FilterIndex: 0,
	})
}

// requestBatchDelete 批量删除：投 `/rmconv <id>…`（一次确认、回执一行，全在内核）。
func (st *historyState) requestBatchDelete(u *UI) {
	ids := st.selIDs()
	if len(ids) == 0 {
		return
	}
	st.sendCmd(u, "rmconv", ids...)
	st.exitSelMode()
}

// dispatchSel 选择态的左栏动作：动作条四颗 + 行点击（= 勾选切换）。
//
// 状态栏的取景控件不在这里——它们与选择无关，选择态期间照常可用（见 dispatch 的 switch）。
func (st *historyState) dispatchSel(u *UI, gtx layout.Context) {
	switch {
	case st.btnSelCancel.Clicked(gtx):
		st.exitSelMode()
		return
	case st.btnSelAll.Clicked(gtx):
		st.selAll()
		return
	case st.btnSelExport.Clicked(gtx):
		st.requestBatchExport(u)
		return
	case st.btnSelDel.Clicked(gtx):
		st.requestBatchDelete(u)
		return
	}
	// 行点击 = 勾选切换（整行都是命中区）。**不**切会话：选择态里点行表达的是「挑这一行」。
	for i := 0; i < len(st.sessions) && i < len(st.rows); i++ {
		if st.rows[i].Clicked(gtx) {
			id := st.sessions[i].ID
			st.setSel(id, !st.sel[id])
			return
		}
	}
}

// selRowByIndex 第 i 行的选择态内容（勾选态由行绑的会话 ID 从 st.sel 读——按 ID 记）。
func selRowByIndex(gtx layout.Context, th *material.Theme, st *historyState, i int) layout.Dimensions {
	if i < 0 || i >= len(st.sessions) {
		return layout.Dimensions{}
	}
	s := st.sessions[i]
	return selRow(gtx, th, s, st.sel[s.ID])
}

// selRow 选择态下的行内容：左侧勾选框（自绘）+ 标题 + （时间 · N 条）。勾选框槽位恒定，
// 勾选不引起文字重排。
func selRow(gtx layout.Context, th *material.Theme, s port.SessionSummary, checked bool) layout.Dimensions {
	pp := paperOf()
	title := s.Title
	if title == "" {
		title = "新会话"
	}
	title = truncRunes(title, 14) // 左侧让出勾选框槽后可用宽度少一截
	meta := s.UpdatedAt.Format("01-02 15:04") + " · " + strconv.Itoa(s.Messages) + " 条"
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return selBoxGlyph(gtx, checked)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), title)
					l.Color = pp.text
					l.Font.Weight = font.Bold
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, paperMicroSp(th), meta)
					l.Color = pp.text3
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
			)
		}),
	)
}

// selBoxGlyph 勾选框（纯绘制，不取事件）：未勾 = 描边空框，已勾 = 品牌色实心框 + 白勾。
//
// 自绘而不是 `material.CheckBox`：它内部是 `widget.Bool`，作为行点击件的子控件收不到
// 指针事件（行里的「✕」踩过同一个坑，见 histLeftColumn 的注释）。整行可点已经满足
// 「点一下就勾上」，勾选框只负责把状态画清楚。
func selBoxGlyph(gtx layout.Context, checked bool) layout.Dimensions {
	pp := paperOf()
	side := gtx.Dp(histSelBoxDp)
	box := image.Rect(0, 0, side, side)
	rad := gtx.Dp(unit.Dp(3))
	if checked {
		paint.FillShape(gtx.Ops, pp.brand, clip.UniformRRect(box, rad).Op(gtx.Ops))
		// 勾：两笔折线（按边长定比例，任何缩放下都读得出是「对」）。
		s := float32(side)
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(s*0.24, s*0.52))
		p.LineTo(f32.Pt(s*0.43, s*0.71))
		p.LineTo(f32.Pt(s*0.77, s*0.31))
		paint.FillShape(gtx.Ops, pp.accentFg,
			clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(unit.Dp(2)))}.Op())
	} else {
		// 未勾：先铺一整块描边色、再往里缩一像素铺卡面色——两层叠出 1px 空框，
		// 比 `clip.Stroke` 在极小尺寸下的抗锯齿表现稳定（14dp 的框只有几十像素）。
		paint.FillShape(gtx.Ops, pp.text3, clip.UniformRRect(box, rad).Op(gtx.Ops))
		bw := gtx.Dp(unit.Dp(1))
		paint.FillShape(gtx.Ops, pp.card,
			clip.UniformRRect(box.Inset(bw), rad).Op(gtx.Ops))
	}
	return layout.Dimensions{Size: image.Pt(side, side)}
}

// selActionBar 选择态的动作条（左栏底部两行）：上「全选 / 全不选」+「已选 N 个」，下
// 「取消 / 导出 / 删除」（未勾任何一项时后两颗呈禁用态）。空库时整条换成一句提示——
// 没有可勾的东西，留着两颗禁用钮只会让人以为界面坏了。
func (st *historyState) selActionBar(gtx layout.Context, th *material.Theme) layout.Dimensions {
	pp := paperOf()
	if len(st.sessions) == 0 {
		l := material.Caption(th, "没有会话可选")
		l.Color = pp.text3
		return l.Layout(gtx)
	}
	n := st.selCount()
	allLabel := "全选"
	if n > 0 && n == len(st.sessions) {
		allLabel = "全不选"
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(histTextBtn(th, &st.btnSelAll, allLabel)),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(th, "已选 "+strconv.Itoa(n)+" 个")
					l.Color = pp.text3
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &st.btnSelCancel, "取消", paperBtnSecondary, true)(gtx)
				}),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &st.btnSelExport, "导出", paperBtnPrimary, n > 0)(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return paperButton(th, &st.btnSelDel, "删除", paperBtnDanger, n > 0)(gtx)
				}),
			)
		}),
	)
}

// selBarPaint 动作条底色（sunken）：给列表与动作条之间分条——同一底色时动作条会长成
// 列表的最后一行。
func selBarPaint(gtx layout.Context) layout.Dimensions {
	paint.FillShape(gtx.Ops, paperOf().sunken,
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
}

// itoa 整数转串（左栏两处用到；避免各写一遍 strconv.Itoa）。
