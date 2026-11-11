package uigui

// inject.go 注入块的折叠（D166 的形态修订）。
//
// 注入块的正文是**进模型上下文的原文**（工具回填、任务收尾、日期说明），它的读者本来
// 是模型而不是人。展开铺一屏英文段落，等于把机器的底稿摊在转写区里；但完全不显示也不行
// ——「模型到底被告知了什么」是用户会追问、也需要自己核对的事。
//
// 所以默认折成一行：`▸ 指令更新 · 日期变更`。第一段说这是哪一类注入（注入本身不是消息，
// 顶头一行先交代它的身份），第二段说来自哪儿。点开才铺触发状态与正文全文。

import (
	"strings"

	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// injectSep 头部两段的连接符（中英同形：窄间隔点，比「·」两侧留白更紧）。
const injectSep = " · "

// injectClick 折叠行的点击控件 get-or-create（与 chipClick 同口径：按块序缓存，
// m.blocks 只增不减，块序变化只发生在清屏后）。
func (u *UI) injectClick(i int) *widget.Clickable {
	for len(u.injectClicks) <= i {
		u.injectClicks = append(u.injectClicks, new(widget.Clickable))
	}
	return u.injectClicks[i]
}

// injectIsOpen 注入块展开态：默认折叠（map 零值 = 收起），视图态只存 UI、不进 model；
// 清屏复位（与 chipOpen/cmdOpen 同口径）。
func (u *UI) injectIsOpen(i int) bool { return u.injectOpen[i] }

// injectHeadline 折叠行整行文案：`▸ 指令更新 · 日期变更`（展开时箭头朝下）。
//
// 箭头不居中——这一行与工具 chip 一样贴左，箭头带的是点击语义（可以点开），
// 与「▸/▾ 点击展开」的既有观感一致。
func injectHeadline(source string, open bool) string {
	arrow := "▸"
	if open {
		arrow = "▾"
	}
	return arrow + " " + injectTitle(source)
}

// injectTitle 类别 + 余下部分，用 ` · ` 连。
//
// 类别按来源前缀定，**余下部分不带前缀词**：`port.InjectSourceLabel` 给的是「工具 think」
// 这样自带类别的整句，直接接在「工具注入」后面会读成「工具注入 · 工具 think」。
// 未知前缀不猜类别，整段回落成来源标签——那时读者更需要看见原文。
func injectTitle(source string) string {
	name, rest := source, ""
	if i := strings.IndexByte(source, ':'); i >= 0 {
		name, rest = source[:i], source[i+1:]
	}
	// 每个类别都写成独立的 i18n.T 字面量：拼接出来的 key 静态扫描看不见，会被当成
	// 没人用的孤儿条目（TestNoOrphanKeys）。
	var kind string
	switch name {
	case "clock":
		// 日期说明没有「余下」这一段：来源本身就是全部信息（port.InjectSourceLabel 同款特判）。
		return i18n.T("ui.inject.kind.clock") + injectSep + i18n.T("port.src.clock")
	case "job":
		kind = i18n.T("ui.inject.kind.job")
	case "poller":
		kind = i18n.T("ui.inject.kind.poller")
	case "tool":
		kind = i18n.T("ui.inject.kind.tool")
	case "plugin":
		kind = i18n.T("ui.inject.kind.plugin")
	case "skill":
		kind = i18n.T("ui.inject.kind.skill")
	case "user":
		kind = i18n.T("ui.inject.kind.user")
	default:
		// 未知前缀不猜类别，整段回落成来源标签——那时读者更需要看见原文。
		return port.InjectSourceLabel(source)
	}
	if rest == "" {
		return kind
	}
	return kind + injectSep + rest
}
