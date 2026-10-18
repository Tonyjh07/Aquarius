package uigui

// history.go 会话历史次窗（D112/S3，§15.7）：左栏会话选择 + 右栏关系图 + 底部状态栏。
//
// 数据面（D120§6.1）：左栏 = port.SessionLister（app 原子发布快照），右图 = port.TreeView
// .Graph()——均为不可变只读快照，历史窗 goroutine 无锁读（§15.5），**不做任何 store I/O**。
//
// 行为（D120§5.2）：点会话行 = 投 `/switch <id>`（经 u.inCh，与键入同路径串行执行）；
// 切会话后 app 在同款发布点重建快照 → 本窗按**签名**识别变更、重排并重锚（R3：
// 平移/缩放/悬停不重排，只有数据变更 / rebudget 才重排——数据变更 = 快照签名变化）。
//
// 落地序（§10.3）：第 6 步 = 左栏 + 状态栏 + 窗壳（右图占位）；第 7 步 = 右图自绘、
// 手势与右键菜单（relmap_frame.go，接入 st.graph）。

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// historyState winHistory 次窗自持帧状态（runSecondary 创建、仅该窗 goroutine 读写）。
type historyState struct {
	// list/rows 左栏滚动与点击件（行数变化时 get-or-create，按序缓存）。
	list     widget.List
	rows     []*widget.Clickable
	sessions []port.SessionSummary // 上帧读到的左栏快照（行数对齐）
	// sig/model/vis 右图数据面：sig = 快照签名（变化才重建模型与预算，R3 懒重排）；
	// model = 派生模型（§6.2），vis = 当前预算切片（A7 的「呈现 M」）。
	sig    string
	model  *relModel
	vis    relVisible
	budget int // 预算（rebudget：−/＋/展开全部，A5/A7）
	// graph 右图帧态（第 7 步：重排/手势/绘制）。
	graph *relGraph
	// ov 消息查看/编辑遮罩（D139，nil = 未开启；开启期间背景照常渲染但不消费事件）。
	ov *overlayState
	// 状态栏控件（− / ＋ / 展开全部 / 装下整树 / 回到初始态 / 三个聚焦域：D128、D140）。
	btnMinus, btnPlus, btnAll, btnHome widget.Clickable
	btnSeed, btnLine, btnNode          widget.Clickable
}

// newHistoryState 构造 winHistory 帧状态并做首帧数据同步。
func newHistoryState(u *UI) *historyState {
	st := &historyState{
		list:   widget.List{List: layout.List{Axis: layout.Vertical}},
		budget: relDefaultBudget,
		graph:  newRelGraph(),
	}
	st.sync(u)
	return st
}

// title 当前会话标题（状态栏「会话：」与根节点陈述用）。
func (st *historyState) title() string {
	for _, s := range st.sessions {
		if s.Current {
			if s.Title == "" {
				return "新会话"
			}
			return s.Title
		}
	}
	return "（未就绪）"
}

// applyBudget rebudget：调整预算并重排（R3：数据/预算变更才重排；重排后仍收敛静止）。
func (st *historyState) applyBudget(delta int) {
	if st.model == nil {
		return
	}
	max := st.model.total
	if max > relHardCap {
		max = relHardCap
	}
	st.budget += delta
	if st.budget < 1 {
		st.budget = 1
	}
	if st.budget > max {
		st.budget = max
	}
	st.vis = relBudget(st.model, st.budget)
	if st.graph != nil {
		st.graph.rebuild(st.model, st.vis)
	}
}

// expandAll 「展开全部」：预算 = 全量（上限 relHardCap，§5.4.3）。
func (st *historyState) expandAll() {
	if st.model == nil {
		return
	}
	st.budget = st.model.total
	if st.budget > relHardCap {
		st.budget = relHardCap
	}
	st.vis = relBudget(st.model, st.budget)
	if st.graph != nil {
		st.graph.rebuild(st.model, st.vis)
	}
}

