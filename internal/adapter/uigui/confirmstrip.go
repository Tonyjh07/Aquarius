package uigui

// confirmstrip.go 权限确认折叠条（D329）：没有 chip 承载的确认（脚本里 `tools.parallel` 同时举手、
// 命令确认）合成一条，默认只留一行「5 次权限确认 · 允许 4 · 拒绝 1」，点开才逐条铺问答。
//
// 为什么要收：这些确认在脚本内执行，不产生顶层 ToolCall 事件，也就没有 chip 可以挂。
// 逐条铺进转写区，一次并行调用就能把正文顶出视口——这是运行痕迹里的噪声，不该和消息抢行。
// 判据与命令折叠条（D265）同一套：确认不是发言，收起只是一枚窄条；条内每一项的完整
// 问句仍在确认态里（跨窗确认卡显示全文），这里只留够认出是哪一次调用的长度。

import (
	"strings"

	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// confirmEntry 折叠条里的一次确认：单行问句与应答回显（"" = 还没答）。
// deny/level/silent 只用于头行报数——应答文案是给人读的句子，按字符串回推成败太脆。
type confirmEntry struct {
	q      string
	a      string
	deny   bool // 拒绝（含带原因）
	level  bool // 改档位：既非允许也非拒绝，下一次调用可能又弹一次
	silent bool // 本轮结束时仍没答：确认态已收掉，这一项作废
}

// confirmCounts 条内的确认总数、允许数、拒绝数、改档数、待答数、作废数。
func confirmCounts(es []confirmEntry) (total, allow, deny, level, pending, silent int) {
	for _, e := range es {
		total++
		switch {
		case e.silent:
			silent++
		case e.a == "":
			pending++
		case e.level:
			level++
		case e.deny:
			deny++
		default:
			allow++
		}
	}
	return total, allow, deny, level, pending, silent
}

// confirmHeaderText 收起态那一行的文案：只报非零段，用「 · 」连起来。
// 末尾带动作提示（与命令条同法子，不带 ▸/▾——整条文字居中，箭头一居中就歪）。
func confirmHeaderText(es []confirmEntry, open bool) string {
	total, allow, deny, level, pending, silent := confirmCounts(es)
	parts := []string{i18n.Tf("ui.confirm.strip_total", "n", total)}
	if allow > 0 {
		parts = append(parts, i18n.Tf("ui.confirm.strip_allowed", "n", allow))
	}
	if deny > 0 {
		parts = append(parts, i18n.Tf("ui.confirm.strip_denied", "n", deny))
	}
	if level > 0 {
		parts = append(parts, i18n.Tf("ui.confirm.strip_level", "n", level))
	}
	if pending > 0 {
		parts = append(parts, i18n.Tf("ui.confirm.strip_pending", "n", pending))
	}
	if silent > 0 {
		parts = append(parts, i18n.Tf("ui.confirm.strip_silent", "n", silent))
	}
	act := i18n.T("ui.cmd.expand")
	if open {
		act = i18n.T("ui.cmd.collapse")
	}
	return strings.Join(parts, " · ") + " · " + act
}

// confirmClick 折叠行点击件 get-or-create（与 cmdClick 同规则：按块序缓存，
// m.blocks 只增不减，块序变化只发生在清屏后）。
func (u *UI) confirmClick(i int) *widget.Clickable {
	for len(u.confirmClicks) <= i {
		u.confirmClicks = append(u.confirmClicks, new(widget.Clickable))
	}
	return u.confirmClicks[i]
}

// confirmIsOpen 折叠条展开态：**正在问的那一条强制展开**——问句藏着，用户就只看到一行
// 「待答 1」，等于没问（与 chip 的强制展开同规则）。其余按用户手动开合：应答后前台上换成
// 下一条（同一块则继续展开）或清空，条自动回到收起；用户自己点开的仍保持展开。
// 视图态只存 UI、不进 model；清屏复位（与 cmdOpen/chipOpen 同规则）。
func (u *UI) confirmIsOpen(i int) bool {
	if u.m != nil && u.m.confirm != nil && u.m.confirm.bi == i && u.m.confirm.ci >= 0 {
		return true
	}
	return u.confirmOpen[i]
}

// addConfirmEntry 把一次没有 chip 承载的确认并入末尾的确认条（连续确认共用一条），
// 否则新开一条。返回块序与条内序号，应答时按这两项就地回填。
func (m *model) addConfirmEntry(line string) (int, int) {
	if strings.TrimSpace(line) == "" {
		return -1, -1
	}
	if n := len(m.blocks); n > 0 && m.blocks[n-1].kind == blockConfirm {
		b := &m.blocks[n-1]
		b.confirms = append(b.confirms, confirmEntry{q: line})
		return n - 1, len(b.confirms) - 1
	}
	m.blocks = append(m.blocks, block{
		kind: blockConfirm, part: -1, confirms: []confirmEntry{{q: line}},
	})
	return len(m.blocks) - 1, 0
}

// echoConfirmEntry 把应答写回条内那一项（就地改，不追加）：一次确认只留一项。
// 块已被清屏（会话重建）时补一条完整的——留痕不能凭空丢。
func (m *model) echoConfirmEntry(pc *pendingConfirm, ans port.ConfirmAnswer) {
	if pc.bi >= 0 && pc.bi < len(m.blocks) && m.blocks[pc.bi].kind == blockConfirm {
		if es := m.blocks[pc.bi].confirms; pc.ci >= 0 && pc.ci < len(es) {
			es[pc.ci].a = confirmEcho(ans)
			es[pc.ci].deny = !ans.Allow
			es[pc.ci].level = strings.TrimSpace(ans.Level) != ""
			return
		}
	}
	m.blocks = append(m.blocks, block{
		kind: blockConfirm, part: -1,
		confirms: []confirmEntry{{
			q: pc.line, a: confirmEcho(ans),
			deny: !ans.Allow, level: strings.TrimSpace(ans.Level) != "",
		}},
	})
}
