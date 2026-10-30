package uigui

// 设置窗的帧骨架：左侧导航栏与导航项、全局底栏。

import (
	"image"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// settingsFrame 设置窗单帧（S4d/D142 分页；D162 页面注册表）：左侧导航栏 + 当前页滚动
// 表单 + 全局底栏。事件先于渲染取尽（Clicked 自带 Update；导航点击本帧即按新页渲染）。
//
// 事件分两层：**帧级**（保存键、开窗焦点、异步结果排空、工具页快照同步——与页无关，恒执行）
// 与**页级**（settingsPages[i].Events，逐页各调一次：未渲染的控件 Clicked 恒假，故等价于
// 只处理当前页，却省掉了「按页分支」）。常规窗不做形状位图/羽化（§15.7 形态）：windowBg 铺底，
// 与主窗内容面同底。
func settingsFrame(gtx layout.Context, th *material.Theme, u *UI, f *settingsForm) layout.Dimensions {
	for p := 0; p < settingsPageCount; p++ {
		for f.navBtns[p].Clicked(gtx) {
			if f.page != p {
				// 选择器弹层属于某一页的控件：换页即收起（不跟着跨页留着，
				// 否则新页上会悬着一个指向旧页控件的弹层）。
				f.closePicker()
			}
			f.page = p
		}
	}
	// 切页钩子（D181）：进入某页时的一次性动作——「模型服务」页借它在首次进入时自动
	// 拉一轮各家服务端清单。页面自己用表单标记去重（本窗只拉一次）。
	if h, ok := settingsPages[f.page].(enterHook); ok {
		h.OnEnter(u, f)
	}
	f.drainTestResults()  // 连通性 / 获取模型异步结果（最新一条覆盖展示）
	f.drainToolResults()  // 工具页异步动作结果回灌状态行
	f.drainSkillResults() // 技能页异步动作结果回灌状态行
	f.drainSkillPicks(u)  // 技能导入选择框结果（只填来源框，不直接落盘）
	f.drainCharPicks(u)   // 角色卡选择框结果（只填来源框，不直接落盘）
	// 工具页原子快照（Revision 变了才重建行/按钮；同历史窗口径）。
	f.syncToolsSnapshot()
	// 密钥库页原子快照（同口径：Revision 变了才重建条目行）。
	f.syncSecretsSnapshot()
	// 技能页原子快照（同口径）。
	f.syncSkillsSnapshot()
	// 角色页快照（本页现取，装配根侧节流读盘）+ 头像异步结果排空。
	f.syncCharactersSnapshot(u)
	for f.save.Clicked(gtx) {
		u.saveSettings(f)
	}
	// 当前页事件（D162：页模块自带事件，谁持有控件谁处理）。**所有页都调一次**：改前
	// provider / 工具页 / profile 等动作本就是帧级无条件消费（未渲染的控件 Clicked 恒假），
	// 这样口径与改前一致；页内需要按当前页收窄的（如模块网格手势）由该页自行判 f.page。
	for _, p := range settingsPages {
		p.Events(gtx, u, f)
	}
	if !f.focused {
		f.focused = true
		if ed := settingsPages[f.page].FirstEditor(f); ed != nil { // 开窗焦点入当前页首个编辑档
			gtx.Execute(key.FocusCmd{Tag: ed})
		}
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			pp := paperOf()
			paint.FillShape(gtx.Ops, pp.win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							w := gtx.Dp(navColumnDp)
							gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
							return f.navColumn(gtx, th)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							rows := f.pageRows(f.page)
							if fp, ok := settingsPages[f.page].(fillPage); ok {
								// 撑满页：最后一个块吸走窗口多出来的高度（高级页整文件编辑）。
								// 只在目标行确实排在末尾时走这条路——它后面还有行的话，Flex
								// 会先把剩余高度分给目标行、再把后续行挤成 0 高，不如照旧滚动。
								if i := fp.FillRow(rows); i >= 0 && i == len(rows)-1 {
									return f.fillRows(gtx, th, rows, i)
								}
							}
							return material.List(th, &f.pages[f.page]).Layout(gtx, len(rows), f.row(th, rows))
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.bottomBar(gtx, th)
				}),
			)
		}),
		// 编辑弹窗画在最后一层（模态）：它必须盖住内容区与底栏，否则遮罩盖不到的
		// 「保存并应用」在弹窗背后仍是亮的，读起来像是可以直接点。D148。
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			dims, _ := f.moduleEditorFrame(gtx, th)
			return dims
		}),
		// 枚举选择器弹层与编辑弹窗同层（也是模态，也是整窗遮罩）。两者互斥：编辑弹窗
		// 开着时选择器按钮在背景里，点不到，故不会同时出现。
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			dims, _ := f.pickerFrame(gtx, th)
			return dims
		}),
	)
}

