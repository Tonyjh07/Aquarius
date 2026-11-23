package uigui

// turn.go Turn 过程折叠（D326）：assistant 定稿块出现后，向前收集本轮的思考块、工具
// chip 块与确认折叠条，折成一行摘要——「工作 x 秒，调用了 x 个工具」，点击展开按原样
// 铺回。过程条目的默认观众是阅读时刻，原文只有排障时才要读。
//
// 数据不动块序：m.blocks 只增不减是不变量（thinkOpen/chipOpen/cmdClicks 按块序缓存、
// 分叉条按块序挂点都压在上面）——折叠只在 frameItems 的产出层生效；键数恒定取子块
// 之和（D265 同规则：折叠不铺正文但键数占位防漂移），摘要行占用第 0 键。展开态按
// turnID（组尾 assistant 块的节点 ID，跨帧稳定）记在 u.turnOpen。

import (
	"strconv"

	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// turnView 一组被折叠的 Turn 过程块（摘要行的数据面）。
type turnView struct {
	blocks   []blockView // 被折叠的子块 view（键数与展开态铺回的顺序来源）
	ms       int         // Σ 思考耗时（无耗时数据的段不计；全无 = -1 → 摘要省略秒段）
	tools    int         // 工具 chip 数（折叠判据与摘要计数）
	confirms int         // 折进来的权限确认条数（摘要补一段数，不收起来也不能凭空少）
	id       conversation.MessageID
	slot     int // 组尾 assistant 块序：摘要行点击件的缓存槽，也是 groups 的键
	start    int // 组首块序（含开头穿透的中途正文段）：摘要行铺在它前面
}

// scanTurnGroups 每帧重算折叠分组：assistant 定稿块（有节点 ID）向前收集本轮的思考
// 块、工具 chip 块与确认折叠条；组内含 ≥1 个 chip 才成组（纯思考组维持思考块自己的
// 折叠行，D42——再包一层摘要没有信息增量）。返回 slot → 组（slot = 组尾 assistant
// 块序）。
//
// 往回走时两类块**穿透**（不折进去也不截断）：本轮中途落块的正文段（无节点 ID，
// D95 工具边界 flush 出来的那几段）——一行「我先看看」不该把后面的工具切出总结；未
// 答完的确认条同理（问句还挂在转写区里，收起来等于没问）。
func (u *UI) scanTurnGroups() map[int]*turnView {
	groups := map[int]*turnView{}
	claimed := make([]bool, len(u.m.blocks))
	for bi, b := range u.m.blocks {
		if b.kind != blockAssistant || b.id == "" {
			continue
		}
		start := bi
		for start > 0 {
			if claimed[start-1] || !u.turnPassThrough(start-1) {
				break
			}
			start--
		}
		if start == bi {
			continue
		}
		tv := &turnView{id: b.id, slot: bi, start: start}
		hasTool := false
		var got []int
		for j := start; j < bi; j++ {
			p := u.m.blocks[j]
			if !turnProcBlock(p) {
				continue // 穿透块：留在原位，不属于任何组
			}
			if p.kind == blockTool {
				hasTool = true
			}
			got = append(got, j)
			switch p.kind {
			case blockTool:
				tv.tools++
				// 子块 view 的构造与 frameItems 逐块路径同款（chip 携 bi 供右键回查）。
				tv.blocks = append(tv.blocks, blockView{kind: blockTool, chip: p.chip, chipIdx: j, bi: j})
			case blockConfirm:
				tv.confirms++
				tv.blocks = append(tv.blocks, blockView{kind: blockConfirm, confirms: p.confirms, bi: j})
			default:
				if p.kind == blockThinking {
					if p.ms >= 0 {
						if tv.ms < 0 {
							tv.ms = 0
						}
						tv.ms += p.ms
					} else if tv.ms == 0 {
						tv.ms = -1 // 目前只有无耗时段：保持「无秒数据」
					}
				}
				tv.blocks = append(tv.blocks, blockView{
					kind: p.kind, text: p.text, ms: p.ms, bi: j, id: p.id,
					imgs: p.imgs, source: p.source, trigger: p.trigger, cmds: p.cmds,
				})
			}
		}
		if !hasTool {
			continue // 纯思考组不折（D42 各有自己的折叠行）
		}
		for _, j := range got {
			claimed[j] = true
		}
		groups[bi] = tv
	}
	return groups
}

// turnPassThrough 往回扫时的取舍：true = 继续往前看这一块。
//
//   - 折叠的子块（思考 / chip / 答完的确认条）：折进来；
//   - 本轮中途落块的正文段：穿透（原位留在转写区，不折）；
//   - 未答完的确认条：穿透（问句不能被收起）；
//   - 其余（上一条用户消息、上一轮定稿回答、注入卡……）：到此为止。
func (u *UI) turnPassThrough(i int) bool {
	b := u.m.blocks[i]
	if turnProcBlock(b) {
		return true
	}
	return b.kind == blockAssistant && b.id == "" // D95 中途段：无节点 ID
}

// turnProcBlock 该块是否属于「过程」：思考、带 chip 的工具、已答完的确认条。
func turnProcBlock(b block) bool {
	switch b.kind {
	case blockThinking:
		return true
	case blockTool:
		return b.chip != nil
	case blockConfirm:
		_, _, _, _, pending, _ := confirmCounts(b.confirms)
		return pending == 0
	}
	return false
}

// turnHeaderText 摘要行文案：有思考耗时给「工作 x（秒段），调用了 n 个工具」，
// 无耗时数据（回放）省略秒段；折进了确认条就再补一段「n 次权限确认」（确认被收起来，
// 数不能跟着消失）。秒段格式与思考块同一套（<1s 毫秒、≥1s 一位小数秒）。
func turnHeaderText(tv *turnView, open bool) string {
	arrow := "▸"
	if open {
		arrow = "▾"
	}
	var body string
	if tv.ms < 0 {
		body = i18n.Tf("ui.turn.summary_plain", "n", tv.tools)
	} else {
		sec := i18n.Tf("ui.turn.sec_ms", "n", tv.ms)
		if tv.ms >= 1000 {
			sec = i18n.Tf("ui.turn.sec_s", "n",
				strconv.FormatFloat(float64(tv.ms)/1000, 'f', 1, 64))
		}
		body = i18n.Tf("ui.turn.summary", "sec", sec, "n", tv.tools)
	}
	if tv.confirms > 0 {
		body += " · " + i18n.Tf("ui.confirm.strip_total", "n", tv.confirms)
	}
	return arrow + " " + body
}

// hasBlock 该块序是否属于本组（折叠态的摘要行要用它把锚在子块上的分叉条顺延出来）。
func (tv *turnView) hasBlock(bi int) bool {
	for _, sub := range tv.blocks {
		if sub.bi == bi {
			return true
		}
	}
	return false
}

// turnShown 组是否已摊开（含动画中）：子行出条目、摘要行让位——键数由子行自己占。
func (u *UI) turnShown(slot int) bool { return u.turnShow[slot] }

// turnClick 摘要行点击件 get-or-create（按块序缓存，同 cmdClick 规则）。
func (u *UI) turnClick(i int) *widget.Clickable {
	for len(u.turnClicks) <= i {
		u.turnClicks = append(u.turnClicks, new(widget.Clickable))
	}
	return u.turnClicks[i]
}

// turnIsOpen 该组是否展开（u.turnOpen 按 turnID 记，跨块序稳定）。
func (u *UI) turnIsOpen(tv *turnView) bool { return u.turnOpen[tv.id] }
