package uigui

import (
	"gioui.org/io/input"
	"testing"
)

// TestCommandRowStyleSeparateShape rowStyle 的 blockCommand 只做转发：拆出去的形状
// 必须与原 case 逐项一致，漏传圆角或提前取主题令牌都会在这里露出来。
func TestCommandRowStyleSeparateShape(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	gtx, _ := frameGtx(input.Source{})
	it := blockView{kind: blockCommand, bi: 0,
		cmds: []cmdEntry{{cmd: "/help", out: "help text"}}}
	cardR := gtx.Dp(cardRadiusDp)

	w1, bg1, r1, right1, bubble1 := u.rowStyle(gtx, it, nil)
	w2, bg2, r2, right2, bubble2 := u.commandRowStyle(gtx, it, nil, cardR)
	if w1 == nil || w2 == nil {
		t.Fatal("折叠条没有可渲染的 widget")
	}
	if bg1 != bg2 || r1 != r2 || right1 != right2 || bubble1 != bubble2 {
		t.Fatalf("拆出的形状与 case 不一致: (%v,%d,%v,%v) vs (%v,%d,%v,%v)",
			bg1, r1, right1, bubble1, bg2, r2, right2, bubble2)
	}
}
