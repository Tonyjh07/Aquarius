package uigui

// history_summary.go 会话历史窗内的「插入摘要」小卡（D152，§15.7）。
//
// 图侧节点右键「插入摘要」在**本窗**开一层模态小卡：卡里写摘要正文，底行两个来源按钮
// （用户拍板：两个按钮 = 两种来源，各自一键落树）：
//   - 「用模型总结以上历史」= 投 `/summary <id>`：内核让当前模型总结该节点**之上**的历史
//     后插入（忽略卡里已写的内容——那是另一个来源）；
//   - 「插入」= 投 `/summary <id> <输入框文本>`：手写摘要直接插入（文本为空时按钮禁用）。
//
// 生成与插入都在内核（同一命令、同一条 /summary 实现），失败不落树。提交后关卡、投命令，
// 本窗不等结果也不回显——历史窗没有转写区，结果与失败都落在主窗（口径同图侧「删除该节点及子树」）。
//
// 模态机制与遮罩 / 确认弹窗同源（overlay.go 头注）：整窗输入区排在命中链顶，遮蔽背景的
// 同链控件；背景侧（historyState.dispatch、relGraph.update）再按 modalActive 显式不取事件
// 作纵深防御。关闭路径 = Esc / 「取消」：**不**点空白关闭——卡里可能刚敲了正文，误触即丢字。
//
// 目标节点由菜单侧把关（会话根与会话首节点人格不出这一项），此处再挡一道：跑到这儿的
// 目标若不可插，直接不开卡（宁可什么都不发生，也不弹一个注定失败的表单）。

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 摘要小卡几何（dp）：定宽 + 视口高比例。正文可能写很长（多行 markdown 摘要），
// 故卡片比确认弹窗高一档，正文区在卡内滚（不撑窗、不裁字）。
const (
	summaryCardWdp  unit.Dp = 420
	summaryCardHFac         = 0.44
)

// summaryDialog 「插入摘要」小卡的帧状态（**仅历史窗 goroutine 读写**；零值可用）。
type summaryDialog struct {
	// tag 整窗输入区的路由锚点（Gio 要求地址稳定）。
	tag                   struct{}
	id                    conversation.MessageID // 目标节点：摘要插在它之前
	ed                    widget.Editor          // 摘要正文（多行；Enter 落换行，靠按钮提交）
	list                  widget.List            // 正文区滚动（超长正文在卡内滚）
	insert, model, cancel widget.Clickable
	focusNeeded           bool
	card                  image.Rectangle
}

// openSummary 开「插入摘要」小卡（D152）。st.graph 为空（图未就绪）时不开。
func (st *historyState) openSummary(u *UI, id conversation.MessageID) {
	if id == "" || st.graph == nil {
		return
	}
	if role, ok := st.nodeRoleOf(id); ok && role == conversation.RoleRoot {
		return // 根前无处可插
	}
	if st.isPersona(id) {
		return // 摘要顶到路径第二位会被装配当成 persona
	}
	st.closeOverlay() // 两层模态不共存：开卡即收遮罩
	d := &summaryDialog{id: id, focusNeeded: true}
	d.ed.SingleLine = false
	d.ed.Submit = false // Enter 落换行：正文是多行文本，提交靠「插入」
	d.list.List.Axis = layout.Vertical
	st.sum = d
}

// closeSummary 关小卡（Esc / 取消 / 提交后）。
func (st *historyState) closeSummary() { st.sum = nil }

// submit 投一条 /summary 经 u.inCh（与键入同路径，缓冲满则丢弃——与图侧 /rm 同口径）。
// text 为空 = 模型档（内核总结目标之上的历史），非空 = 手写档。
// 顺带唤出主窗：模型档的生成过程与两档的结果行都落在主窗转写区，主窗收在悬浮球里时
// 点了没反应（同遮罩提交的收尾口径）。
func (d *summaryDialog) submit(u *UI, text string) {
	args := []string{string(d.id)}
	if text != "" {
		args = append(args, text)
	}
	select {
	case u.inCh <- port.UserInput{Command: &port.Command{Name: "summary", Args: args}}:
	default:
	}
	_ = u.post(showExpandMsg{})
}

