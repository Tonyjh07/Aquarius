package uigui

// 模块编辑弹窗（遮罩、标题、字段、按钮）与提示词页的引用行、内置模块行。

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// ── 模块编辑弹窗（D148）─────────────────────────────────────────────────────
//
// 右键磁贴直接弹出，不再多一层菜单：菜单只是多一次点击，而这些字段本来就该在同一个
// 表单里改（改名与改正文是同一件事的两面）。删除放在弹窗左下、与「保存」隔开。

// moduleEditorFrame 右键编辑窗（模态）：遮罩铺满宿主盒 + 居中卡片。
//
// **模态怎么成立**（与历史窗遮罩同一机制）：Gio 把一次点击投给所有命中的 handler，
// 所以「画一层挡板」挡不住背景。真正起作用的是整盒输入区排在命中链顶——背景控件
// 收不到指针事件；背景侧的显式不消费是纵深防御。宿主须把它画在最后一层（盖住
// 自己的页脚钮）。
func (f *promptEditor) moduleEditorFrame(gtx layout.Context, th *material.Theme) (layout.Dimensions, bool) {
	st := &f.modEdit
	if !st.open {
		return layout.Dimensions{}, false
	}
	area := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	gtx.Event(key.FocusFilter{Target: &st.tag})
	event.Op(gtx.Ops, &st.tag)
	area.Pop()

	// Esc 关闭：弹窗是编辑区里唯一能「取消」的地方，没有它改错了只能关窗重开。
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &st.tag, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			f.closeModuleEditor()
			return layout.Dimensions{}, false
		}
	}
	// 点卡片外关闭（抬起点不在卡片内）。card 在第一帧还是零值，此时不能判——
	// 否则弹窗刚开就被自己这一帧的抬起关掉。
	if !st.card.Empty() {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &st.tag, Kinds: pointer.Release})
			if !ok {
				break
			}
			e, isPtr := ev.(pointer.Event)
			if !isPtr {
				continue
			}
			p := image.Pt(int(e.Position.X), int(e.Position.Y))
			if !p.In(st.card) {
				f.closeModuleEditor()
				return layout.Dimensions{}, false
			}
		}
	}

	// 动作先消费：本帧点了保存/删除，就不该再按旧内容画一帧。
	if st.btnClose.Clicked(gtx) || st.btnCancel.Clicked(gtx) {
		f.closeModuleEditor()
		return layout.Dimensions{}, false
	}
	if st.btnSave.Clicked(gtx) {
		f.commitModuleEditor()
		return layout.Dimensions{}, false
	}
	if st.btnDel.Clicked(gtx) && st.idx >= 0 {
		f.delPromptModule(st.idx)
		f.closeModuleEditor()
		return layout.Dimensions{}, false
	}

	w, h := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	dw, dh := gtx.Dp(modDlgWdp), gtx.Dp(modDlgHdp)
	if lim := w - 2*gtx.Dp(24); dw > lim {
		dw = lim
	}
	if lim := h - 2*gtx.Dp(24); dh > lim {
		dh = lim
	}
	card := image.Rect((w-dw)/2, (h-dh)/2, (w+dw)/2, (h+dh)/2)
	st.card = card

	// 遮罩铺满宿主盒（含页脚）：只盖内容区会让「保存」在弹窗背后仍是亮的，
	// 读起来像是可以直接点。
	pp := paperOf()
	paint.FillShape(gtx.Ops, scrimColor(),
		clip.Rect{Max: image.Pt(w, h)}.Op())
	// 弹窗是浮层：阴影必须比卡片更远（spread 10 vs 3），否则「盖在上面」不成立。
	rad := gtx.Dp(unit.Dp(pp.rCard))
	paperShadow(gtx, card, rad, 14, 8, 0x38)
	paperPanel(gtx, card, rad, gtx.Dp(1), pp.card, pp.hairline)

	stack := op.Offset(card.Min).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Constraints = layout.Exact(card.Size())
	layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.moduleEditorTitle(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "名称", editorBox(th, &st.name, "唯一名；不以 @ 开头（保留给内置）"))
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "正文 / 组头", editorBoxH(th, &st.text, "叶：正文；组：组头（可空）", 84))
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "子模块", editorBox(th, &st.children, "逗号分隔；填了即成为组，清空即转为叶"))
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), "改结构不动已有会话：人格在新建会话时才重新组合。")
				l.Color = pp.text3
				return l.Layout(gtx)
			}),
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.moduleEditorButtons(gtx, th)
			}),
		)
	})
	return layout.Dimensions{Size: image.Pt(w, h)}, true
}

// moduleEditorTitle 标题行：标题带模块名（一眼看出改的是哪个）+ 右上关闭。
func (f *promptEditor) moduleEditorTitle(gtx layout.Context, th *material.Theme) layout.Dimensions {
	st := &f.modEdit
	title := "编辑模块 · " + st.name.Text()
	if st.idx < 0 {
		title = "新增模块"
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperTitleSp(th), title)
				l.Font.Weight = font.Bold
				l.Color = paperOf().text
				return l.Layout(gtx)
			}(gtx)
		}),
		layout.Rigid(overlayBtn(&st.btnClose, th, "✕", false, true)),
	)
}

// moduleEditorButtons 底行：删除（左）+ 取消 / 保存（右）。
func (f *promptEditor) moduleEditorButtons(gtx layout.Context, th *material.Theme) layout.Dimensions {
	st := &f.modEdit
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if st.idx < 0 { // 新增时没有可删的东西
				return layout.Dimensions{}
			}
			return paperButton(th, &st.btnDel, "删除模块", paperBtnDanger, true)(gtx)
		}),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(paperButton(th, &st.btnCancel, "取消", paperBtnSecondary, true)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(paperButton(th, &st.btnSave, "保存", paperBtnPrimary, true)),
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// scrimColor 遮罩色（半透黑；深浅主题同值——遮罩的作用是压暗，不是配色）。
func scrimColor() color.NRGBA {
	return color.NRGBA{R: 0x10, G: 0x14, B: 0x18, A: modScrimAlpha}
}

// ── 行内容：引用行与内置模块行（D144）──────────────────────────────────────

// promptRefRow 人格引用行（S7/D144）：序号 + 引用名 + 上移/下移/删除（顺序 = 拼接顺序）。
func (f *promptEditor) promptRefRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	rf := f.promptRefs[i]
	small := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, c, label, paperBtnSecondary, true))
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, strconv.Itoa(i+1)+".")
			l.Color = paperOf().text3
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, l.Layout)
		}),
		layout.Flexed(1, editorBox(th, &rf.name, "引用名（模块名或内置 @名）")),
		layout.Rigid(small(&rf.up, "↑")),
		layout.Rigid(small(&rf.down, "↓")),
		layout.Rigid(small(&rf.del, "✕")),
	)
}

// promptBuiltinRow 内置只读模块行（S7/D144）：引用名 + 说明 + 「引用」按钮（加入人格引用）。
func (f *promptEditor) promptBuiltinRow(gtx layout.Context, th *material.Theme, b BuiltinModuleView) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Body2(th, b.Name).Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, b.Label)
					l.Color = paperOf().text2
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if f.promptBtns == nil { // 零值构造（测试/直建）也要能用
				f.promptBtns = map[string]*widget.Clickable{}
			}
			btn := f.promptBtns["ref:"+b.Name]
			if btn == nil {
				btn = new(widget.Clickable)
				f.promptBtns["ref:"+b.Name] = btn
			}
			return paperButton(th, btn, "引用", paperBtnSecondary, true)(gtx)
		}),
	)
}
