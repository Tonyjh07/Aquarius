package uigui

// settings_select.go 设置窗的**枚举选择器**（S4e）。
//
// 为什么不是「点一下进一档」：那让用户每换一档都得先背下档位顺序——想从「对话接口」换到
// 「FunASR 原生」要连点三下，中间那几档还会在视觉上掠过一次，看着像是已经选过了。选择器
// 把**全部选项连同当前选中项**一次摊开，点谁是谁。
//
// 为什么不是锚定下拉（点按钮、浮层挂在按钮正下方）：Gio 的布局不暴露控件的绝对坐标——
// ops 记录后不可读，能拿到的只有「上一帧」画过的矩形。用上一帧矩形当锚点，滚动或缩放后
// 会错位一帧；而表单本身是虚拟化的 material.List，锚在行内的浮层还会被列表视口裁掉。
// 居中模态卡片没有这两个问题：它铺满整个设置窗（盖住内容区与底栏），不依赖任何行坐标，
// 也不会被裁剪。与人格模块编辑窗、历史窗确认卡同一套机制。
//
// 模态怎么成立（与 overlay.go 头注同一机制）：Gio 把一次点击投给**所有**命中的 handler，
// 「画一层挡板」挡不住背景。真正起作用的是整窗输入区排在命中链顶——背景控件收不到指针
// 事件；背景侧的显式不取作纵深防御。

