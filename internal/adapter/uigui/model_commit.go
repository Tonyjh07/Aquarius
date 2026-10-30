package uigui

// 节点的定稿与回放：commit 的调和渲染、草稿段收口、分片切分与历史节点回放。

import (
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// commit 提交节点定稿（D95 Turn 粒度：每 Turn 一次）：system 节点（/compact 摘要）
// 入块；用量累计；未完成 chip 一并收口（取消/异常收尾没有结果事件，D67）。
// assistant 节点做调和而非重渲——flushed 草稿段就地回填权威文本、chips 保留实时现场
// （确认问答留痕不丢），末段从 parts 追加渲染。
func (m *model) commit(msg conversation.Message) {
	m.closeOpenChips()
	m.usage = addUsage(m.usage, msg.Usage)
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleAssistant:
		start := len(m.blocks)
		// D100②：本轮思考块补盖节点 ID（未盖章 = 本轮所产——前轮已在各自 commit 盖过），
		// thinking 右键取"所源节点"语义；分叉条挂点口径不变（branchStrips 不取 thinking）。
		for i := range m.blocks {
			if b := &m.blocks[i]; b.kind == blockThinking && b.id == "" {
				b.id = msg.ID
			}
		}
		m.resetDraft()
		segs := nodeSegments(msg.Content)
		if len(segs) == 0 && len(m.flushedDrafts) == 0 {
			// 空内容的取消/错误也要有反馈（首个 token 前取消是最常见场景）；
			// 纯工具/思考轮（chip/思考卡已实时入块）走 D89 锚点回填节点 ID。
			// 空 Outcome 不打标记（D107 补：旧版 Revise 产的修订节点零值——标记会渲染成 `[]`）。
			if msg.Outcome != "" && msg.Outcome != conversation.OutcomeDone {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), msg.ID)
			} else {
				m.stampAssistantAnchor(msg, start)
			}
			return
		}
		// flushed 草稿段就地回填（FIFO；权威文本以节点为准），并盖段序（D107④）。
		consumed := 0
		// D136：末个 flushed 段的索引在清空前捕获——「Turn 以工具收场（有助手正文、无
		// 追加余段）」时 stamped 恒 false，而下面那条回填分支原来判的是**已清空的**
		// flushedDrafts ⇒ 恒假 ⇒ 该 Turn 的挂点丢失、分叉条不出现（外部审查 2026-10-13）。
		lastFlushed := -1
		if n := len(m.flushedDrafts); n > 0 {
			lastFlushed = m.flushedDrafts[n-1]
		}
		for _, idx := range m.flushedDrafts {
			if consumed >= len(segs) {
				break
			}
			m.blocks[idx].text = segs[consumed].text
			m.blocks[idx].imgs = segs[consumed].imgs // D207：段内图片随段调和
			m.blocks[idx].part = segs[consumed].start
			consumed++
		}
		m.flushedDrafts = m.flushedDrafts[:0]
		// 余段（收场轮的正文，通常仍在草稿）追加渲染；最后一段盖节点 ID
		// ——分叉条（D81）每 Turn 一个挂点，挂在回答气泡下。
		stamped := false
		for i := consumed; i < len(segs); i++ {
			blk := block{kind: blockAssistant, text: segs[i].text, part: segs[i].start, imgs: segs[i].imgs}
			if msg.Outcome != "" && msg.Outcome != conversation.OutcomeDone && i == len(segs)-1 {
				mark := fmt.Sprintf("[%s]", msg.Outcome)
				if strings.TrimSpace(blk.text) == "" {
					blk.text = mark
				} else {
					blk.text = mark + "\n" + blk.text
				}
			}
			if i == len(segs)-1 {
				blk.id = msg.ID
				stamped = true
			}
			m.blocks = append(m.blocks, blk)
		}
		// Turn 以工具收场（正文段全部经工具边界落块）：末个已落块盖 ID。
		// D136：索引取**清空前捕获**的 lastFlushed——原实现判的是上面 [:0] 清空后的
		// flushedDrafts，恒假 ⇒ 这类 Turn 一个挂点都没有、分叉条不出现（外部审查 2026-10-13）。
		if !stamped && lastFlushed >= 0 && lastFlushed < len(m.blocks) {
			m.blocks[lastFlushed].id = msg.ID
		}
		m.flushedDrafts = m.flushedDrafts[:0]
	case conversation.RoleSystem:
		m.resetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	case conversation.RoleInject:
		// 注入节点（D166）：独立成档，不落 user 盖章、不改标题、不挂分叉条——
		// 头部由渲染期按 kind 生成（来源 + 触发状态），正文原样可选。
		m.resetDraft()
		m.addInject(text, msg.Source, msg.Trigger)
	default:
		// user 无独立事件面：输入行已在 submit 时回显入块——但回显时节点尚不存在、
		// 块无 ID（D87），此处按「最早未盖章且文本一致」回填节点 ID，分叉条（D81）
		// 在实时会话才能出现；工具结果随 assistant 节点分片呈现（D95）。
		// D207：纯图提交（无正文）也要盖章——回显块的正文同样为空，按空串配对。
		if msg.Role == conversation.RoleUser {
			if imgs := nodeImages(msg.Content); text != "" || len(imgs) > 0 {
				m.stampUserBlock(text, msg.ID, imgs)
			}
		}
		m.resetDraft()
	}
}

