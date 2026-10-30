package uigui

// 历史窗的帧骨架、右栏关系图区与底部状态栏（控件、图例）。

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
)

// historyFrame winHistory 单帧：左栏 + 右图 + 状态栏（§15.7 常规窗，主题 Bg 铺底）。
func historyFrame(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	st.sync(u)
	st.avatars.drain() // 角色卡头像后台解码结果入缓存（§5.17）
	st.drainNodeAction(u, gtx)
	st.drainFilePicks(u)
	// Esc 退出选择态：选择态不是模态层（不铺 scrim），所以不夺焦点，只挂一条不限焦点的
	// 过滤器；模态层在屏时 dispatch 见门即返回，Esc 归那一层自己。
	if !st.modalActive(u) {
		st.drainSelKeys(gtx)
	}
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
					// 内容区（左栏 + 右图）与轻提示同层：轻提示贴**内容区**底缘，
					// 不让开状态栏会在顶层 Stack 里压住状态栏（顶层只知整窗高，取像实测）。
					return layout.Stack{}.Layout(gtx,
						layout.Expanded(func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									// D159：左栏可收起——收起态只剩一条窄轨（展开钮），宽度让给关系图。
									// 两态都读**本帧**的 st.leftCollapsed：钮在同帧 dispatch 里翻转，
									// 故点一下当帧就换宽度，不等到下一帧。
									w, body := histLeftColumnDp, histLeftColumn
									if st.leftCollapsed {
										w, body = histLeftRailDp, histLeftRail
									}
									px := gtx.Dp(w)
									gtx.Constraints = layout.Constraints{Min: image.Pt(px, 0), Max: image.Pt(px, gtx.Constraints.Max.Y)}
									return body(gtx, th, u, st)
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
						// 轻提示（复制 ID / 复制会话 ID 反馈）：贴内容区底缘、短促、不拦截输入。
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return toastFrame(gtx, th, &st.toast, paperOf().tipBg)
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
		// D157：同步确认弹窗再置顶（模态；主窗确认态与它同源，点任一处都作答）。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.confirmFrame(gtx, th, u)
		}),
		// D152：「插入摘要」小卡置顶（模态；由节点右键菜单开，两钮各投一条 /summary）。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.summaryFrame(gtx, th, u)
		}),
		// D249：导入多会话归档的勾选面板置顶（模态；选完文件后由 drainFilePicks 开）。
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return st.importPickFrame(gtx, th, u)
		}),
	)
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

// histCtlItem 状态栏一个控件：click 非空 = 按钮，空 = 只读文字（缩放百分比）。
// 项集由 histStatusCtlItems 一处给出——测试锁定「有哪些控件 / 什么文案」，绘制侧只按序排
// （加减控件必须改这一处，不会出现「改了绘制、忘了改契约测试」）。
type histCtlItem struct {
	label string
	click *widget.Clickable
}

// histStatusCtlItems 状态栏控件项集（自左向右）。2026-10-19 删掉「展开全部」：会话在默认
// 预算内就已是全量，点了没有任何变化（用户批注「没什么效果，删了」）；预算仍由 −/＋ 调
// （上限 relHardCap）。
func (st *historyState) histStatusCtlItems() []histCtlItem {
	zoomPct := "100%"
	if st.graph != nil {
		zoomPct = fmt.Sprintf("%d%%", int(st.graph.zoom*100+0.5))
	}
	return []histCtlItem{
		{label: "−", click: &st.btnMinus},
		{label: "＋", click: &st.btnPlus},
		{label: "缩放 " + zoomPct}, // 只读文字
		{label: "装下整树", click: &st.btnHome},
		{label: "回到初始态", click: &st.btnSeed},
		{label: "聚焦当前历史", click: &st.btnLine},
		{label: "聚焦当前节点", click: &st.btnNode},
	}
}

// histStatusBar 状态栏（A7 落点）：会话标题 · 共 N · 呈现 M + 控件项集 + 图例。
func histStatusBar(gtx layout.Context, th *material.Theme, u *UI, st *historyState) layout.Dimensions {
	N, M := 0, 0
	if st.model != nil {
		N = st.model.total
		M = len(st.vis.nodes)
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
						kids := []layout.FlexChild{
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Label(th, paperMicroSp(th), text)
								l.Color = paperOf().text2
								return l.Layout(gtx)
							}),
							layout.Flexed(1, layout.Spacer{}.Layout),
						}
						for _, it := range st.histStatusCtlItems() {
							it := it
							kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return histCtl(gtx, th, it)
							}))
						}
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return histLegend(gtx, th)
					}),
				)
			})
		}),
	)
}

// histCtl 状态栏一个小控件（click 空 = 只读文字）。悬停/按下靠 Clickable 自带的 ripple
// 投影到 Paper 的次级面上——状态栏底色已是 sunken，故只给文字色，不再叠第二层底。
// 每个控件左侧留 8dp 间距（否则「＋ 缩放 98%」会连成一句，见 D158）。
func histCtl(gtx layout.Context, th *material.Theme, it histCtlItem) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		mk := func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, it.label)
			l.Color = paperOf().text2
			return l.Layout(gtx)
		}
		if it.click == nil {
			return mk(gtx)
		}
		return it.click.Layout(gtx, mk)
	})
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
	// 2026-10-19 撤掉色带后面那个空心圆：它没有标签，读起来像第 5 档色带（用户批注
	// 「这里不知道哪来的圆」），而「◯ 会话根」在下面的形状组里已经写明。
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
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(label("形状：□ 你 · ● 助手 · ◇ 系统 · ◯ 会话根 · △ 注入")),
	)
}
