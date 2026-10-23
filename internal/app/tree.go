package app

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Session 实现会话树只读视图端口（D80/§7.5）：本端口消费方是 UI 适配器、实现方是 app，
// 与 llm/store 的 实现方=adapter 反向；两侧都只依赖 port，依赖方向不变（§5 铁律 1）。
var _ port.TreeView = (*Session)(nil)

// treeSnapshot 会话树只读快照（D80/§7.5，前置 A）：整树一次重建、整体发布的**不可变**
// 值——发布后不再改写，UI 事件循环 goroutine 无锁读，故无竞态（-race 干净）。
//
// 口径：展示面（供 UI 画分叉编号与左右切换），不参与推理装配（装配走 assemblePath/水位）。
type treeSnapshot struct {
	// branches 每个节点 → 其同级视图（同父全部孩子按创建序 + 自身下标）。
	// 同一父节点下的所有孩子共享同一个 IDs 切片（只读，调用方不得改写）。
	branches map[conversation.MessageID]port.BranchInfo
	// tails 每个节点 → 其版本子树的对话末端（D94：最新叶子，分支切换落点）。
	tails map[conversation.MessageID]conversation.MessageID
	// graph 整树快照（D112②/D120⑦，S3 relation-map 数据面）：同一构建过程产出，
	// 与 branches/tails 自洽（同一次发布）。
	graph port.TreeGraph
	// msgs 每个节点的只读副本（D132：气泡级动作要正文全文，GraphNode.Snippet 只有 40 字
	// 摘要）。节点**不可变**（§2 铁律 2：改动一律经 Revise 造新节点换 map 项），故按值
	// 拷贝结构体即与整份正文共享底层分片——不重复占内存，也不会被后续 Revise 改写。
	msgs map[conversation.MessageID]conversation.Message
}

// makeTreeSnapshot 把领域视图翻译成展示面端口结构（D80/§7.5）。
//
// 排序与末端口径都不在这里：同级按 (CreatedAt, ID) 升序（D15/D16）与「子树最新叶子」
// （D94）都是领域规则，由 `Conversation.View` 给出——展示面只做形状转换。
// 此前这里各写了一份，规则一改就要改两处。
func makeTreeSnapshot(c *conversation.Conversation) *treeSnapshot {
	view := c.View()
	total := len(view.Nodes)
	snap := &treeSnapshot{
		branches: make(map[conversation.MessageID]port.BranchInfo, total),
		tails:    view.Tails,
		msgs:     make(map[conversation.MessageID]conversation.Message, total),
	}
	sibIdx := make(map[conversation.MessageID]int, total)
	sibN := make(map[conversation.MessageID]int, total)
	for _, group := range view.Groups {
		for i, kid := range group.IDs {
			snap.branches[kid] = port.BranchInfo{IDs: group.IDs, Index: i}
			sibIdx[kid], sibN[kid] = i, len(group.IDs)
		}
	}
	// 整树节点表（graph）：View.Nodes 已按 (CreatedAt, ID) 升序，布局可复现。
	nodes := make([]port.GraphNode, 0, total)
	for _, m := range view.Nodes {
		snap.msgs[m.ID] = m // D132：节点只读副本（共享分片底层数组，节点不可变）
		ek := port.EdgeSeq
		if m.Parent == "" {
			ek = "" // 结构根无父边（D120§6.1）
		} else if _, revised := c.Edition(m.ID); revised {
			ek = port.EdgeRevise
		}
		nodes = append(nodes, port.GraphNode{
			ID:           m.ID,
			Parent:       m.Parent,
			Role:         m.Role,
			Snippet:      nodeSummary(m),
			Source:       m.Source,
			Trigger:      m.Trigger,
			CreatedAt:    m.CreatedAt,
			EdgeKind:     ek,
			SiblingIdx:   sibIdx[m.ID],
			SiblingCount: sibN[m.ID],
		})
	}
	snap.graph = port.TreeGraph{Anchor: view.Head, Nodes: nodes, Total: len(nodes)}
	return snap
}

// publishTree 重建并原子发布树只读快照（D80/§7.5）：树变更后调用。发布点只有两个——
// 构造完成（NewSession）与每次 Handle 返回前；树变更只发生在 Handle 内（命令面与 Turn）。
func (s *Session) publishTree() {
	if s.cur == nil {
		return
	}
	s.tree.Store(makeTreeSnapshot(s.cur))
}