// summaryFrame 小卡单帧：未开卡 = 零尺寸空操作；开卡 = scrim + 居中卡片。
func (st *historyState) summaryFrame(gtx layout.Context, th *material.Theme, u *UI) layout.Dimensions {
	d := st.sum
	if d == nil {
		return layout.Dimensions{}
	}
	// 整窗命中区 + 路由锚点：这一处就是隔离的主机制（同遮罩 / 确认弹窗）。
	stack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Event(key.FocusFilter{Target: &d.tag})
	event.Op(gtx.Ops, &d.tag)

	// Esc 双挂：编辑器获焦与整窗锚点各一（Gio 只把键事件投给当前获焦的 handler）。
	for _, tgt := range []event.Tag{&d.ed, &d.tag} {
		for {
			ev, ok := gtx.Event(key.Filter{Focus: tgt, Name: key.NameEscape})
			if !ok {
				break
			}
			if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
				st.closeSummary()
				return layout.Dimensions{}
			}
		}
	}

	// 先收按钮再画（顺序即语义：一次点击只算一件事）。
	text := strings.TrimSpace(d.ed.Text())
	switch {
	case d.cancel.Clicked(gtx):
		st.closeSummary()
		return layout.Dimensions{}
	case d.insert.Clicked(gtx) && text != "":
		d.submit(u, text)
		st.closeSummary()
		return layout.Dimensions{}
	case d.model.Clicked(gtx):
		d.submit(u, "")
		st.closeSummary()
		return layout.Dimensions{}
	}

	card := summaryCardBox(gtx)
	d.card = card
	paint.FillShape(gtx.Ops, color.NRGBA{A: overlayScrimAlpha},
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	cardRad := gtx.Dp(unit.Dp(paperOf().rCard))
	paperShadow(gtx, card, cardRad, 14, 8, 0x38)
	// 显式定尺定位到卡片盒（同遮罩 / 确认弹窗：layout.Stacked 会清 Min，走 Center 会错位）。
	gtx.Constraints.Min, gtx.Constraints.Max = card.Size(), card.Size()
	trans := op.Offset(card.Min).Push(gtx.Ops)
	dims := summaryCard(gtx, th, st, d)
	trans.Pop()
	dims.Size = card.Size()
	return dims
}

// summaryCardBox 小卡盒（居中、定宽、按视口夹取）。
func summaryCardBox(gtx layout.Context) image.Rectangle {
	margin := gtx.Dp(overlayCardMargindp)
	avail := gtx.Constraints.Max
	w := gtx.Dp(summaryCardWdp)
	if lim := avail.X - 2*margin; w > lim {
		w = lim
	}
	if w < 1 {
		w = avail.X
	}
	h := int(float64(avail.Y) * summaryCardHFac)
	if lim := avail.Y - 2*margin; h > lim {
		h = lim
	}
	if h < 1 {
		h = avail.Y
	}
	card := image.Rect(0, 0, w, h)
	return card.Add(image.Pt((avail.X-card.Dx())/2, (avail.Y-card.Dy())/2))
}

// summaryCard 卡片内容：标题 + 一行说明（含目标节点 ID）+ 正文编辑区（卡内滚）+ 底行三钮。
// 底行 = 取消 / 用模型总结以上历史 / 插入（主钮在最右；空文本时「插入」呈禁用态）。
func summaryCard(gtx layout.Context, th *material.Theme, st *historyState, d *summaryDialog) layout.Dimensions {
	pp := paperOf()
	paperPanel(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		gtx.Dp(unit.Dp(pp.rCard)), gtx.Dp(1), pp.card, pp.hairline)
	text := strings.TrimSpace(d.ed.Text())
	return layout.UniformInset(overlayCardPadDp).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), "插入摘要")
				l.Font.Weight = font.Bold
				l.Color = pp.text
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th),
					"插在 "+summaryShortID(d.id)+" 之前；它之上的历史此后不再回传。"+
						"「插入」用你写的正文，「用模型总结以上历史」让模型来写。")
				l.Color = pp.text3
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			// 正文区：sunken 面 + 卡内滚（长摘要不撑窗）。
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return summaryField(gtx, th, d)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(overlayBtn(&d.cancel, th, "取消", false, true)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(overlayBtn(&d.model, th, "用模型总结以上历史", false, true)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(overlayBtn(&d.insert, th, "插入", true, text != "")),
				)
			}),
		)
	})
}

// summaryField 正文编辑区：sunken 底板 + 内衬 + 多行编辑器（首帧把焦点交给编辑器）。
func summaryField(gtx layout.Context, th *material.Theme, d *summaryDialog) layout.Dimensions {
	if d.focusNeeded {
		d.focusNeeded = false
		gtx.Execute(key.FocusCmd{Tag: &d.ed})
	}
	pp := paperOf()
	radius := gtx.Dp(unit.Dp(pp.rChip))
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			paperPanel(gtx, image.Rectangle{Max: sz}, radius, gtx.Dp(1), pp.sunken, pp.hairline)
			return layout.Dimensions{Size: sz}
		},
		func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return d.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
					return material.Editor(th, &d.ed, "写摘要正文…").Layout(gtx)
				})
			})
		},
	)
}

// summaryShortID 目标节点的短 ID（卡片说明行用；全 ID 太长会把说明挤成两行）。
func summaryShortID(id conversation.MessageID) string {
	const n = 8
	s := string(id)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