// flushDraftSegment 工具边界把流式草稿落为正文气泡（D95 Turn 粒度）：块先不带节点
// ID（提交调和时回填权威文本并盖章），草稿清空后第 2 轮文本重新流式。
func (m *model) flushDraftSegment() {
	if !m.drafting && m.draft.Len() == 0 {
		return
	}
	text := m.draft.String()
	m.resetDraft()
	if strings.TrimSpace(text) == "" {
		return
	}
	m.flushedDrafts = append(m.flushedDrafts, len(m.blocks))
	m.blocks = append(m.blocks, block{kind: blockAssistant, text: text, part: -1}) // 段序提交调和时盖（D107④）
}

// nodeSeg 节点正文的一段（D95 Turn 粒度 + D207 图片）：text = 段内可读文本（§4.2 占位、
// §9 出口消毒），imgs = 段内图片（按出现序），start = 段内首个 PartText 的 Content 序号。
type nodeSeg struct {
	text  string
	imgs  []imgRef
	start int
}

// nodeSegments 按工具分片把节点正文切段（D95）：段 = tool 分片之间的连续
// text/image/audio/doc 分片；thinking 不进正文（回放单独成块，D42）。
//
// 判空口径（D207 修订）：**有图且无字也要成段**——纯图消息原来会被 TrimSpace 判空整段
// 丢掉，图就再也不出现。段序与 textSegments 原口径一致（仅工具边界断段）。
func nodeSegments(parts []conversation.Part) []nodeSeg {
	var (
		segs  []nodeSeg
		b     strings.Builder
		imgs  []imgRef
		ord   = -1
		start = -1
	)
	flush := func() {
		text := sanitizeControl(b.String())
		if strings.TrimSpace(text) != "" || len(imgs) > 0 {
			segs = append(segs, nodeSeg{text: text, imgs: imgs, start: start})
		}
		b.Reset()
		imgs = nil
		start = -1
	}
	for _, p := range parts {
		if p.Kind == conversation.PartTool {
			flush()
			continue
		}
		if p.Kind == conversation.PartText {
			ord++
			if start == -1 {
				start = ord
			}
		}
		if p.Kind == conversation.PartImage {
			if r, ok := imageRefOf(p); ok {
				imgs = append(imgs, r)
				continue // 图片由真图承载，正文里不再留文字占位
			}
			// 缺 BlobRef 的分片没有可寻址的图：回落文字占位（见 appendPartText）。
		}
		appendPartText(&b, p)
	}
	flush()
	return segs
}