import (
	"image"
	"image/color"
	"slices"

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

// selectField 枚举选择器：按钮上显示当前项，点击弹出**全部选项**的居中模态，点一项即选中。
//
// displays 与 values 按下标一一对应（展示名是人话，值是 config 里的键值）；构造处保证等长。
// 空值项（「未配置」「不借」）也照常是列表里的一项——留空显示成看不见的空格，用户会以为
// 控件坏了，所以这类选项的展示名带括号说明。
type selectField struct {
	click    widget.Clickable
	displays []string
	values   []string
	idx      int
}

// setValue 按键值定位；未命中保持当前（快照异常值不致越界）。
func (c *selectField) setValue(v string) {
	for i, x := range c.values {
		if x == v {
			c.idx = i
			return
		}
	}
}

// value 当前键值（空表返回 ""）。
func (c *selectField) value() string {
	if len(c.values) == 0 {
		return ""
	}
	c.clamp()
	return c.values[c.idx]
}

// display 当前项的展示名（空表返回 ""）。
func (c *selectField) display() string {
	if len(c.displays) == 0 {
		return ""
	}
	c.clamp()
	if c.idx >= len(c.displays) {
		return c.displays[len(c.displays)-1]
	}
	return c.displays[c.idx]
}

// clamp 把下标夹回合法区间（选项表在开窗/增删后可能变短）。
func (c *selectField) clamp() {
	if c.idx < 0 {
		c.idx = 0
	}
	if c.idx >= len(c.values) {
		c.idx = len(c.values) - 1
	}
}

// choose 选中第 i 项（越界 no-op）。弹层里点一项、以及测试里模拟选择都走这里。
func (c *selectField) choose(i int) {
	if i < 0 || i >= len(c.values) {
		return
	}
	c.idx = i
}

// pickerState 选择器弹层的帧状态（全窗唯一的模态；零值可用）。
//
// 弹层是**纯界面状态**：它不写 config，也不参与保存 diff——收起即回到印证过的当前值。
type pickerState struct {
	open  bool
	title string
	field *selectField
	// 弹层常驻控件（打开时按选项数按需增长）。
	buttons []widget.Clickable
	cancel  widget.Clickable
	list    widget.List
	tag     struct{}        // 整窗输入区路由锚点（Gio 要求地址稳定）
	card    image.Rectangle // 上一帧的卡片盒（点卡片外判定用）
	// scrolled 本次打开已把选中项滚进视野（只做一次——每帧都调 ScrollTo 会把列表
	// 钉死在选中项上，用户就没法往下翻其它选项）。
	scrolled bool
	// focus 下帧把键盘焦点夺到弹层（Gio 只把键事件投给获焦的 handler：不夺，
	// 背景编辑框仍在获焦态，Esc 会落在它身上，而且打字会继续改背后的字段）。
	focus bool
}

// openPicker 消费某个选择器按钮的点击：命中即把**它**设为当前弹层。
// 每个选择器都在所属页的 Events 里调一次（未渲染的控件 Clicked 恒假，代价可忽略）。
func (f *settingsForm) openPicker(gtx layout.Context, label string, c *selectField) {
	if len(c.values) == 0 {
		return
	}
	for c.click.Clicked(gtx) {
		f.pick.open = true
		f.pick.title = label
		f.pick.field = c
		f.pick.card = image.Rectangle{}
		f.pick.list.Position = layout.Position{}
		f.pick.scrolled = false
		f.pick.focus = true
	}
}

// closePicker 收起弹层（不改选项）。
func (f *settingsForm) closePicker() {
	f.pick.open = false
	f.pick.field = nil
}

// pickerFrame 选择器弹层单帧：未打开 = 零尺寸空操作。**须画在设置窗最后一层**（盖住
// 内容区与底栏）。返回 false 表示本帧不画。
func (f *settingsForm) pickerFrame(gtx layout.Context, th *material.Theme) (layout.Dimensions, bool) {
	p := &f.pick
	if !p.open || p.field == nil {
		return layout.Dimensions{}, false
	}
	c := p.field
	// 整窗命中区 + 路由锚点：模态的主机制（排在链顶的整窗输入区按 Gio 链式命中连带
	// 遮蔽背景：导航栏、底栏「保存并应用」、其它选择器按钮都收不到指针事件）。
	area := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	gtx.Event(key.FocusFilter{Target: &p.tag})
	event.Op(gtx.Ops, &p.tag)
	area.Pop()
	// 键盘焦点夺到弹层：Gio 只把键事件投给当前获焦的 handler，不夺的话背景编辑框
	// 仍是获焦态——Esc 会落到它身上，打字还会继续改弹层背后那个字段。
	if p.focus {
		p.focus = false
		gtx.Execute(key.FocusCmd{Tag: &p.tag})
	}

	// Esc 关闭：弹层开着时键盘落点在遮罩上，没有 Esc 就只能靠点空白退出。
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &p.tag, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			f.closePicker()
			return layout.Dimensions{}, false
		}
	}
	// 先消费动作再画：本帧点了某一项，就不该再按旧值画一帧。
	n := len(c.values)
	if n > len(p.buttons) {
		p.buttons = append(p.buttons, make([]widget.Clickable, n-len(p.buttons))...)
	}
	for i := 0; i < n; i++ {
		if p.buttons[i].Clicked(gtx) {
			c.choose(i)
			f.closePicker()
			return layout.Dimensions{}, false
		}
	}
	if p.cancel.Clicked(gtx) {
		f.closePicker()
		return layout.Dimensions{}, false
	}
	// 点卡片外关闭（抬起点不在卡片内）。card 在第一帧还是零值，此时不能判——
	// 否则弹层刚开就被自己这一帧的抬起关掉。
	if !p.card.Empty() {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &p.tag, Kinds: pointer.Release})
			if !ok {
				break
			}
			e, isPtr := ev.(pointer.Event)
			if !isPtr {
				continue
			}
			pt := image.Pt(int(e.Position.X), int(e.Position.Y))
			if !pt.In(p.card) {
				f.closePicker()
				return layout.Dimensions{}, false
			}
		}
	}

	card := pickerCardBox(gtx, n)
	p.card = card

	// 遮罩铺满整窗（含底栏）：只盖内容区会让「保存并应用」在弹层背后仍是亮的，
	// 读起来像是可以直接点。
	pp := paperOf()
	paint.FillShape(gtx.Ops, color.NRGBA{R: pp.scrim.R, G: pp.scrim.G, B: pp.scrim.B, A: overlayScrimAlpha},
		clip.Rect{Max: gtx.Constraints.Max}.Op())
	rad := gtx.Dp(unit.Dp(pp.rCard))
	paperShadow(gtx, card, rad, 14, 8, 0x38)
	paperPanel(gtx, card, rad, gtx.Dp(1), pp.card, pp.hairline)

	stack := op.Offset(card.Min).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Constraints = layout.Exact(card.Size())
	f.pickerCard(gtx, th)
	return layout.Dimensions{Size: card.Size()}, true
}