// Branches 实现 port.TreeView（D80/§7.5）：读已发布的不可变快照，无锁——可从 UI 事件
// 循环 goroutine 直接调用（§15.5 线程安全回调口径）。id 不在树中（或尚未发布）→ ok=false。
func (s *Session) Branches(id conversation.MessageID) (port.BranchInfo, bool) {
	snap := s.tree.Load()
	if snap == nil {
		return port.BranchInfo{}, false
	}
	bi, ok := snap.branches[id]
	return bi, ok
}

// Tail 实现 port.TreeView（D94）：读已发布的不可变快照，无锁——口径同 Branches。
func (s *Session) Tail(id conversation.MessageID) (conversation.MessageID, bool) {
	snap := s.tree.Load()
	if snap == nil {
		return "", false
	}
	t, ok := snap.tails[id]
	return t, ok
}

// Graph 实现 port.TreeView（D112②/D120⑦，S3 relation-map 数据面）：读已发布的不可变
// 快照，无锁——口径同 Branches；尚未发布时 ok=false。返回值整体按值拷贝，Nodes 切片为
// 快照内部数据，调用方只读、不得改写。
func (s *Session) Graph() (port.TreeGraph, bool) {
	snap := s.tree.Load()
	if snap == nil {
		return port.TreeGraph{}, false
	}
	return snap.graph, true
}

// NodeText 实现 port.TreeView（D132：气泡级动作的数据源）：快照内该节点的正文全文，
// 无锁——口径同 Branches/Tail/Graph；尚未发布或 id 不在树中 → ok=false。
func (s *Session) NodeText(id conversation.MessageID) (string, bool) {
	snap := s.tree.Load()
	if snap == nil {
		return "", false
	}
	m, ok := snap.msgs[id]
	if !ok {
		return "", false
	}
	return nodeBodyText(m), true
}

// NodeAttachments 实现 port.TreeView（D188）：节点内附件分片（image/doc/audio）按出现序
// 编号，只读。id 不在树中 → nil（UI 侧据此不画附件 chip 排）。
func (s *Session) NodeAttachments(id conversation.MessageID) []port.AttachmentView {
	snap := s.tree.Load()
	if snap == nil {
		return nil
	}
	m, ok := snap.msgs[id]
	if !ok {
		return nil
	}
	return nodeAttachments(m)
}

// nodeAttachments 节点附件分片清单（D188）：序号即 `/edit --drop N` 的 N——与 editParts
// 的删除口径同源（都按 image/doc/audio 分片的出现序数），两处必须一起改。
func nodeAttachments(m conversation.Message) []port.AttachmentView {
	var out []port.AttachmentView
	n := 0
	for _, p := range m.Content {
		if !isAttachmentPart(p.Kind) {
			continue
		}
		v := port.AttachmentView{Index: n, MIME: string(p.Kind)}
		if p.Ref != nil {
			v.Name, v.MIME, v.Size = p.Ref.Name, p.Ref.MIME, p.Ref.Size
		}
		if v.Name == "" {
			v.Name = "(未命名附件)"
		}
		out = append(out, v)
		n++
	}
	return out
}

// isAttachmentPart 附件分片判定（D188）：image/doc/audio——与 nodeBodyText 跳过的分片、
// editParts 保留的分片同一口径，三处共用这一个判据。
func isAttachmentPart(k conversation.PartKind) bool {
	switch k {
	case conversation.PartImage, conversation.PartDoc, conversation.PartAudio:
		return true
	}
	return false
}

// nodeBodyText 节点正文全文（D132）：PartText 分片依序拼接，跳过思考/工具/附件分片——
// 与气泡块的正文口径一致（编辑预填、复制、引用、查看原文共用同一份）。分隔 "\n\n" 是
// `/edit` 缺省合段（D107③ 把全部正文分片并为一条）的逆运算：原样提交即无损往返。
// 不做 TrimSpace——正文可能是代码块，首尾空白是有内容的（§9）。
func nodeBodyText(m conversation.Message) string {
	var parts []string
	for _, p := range m.Content {
		if p.Kind == conversation.PartText && p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