// imageRefOf 附件分片 → 转写区图片引用（D207）：BlobRef 齐备才算（Hash 是缓存键，
// 缺了没法定址）；缺引用的分片退回文字占位，不至于凭空消失。
func imageRefOf(p conversation.Part) (imgRef, bool) {
	if p.Ref == nil || p.Ref.Hash == "" {
		return imgRef{}, false
	}
	return imgRef{blob: p.Ref, name: p.Ref.Name}, true
}

// nodeImages 节点正文里的全部图片（按出现序）：用户消息整条归一在一个气泡里显示，
// 不需要按工具边界分段。
func nodeImages(parts []conversation.Part) []imgRef {
	var out []imgRef
	for _, p := range parts {
		if p.Kind != conversation.PartImage {
			continue
		}
		if r, ok := imageRefOf(p); ok {
			out = append(out, r)
		}
	}
	return out
}

// stampAssistantAnchor 无正文 assistant 节点的分叉锚点回填（D89）：纯工具轮（模型
// 径直发起调用、无正文）在转写里没有任何带 ID 的**分叉挂点块** → 分叉条无处可挂
// （branchStrips 跳过空 ID 块），切到该分支后无法切回。优先按调用 ID 匹配本轮 chip
// （该节点的可见表示），无工具则锚到本轮最后一个新块。已有锚（挂点块直携 ID）时
// no-op——检查只看 user/assistant/tool（D100②：thinking 块现已盖章，但 branchStrips
// 不取 thinking，思考块带 ID 不构成锚）。
func (m *model) stampAssistantAnchor(msg conversation.Message, start int) {
	if start > len(m.blocks) {
		start = len(m.blocks)
	}
	for _, b := range m.blocks[start:] {
		if b.id == msg.ID && (b.kind == blockUser || b.kind == blockAssistant || b.kind == blockTool) {
			return
		}
	}
	for _, p := range msg.Content {
		if p.Kind == conversation.PartTool && p.Tool != nil {
			if i := m.findChipBlock(tool.CallID(p.Tool.CallID)); i >= 0 {
				m.blocks[i].id = msg.ID
				return
			}
		}
	}
	if len(m.blocks) > start {
		m.blocks[len(m.blocks)-1].id = msg.ID
	}
}

// findChipBlock 按调用 ID 找 chip 块序（D89 锚点匹配；D95 后调用声明在节点分片里）。
func (m *model) findChipBlock(id tool.CallID) int {
	for i := range m.blocks {
		if c := m.blocks[i].chip; c != nil && c.id == id {
			return i
		}
	}
	return -1
}

// stampUserBlock 给回显的用户行补盖节点 ID（D87）：按「最早未盖章且文本一致」匹配
// ——回显与提交同经输入通道串行 FIFO，重名按序对齐；命令行回显以 "/" 开头、
// parseInput 恒路由为命令、永不会成为节点文本，插队也不会被错盖。找不到匹配
// （如附件等无文本提交）静默跳过，维持无 ID 现状。
//
// D207：顺手把图片换成节点里的权威引用（内容屏蔽路径项——暂存时按本地路径解的缩略图，
// 换成入库后的哈希，文件此后被移走/改名也不影响显示）。纯图消息的 want 为空串，
// 与「只有图片没有正文」的回显块配对，同样能盖上 ID（分叉条才有挂点）。
func (m *model) stampUserBlock(text string, id conversation.MessageID, imgs []imgRef) {
	want := sanitizeControl(text)
	for i := range m.blocks {
		b := &m.blocks[i]
		if b.kind != blockUser || b.id != "" || sanitizeControl(b.text) != want {
			continue
		}
		b.id = id
		b.part = 0 // D107④：user 节点单正文分片（标题/随文合并后恒一段）
		if len(imgs) > 0 {
			b.imgs = imgs
		}
		return
	}
}
