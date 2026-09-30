package app

import (
	"sort"

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
}

// makeTreeSnapshot 由会话树构建快照：每个节点一条 BranchInfo，同级按 (CreatedAt, ID)
// 升序——与 domain `Conversation.Branches` 的排序口径一致（D15/D16：Revise 出来的
// 兄弟节点 CreatedAt 不保证等于插入序，故必须显式排序而非依赖 Children 切片顺序）。
func makeTreeSnapshot(c *conversation.Conversation) *treeSnapshot {
	snap := &treeSnapshot{branches: make(map[conversation.MessageID]port.BranchInfo, len(c.Nodes))}
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
		}
	}
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
