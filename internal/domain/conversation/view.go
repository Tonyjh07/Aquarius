package conversation

import "sort"

// 本文件是聚合根的**只读视图 API**：把外部（应用层、展示面）需要的查询收成方法，
// 而不是让它们直接读 Nodes/Children/RevisedFrom 三张表。
//
// 这些访问器存在的那一天，三张表才可能私有化——先有出口，再关门（否则应用层无处可去）。
// 返回的切片一律是副本：调用方改写它不会影响树。

// NodeCount 树中节点总数（/rm 统计删除量、快照容量估算用）。
func (c *Conversation) NodeCount() int { return len(c.Nodes) }

// ChildIDs 返回 id 的直接孩子，按结构插入序（Root 挂点用 "" 查询）。
// 返回切片是副本；id 不在树中或无孩子时返回 nil。
//
// 注意：插入序**不是**展示序——Revise 出来的兄弟节点 CreatedAt 不保证等于插入序，
// 要展示请用 Branches（已按 (CreatedAt, ID) 排序）。
func (c *Conversation) ChildIDs(id MessageID) []MessageID {
	kids := c.Children[id]
	if len(kids) == 0 {
		return nil
	}
	return append([]MessageID(nil), kids...)
}

// IDs 全部节点 ID，按 ID 升序（确定性；供需要枚举节点的命令面与视图构建用）。
func (c *Conversation) IDs() []MessageID {
	out := make([]MessageID, 0, len(c.Nodes))
	for id := range c.Nodes {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SubtreeSize 统计 id 及其整棵子树的节点数（/rm 确认提示用）；id 不在树中返回 0。
// 与 Prune 的删除范围同口径（D95 后无跨节点引用，剪除范围即子树本身）。
func (c *Conversation) SubtreeSize(id MessageID) int {
	if _, ok := c.Nodes[id]; !ok {
		return 0
	}
	seen := map[MessageID]bool{}
	var walk func(MessageID)
	walk = func(x MessageID) {
		if seen[x] {
			return
		}
		seen[x] = true
		for _, ch := range c.Children[x] {
			walk(ch)
		}
	}
	walk(id)
	return len(seen)
}

// Edition 返回 id 的版本链上游（被它修订掉的那个节点）；非修订节点返回 ("", false)。
// 这是 RevisedFrom 的唯一正向查询口（"这个节点是谁的新版本"）。
func (c *Conversation) Edition(id MessageID) (MessageID, bool) {
	old, ok := c.RevisedFrom[id]
	return old, ok
}
