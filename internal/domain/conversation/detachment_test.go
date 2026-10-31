package conversation

import (
	"errors"
	"reflect"
	"testing"
)

// TestReviseCarryRecordsDetachment Carry 整批转移必须留下且只留下一条路牌（D256）：
// 锚点是旧版本、子树根是整批孩子、新位置是新版本；Fresh 什么都不记（旧子树没动）。
func TestReviseCarryRecordsDetachment(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	if err := c.Checkout(u1.ID); err != nil { // 回到 q1 再挂一支，凑成两个同级孩子
		t.Fatalf("checkout: %v", err)
	}
	a1b := mustAppend(t, c, RoleAssistant, "a1b")

	if _, err := c.Revise(u1.ID, textParts("q1'"), Fresh); err != nil {
		t.Fatalf("revise fresh: %v", err)
	}
	if got := c.Detachments(); len(got) != 0 {
		t.Fatalf("fresh revise recorded %d detachments, want 0", len(got))
	}

	m, err := c.Revise(u1.ID, textParts("q1''"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	got := c.Detachments()
	if len(got) != 1 {
		t.Fatalf("detachments = %d, want 1", len(got))
	}
	d := got[0]
	if d.Anchor != u1.ID || d.NewHome != m.ID || d.Kind != DetachTransfer {
		t.Fatalf("detachment = %+v, want anchor %s -> new home %s (transfer)", d, u1.ID, m.ID)
	}
	if !reflect.DeepEqual(d.Roots, []MessageID{a1.ID, a1b.ID}) {
		t.Fatalf("roots = %v, want [%s %s]（整批且保持兄弟顺序）", d.Roots, a1.ID, a1b.ID)
	}
	if d.Stale {
		t.Fatalf("freshly recorded detachment marked stale")
	}
	if d.At.IsZero() {
		t.Fatalf("detachment has zero timestamp")
	}
}

// TestDetachmentsStaleIsComputed 陈旧是现算的：把被记的子树根又搬走，记录仍在但标 Stale；
// 搬回去又成立。记录本身不删——另一次搬运可能让它重新生效。
func TestDetachmentsStaleIsComputed(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	m, err := c.Revise(u1.ID, textParts("q1'"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	// 模拟「后来又被搬走」：a1 从新版本挪到 Root 下（改父尚未落地，直接改结构）。
	c.children[m.ID] = nil
	c.children[MessageID(c.ID)] = append(c.children[MessageID(c.ID)], a1.ID)
	am := c.nodes[a1.ID]
	am.Parent = MessageID(c.ID)
	c.nodes[a1.ID] = am
	if err := c.Validate(); err != nil {
		t.Fatalf("validate after move: %v", err)
	}
	got := c.Detachments()
	if len(got) != 1 || !got[0].Stale {
		t.Fatalf("detachments = %+v, want one stale record", got)
	}

	// 搬回去：同一条记录重新成立，Stale 归 false。
	c.children[MessageID(c.ID)] = nil
	c.children[m.ID] = append(c.children[m.ID], a1.ID)
	am = c.nodes[a1.ID]
	am.Parent = m.ID
	c.nodes[a1.ID] = am
	got = c.Detachments()
	if len(got) != 1 || got[0].Stale {
		t.Fatalf("detachments = %+v, want one live record", got)
	}
}

// TestUndoLastDetachmentReversesCarry 撤销不分种类（D256）：转移留下的路牌也能反做——
// 整批孩子挂回旧版本，记录被消费，且撤销本身不再写新记录。
func TestUndoLastDetachmentReversesCarry(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	m, err := c.Revise(u1.ID, textParts("q1'"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	if c.nodes[a1.ID].Parent != m.ID {
		t.Fatalf("fixture: a1 should hang under %s", m.ID)
	}

	d, err := c.UndoLastDetachment()
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if d.Kind != DetachTransfer || len(d.Roots) != 1 || d.Roots[0] != a1.ID {
		t.Fatalf("undone record = %+v, want transfer with root %s", d, a1.ID)
	}
	if c.nodes[a1.ID].Parent != u1.ID {
		t.Fatalf("a1.parent after undo = %q, want %q", c.nodes[a1.ID].Parent, u1.ID)
	}
	if n := len(c.Detachments()); n != 0 {
		t.Fatalf("detachments after undo = %d, want 0", n)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestUndoRefusesStaleRecord 记录陈旧时拒绝撤销：之后还有别的搬运，盲目往回挪会把树
// 搬到意料之外；明确报错比猜一个位置好。
func TestUndoRefusesStaleRecord(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	m, err := c.Revise(u1.ID, textParts("q1'"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	// 模拟「之后又被搬走」：a1 离开新版本，记录随之陈旧。
	c.children[m.ID] = nil
	c.children[MessageID(c.ID)] = append(c.children[MessageID(c.ID)], a1.ID)
	am := c.nodes[a1.ID]
	am.Parent = MessageID(c.ID)
	c.nodes[a1.ID] = am

	if _, err := c.UndoLastDetachment(); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("undo stale = %v, want ErrInvalidNode", err)
	}
	if n := len(c.Detachments()); n != 1 {
		t.Fatalf("stale record was dropped: %d", n)
	}
	// 树没被动过。
	if c.nodes[a1.ID].Parent != MessageID(c.ID) {
		t.Fatalf("stale undo moved the node to %q", c.nodes[a1.ID].Parent)
	}
}

// TestPruneDropsDetachments 剪掉任一端就清掉整条记录：留着半条会让撤销去挪不存在的节点。
func TestPruneDropsDetachments(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	u2 := mustAppend(t, c, RoleUser, "q2")
	a2 := mustAppend(t, c, RoleAssistant, "a2")
	if _, err := c.Revise(u1.ID, textParts("q1'"), Carry); err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	if got := len(c.Detachments()); got != 1 {
		t.Fatalf("detachments = %d, want 1", got)
	}
	// 剪掉与记录无关的一支：记录保留。
	if err := c.Prune(a2.ID); err != nil {
		t.Fatalf("prune unrelated: %v", err)
	}
	if got := len(c.Detachments()); got != 1 {
		t.Fatalf("after unrelated prune detachments = %d, want 1", got)
	}
	// 剪掉被搬的子树根：记录随之清掉。
	if err := c.Prune(a1.ID); err != nil {
		t.Fatalf("prune root: %v", err)
	}
	if got := len(c.Detachments()); got != 0 {
		t.Fatalf("after pruning a moved root detachments = %d, want 0", got)
	}
	_ = u2
}

// TestSnapshotRoundTripPreservesDetachments 路牌走 Snapshot/Restore 原样往返：
// 顺序、子树根列表、种类与时刻都不丢，Stale 依然由结构现算。
func TestSnapshotRoundTripPreservesDetachments(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	mustAppend(t, c, RoleAssistant, "a1")
	if _, err := c.Revise(u1.ID, textParts("q1'"), Carry); err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	restored, err := Restore(c.Snapshot())
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	want, got := c.Detachments(), restored.Detachments()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("detachments after round trip = %+v, want %+v", got, want)
	}
}

// TestValidateRejectsDetachmentOutsideTree 路牌指向树外节点属于坏数据（磁盘不可信）：
// 三端任一不在树就报错，而不是让它进内存后半死不活。
func TestValidateRejectsDetachmentOutsideTree(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	mustAppend(t, c, RoleAssistant, "a1")
	if _, err := c.Revise(u1.ID, textParts("q1'"), Carry); err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	c.detached[0].NewHome = "GHOST"
	err := c.Validate()
	if err == nil {
		t.Fatalf("validate accepted detachment pointing outside tree")
	}
}
