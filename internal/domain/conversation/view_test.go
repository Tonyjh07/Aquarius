package conversation

import (
	"sort"
	"testing"
	"time"
)

// buildTree 造一棵有分叉的树：Root → u1 → a1 → u2；u1 → a2（同级分叉）。
func buildTree(t *testing.T) *Conversation {
	t.Helper()
	c := New("conv-view", "视图测试")
	u1, err := c.Append(RoleUser, []Part{{Kind: PartText, Text: "问一"}})
	if err != nil {
		t.Fatalf("append u1: %v", err)
	}
	a1, err := c.Append(RoleAssistant, []Part{{Kind: PartText, Text: "答一"}})
	if err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if err := c.Checkout(u1.ID); err != nil {
		t.Fatalf("checkout u1: %v", err)
	}
	a2, err := c.Append(RoleAssistant, []Part{{Kind: PartText, Text: "答二"}})
	if err != nil {
		t.Fatalf("append a2: %v", err)
	}
	if err := c.Checkout(a1.ID); err != nil {
		t.Fatalf("checkout a1: %v", err)
	}
	if _, err := c.Append(RoleUser, []Part{{Kind: PartText, Text: "问二"}}); err != nil {
		t.Fatalf("append u2: %v", err)
	}
	_ = a2
	return c
}

// TestChildIDsReturnsCopy ChildIDs 的返回值是副本：调用方改写它不得影响树。
//
// 这条不是洁癖：展示面拿到切片后排序/截断是常见动作，若共享底层数组，
// 「画一次树」就会把结构顺序改掉。
func TestChildIDsReturnsCopy(t *testing.T) {
	c := buildTree(t)
	root := MessageID(c.ID)
	kids := c.ChildIDs(root)
	if len(kids) != 1 {
		t.Fatalf("根的孩子 = %v, want 1 个（顶层消息）", kids)
	}
	kids[0] = "篡改"
	again := c.ChildIDs(root)
	if again[0] == "篡改" {
		t.Fatal("ChildIDs 返回的切片与树共享底层数组（改写泄漏回树）")
	}
	if got := c.ChildIDs("不存在的节点"); got != nil {
		t.Fatalf("未知节点应返回 nil, got %v", got)
	}
}

// TestNodeCountAndIDs NodeCount 与 IDs 反映同一节点集，IDs 有序且长度一致。
func TestNodeCountAndIDs(t *testing.T) {
	c := buildTree(t)
	ids := c.IDs()
	if len(ids) != c.NodeCount() {
		t.Fatalf("IDs %d 条 ≠ NodeCount %d", len(ids), c.NodeCount())
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("IDs 未按升序：%s 紧跟 %s", ids[i-1], ids[i])
		}
	}
	for _, id := range ids {
		if _, ok := c.Find(id); !ok {
			t.Fatalf("IDs 给出了树中不存在的节点 %q", id)
		}
	}
}

// TestSubtreeSizeMatchesPrune SubtreeSize 的口径必须与 Prune 的实际删除量一致——
// 它俩是同一个数字的两种取法（/rm 的确认提示与实际结果），不一致就是骗用户。
func TestSubtreeSizeMatchesPrune(t *testing.T) {
	c := buildTree(t)
	root := MessageID(c.ID)
	if got := c.SubtreeSize(root); got != c.NodeCount() {
		t.Fatalf("根的子树大小 = %d, want 全部节点 %d", got, c.NodeCount())
	}
	u1 := c.ChildIDs(root)[0]

	// 深拷贝一份同名树，对比「预言值」与 Prune 后的实际减少量。
	predicted := c.SubtreeSize(u1)
	before := c.NodeCount()
	if err := c.Prune(u1); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed := before - c.NodeCount(); removed != predicted {
		t.Fatalf("SubtreeSize 预报 %d 条，Prune 实际删了 %d 条", predicted, removed)
	}
	if got := c.SubtreeSize(u1); got != 0 {
		t.Fatalf("已剪除节点的子树大小 = %d, want 0", got)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("剪除后树不合法: %v", err)
	}
}

// TestEditionFollowsVersionChain Edition 是 RevisedFrom 的唯一正向查询口：
// 新版本 → 旧版本；未修订过的节点无上游。
func TestEditionFollowsVersionChain(t *testing.T) {
	c := buildTree(t)
	root := MessageID(c.ID)
	u1 := c.ChildIDs(root)[0]
	old, ok := c.Find(u1)
	if !ok {
		t.Fatal("找不到 u1")
	}
	if _, ok := c.Edition(u1); ok {
		t.Fatal("未修订节点不该有版本上游")
	}
	revised, err := c.Revise(u1, []Part{{Kind: PartText, Text: "问一（改）"}}, Fresh)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	got, ok := c.Edition(revised.ID)
	if !ok || got != old.ID {
		t.Fatalf("Edition(%s) = (%q, %v), want (%q, true)", revised.ID, got, ok, old.ID)
	}
}

