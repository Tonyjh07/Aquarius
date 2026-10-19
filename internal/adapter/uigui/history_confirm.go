package uigui

// history_confirm.go 会话历史窗内的**跨窗同步二次确认弹窗**（D157，§15.7）。
//
// 内核的确认等待只有**一条**（`Confirmer.Confirm` 的应答通道），所以「两个窗同时显示同一次
// 确认」不能靠两个应答者并存——那会让先到者应答、另一个永久挂起。做法是**一次确认 + 一份
// 只读快照**：主窗事件循环在 `startConfirm` 把问句发布到 `UI.confirmShare`、`replyConfirm`
// 收尾时撤下；本窗每帧读快照，非空即在窗内画一层模态卡片（问句 + 「拒绝 / 允许」），点击把
// 应答（带快照身份）投回主窗事件循环 → 同一条 `replyConfirm`。于是：
//   - 主窗输入栏确认态与历史窗卡片显示的是**同一份**确认，点任一处都生效；
//   - 应答只对**同一次**确认生效（身份不符的迟到应答被丢弃，不会落到刚新起的下一次确认上）；
//   - 弹窗状态不存在本窗——关窗重开、或历史窗本来就是后开的，只要确认还挂着就照旧呈现。
//
// **模态机制与遮罩同源**（overlay.go 头注）：真正起作用的是**整窗输入区**
//（`clip.Rect(整窗)` + `event.Op`）——它排在命中链顶，会把背景的**同链控件**一并遮住
//（取像实测：弹窗在屏时左栏行的 hover 都没了）；背景侧再显式不取
//（`historyState.dispatch` / `relGraph.update` 见门即返回）作纵深防御。

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 确认卡片几何（dp）：定宽 + 视口高的比例。问句通常是短文本（工具确认的参数可能长），
// 所以卡片按「3–4 行问句」定高：短问句下方留白，长问句在卡内滚（不撑窗、不裁字）。
const (
	confirmCardWdp  unit.Dp = 420
	confirmCardHFac         = 0.32
)

// confirmDialog 本窗确认弹窗的帧状态（**仅历史窗 goroutine 读写**；零值可用）。
type confirmDialog struct {
	// tag 整窗输入区的路由锚点（Gio 要求地址稳定）。
	tag         struct{}
	promptList  widget.List // 问句区（超长问句在卡内滚，不撑窗）
	allow, deny widget.Clickable
}

// confirmFrame 确认弹窗单帧：投影为空 = 零尺寸空操作；非空 = scrim + 居中卡片。
func (st *historyState) confirmFrame(gtx layout.Context, th *material.Theme, u *UI) layout.Dimensions {
	snap := u.confirmShare.Load()
	if snap == nil {
		return layout.Dimensions{}
	}
	// 整窗命中区 + 路由锚点：**这一处就是隔离的主机制**（排在链顶的整窗输入区按 Gio
	// 链式命中连带遮蔽背景：左栏 / 右图 / 状态栏都收不到指针事件）。
	stack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	defer stack.Pop()
	event.Op(gtx.Ops, &st.cfm.tag)

	// 先收应答再画（顺序即语义：点「拒绝」不会被当成「允许」）。
	if st.cfm.deny.Clicked(gtx) {
		st.answerConfirm(u, snap, port.ConfirmAnswer{})
	} else if st.cfm.allow.Clicked(gtx) {
		st.answerConfirm(u, snap, port.ConfirmAnswer{Allow: true})
	}

	card := confirmCardBox(gtx)
	// scrim：铺满视口的半透暗色（背景仍在其下渲染 → 观感是「压暗一层」）。
	paint.FillShape(gtx.Ops, color.NRGBA{A: overlayScrimAlpha},
		clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
	// 浮层阴影先于卡片铺（自小而大），否则会被卡片自身盖掉。
	cardRad := gtx.Dp(unit.Dp(paperOf().rCard))
	paperShadow(gtx, card, cardRad, 14, 8, 0x38)
	// 显式定尺定位到卡片盒（同遮罩：`layout.Stacked` 会把子项 Min 清 0，走 Center 会画到
	// 左上角，与命中盒错位）。
	gtx.Constraints.Min, gtx.Constraints.Max = card.Size(), card.Size()
	trans := op.Offset(card.Min).Push(gtx.Ops)
	dims := confirmCard(gtx, th, st, snap)
	trans.Pop()
	dims.Size = card.Size()
	return dims
}

// answerConfirm 把本窗的应答投回主窗事件循环（带快照身份；主窗据身份丢弃迟到应答）。
// 只投、不就地改本窗状态：真正的收尾（replyConfirm → 撤下快照 → 催本窗重绘）在主窗，
// 本窗下一帧自然收起。
func (st *historyState) answerConfirm(u *UI, snap *confirmShare, ans port.ConfirmAnswer) {
	_ = u.post(confirmResultMsg{ans: ans, share: snap})
}

// confirmCardBox 确认卡片盒（居中、定宽、按视口高夹取）。
func confirmCardBox(gtx layout.Context) image.Rectangle {
	margin := gtx.Dp(overlayCardMargindp)
	avail := gtx.Constraints.Max
	w := gtx.Dp(confirmCardWdp)
	if lim := avail.X - 2*margin; w > lim {
		w = lim
	}
	if w < 1 {
		w = avail.X
	}
	h := int(float64(avail.Y) * confirmCardHFac)
	if lim := avail.Y - 2*margin; h > lim {
		h = lim
	}
	if h < 1 {
		h = avail.Y
	}
	card := image.Rect(0, 0, w, h)
	return card.Add(image.Pt((avail.X-card.Dx())/2, (avail.Y-card.Dy())/2))
}

// confirmCard 卡片内容：标题 + 一行说明 + 问句（可滚）+ 底行两钮（拒绝次要 / 允许强调）。
// 底行顺序与主窗确认态一致（拒绝在前），避免同一确认在两处看起来是两件事。
func confirmCard(gtx layout.Context, th *material.Theme, st *historyState, snap *confirmShare) layout.Dimensions {
	pp := paperOf()
	paperPanel(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		gtx.Dp(unit.Dp(pp.rCard)), gtx.Dp(1), pp.card, pp.hairline)
	return layout.UniformInset(overlayCardPadDp).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperNoteSp(th), "需要确认")
				l.Font.Weight = font.Bold
				l.Color = pp.text
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), "主窗与本窗显示的是同一次确认，任一处作答即可。")
				l.Color = pp.text3
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return st.cfm.promptList.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
					l := material.Label(th, paperNoteSp(th), snap.prompt)
					l.Color = pp.text
					return l.Layout(gtx)
				})
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(overlayBtn(&st.cfm.deny, th, "拒绝", false, true)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(overlayBtn(&st.cfm.allow, th, "允许", true, true)),
				)
			}),
		)
	})
}
