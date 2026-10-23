package conversation

import "sort"

// 本文件是聚合根的**只读视图 API**：把外部（应用层、展示面）需要的查询收成方法，
// 而不是让它们直接读 Nodes/Children/RevisedFrom 三张表。
//
// 这些访问器存在的那一天，三张表才可能私有化——先有出口，再关门（否则应用层无处可去）。
// 返回的切片一律是副本：调用方改写它不会影响树。

// NodeCount 树中节点总数（/rm 统计删除量、快照容量估算用）。
func (c *Conversation) NodeCount() int { return len(c.nodes) }

// ChildIDs 返回 id 的直接孩子，按结构插入序（Root 挂点用 "" 查询）。
// 返回切片是副本；id 不在树中或无孩子时返回 nil。
//
// 注意：插入序**不是**展示序——Revise 出来的兄弟节点 CreatedAt 不保证等于插入序，
// 要展示请用 Branches（已按 (CreatedAt, ID) 排序）。
func (c *Conversation) ChildIDs(id MessageID) []MessageID {
	kids := c.children[id]
	if len(kids) == 0 {
		return nil
	}
	return append([]MessageID(nil), kids...)
}

// IDs 全部节点 ID，按 ID 升序（确定性；供需要枚举节点的命令面与视图构建用）。
func (c *Conversation) IDs() []MessageID {
	out := make([]MessageID, 0, len(c.nodes))
	for id := range c.nodes {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SubtreeSize 统计 id 及其整棵子树的节点数（/rm 确认提示用）；id 不在树中返回 0。
// 与 Prune 的删除范围同口径（D95 后无跨节点引用，剪除范围即子树本身）。
func (c *Conversation) SubtreeSize(id MessageID) int {
	if _, ok := c.nodes[id]; !ok {
		return 0
	}
	seen := map[MessageID]bool{}
	var walk func(MessageID)
	walk = func(x MessageID) {
		if seen[x] {
			return
		}
		seen[x] = true
		for _, ch := range c.children[x] {
			walk(ch)
		}
	}
	walk(id)
	return len(seen)
}

// Edition 返回 id 的版本链上游（被它修订掉的那个节点）；非修订节点返回 ("", false)。
// 这是 RevisedFrom 的唯一正向查询口（"这个节点是谁的新版本"）。
func (c *Conversation) Edition(id MessageID) (MessageID, bool) {
	old, ok := c.revisedFrom[id]
	return old, ok
}

// View 整树只读视图：一次构建、发布后不再改写（调用方只读，不得改写其中切片）。
//
// 为什么由领域提供：同级排序口径（(CreatedAt, ID) 升序——Revise 出来的兄弟节点
// CreatedAt 不保证等于插入序，D15/D16）与「末端」口径（D94：子树里最新的叶子，分支切换
// 落点）都是领域规则。此前 app 侧把两条各写了一遍，规则一改就要改两处，还很难保证一致。
type View struct {
	// Head 当前游标（快照需要它做图谱锚点）。
	Head MessageID
	// Nodes 全部节点，按 (CreatedAt, ID) 升序——布局可复现，不依赖 map 遍历顺序。
	Nodes []Message
	// Groups 每个有孩子的父节点一组（父 "" 那组挂的是 Root，D19），按父 ID 升序。
	Groups []SiblingGroup
	// Tails 每个节点 → 其版本子树的对话末端（D94），口径与 Tail 相同。
	Tails map[MessageID]MessageID
}

// SiblingGroup 一个父节点下的孩子集合：IDs 按 (CreatedAt, ID) 升序，下标即分叉序号。
type SiblingGroup struct {
	Parent MessageID
	IDs    []MessageID
}

// View 构建整树只读视图。排序与末端聚合各做一次，展示面直接用结果。
func (c *Conversation) View() View {
	v := View{
		Head:   c.Head,
		Nodes:  make([]Message, 0, len(c.nodes)),
		Groups: make([]SiblingGroup, 0, len(c.children)),
		Tails:  make(map[MessageID]MessageID, len(c.nodes)),
	}
	for _, m := range c.nodes {
		v.Nodes = append(v.Nodes, m)
	}
	sort.Slice(v.Nodes, func(i, j int) bool {
		return nodeLess(v.Nodes[i], v.Nodes[j])
	})

	for parent, kids := range c.children {
		ids := make([]MessageID, 0, len(kids))
		for _, kid := range kids {
			if _, ok := c.nodes[kid]; ok { // 悬空边不该存在（Validate 会报），视图只保证自洽
				ids = append(ids, kid)
			}
		}
		sort.Slice(ids, func(i, j int) bool {
			a, okA := c.nodes[ids[i]]
			b, okB := c.nodes[ids[j]]
			if !okA || !okB {
				return ids[i] < ids[j]
			}
			return nodeLess(a, b)
		})
		v.Groups = append(v.Groups, SiblingGroup{Parent: parent, IDs: ids})
	}
	sort.Slice(v.Groups, func(i, j int) bool { return v.Groups[i].Parent < v.Groups[j].Parent })

	// 末端：后序遍历，叶子取自身，父取孩子中最新的末端；平局取 ID 序最大（与 Tail 一致）。
	var walk func(MessageID) MessageID
	walk = func(id MessageID) MessageID {
		kids := c.children[id]
		if len(kids) == 0 {
			v.Tails[id] = id
			return id
		}
		best := MessageID("")
		for _, kid := range kids {
			t := walk(kid)
			if best == "" {
				best = t
				continue
			}
			if nodeLess(c.nodes[best], c.nodes[t]) {
				best = t
			}
		}
		v.Tails[id] = best
		return best
	}
	walk(MessageID(c.ID))
	return v
}

// nodeLess 统一排序口径：(CreatedAt, ID) 升序。同级排序与整树排序共用这一处，
// 避免「分叉编号」与「节点表」用两种顺序（D15/D16）。
func nodeLess(a, b Message) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}