// TestTailMatchesSubtreeNewestLeaf Tail 的返回值必须就是该子树里 CreatedAt 最新的叶子
// （平局取 ID 序最大）——展示面的「切到这个版本」靠它落点。
func TestTailMatchesSubtreeNewestLeaf(t *testing.T) {
	c := buildTree(t)
	root := MessageID(c.ID)
	leaf := newestLeafForTest(t, c, root)
	got, ok := c.Tail(root)
	if !ok || got != leaf {
		t.Fatalf("Tail(根) = (%q, %v), want (%q, true)", got, ok, leaf)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("树不合法: %v", err)
	}
}

// newestLeafForTest 独立于 Tail 实现，用「先收集全部叶子、再逐个比较」的最直白写法给出// 期望值——与被测实现不共用遍历逻辑，否则错了也一起错。
func newestLeafForTest(t *testing.T, c *Conversation, root MessageID) MessageID {
	t.Helper()
	var leaves []MessageID
	var walk func(MessageID)
	walk = func(id MessageID) {
		kids := c.ChildIDs(id)
		if len(kids) == 0 {
			leaves = append(leaves, id)
			return
		}
		for _, k := range kids {
			walk(k)
		}
	}
	walk(root)
	best := MessageID("")
	var bestAt time.Time
	for _, id := range leaves {
		m, ok := c.Find(id)
		if !ok {
			t.Fatalf("叶子 %q 不在树中", id)
		}
		if best == "" || m.CreatedAt.After(bestAt) || (m.CreatedAt.Equal(bestAt) && id > best) {
			best, bestAt = id, m.CreatedAt
		}
	}
	return best
}

// TestViewMatchesTailAndBranches View 是展示面唯一的数据来源，它给的口径必须与
// Tail / Branches 逐节点一致——否则「切到某个版本的末端」与界面画出的分叉会各说各话。
func TestViewMatchesTailAndBranches(t *testing.T) {
	c := buildTree(t)
	view := c.View()

	if view.Head != c.Head {
		t.Fatalf("View.Head = %s, want %s", view.Head, c.Head)
	}
	if len(view.Nodes) != c.NodeCount() {
		t.Fatalf("View.Nodes %d 条, want %d", len(view.Nodes), c.NodeCount())
	}
	for i := 1; i < len(view.Nodes); i++ {
		prev, cur := view.Nodes[i-1], view.Nodes[i]
		if cur.CreatedAt.Before(prev.CreatedAt) || (cur.CreatedAt.Equal(prev.CreatedAt) && cur.ID < prev.ID) {
			t.Fatalf("View.Nodes 未按 (CreatedAt, ID) 升序：%s 紧跟 %s", prev.ID, cur.ID)
		}
	}
	for _, m := range view.Nodes {
		want, ok := c.Tail(m.ID)
		if !ok {
			t.Fatalf("Tail(%s) 不存在，但 View 列了它", m.ID)
		}
		if got := view.Tails[m.ID]; got != want {
			t.Fatalf("View.Tails[%s] = %s, want %s（与 Tail 同口径）", m.ID, got, want)
		}
	}
	// 版本组 = 每个节点归到「最近的非衬垫祖先」名下，组内按 (CreatedAt, ID) 升序；
	// 结构根挂在 "" 组下（D19）。这棵树没有衬垫，故版本组与「同父孩子」一致。
	if len(view.VersionGroups) == 0 || view.VersionGroups[0].Parent != "" {
		t.Fatalf("版本组首项应为结构根挂点，got %+v", view.VersionGroups)
	}
	if ids := view.VersionGroups[0].IDs; len(ids) != 1 || ids[0] != MessageID(c.ID) {
		t.Fatalf(`版本组 "" = %v, want 只有 Root %q（D19 唯一实根）`, ids, c.ID)
	}
	total := 0
	for _, group := range view.VersionGroups {
		if group.Parent == "" {
			total += len(group.IDs)
			continue
		}
		want := c.ChildIDs(group.Parent)
		if len(want) != len(group.IDs) {
			t.Fatalf("父 %q 的版本组 %d 个成员, 孩子给出 %d 个", group.Parent, len(group.IDs), len(want))
		}
		sorted := make([]Message, 0, len(want))
		for _, id := range want {
			m, ok := c.Find(id)
			if !ok {
				t.Fatalf("ChildIDs(%q) 给出树外节点 %q", group.Parent, id)
			}
			sorted = append(sorted, m)
		}
		sort.Slice(sorted, func(i, j int) bool { return nodeLess(sorted[i], sorted[j]) })
		for i, m := range sorted {
			if group.IDs[i] != m.ID {
				t.Fatalf("父 %q 的版本序第 %d 位 = %s, want %s（(CreatedAt, ID) 升序）",
					group.Parent, i, group.IDs[i], m.ID)
			}
		}
		total += len(group.IDs)
	}
	// 每个节点恰好出现在一个版本组里（成员数之和 = 节点数：Root 算在 "" 组）。
	if total != c.NodeCount() {
		t.Fatalf("版本组成员总数 = %d, want 节点数 %d", total, c.NodeCount())
	}
}