// relSig 快照签名：Total + Anchor + 末节点（Nodes 按 (CreatedAt, ID) 升序，任何树变更
// 都会反映到节点集或末节点——签名相同 = 数据未变 = 不重排）。
func relSig(g port.TreeGraph) string {
	var last conversation.MessageID
	if len(g.Nodes) > 0 {
		last = g.Nodes[len(g.Nodes)-1].ID
	}
	return fmt.Sprintf("%d|%s|%s", g.Total, g.Anchor, last)
}

// sync 帧首同步数据面：左栏快照直接取（行数对齐）；右图签名变化才重建模型与预算。
func (st *historyState) sync(u *UI) {
	if u.opts.Tree != nil {
		if g, ok := u.opts.Tree.Graph(); ok {
			if sig := relSig(g); sig != st.sig {
				st.sig = sig
				if m, ok := relNewModel(g); ok {
					st.model = m
					st.vis = relBudget(m, st.budget)
					if st.graph != nil {
						st.graph.rebuild(m, st.vis)
					}
				}
			}
		}
	}
	if u.opts.Lister != nil {
		st.sessions = u.opts.Lister.List()
	}
}

// rowClick 左栏会话行点击件 get-or-create（按行序缓存，同 branchClick 口径）。
func (st *historyState) rowClick(i int) *widget.Clickable {
	for len(st.rows) <= i {
		st.rows = append(st.rows, new(widget.Clickable))
	}
	return st.rows[i]
}

// sendSwitch 点会话行 → 投 /switch <id>（D120§5.2 resolve；经 u.inCh 与键入同路径，
// 壳内不旁路内核）。缓冲满时丢弃（历史窗无转写区，静默——与右图「失败不空转」同向）。
func (st *historyState) sendSwitch(u *UI, id conversation.ID) {
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "switch", Args: []string{string(id)}}}:
	default:
	}
}

// dispatch 帧内点击消费：会话行切换 + 状态栏控件（预算 −/＋/展开全部/装下整树/回到初始态/
// 两个聚焦域）。
//
// **遮罩开启期一律不取事件**（D139）：纵深防御——遮罩的整窗输入区已按 Gio 命中链把背景
// 指针事件一并遮蔽（overlay.go 头注），此处再显式关一道：将来若遮罩输入区不是整窗、
// 或背景控件被排到遮罩之上，模态语义仍在。左栏行与状态栏钮因此在遮罩期间完全失灵。
func (st *historyState) dispatch(u *UI, gtx layout.Context) {
	if st.overlayActive() {
		return
	}
	for i, c := range st.rows {
		if c.Clicked(gtx) {
			if i < len(st.sessions) {
				st.sendSwitch(u, st.sessions[i].ID)
			}
			return
		}
	}
	switch {
	case st.btnMinus.Clicked(gtx):
		st.applyBudget(-10)
	case st.btnPlus.Clicked(gtx):
		st.applyBudget(+10)
	case st.btnAll.Clicked(gtx):
		st.expandAll()
	case st.btnHome.Clicked(gtx): // 装下整树（D140：原「回到当前」——只对右基准位，树装不下时什么都改不了）
		if st.graph != nil {
			st.graph.viewPending = vaFitAll
		}
	case st.btnSeed.Clicked(gtx): // 回到初始态：重排 + 视口容纳整树
		if st.graph != nil {
			st.graph.relayoutFromSeed()
		}
	case st.btnLine.Clicked(gtx): // 聚焦当前历史：视口容纳 root → Head 的历史线
		if st.graph != nil {
			st.graph.viewPending = vaFocusLine
		}
	case st.btnNode.Clicked(gtx): // 聚焦当前节点：视口容纳 Head 及其父子一跳
		if st.graph != nil {
			st.graph.viewPending = vaFocusNode
		}
	}
}

