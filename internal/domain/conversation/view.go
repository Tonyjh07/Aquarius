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

// DroppedEditions 统计删掉 id 这棵子树会顺带清掉多少条溯源记录：版本链指向子树内旧节点、
// 而新版本在子树外——剪除后那条边成了悬空边，Prune 会把它一并删除。于是**新版本会失去
// 「修订自谁」的记忆**（图上版本虚线退回顺接实线，`/branch` 也不再标修订来源）。
// 调用方（/rm 的确认句）据此提前说明，不让人删完才发现。
func (c *Conversation) DroppedEditions(id MessageID) int {
	if _, ok := c.nodes[id]; !ok {
		return 0
	}
	sub := map[MessageID]bool{}
	c.collectSubtree(id, sub)
	n := 0
	for newID, oldID := range c.revisedFrom {
		if sub[oldID] && !sub[newID] {
			n++
		}
	}
	return n
}

// Edition 返回 id 的版本链上游（被它修订掉的那个节点）；非修订节点返回 ("", false)。
// 这是 RevisedFrom 的唯一正向查询口（"这个节点是谁的新版本"）。
func (c *Conversation) Edition(id MessageID) (MessageID, bool) {
	old, ok := c.revisedFrom[id]
	return old, ok
}

// padNode 报告 id 是否为「衬垫」（D152）：为把摘要或注入上下文顶进路径中间而造的节点，
// 本身不是任何发言的版本分支。判据不落库、全从结构读：
//   - 角色 system|inject——衬垫承载的是上下文与水位，不是伪造的发言；
//   - 恰好一个孩子——衬垫占据目标原来的位置，目标改挂到它下面；
//   - 孩子比它自己更早——InsertBefore 顶开的是一个**已在树中**的节点，故目标先于衬垫
//     存在；顺着链路追加的水位（压缩摘要）孩子恒晚于它，两者靠这一点区分；
//   - 父不是结构根——会话首节点（人格快照）是真正的一层，不能被跨过。
//
// 展示面（版本组）据它把衬垫当透明：版本组跨过衬垫，它的孩子提升到祖父那一层。
func (c *Conversation) padNode(id MessageID) bool {
	m, ok := c.nodes[id]
	if !ok {
		return false
	}
	if m.Role != RoleSystem && m.Role != RoleInject {
		return false
	}
	if m.Parent == "" || m.Parent == MessageID(c.ID) {
		return false
	}
	kids := c.children[id]
	if len(kids) != 1 {
		return false
	}
	kid, ok := c.nodes[kids[0]]
	if !ok {
		return false
	}
	return kid.CreatedAt.Before(m.CreatedAt)
}

// versionOwner 返回 id 所在版本组的分组键：沿父链上溯跳过衬垫，第一个不是衬垫的祖先
// 即键（"" = 根挂点，即顶层消息组）。衬垫自己不属于任何版本组，调用方应先判 padNode。
func (c *Conversation) versionOwner(id MessageID) MessageID {
	m, ok := c.nodes[id]
	if !ok {
		return ""
	}
	p := m.Parent
	for p != "" && c.padNode(p) {
		p = c.nodes[p].Parent
	}
	return p
}

// versionGroup 返回分组键 owner 名下的版本组成员（衬垫的孩子提升到这一层），
// 按 (CreatedAt, ID) 升序。
func (c *Conversation) versionGroup(owner MessageID) []MessageID {
	var out []MessageID
	var walk func(MessageID)
	walk = func(p MessageID) {
		for _, kid := range c.children[p] {
			if c.padNode(kid) {
				walk(kid) // 衬垫透明：它的孩子算这一层的版本
				continue
			}
			out = append(out, kid)
		}
	}
	walk(owner)
	sort.Slice(out, func(i, j int) bool {
		a, okA := c.nodes[out[i]]
		b, okB := c.nodes[out[j]]
		if !okA || !okB {
			return out[i] < out[j]
		}
		return nodeLess(a, b)
	})
	return out
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
	// VersionGroups 每个版本组一组（键 "" 那组挂的是 Root，D19），按分组键升序。
	// 版本组不是「同一个父的孩子」：衬垫（padNode）被跨过，它的孩子提升到祖父那一层
	// ——否则插一条摘要就会把原来同父的两个回答拆到两个组里，分叉编号与切换按钮一起消失。
	VersionGroups []SiblingGroup
	// Tails 每个节点 → 其版本子树的对话末端（D94），口径与 Tail 相同。
	Tails map[MessageID]MessageID
}

// SiblingGroup 一个版本组的成员：IDs 按 (CreatedAt, ID) 升序，下标即版本序号。
// Parent = 分组键（最近的非衬垫祖先），不是「结构上的父」——衬垫之后的原节点与它的
// 兄弟仍在同一组，尽管它们的 Parent 已经不是同一个节点。
type SiblingGroup struct {
	Parent MessageID
	IDs    []MessageID
}

// View 构建整树只读视图。排序与末端聚合各做一次，展示面直接用结果。
func (c *Conversation) View() View {
	v := View{
		Head:          c.Head,
		Nodes:         make([]Message, 0, len(c.nodes)),
		VersionGroups: make([]SiblingGroup, 0, len(c.nodes)),
		Tails:         make(map[MessageID]MessageID, len(c.nodes)),
	}
	for _, m := range c.nodes {
		v.Nodes = append(v.Nodes, m)
	}
	sort.Slice(v.Nodes, func(i, j int) bool {
		return nodeLess(v.Nodes[i], v.Nodes[j])
	})

	// 版本组：按分组键（最近的、本身不是衬垫的祖先）聚合。衬垫不进任何组——它只是把
	// 上下文顶进路径中间，不是发言的版本；它的孩子由 versionGroup 提升到祖父那一层。
	grouped := make(map[MessageID][]MessageID, len(c.nodes))
	for id := range c.nodes {
		if id == MessageID(c.ID) || c.padNode(id) {
			continue
		}
		owner := c.versionOwner(id)
		grouped[owner] = append(grouped[owner], id)
	}
	owners := make([]MessageID, 0, len(grouped))
	for owner := range grouped {
		owners = append(owners, owner)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	for _, owner := range owners {
		ids := c.versionGroup(owner)
		if len(ids) == 0 {
			continue
		}
		v.VersionGroups = append(v.VersionGroups, SiblingGroup{Parent: owner, IDs: ids})
	}
	// 结构根自成一组挂在 "" 下（D19 唯一实根），与顶层消息组（键 = Root ID）分开。
	v.VersionGroups = append(v.VersionGroups, SiblingGroup{Parent: "", IDs: []MessageID{MessageID(c.ID)}})
	sort.SliceStable(v.VersionGroups, func(i, j int) bool { return v.VersionGroups[i].Parent < v.VersionGroups[j].Parent })

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