// TestViewVersionGroupsCrossPads 衬垫透明（D152）：在 a1 之前插一条摘要之后，a1 的结构父
// 变成衬垫，但它与 a2 仍是同一个问题的两个版本——版本组必须跨过衬垫把它们放在一起，
// 否则一次「插入摘要」就会让两个版本各自 1/1，图上分叉编号与左右切换一起消失。
func TestViewVersionGroupsCrossPads(t *testing.T) {
	useFixedClock(t)
	c := buildTree(t) // u1 → a1 → u2；u1 → a2
	u1 := c.ChildIDs(MessageID(c.ID))[0]
	a1 := c.ChildIDs(u1)[0]

	pad, err := c.InsertBefore(a1, RoleSystem, textParts("摘要"))
	if err != nil {
		t.Fatalf("insert pad: %v", err)
	}
	if !c.padNode(pad.ID) {
		t.Fatal("插入的摘要节点应被认作衬垫")
	}
	if c.padNode(a1) || c.padNode(u1) {
		t.Fatal("普通节点不该被认作衬垫")
	}

	// 版本组：a1 与 a2 同组（跨过衬垫），衬垫自己不占槽位。
	group := c.versionGroup(u1)
	if len(group) != 2 || group[0] != a1 || group[1] != c.ChildIDs(u1)[1] {
		t.Fatalf("版本组(u1) = %v, want [a1 a2]（衬垫透明）", group)
	}
	if br := c.Branches(a1); len(br) != 1 {
		t.Fatalf("Branches(a1) = %v, want 一个版本（a2）", ids(br))
	}
	if br := c.Branches(pad.ID); br != nil {
		t.Fatalf("衬垫不属于任何版本组，Branches 应为 nil, got %v", ids(br))
	}

	// View 的分组表同样：u1 组两名成员，衬垫不出现在任何组里。
	v := c.View()
	seenPad := false
	for _, g := range v.VersionGroups {
		for _, id := range g.IDs {
			if id == pad.ID {
				seenPad = true
			}
		}
		if g.Parent != u1 {
			continue
		}
		if len(g.IDs) != 2 || g.IDs[0] != a1 {
			t.Fatalf("View 版本组(u1) = %v, want [a1 a2]", g.IDs)
		}
	}
	if seenPad {
		t.Fatal("衬垫不该出现在任何版本组里")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestViewVersionGroupsKeepAppendedWatermark 顺着链路追加的压缩摘要不是衬垫：它的孩子晚于
// 它，不能把后续内容提升到祖父那一层（那会把「继续对话」错当成「另一个版本」）。
func TestViewVersionGroupsKeepAppendedWatermark(t *testing.T) {
	useFixedClock(t)
	c := New("conv-wm", "水位")
	u1, _ := c.Append(RoleUser, textParts("问"))
	a1, _ := c.Append(RoleAssistant, textParts("答"))
	if err := c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: a1.ID, Role: RoleSystem,
		Content: textParts("摘要"), CreatedAt: nowFunc(),
	}); err != nil {
		t.Fatalf("append summary: %v", err)
	}
	if c.padNode(c.Head) {
		t.Fatal("顺着链路追加的水位不是衬垫")
	}
	// 同一个问句下的另一个回答：版本组仍是 a1 与它，摘要不算版本。
	if err := c.Checkout(u1.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	a2, _ := c.Append(RoleAssistant, textParts("另一个答"))
	if br := c.Branches(a1.ID); len(br) != 1 || br[0].ID != a2.ID {
		t.Fatalf("Branches(a1) = %v, want 只有另一个回答（摘要不入组）", ids(br))
	}
}
