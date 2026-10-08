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
	sig   string
	model *relModel
	vis   relVisible
}

// newHistoryState 构造 winHistory 帧状态并做首帧数据同步。
func newHistoryState(u *UI) *historyState {
	st := &historyState{list: widget.List{List: layout.List{Axis: layout.Vertical}}}
	st.sync(u)
	return st
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
					st.vis = relBudget(m, relDefaultBudget)
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

// dispatch 帧内点击消费：任一行的 Clicked() 即切会话（一次一个，先到先得）。
func (st *historyState) dispatch(u *UI, gtx layout.Context) {
	for i, c := range st.rows {
		if c.Clicked(gtx) {
			if i < len(st.sessions) {
				st.sendSwitch(u, st.sessions[i].ID)
			}
			return
		}
	}
}

// historyFrame winHistory 单帧：左栏 + 右图 + 状态栏（§15.7 常规窗，主题 Bg 铺底）。
func historyFrame(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	st.sync(u)
	st.dispatch(u, gtx)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, th.Bg,
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
				l := material.Label(th, unit.Sp(12), "会话")
				l.Color = textMuted
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
								fill := pillBg
								if s.Current {
									fill = cardSystem
								}
								paint.FillShape(gtx.Ops, fill,
									clip.RRect{Rect: image.Rectangle{Max: gtx.Constraints.Max},
										SE: gtx.Dp(8), SW: gtx.Dp(8), NE: gtx.Dp(8), NW: gtx.Dp(8)}.Op(gtx.Ops))
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
			l := material.Body2(th, mark+" "+title)
			l.Color = th.Fg
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, meta)
			l.Color = textMuted
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

// histGraphArea 右栏：第 7 步落地右图；当前为占位（模型/预算已就绪，显示数量）。
func histGraphArea(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		msg := "关系图未就绪"
		if st.model != nil {
			msg = fmt.Sprintf("整树 %d 节点（呈现 %d）——关系图第 7 步落地", st.model.total, len(st.vis.nodes))
		}
		l := material.Body2(th, msg)
		l.Color = textMuted
		return l.Layout(gtx)
	})
}

// histStatusBar 状态栏（A7 落点）：`会话：<标题> · 共 N 条 · 呈现 M` + 深度色带图例。
func histStatusBar(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	title := "（未就绪）"
	for _, s := range st.sessions {
		if s.Current {
			title = s.Title
			if title == "" {
				title = "新会话"
			}
		}
	}
	N, M := 0, 0
	if st.model != nil {
		N = st.model.total
		M = len(st.vis.nodes)
	}
	text := fmt.Sprintf("会话：%s · 共 %d 条 · 呈现 %d", title, N, M)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, cardTool,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, text)
						l.Color = textMuted
						return l.Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return histLegend(gtx, th)
					}),
				)
			})
		}),
	)
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
			l := material.Caption(th, s)
			l.Color = textMuted
			return l.Layout(gtx)
		}
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(label("色带：")),
		layout.Rigid(swatch(depthMap[0])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(depthMap[1])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(depthMap[2])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(swatch(depthMap[3])),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(label("形状：□你 ●AI ◇系统 ◯会话根")),
	)
}
