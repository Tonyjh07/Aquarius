package conversation

import (
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

// newestLeafForTest 独立于 Tail 实现，用「先收集全部叶子、再逐个比较」的最直白写法给出
// 期望值——与被测实现不共用遍历逻辑，否则错了也一起错。
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
