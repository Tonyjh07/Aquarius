package uigui

// turn.go Turn 过程折叠（D326）：assistant 定稿块出现后，其前**紧邻连续**的思考块与
// 工具 chip 块在渲染分组层折成一行摘要——「工作 x 秒，调用了 x 个工具」，点击展开按
// 原样铺回。过程条目的默认观众是阅读时刻，原文只有排障时才要读。
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
	blocks []blockView // 被折叠的子块 view（键数与展开态铺回的顺序来源）
	ms     int         // Σ 思考耗时（无耗时数据的段不计；全无 = -1 → 摘要省略秒段）
	tools  int         // 工具 chip 数（折叠判据与摘要计数）
	id     conversation.MessageID
}

// scanTurnGroups 每帧重算折叠分组：assistant 定稿块（有节点 ID）向前收集紧邻连续的
// 思考块与工具 chip 块；组内含 ≥1 个 chip 才成组（纯思考组维持思考块自己的折叠行，
// D42——再包一层摘要没有信息增量）。返回 bi → 组（仅组内块序与组尾 assistant 键）。
// 组间互不重叠：每个过程块归属**其后**第一个 assistant 定稿块。
func (u *UI) scanTurnGroups() map[int]*turnView {
	groups := map[int]*turnView{}
	claimed := make([]bool, len(u.m.blocks))
	for bi, b := range u.m.blocks {
		if b.kind != blockAssistant || b.id == "" {
			continue
		}
		start := bi
		for start > 0 {
			p := u.m.blocks[start-1]
			isProc := p.kind == blockThinking || (p.kind == blockTool && p.chip != nil)
			if claimed[start-1] || !isProc {
				break
			}
			start--
		}
		if start == bi {
			continue
		}
		tv := &turnView{id: b.id}
		hasTool := false
		for j := start; j < bi; j++ {
			claimed[j] = true
			p := u.m.blocks[j]
			if p.kind == blockTool && p.chip != nil {
				tv.tools++
				hasTool = true
			}
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
			// 子块 view 的构造与 frameItems 逐块路径同款（chip 携 bi 供右键回查）。
			if p.kind == blockTool {
				tv.blocks = append(tv.blocks, blockView{kind: blockTool, chip: p.chip, chipIdx: j, bi: j})
			} else {
				tv.blocks = append(tv.blocks, blockView{
					kind: p.kind, text: p.text, ms: p.ms, bi: j, id: p.id,
					imgs: p.imgs, source: p.source, trigger: p.trigger, cmds: p.cmds,
				})
			}
		}
		if hasTool {
			groups[bi] = tv
		}
	}
	return groups
}

// turnHeaderText 摘要行文案：有思考耗时给「工作 x（秒段），调用了 n 个工具」，
// 无耗时数据（回放）省略秒段。秒段格式与思考块同一套（<1s 毫秒、≥1s 一位小数秒）。
func turnHeaderText(tv *turnView) string {
	if tv.ms < 0 {
		return i18n.Tf("ui.turn.summary_plain", "n", tv.tools)
	}
	sec := i18n.Tf("ui.turn.sec_ms", "n", tv.ms)
	if tv.ms >= 1000 {
		sec = i18n.Tf("ui.turn.sec_s", "n",
			strconv.FormatFloat(float64(tv.ms)/1000, 'f', 1, 64))
	}
	return i18n.Tf("ui.turn.summary", "sec", sec, "n", tv.tools)
}

// turnClick 摘要行点击件 get-or-create（按块序缓存，同 cmdClick 规则）。
func (u *UI) turnClick(i int) *widget.Clickable {
	for len(u.turnClicks) <= i {
		u.turnClicks = append(u.turnClicks, new(widget.Clickable))
	}
	return u.turnClicks[i]
}

// turnIsOpen 该组是否展开（u.turnOpen 按 turnID 记，跨块序稳定）。
func (u *UI) turnIsOpen(tv *turnView) bool { return u.turnOpen[tv.id] }