// navColumn 左侧导航栏（S4d/D142）：页名竖排，当前页高亮；点击切页在帧首消费。
func (f *settingsForm) navColumn(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, settingsPageCount)
		for p := 0; p < settingsPageCount; p++ {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.navItem(gtx, th, p)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// navItem 单个导航项：整宽高亮底衬（当前页）+ 页名；点击件即整项。
//
// Paper 版的选中态 = 软底 + 左侧 2dp 品牌色短条。短条不与文字重叠，故可以用品牌色
// #00AEEF 本身（承载白字的那档才需要深一档）；选中项文字同时升到 text 最亮一档。
func (f *settingsForm) navItem(gtx layout.Context, th *material.Theme, p int) layout.Dimensions {
	return f.navBtns[p].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(navItemDpH))
		return layout.Stack{Alignment: layout.W}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				if f.page == p {
					pp := paperOf()
					r := image.Rectangle{Max: size}
					paint.FillShape(gtx.Ops, pp.sel,
						clip.UniformRRect(r, gtx.Dp(unit.Dp(pp.rChip))).Op(gtx.Ops))
					// 左条：贴着圆角内侧，上下各让 6dp——贴满整高会与圆角打架。
					bar := image.Rect(r.Min.X+gtx.Dp(2), r.Min.Y+gtx.Dp(6),
						r.Min.X+gtx.Dp(4), r.Max.Y-gtx.Dp(6))
					paint.FillShape(gtx.Ops, pp.brand,
						clip.UniformRRect(bar, gtx.Dp(1)).Op(gtx.Ops))
				}
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pp := paperOf()
					l := material.Body1(th, settingsPageNames[p])
					if f.page == p {
						l.Color = pp.text
					} else {
						l.Color = pp.text2
					}
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// bottomBar 全局底栏（S4d/D142）：「保存并应用」+ 反馈文案任何页常驻——保存仍是一次
// 全量 patch（跨页 diff 基准 = 开窗快照 f.snap），语义与改前一致。
func (f *settingsForm) bottomBar(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 底衬（Expanded 拿到 Stacked 子项的最大尺寸）+ 1dp hairline 分隔内容区。
			// 底栏与窗体同色、只靠这条线分层：Paper 里不出现第二块大底色。
			pp := paperOf()
			size := gtx.Constraints.Min
			paint.FillShape(gtx.Ops, pp.win, clip.Rect(image.Rectangle{Max: size}).Op())
			paint.FillShape(gtx.Ops, pp.hairline,
				clip.Rect(image.Rectangle{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}).Op())
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.actionsRow(gtx, th)
			})
		}),
	)
}

// row 第 i 行渲染（material.List 回调；行内容按 pageRows 的类型分派）。
//
// 版面口径（D148 Paper 版）：
//   - **表单行**（字段/开关/按钮组）落进「分组卡」：连续的字段行合成一张卡，卡内
//     行间一条 hairline，卡外沿 1dp 描边、组首/组尾给圆角。
//   - **非表单行**（页标题/分区标题/说明/网格/预览/整组按钮行）自带上下留白，不落卡。
//
// 为什么卡底按行画而不是整块画：设置页是 material.List（虚拟化），滚出视口的行为
// 不参与布局，拿不到「这一组的完整矩形」。按行画、组界只补外沿，视觉上仍是连续一张
// 卡——这也是 iOS 分组表的画法（被视口切断处是直边，本来就不该有圆角）。
func (f *settingsForm) row(th *material.Theme, rows []rowDesc) func(layout.Context, int) layout.Dimensions {
	return func(gtx layout.Context, i int) layout.Dimensions {
		return rowLayout(gtx, rows, i, f.rowBodyFn(th, rows, i))
	}
}