// pickerCardBox 弹层卡片盒（居中、限宽、按选项数定高并在视口内夹取）。
func pickerCardBox(gtx layout.Context, options int) image.Rectangle {
	margin := gtx.Dp(overlayCardMargindp)
	avail := gtx.Constraints.Max
	w := gtx.Dp(420)
	if lim := avail.X - 2*margin; w > lim {
		w = lim
	}
	if w < gtx.Dp(240) {
		w = avail.X // 视口本身太窄：铺满不留边
	}
	h := gtx.Dp(96) + options*gtx.Dp(38) // 标题两行 + 选项 + 底行 + 内衬
	if maxH := int(float64(avail.Y) * 0.7); h > maxH {
		h = maxH
	}
	if lim := avail.Y - 2*margin; h > lim {
		h = lim
	}
	if h < 1 {
		h = avail.Y
	}
	card := image.Rect(0, 0, w, h)
	return card.Add(image.Pt((avail.X-card.Dx())/2, (avail.Y-card.Dy())/2))
}

// pickerCard 卡片内容：标题 + 一行操作说明 + 选项列表（可滚）+ 底行「取消」。
func (f *settingsForm) pickerCard(gtx layout.Context, th *material.Theme) layout.Dimensions {
	p := &f.pick
	c := p.field
	pp := paperOf()
	// 选中项可能在视口外（选项多时）：打开当帧把它滚进来，之后不再干预用户滚动。
	if !p.scrolled {
		p.scrolled = true
		if c.idx > 0 && c.idx < len(c.values) {
			p.list.ScrollTo(c.idx)
		}
	}
	return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperBodySp(th), p.title)
				l.Font.Weight = font.Bold
				l.Color = pp.text
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), "点一项即选中；点卡片外或按 Esc 取消。")
				l.Color = pp.text3
				return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(6)}.Layout(gtx, l.Layout)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				n := len(c.values)
				return p.list.Layout(gtx, n, func(gtx layout.Context, i int) layout.Dimensions {
					return pickerOption(gtx, th, &p.buttons[i], c.displays[i], i == c.idx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(paperButton(th, &p.cancel, "取消", paperBtnSecondary, true)),
					)
				})
			}),
		)
	})
}

// pickerOption 弹层里一个选项：整行可点，当前项用品牌色底 + 勾号 + 强调色标出。
func pickerOption(gtx layout.Context, th *material.Theme, btn *widget.Clickable, label string, selected bool) layout.Dimensions {
	pp := paperOf()
	h := gtx.Dp(38)
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := image.Pt(gtx.Constraints.Max.X, h)
		if selected {
			r := image.Rectangle{Max: sz}.Inset(gtx.Dp(2))
			paint.FillShape(gtx.Ops, pp.brandSoft,
				clip.UniformRRect(r, gtx.Dp(unit.Dp(pp.rChip))).Op(gtx.Ops))
		}
		mark := "   "
		if selected {
			mark = "✓ "
		}
		l := material.Label(th, paperBodySp(th), mark+label)
		l.Color = pp.text
		if selected {
			l.Color = pp.accent
			l.Font.Weight = font.Bold
		}
		return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(10), Top: unit.Dp(8)}.Layout(gtx, l.Layout)
	})
}

// pickerOptions 当前弹层的选项文案（未打开返回 nil；测试与调试读口）。
func (f *settingsForm) pickerOptions() []string {
	if !f.pick.open || f.pick.field == nil {
		return nil
	}
	return slices.Clone(f.pick.field.displays)
}