// drainNodeAction 排空图侧菜单动作槽（D132）：菜单分发在 shell 线程，而**剪贴板写入必须
// 在 Gio 帧内**、聚焦待办属图状态——故两者都经 u.nodeAction 原子槽投到本窗帧首执行。
// D139 再加两项：开遮罩（编辑三项/查看原文改道）与「在主窗定位」（/goto + 唤出主窗）。
func (st *historyState) drainNodeAction(u *UI, gtx layout.Context) {
	act := u.nodeAction.Swap(nil)
	if act == nil || st.graph == nil {
		return
	}
	switch act.kind {
	case nodeActCopyID:
		if id := string(act.id); id != "" {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text",
				Data: io.NopCloser(strings.NewReader(id))})
		}
	case nodeActHome:
		st.graph.viewPending = vaFitAll
	case nodeActGoto:
		st.sendGoto(u, act.id)
		_ = u.post(showExpandMsg{}) // /goto 只改主窗转写区：主窗收在球里则等于没反应
	case nodeActOpenOverlay:
		st.openOverlay(u, act.id, act.mode, act.page)
	}
}

// sendGoto 图侧「在主窗定位」（D139）：投 /goto <id>（与键入同路径，缓冲满丢弃）。
func (st *historyState) sendGoto(u *UI, id conversation.MessageID) {
	if id == "" {
		return
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "goto", Args: []string{string(id)}}}:
	default:
	}
}

// historyFrame winHistory 单帧：左栏 + 右图 + 状态栏（§15.7 常规窗，主题 Bg 铺底）。
func historyFrame(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	st.sync(u)
	st.drainNodeAction(u, gtx)
	st.dispatch(u, gtx)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, paperOf().win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							w := gtx.Dp(histLeftColumnDp)
							gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
							return histLeftColumn(gtx, th, u, st)
						}),
						// 左栏与右图之间一条 hairline：两栏各自整高填充同色时，没有这条线
						// 会长成一块——读者分不出「列表到此为止」。
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							pp := paperOf()
							paint.FillShape(gtx.Ops, pp.hairline,
								clip.Rect(image.Rectangle{Max: image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)}).Op())
							return layout.Dimensions{Size: image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)}
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return histGraphArea(gtx, th, u, st)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return histStatusBar(gtx, th, u, st)
				}),
			)
		}),
		// D139：遮罩层置顶（scrim + 居中卡片）。未开启时零尺寸，不影响布局。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.overlayFrame(gtx, th, u)
		}),
	)
}

// histLeftColumnDp 左栏固定宽度（§5.4）。
const histLeftColumnDp unit.Dp = 200

// histLeftColumn 左栏：会话列表（标题 / 时间 / 消息数；当前会话高亮）。
func histLeftColumn(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	rowH := gtx.Dp(44)
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), "会话")
				l.Font.Weight = font.Bold
				l.Color = paperOf().text3
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return st.list.Layout(gtx, len(st.sessions), func(gtx layout.Context, i int) layout.Dimensions {
					c := st.rowClick(i)
					return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Stack{}.Layout(gtx,
							layout.Expanded(func(gtx layout.Context) layout.Dimensions {
								s := st.sessions[i]
								pp := paperOf()
								fill := pp.win
								if s.Current {
									fill = pp.sel
								}
								paint.FillShape(gtx.Ops, fill,
									clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Max},
										gtx.Dp(unit.Dp(pp.rChip))).Op(gtx.Ops))
								return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, rowH)}
							}),
							layout.Stacked(func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return histSessionRow(gtx, th, st.sessions[i])
								})
							}),
						)
					})
				})
			}),
		)
	})
}

// histSessionRow 单行内容：标题 + （时间 · N 条）；当前会话带「●」。
func histSessionRow(gtx layout.Context, th *material.Theme, s port.SessionSummary) layout.Dimensions {
	title := s.Title
	if title == "" {
		title = "新会话"
	}
	title = truncRunes(title, 18)
	mark := " "
	if s.Current {
		mark = "●"
	}
	meta := fmt.Sprintf("%s · %d 条", s.UpdatedAt.Format("01-02 15:04"), s.Messages)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperNoteSp(th), mark+" "+title)
			l.Color = paperOf().text
			l.Font.Weight = font.Bold
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperMicroSp(th), meta)
			l.Color = paperOf().text3
			return l.Layout(gtx)
		}),
	)
}

// truncRunes 截断到最多 n 个 rune（超长标题防溢出）。
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// histGraphArea 右栏：关系图帧（第 7 步；数据未就绪时居中占位）。
func histGraphArea(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	if st.graph != nil && st.model != nil {
		return st.graph.frame(gtx, th, u, st)
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperNoteSp(th), "关系图未就绪")
		l.Color = paperOf().text3
		return l.Layout(gtx)
	})
}

