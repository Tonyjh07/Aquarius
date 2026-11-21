package uigui

// cmd.go 命令折叠条（D265）：命令回显与命令返回都是**运行输出**——两者不在会话树里，
// 逐条铺进转写区会把转写区写成一堵机器话（每条还带着 26 位节点 id），而「已重新生成」
// 这类句子与助手气泡同底板、同字号，读起来像模型在说话。连续的命令因此合成一条淡灰
// 圆角条，默认只留一行「折叠 N 条命令，点击展开」，点开才铺命令行与输出。
//
// 判据仍是 D75/D81 的镜面规则：转写区里的**内容**必须能在树上找到；命令不是内容，
// 所以既不占一条消息的位置，也不冒充用户气泡。
//
// 形态（D266）：底色是**带 alpha 的深色薄层**（`cardCmd`），不是实色卡——命令回显是
// 运行痕迹，该读作「沉在背景里的一层雾」。深色相位给出槽的层次，alpha 让该薄层吃掉背后
// 的桌面、边界不硬；实色灰只能在「够深」与「够淡」之间二选一。宽度随开合变：收起态
// 贴合文案、水平居中成一枚窄条（满宽条即使再淡也还在和消息抢行），展开态才铺满转写区
// ——那时要读整行输出（`centerStrip`）。

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// cmdEntry 命令折叠条里的一条：命令行原文（"" = 只有输出，历史装配的兜底）与其返回文本。
// 两者都是运行输出，展开后命令行就是「这条结果是谁产生的」的出处。note = 一条操作回执
// （D267）：没有命令行、也不是命令输出，与命令同行时为「这次操作的附注」。err = 一条
// 错误（D323）：错误也是运行输出，同样收进折叠条——展开后用错误色渲染，头行报数。
type cmdEntry struct {
	cmd  string
	out  string
	note string
	err  string
}

// cmdClick 折叠行的点击控件 get-or-create（与 thinkClick 同规则：按块序缓存，
// m.blocks 只增不减，块序变化只发生在清屏后）。
func (u *UI) cmdClick(i int) *widget.Clickable {
	for len(u.cmdClicks) <= i {
		u.cmdClicks = append(u.cmdClicks, new(widget.Clickable))
	}
	return u.cmdClicks[i]
}

// cmdIsOpen 命令条展开态：默认折叠（map 零值 = 收起），视图态只存 UI、不进 model；
// 清屏复位（与 chipOpen/thinkOpen 同规则）。
func (u *UI) cmdIsOpen(i int) bool { return u.cmdOpen[i] }

// cmdHeaderText 折叠行文案：按构成计数 + 动作提示。整条文字居中，所以不带 ▸/▾ 箭头——
// 箭头一居中就歪，点击语义由「点击展开/收起」这半句说清。
//
// 条数各按自己的种类算（D323）：命令按**命令行**数（一条命令常常带一项输出或一条回执，
// 按条目数会报成「2 条命令」而屏幕上只有一条）、回执按 note 数、错误按 err 数；只报
// 非零段，用「 · 」连起来。没有命令行（nCmd=0）= 这条只装着回执/错误。
func cmdHeaderText(nCmd, nNote, nErr int, open bool) string {
	var parts []string
	if nCmd > 0 {
		parts = append(parts, i18n.Tf("ui.cmd.header_commands", "n", nCmd))
	}
	if nNote > 0 {
		parts = append(parts, i18n.Tf("ui.cmd.header_notes", "n", nNote))
	}
	if nErr > 0 {
		parts = append(parts, i18n.Tf("ui.cmd.header_errors", "n", nErr))
	}
	act := i18n.T("ui.cmd.expand")
	if open {
		act = i18n.T("ui.cmd.collapse")
	}
	return strings.Join(parts, " · ") + " · " + act
}

// cmdCounts 折叠条里的命令行数、回执数与错误数。
func cmdCounts(es []cmdEntry) (nCmd, nNote, nErr int) {
	for _, e := range es {
		if e.cmd != "" {
			nCmd++
		}
		if e.note != "" {
			nNote++
		}
		if e.err != "" {
			nErr++
		}
	}
	return nCmd, nNote, nErr
}

// cmdHeadColor 折叠行文字色（D266）：工具灰再退一档（半透明）——条本身已经退成一层
// 薄雾，文字跟着退，整块才读作运行痕迹而不是一条消息。
func cmdHeadColor() color.NRGBA {
	c := tok().textMuted
	c.A = 0xB4
	return c
}

// centerStrip 把一行文案铺在**整条可用宽度**上并居中（D265）：展开态的命令条是横贯
// 转写区的底板，宽度跟容器走、不跟文字走——所以先量文字，再按剩余宽度摆。返回尺寸的
// X 恒等于可用宽，measureRow 据此把底板拉满；左右留白由行内衬（cardPadX）提供。
//
// 折叠态**不走这里**（D266）：那时条宽贴合文案（由 paintRow 居中摆），中心是条自己的
// 中心，再在条内二次居中反而把宽度又拉满。
func centerStrip(gtx layout.Context, w layout.Widget) layout.Dimensions {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	txt := m.Stop()
	width := gtx.Constraints.Max.X
	if width < d.Size.X { // 极窄窗：宁可让底板比容器宽，也不把文字裁掉
		width = d.Size.X
	}
	st := op.Offset(image.Pt((width-d.Size.X)/2, 0)).Push(gtx.Ops)
	txt.Add(gtx.Ops)
	st.Pop()
	return layout.Dimensions{Size: image.Pt(width, d.Size.Y)}
}
