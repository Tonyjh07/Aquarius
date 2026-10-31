package conversation

import (
	"errors"
	"testing"
)

// personaThenQuestion 造一棵带人格的会话：Root → persona(system) → q1 → a1。
// 人格是 Root 的首孩子，其余节点都在它下面——这正是环拦截让人格搬不动的前提。
func personaThenQuestion(t *testing.T) (c *Conversation, persona, q1, a1 Message) {
	t.Helper()
	c = New(NewID(), "t")
	if err := c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: c.Head, Role: RoleSystem,
		Content: textParts("你是 Aquarius。"), CreatedAt: nowFunc(),
	}); err != nil {
		t.Fatalf("append persona: %v", err)
	}
	persona = c.nodes[c.Head]
	q1 = mustAppend(t, c, RoleUser, "q1")
	a1 = mustAppend(t, c, RoleAssistant, "a1")
	return c, persona, q1, a1
}

// TestReparentMovesSubtreeAndKeepsHead 改父搬整棵子树：父指针改掉、旧父孩子表摘干净、
// 新父末尾多一项；Head 在被搬子树内随行（值不变），孙代字节级不动。
func TestReparentMovesSubtreeAndKeepsHead(t *testing.T) {
	useFixedClock(t)
	c, persona, q1, a1 := personaThenQuestion(t)
	q2 := mustAppend(t, c, RoleUser, "q2") // Head 在 q2，位于 a1 的子树内
	deep := mustAppend(t, c, RoleAssistant, "a2")

	deepBefore, _ := c.Find(deep.ID)
	res, err := c.Reparent(a1.ID, persona.ID)
	if err != nil {
		t.Fatalf("reparent: %v", err)
	}
	if res.Moved != 3 { // a1 + q2 + deep
		t.Fatalf("moved = %d, want 3", res.Moved)
	}
	got, _ := c.Find(a1.ID)
	if got.Parent != persona.ID {
		t.Fatalf("a1.parent = %q, want %s", got.Parent, persona.ID)
	}
	if kids := c.ChildIDs(q1.ID); len(kids) != 0 {
		t.Fatalf("old parent keeps children: %v", kids)
	}
	if kids := c.ChildIDs(persona.ID); len(kids) != 2 || kids[0] != q1.ID || kids[1] != a1.ID {
		t.Fatalf("new parent children = %v, want [q1 a1]（改父追加在末尾）", kids)
	}
	if c.Head != deep.ID { // Head 在被搬子树内：随行，值不变
		t.Fatalf("head = %q, want unchanged %q", c.Head, deep.ID)
	}
	if deepAfter, _ := c.Find(deep.ID); !sameContent(deepBefore, deepAfter) || deepAfter.Parent != q2.ID {
		t.Fatalf("deep node rewritten: %+v -> %+v", deepBefore, deepAfter)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// 路径跟着新父走：Root → persona → a1 → q2 → a2。
	path := c.Path()
	if len(path) != 5 || path[1].ID != persona.ID || path[2].ID != a1.ID ||
		path[3].ID != q2.ID || path[4].ID != deep.ID {
		t.Fatalf("path = %v, want Root→persona→a1→q2→a2", ids(path))
	}
}

// TestReparentRecordsDetachment 每次改父记一条路牌：锚点是原父、根只有一项、新位置是新父；
// 再搬一次就有两条，先前那条按结构现算成陈旧。
func TestReparentRecordsDetachment(t *testing.T) {
	useFixedClock(t)
	c, persona, q1, a1 := personaThenQuestion(t)

	if _, err := c.Reparent(a1.ID, persona.ID); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	got := c.Detachments()
	if len(got) != 1 {
		t.Fatalf("detachments = %d, want 1", len(got))
	}
	d := got[0]
	if d.Anchor != q1.ID || d.NewHome != persona.ID || d.Kind != DetachReparent ||
		len(d.Roots) != 1 || d.Roots[0] != a1.ID || d.Stale {
		t.Fatalf("detachment = %+v, want %s -> %s kind reparent live", d, q1.ID, persona.ID)
	}

	// 再挪一次：两条记录，第一条因为根已不在原新位置而陈旧。
	if _, err := c.Reparent(a1.ID, q1.ID); err != nil {
		t.Fatalf("reparent back: %v", err)
	}
	got = c.Detachments()
	if len(got) != 2 {
		t.Fatalf("detachments = %d, want 2", len(got))
	}
	if !got[0].Stale {
		t.Fatalf("first record should be stale after moving again: %+v", got[0])
	}
	if got[1].Stale || got[1].Anchor != persona.ID || got[1].NewHome != q1.ID {
		t.Fatalf("second record = %+v, want live persona -> q1", got[1])
	}
}

// TestReparentRejectsIllegalTargets 四条硬拦截 + 两条不存在：
// Root 不可搬、Root 不可作父、不能挂到自己或后代下、不能挂到当前父下。
func TestReparentRejectsIllegalTargets(t *testing.T) {
	useFixedClock(t)
	c, persona, q1, a1 := personaThenQuestion(t)
	q2 := mustAppend(t, c, RoleUser, "q2")
	root := MessageID(c.ID)

	cases := []struct {
		name     string
		id, into MessageID
		want     error
	}{
		{"root as subject", root, persona.ID, ErrInvalidNode},
		{"root as parent", a1.ID, root, ErrInvalidNode},
		{"self", a1.ID, a1.ID, ErrInvalidNode},
		{"own descendant", q1.ID, q2.ID, ErrInvalidNode},
		{"already there", a1.ID, q1.ID, ErrInvalidNode},
		{"missing subject", "NOPE", q1.ID, ErrNotFound},
		{"missing parent", a1.ID, "NOPE", ErrNotFound},
	}
	for _, tc := range cases {
		if _, err := c.Reparent(tc.id, tc.into); !errors.Is(err, tc.want) {
			t.Fatalf("%s: reparent(%s, %s) = %v, want %v", tc.name, tc.id, tc.into, err, tc.want)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: rejected reparent left tree broken: %v", tc.name, err)
		}
	}
	// 被拒绝的调用一条路牌都不该留下。
	if got := c.Detachments(); len(got) != 0 {
		t.Fatalf("rejected reparents recorded %d detachments", len(got))
	}
	if kids := c.ChildIDs(persona.ID); len(kids) != 1 || kids[0] != q1.ID {
		t.Fatalf("persona children = %v, want unchanged [%s]", kids, q1.ID)
	}
}

// TestReparentHeadOutsideSubtreeStays 被搬子树不含 Head 时 Head 一动不动，
// 路径只是换了一条上来的路（从新父那侧）。
func TestReparentHeadOutsideSubtreeStays(t *testing.T) {
	useFixedClock(t)
	c, persona, _, a1 := personaThenQuestion(t)
	// 另起一支挂在 persona 下，Head 停在它上面。
	if err := c.Checkout(persona.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	side := mustAppend(t, c, RoleUser, "side")

	if _, err := c.Reparent(a1.ID, persona.ID); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	if c.Head != side.ID {
		t.Fatalf("head = %q, want unchanged %q", c.Head, side.ID)
	}
	path := c.Path()
	if len(path) != 3 || path[2].ID != side.ID {
		t.Fatalf("path = %v, want Root→persona→side", ids(path))
	}
	// 新父的孩子顺序：改父追加在末尾，原有孩子位置不动。
	if kids := c.ChildIDs(persona.ID); len(kids) != 3 || kids[1] != side.ID || kids[2] != a1.ID {
		t.Fatalf("persona children = %v, want [q1 side a1]（原有孩子不动，新孩子排末尾）", kids)
	}
}

// TestReparentKeepsEditionChain 版本链不随结构走（D255）：A′ 挪到别的父下后，
// 它仍记着「修订自 A」，而版本组按结构重算——原先的兄弟关系不再成立。
func TestReparentKeepsEditionChain(t *testing.T) {
	useFixedClock(t)
	c, persona, _, a1 := personaThenQuestion(t)
	rev, err := c.Revise(a1.ID, textParts("a1'"), Fresh) // 与 a1 同父（q1）
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if old, ok := c.Edition(rev.ID); !ok || old != a1.ID {
		t.Fatalf("edition(%s) = %q,%v, want %s", rev.ID, old, ok, a1.ID)
	}
	if got := c.Branches(a1.ID); len(got) != 1 || got[0].ID != rev.ID {
		t.Fatalf("branches before reparent = %v, want [%s]", ids(got), rev.ID)
	}

	if _, err := c.Reparent(rev.ID, persona.ID); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	if old, ok := c.Edition(rev.ID); !ok || old != a1.ID {
		t.Fatalf("edition lost after reparent: %q,%v", old, ok)
	}
	// 结构兄弟关系随之消失（版本组按结构算），但版本链仍可查。
	if got := c.Branches(a1.ID); len(got) != 0 {
		t.Fatalf("branches after reparent = %v, want 空", ids(got))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