// histStatusBar 状态栏（A7 落点）：会话标题 · 共 N · 呈现 M + 预算/缩放/装下整树控件 + 图例。
func histStatusBar(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	N, M := 0, 0
	if st.model != nil {
		N = st.model.total
		M = len(st.vis.nodes)
	}
	zoomPct := "100%"
	if st.graph != nil {
		zoomPct = fmt.Sprintf("%d%%", int(st.graph.zoom*100+0.5))
	}
	text := fmt.Sprintf("会话：%s · 共 %d 条 · 呈现 %d", st.title(), N, M)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, paperOf().sunken,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Label(th, paperMicroSp(th), text)
								l.Color = paperOf().text2
								return l.Layout(gtx)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Spacer{}.Layout(gtx)
							}),
							layout.Rigid(histCtl(th, &st.btnMinus, "−")),
							layout.Rigid(histCtl(th, &st.btnPlus, "＋")),
							layout.Rigid(histCtl(th, &st.btnAll, "展开全部")),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Label(th, paperMicroSp(th), "缩放 "+zoomPct)
								l.Color = paperOf().text2
								return l.Layout(gtx)
							}),
							layout.Rigid(histCtl(th, &st.btnHome, "装下整树")),
							// D128 三种聚焦域：整树（重排）/ 当前历史线 / 当前节点及父子
							layout.Rigid(histCtl(th, &st.btnSeed, "回到初始态")),
							layout.Rigid(histCtl(th, &st.btnLine, "聚焦当前历史")),
							layout.Rigid(histCtl(th, &st.btnNode, "聚焦当前节点")),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return histLegend(gtx, th)
					}),
				)
			})
		}),
	)
}

// histCtl 状态栏小按钮（clickable + 标签）。悬停/按下靠 Clickable 自带的 ripple 投影
// 到 Paper 的次级面上——状态栏底色已是 sunken，故只给文字色，不再叠第二层底。
func histCtl(th *material.Theme, c *widget.Clickable, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(th, text)
				l.Color = paperOf().text2
				return l.Layout(gtx)
			})
		})
	}
}

// swatchRing 空心色块（结构根 = 描边圆，与 drawNode 的 relShapeRing 同形态）。
func swatchRing(c color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := gtx.Dp(10)
		box := image.Rectangle{Max: image.Pt(size, size)}
		cw := gtx.Dp(1)
		if cw < 1 {
			cw = 1
		}
		out := clip.UniformRRect(box, size/2).Path(gtx.Ops)
		paint.FillShape(gtx.Ops, c, clip.Stroke{Path: out, Width: float32(cw * 2)}.Op())
		return layout.Dimensions{Size: image.Pt(size, size)}
	}
}

// histLegend 状态栏第二行：深度色带图例 + 形状图例（A2 的可解释性）。
func histLegend(gtx layout.Context, th *material.Theme) layout.Dimensions {
	swatch := func(c color.NRGBA) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			size := gtx.Dp(10)
			paint.FillShape(gtx.Ops, c, clip.RRect{Rect: image.Rectangle{Max: image.Pt(size, size)},
				SE: 2, SW: 2, NE: 2, NW: 2}.Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(size, size)}
		}
	}
	label := func(s string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperMicroSp(th), s)
			l.Color = paperOf().text2
			return l.Layout(gtx)
		}
	}
	// 图例必须与画布同源：色块直接取 Paper 的深度色带，形状词与 relShapeOf 一一对应
	// （□ user / ● assistant / ◇ system / ◯ 结构根）。此前图例列 4 色而画布只现 3 档、
	// 结构根画成实心圆，图例与画面对不上——图例的用处就是「照着认」，对不上就是错的。
	d := paperOf().depth
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(label("色带")),
		layout.Rigid(swatch(d[0])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(d[1])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(d[2])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(d[3])),
		layout.Rigid(swatchRing(d[0])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(label("形状：□ 你 · ● 助手 · ◇ 系统 · ◯ 会话根")),
	)
}
