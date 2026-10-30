package uigui

// 行体与控件构件：行内容分派、字段行、选择框、编辑器与动作行。

import (
	"image"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// rowBodyFn 绑定「第 i 行内容」的 widget（Stacked 子项需要 widget 而不是立即调用）。
func (f *settingsForm) rowBodyFn(th *material.Theme, rows []rowDesc, i int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return f.rowBody(gtx, th, rows, i)
	}
}

// rowBody 行内容本体（版面归 row，内容按注册表分派，D162）：先问框架通用行型
// （页标题 / 分区标题），未命中再交给**当前页**的模块认领。未认领的行不再有 `default`
// 静默兜底——交由行型覆盖测试断言恒被认领。
func (f *settingsForm) rowBody(gtx layout.Context, th *material.Theme, rows []rowDesc, i int) layout.Dimensions {
	r := rows[i]
	if dims, ok := frameworkRow(gtx, th, r); ok {
		return dims
	}
	dims, ok := settingsPages[f.page].Body(gtx, th, f, rows, i)
	if !ok {
		// 认不出的行型曾静默落进 default 兜底（D162 取消）；这里退化成空 Dimensions，
		// 让行型覆盖测试与离屏像素回归去发现，而不是悄悄画成别的控件。
		return layout.Dimensions{}
	}
	return dims
}

// trimRunes 按字符数截断（超长模型名在 chip 里以省略号收尾）。
func trimRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

// fieldRow 一行：定宽标签列 + 控件（基线对齐，各行控件起点一致）。标签列宽随
// 度量缩放（Dp）——裸 px 常量在高 DPI 下实际变窄，「接口地址」等四字标签折行。
func fieldRow(gtx layout.Context, th *material.Theme, label string, control layout.Widget) layout.Dimensions {
	// **不能按基线对齐**：基线只对单行文字成立，多行控件（正文区）的基线与它的
	// 顶边差很远，标签会掉到输入框下方去（离屏渲染实测：标签浮在框底之下）。
	// 改为顶端对齐 + 给标签加与输入框首行等高的内衬，视觉上仍与首行文字齐平。
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			labelW := gtx.Dp(fieldLabelW)
			gtx.Constraints.Min.X = labelW
			gtx.Constraints.Max.X = labelW
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(th, label)
					l.Color = paperOf().text2
					return l.Layout(gtx)
				})
			})
		}),
		layout.Flexed(1, control),
	)
}

// selectBtn 枚举选择器按钮：显示当前项，点击弹出全部选项的居中模态（见
// settings_select.go）。带一个 ▾ 指示「点开有得选」——不带指示的纯文字按钮读起来像
// 另一个动作按钮，而不是一个可选值。
func selectBtn(th *material.Theme, c *selectField) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := c.display() + " ▾"
		return paperButton(th, &c.click, label, paperBtnSecondary, true)(gtx)
	}
}

// fillFieldBg 输入框底衬：Paper 的「下沉面 + 1dp hairline」。
//
// Gio 没有描边，靠「先铺满描边色、再内缩 1dp 铺底色」画出真正的空心框——直接给透明
// 底描边会把整块涂成描边色（这条坑在次窗与主窗同样成立）。
//
// 为什么不用窗底色当输入框底：设置页的窗体底就是白，输入框同白等于没有边界，用户
// 看不出哪里能打字。故下沉一档（sunken），边界用 hairline。
func fillFieldBg(gtx layout.Context, th *material.Theme, r image.Rectangle) {
	if r.Empty() {
		return
	}
	paperSurface(gtx, paperOf(), r, gtx.Dp(unit.Dp(paperOf().rCtl)))
}

// editorBox 文本档输入区（layout.Widget，供 fieldRow 装配）：满宽底衬，高度自适应
// ——max(Dp(fieldH) 下限, 编辑器实测行高 + 2×留白)。固定裸 px 定高会在任何 DPI 裁掉
// 文字底部（CJK 字面行高 > 内容区，D113 验收补充；实测行高 38px @1.0 / 48px @1.25 vs
// 旧内容区 24px/20px），自适应后框随文字度量走。
func editorBox(th *material.Theme, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{Alignment: layout.NW}.Layout(gtx,
			// 底衬（Expanded 拿 Stacked 最大尺寸；垫底项保证下限，编辑器项决定实际高）。
			// 浅底 + 内缩一圈的窗底 = 1dp 描边环：设置页底与窗底同色，只铺 th.Bg
			// 等于没铺，用户看不出哪里可以打字。
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				fillFieldBg(gtx, th, image.Rectangle{Max: gtx.Constraints.Min})
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Point{Y: gtx.Dp(fieldH)}}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X // 铺满行宽：点击聚焦区 = 整框
				return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Editor(th, ed, hint).Layout(gtx)
				})
			}),
		)
	}
}

// editorBoxH 定高多行编辑区（编辑框的加高变体）：提示词用。高度随度量缩放（Dp）——
// 多行内容超高走编辑器内部滚动，但首行行高必须容得下。
func editorBoxH(th *material.Theme, ed *widget.Editor, hint string, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return editorBlock(gtx, th, ed, hint, image.Point{X: gtx.Constraints.Max.X, Y: gtx.Dp(unit.Dp(h))})
	}
}

// editorBoxFill 撑满可用高度的多行编辑区（高级页整文件编辑）：高度取约束上限，由外层
// 撑满行决定——上层把「页内容区减去它上面的行」作为确切高度给下来。
//
// 为什么不能像 editorBoxH 那样写死一个数值：这个框没有内容决定的「自然高度」，只有
// 窗口愿意给多高。写死 220dp 在窗口拉高后底下就是一大片空白，窗口压低时同一块又显得
// 顶到边。约束上限在这里恰好就是「剩下的空间」，照抄即可。
func editorBoxFill(th *material.Theme, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return editorBlock(gtx, th, ed, hint, gtx.Constraints.Max)
	}
}

// editorBlock 两种高度口径共用的编辑区本体：下沉底衬 + 内缩一圈窗底（1dp 描边环）+
// 内边距 8dp。
func editorBlock(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string, size image.Point) layout.Dimensions {
	fillFieldBg(gtx, th, image.Rectangle{Max: size})
	gtx.Constraints = layout.Exact(size)
	layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Editor(th, ed, hint).Layout(gtx)
	})
	return layout.Dimensions{Size: size}
}

// sizeRow 窗口尺寸双数值档（D90）：宽 × 高（px），并排等分；空 = 保持缺省。
func sizeRow(gtx layout.Context, th *material.Theme, w, h *widget.Editor) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, editorBox(th, w, "宽 px")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, "×").Layout(gtx)
				})
			}),
			layout.Flexed(1, editorBox(th, h, "高 px")),
		)
	}
}

// actionsRow 保存键 + 反馈文案（成功绿/错误红 = 转写区语义色；空 = 默认提示）。
func (f *settingsForm) actionsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.save, "保存并应用", paperBtnPrimary, true)(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.status
				col := paperOf().text2
				switch {
				case text == "":
					text, col = "保存后立即生效；provider 列表在「模型服务」页应用（重启生效）。", paperOf().text3
				case f.err:
					col = paperOf().danger
				}
				l := material.Label(th, paperNoteSp(th), text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}
