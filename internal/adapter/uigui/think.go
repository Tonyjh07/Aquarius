package uigui

import (
	"fmt"

	"gioui.org/widget"
)

// thinkClick 思考块折叠行的点击控件 get-or-create（D182）：与 chipClick 同口径——按
// 块序缓存（m.blocks 只增不减，块序即索引），块序变化只发生在清屏后（新会话从 0 起）。
func (u *UI) thinkClick(i int) *widget.Clickable {
	for len(u.thinkClicks) <= i {
		u.thinkClicks = append(u.thinkClicks, new(widget.Clickable))
	}
	return u.thinkClicks[i]
}

// thinkIsOpen 思考块展开态（D182）：定稿块默认折叠（map 零值 = 收起，只留折叠行），
// 用户点开才铺正文；视图态只存 UI、不进 model（与 chip 同口径）。
func (u *UI) thinkIsOpen(i int) bool { return u.thinkOpen[i] }

// thinkHeaderText 思考块折叠行（D182）：▸/▾ 开合指示 + 耗时（"已思考 12 秒"；回放无
// 耗时只写"已思考"）。流式中不带指示——那时还没到能折叠的定稿态，始终铺开。
func thinkHeaderText(it blockView, open bool) string {
	if it.live {
		return "思考中…"
	}
	arrow := "▸"
	if open {
		arrow = "▾"
	}
	label := "已思考"
	if it.secs >= 0 {
		label = fmt.Sprintf("已思考 %d 秒", it.secs)
	}
	return arrow + " " + label
}
