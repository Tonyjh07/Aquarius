package app

import (
	"sort"
	"time"

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
}

// makeTreeSnapshot 由会话树构建快照：每个节点一条 BranchInfo，同级按 (CreatedAt, ID)
// 升序——与 domain `Conversation.Branches` 的排序口径一致（D15/D16：Revise 出来的
// 兄弟节点 CreatedAt 不保证等于插入序，故必须显式排序而非依赖 Children 切片顺序）；
// tails 按 (CreatedAt, ID) 最大叶子 post-order 聚合，与 domain `Conversation.Tail`
// 同口径（D94）；graph 同轮产出整树节点表（节点按 (CreatedAt, ID) 升序，布局可复现）。
func makeTreeSnapshot(c *conversation.Conversation) *treeSnapshot {
	snap := &treeSnapshot{
		branches: make(map[conversation.MessageID]port.BranchInfo, len(c.Nodes)),
		tails:    make(map[conversation.MessageID]conversation.MessageID, len(c.Nodes)),
	}
	sibIdx := make(map[conversation.MessageID]int, len(c.Nodes))
	sibN := make(map[conversation.MessageID]int, len(c.Nodes))
	for _, kids := range c.Children {
		ordered := make([]conversation.MessageID, 0, len(kids))
		for _, kid := range kids {
			if _, ok := c.Nodes[kid]; ok {
				ordered = append(ordered, kid)
			}
		}
		sort.Slice(ordered, func(i, j int) bool {
			a, b := c.Nodes[ordered[i]], c.Nodes[ordered[j]]
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.ID < b.ID
		})
		for i, kid := range ordered {
			snap.branches[kid] = port.BranchInfo{IDs: ordered, Index: i}
			sibIdx[kid], sibN[kid] = i, len(ordered)
		}
	}
	// 整树节点表（graph）：节点集 = 全部入树节点，按 (CreatedAt, ID) 升序。
	nodes := make([]port.GraphNode, 0, len(c.Nodes))
	for id, m := range c.Nodes {
		ek := port.EdgeSeq
		if m.Parent == "" {
			ek = "" // 结构根无父边（D120§6.1）
		} else if _, revised := c.RevisedFrom[id]; revised {
			ek = port.EdgeRevise
		}
		nodes = append(nodes, port.GraphNode{
			ID:           id,
			Parent:       m.Parent,
			Role:         m.Role,
			Snippet:      nodeSummary(m),
			CreatedAt:    m.CreatedAt,
			EdgeKind:     ek,
			SiblingIdx:   sibIdx[id],
			SiblingCount: sibN[id],
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if !nodes[i].CreatedAt.Equal(nodes[j].CreatedAt) {
			return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
		}
		return nodes[i].ID < nodes[j].ID
	})
	snap.graph = port.TreeGraph{Anchor: c.Head, Nodes: nodes, Total: len(nodes)}
	var tailOf func(conversation.MessageID) conversation.MessageID
	tailOf = func(x conversation.MessageID) conversation.MessageID {
		if t, ok := snap.tails[x]; ok {
			return t
		}
		best, bestAt := conversation.MessageID(""), time.Time{}
		kids := c.Children[x]
		if len(kids) == 0 {
			best, bestAt = x, c.Nodes[x].CreatedAt
		}
		for _, kid := range kids {
			t := tailOf(kid)
			n := c.Nodes[t]
			if best == "" || n.CreatedAt.After(bestAt) || (n.CreatedAt.Equal(bestAt) && t > best) {
				best, bestAt = t, n.CreatedAt
			}
		}
		snap.tails[x] = best
		return best
	}
	tailOf(conversation.MessageID(c.ID))
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
