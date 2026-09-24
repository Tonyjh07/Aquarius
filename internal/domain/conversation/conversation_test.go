package conversation

import (
	"errors"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

func mustAppend(t *testing.T, c *Conversation, role Role, text string) Message {
	t.Helper()
	m, err := c.Append(role, textParts(text))
	if err != nil {
		t.Fatalf("append(%s): %v", role, err)
	}
	return m
}

func mustCommit(t *testing.T, c *Conversation, m Message) Message {
	t.Helper()
	if err := c.AppendCommitted(m); err != nil {
		t.Fatalf("append committed(%s): %v", m.ID, err)
	}
	return c.Nodes[m.ID]
}

func TestNewMessageIDMonotonic(t *testing.T) {
	prev := ""
	for i := 0; i < 1000; i++ {
		id := string(NewMessageID())
		if len(id) != 26 {
			t.Fatalf("ulid length = %d, want 26", len(id))
		}
		if id <= prev {
			t.Fatalf("ulid not monotonic: %q <= %q", id, prev)
		}
		prev = id
	}
}

func TestAppendAdvancesHeadAndBuildsPath(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	rootID := MessageID(c.ID)
	if c.Head != rootID {
		t.Fatalf("new conversation head = %q, want root %q", c.Head, rootID)
	}
	if path := c.Path(); len(path) != 1 || path[0].ID != rootID || path[0].Role != RoleRoot {
		t.Fatalf("initial path = %v, want [root]", ids(path))
	}

	u := mustAppend(t, c, RoleUser, "hi")
	if u.Parent != rootID {
		t.Fatalf("first message parent = %q, want root %q", u.Parent, rootID)
	}
	if c.Head != u.ID {
		t.Fatalf("head = %q, want %q", c.Head, u.ID)
	}
	a := mustAppend(t, c, RoleAssistant, "hello")

	path := c.Path()
	if len(path) != 3 || path[0].ID != rootID || path[1].ID != u.ID || path[2].ID != a.ID {
		t.Fatalf("path = %v, want [root %s %s]", ids(path), u.ID, a.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestCommitToolFlowAndRejectBadRefs(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u := mustAppend(t, c, RoleUser, "run think")

	if _, err := c.Append(RoleTool, nil); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("append(tool) = %v, want ErrInvalidNode", err)
	}

	calls := []tool.Call{{ID: "c1", Name: "think", Args: []byte(`{}`)}}
	a := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u.ID, Role: RoleAssistant,
		ToolCalls: calls, Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})
	tr := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleTool,
		ToolResult: &tool.Result{CallID: "c1", OK: true, Output: "ok"},
		Outcome:    OutcomeDone, CreatedAt: nowFunc(),
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// 不变量 2：存在性引用——悬挂引用必须被拒。
	err := c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: tr.ID, Role: RoleTool,
		ToolResult: &tool.Result{CallID: "missing"}, CreatedAt: nowFunc(),
	})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("dangling tool result = %v, want ErrInvalidNode", err)
	}
	// 调用 ID 全树唯一：撞车必须被拒。
	err = c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: tr.ID, Role: RoleAssistant,
		ToolCalls: calls, CreatedAt: nowFunc(),
	})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("duplicate call id = %v, want ErrInvalidNode", err)
	}
	// 重复消息 ID / 父不存在必须被拒。
	err = c.AppendCommitted(Message{ID: a.ID, Parent: u.ID, Role: RoleUser, CreatedAt: nowFunc()})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("duplicate node id = %v, want ErrInvalidNode", err)
	}
	err = c.AppendCommitted(Message{ID: NewMessageID(), Parent: "no-parent", Role: RoleUser, CreatedAt: nowFunc()})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("dangling parent = %v, want ErrInvalidNode", err)
	}
	// D18：tool 结果节点不可 Revise。
	if _, err := c.Revise(tr.ID, textParts("x"), Fresh); !errors.Is(err, ErrReviseTool) {
		t.Fatalf("revise(tool) = %v, want ErrReviseTool", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate after rejects: %v", err)
	}
}

func TestReviseFreshStartsNewBranch(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	u2 := mustAppend(t, c, RoleUser, "q2")
	a2 := mustAppend(t, c, RoleAssistant, "a2")

	m, err := c.Revise(u2.ID, textParts("q2'"), Fresh)
	if err != nil {
		t.Fatalf("revise fresh: %v", err)
	}
	if m.Parent != a1.ID || m.Role != RoleUser {
		t.Fatalf("revised node = %+v, want sibling of %s", m, u2.ID)
	}
	if c.RevisedFrom[m.ID] != u2.ID {
		t.Fatalf("revised_from[%s] = %q, want %q", m.ID, c.RevisedFrom[m.ID], u2.ID)
	}
	if c.Head != m.ID {
		t.Fatalf("head = %q, want new node %q", c.Head, m.ID)
	}
	// 旧分支原样保留。
	if c.Nodes[a2.ID].Parent != u2.ID {
		t.Fatalf("old subtree moved on Fresh: a2.parent = %q", c.Nodes[a2.ID].Parent)
	}
	br := c.Branches(u2.ID)
	if len(br) != 1 || br[0].ID != m.ID {
		t.Fatalf("branches(%s) = %v, want [%s]", u2.ID, ids(br), m.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestReviseCarryMovesSubtreeAndKeepsHead(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	u2 := mustAppend(t, c, RoleUser, "q2")
	a2 := mustAppend(t, c, RoleAssistant, "a2")
	u3 := mustAppend(t, c, RoleUser, "q3") // Head

	m, err := c.Revise(u2.ID, textParts("q2'"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	// 边转移：a2 整批改挂到新节点，旧节点成为旧版本叶子。
	if c.Nodes[a2.ID].Parent != m.ID {
		t.Fatalf("a2.parent = %q, want %q", c.Nodes[a2.ID].Parent, m.ID)
	}
	if len(c.Children[u2.ID]) != 0 {
		t.Fatalf("old node keeps children: %v", c.Children[u2.ID])
	}
	if c.Nodes[u3.ID].Parent != a2.ID {
		t.Fatalf("deep node re-parented: u3.parent = %q", c.Nodes[u3.ID].Parent)
	}
	// 旧 Head 是 u2 的严格后代：历史随行，Head 保持。
	if c.Head != u3.ID {
		t.Fatalf("head = %q, want %q", c.Head, u3.ID)
	}
	if !c.isStrictDescendant(a1.ID, m.ID) || m.Parent != a1.ID {
		t.Fatalf("revised node not sibling of target")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestReviseCarryOnHeadMovesHead(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1") // Head = 修订目标

	m, err := c.Revise(a1.ID, textParts("a1'"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	if c.Head != m.ID {
		t.Fatalf("head = %q, want %q", c.Head, m.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestReviseTopLevelMessage(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "first")

	m, err := c.Revise(u1.ID, textParts("new first"), Fresh)
	if err != nil {
		t.Fatalf("revise top-level: %v", err)
	}
	rootID := MessageID(c.ID)
	if m.Parent != rootID {
		t.Fatalf("revised top-level parent = %q, want root %q", m.Parent, rootID)
	}
	if top := c.Branches(rootID); len(top) != 2 {
		t.Fatalf("top-level messages = %v, want 2", ids(top))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestPruneSubtreeAndHeadFallback(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	u2 := mustAppend(t, c, RoleUser, "q2")
	a2 := mustAppend(t, c, RoleAssistant, "a2")

	if err := c.Checkout(a2.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if err := c.Prune(u2.ID); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, ok := c.Find(u2.ID); ok {
		t.Fatalf("pruned node still present")
	}
	if _, ok := c.Find(a2.ID); ok {
		t.Fatalf("pruned subtree node still present")
	}
	if c.Head != a1.ID {
		t.Fatalf("head = %q, want fallback %q", c.Head, a1.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// 剪掉全部内容节点：只剩 Root，Head 回到 Root（Root 不可剪，D19）。
	if err := c.Prune(u1.ID); err != nil {
		t.Fatalf("prune top-level: %v", err)
	}
	if len(c.Nodes) != 1 || c.Head != MessageID(c.ID) {
		t.Fatalf("nodes = %d head = %q, want only root", len(c.Nodes), c.Head)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := c.Prune("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("prune missing = %v, want ErrNotFound", err)
	}
	if err := c.Prune(MessageID(c.ID)); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("prune root = %v, want ErrInvalidNode", err)
	}
}

func TestPruneCascadesOrphanToolResult(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u := mustAppend(t, c, RoleUser, "q1")
	a := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u.ID, Role: RoleAssistant,
		ToolCalls: []tool.Call{{ID: "c1", Name: "think"}}, CreatedAt: nowFunc(),
	})
	tr := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleTool,
		ToolResult: &tool.Result{CallID: "c1", OK: true, Output: "ok"}, CreatedAt: nowFunc(),
	})
	n := mustAppend(t, c, RoleAssistant, "a2") // Head，位于 tr 之下

	// Carry 修订 assistant：tool 结果改挂到新 assistant 之下（存在性引用仍指向旧 assistant）。
	m, err := c.Revise(a.ID, textParts("a1'"), Carry)
	if err != nil {
		t.Fatalf("revise carry: %v", err)
	}
	if c.Nodes[tr.ID].Parent != m.ID {
		t.Fatalf("tool result not carried: parent = %q", c.Nodes[tr.ID].Parent)
	}

	// 剪掉旧 assistant：tool 结果失联 → 连带清理，其子树改挂最近存活祖先（历史不丢）。
	if err := c.Prune(a.ID); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, ok := c.Find(tr.ID); ok {
		t.Fatalf("orphan tool result not cascaded")
	}
	if c.Nodes[n.ID].Parent != m.ID {
		t.Fatalf("history lost: n.parent = %q, want %q", c.Nodes[n.ID].Parent, m.ID)
	}
	if c.Head != n.ID {
		t.Fatalf("head = %q, want %q", c.Head, n.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestCheckoutRootRealNode(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	mustAppend(t, c, RoleUser, "q1")
	rootID := MessageID(c.ID)

	if err := c.Checkout(rootID); err != nil {
		t.Fatalf("checkout root: %v", err)
	}
	if path := c.Path(); len(path) != 1 || path[0].ID != rootID {
		t.Fatalf("path = %v, want [root]", ids(path))
	}
	m := mustAppend(t, c, RoleUser, "fresh top-level")
	if m.Parent != rootID {
		t.Fatalf("parent = %q, want root %q", m.Parent, rootID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := c.Checkout(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("checkout empty = %v, want ErrNotFound（空串特例已移除，D19）", err)
	}
	if err := c.Checkout("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("checkout missing = %v, want ErrNotFound", err)
	}
}

// TestRootGuardsAndSystemRole D19/D20：Root 不可 Append/Commit/Revise/Prune 且必须为空；
// system 节点经 AppendCommitted 入树且可 Revise；非 Root 不得无父。
func TestRootGuardsAndSystemRole(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	rootID := MessageID(c.ID)

	root, ok := c.Find(rootID)
	if !ok || root.Role != RoleRoot || root.Parent != "" || len(root.Content) != 0 || root.ToolResult != nil {
		t.Fatalf("root = %+v, want 空内容 RoleRoot", root)
	}
	if top := c.Branches(rootID); len(top) != 0 {
		t.Fatalf("fresh top-level = %v, want none", ids(top))
	}

	for _, role := range []Role{RoleSystem, RoleRoot, RoleTool} {
		if _, err := c.Append(role, textParts("x")); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("append(%s) = %v, want ErrInvalidNode", role, err)
		}
	}
	if err := c.AppendCommitted(Message{ID: NewMessageID(), Role: RoleRoot, CreatedAt: nowFunc()}); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("commit root = %v, want ErrInvalidNode", err)
	}
	if err := c.AppendCommitted(Message{ID: NewMessageID(), Role: RoleUser, CreatedAt: nowFunc()}); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("commit parentless user = %v, want ErrInvalidNode", err)
	}
	if _, err := c.Revise(rootID, textParts("x"), Fresh); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("revise(root) = %v, want ErrInvalidNode", err)
	}
	if err := c.Prune(rootID); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("prune(root) = %v, want ErrInvalidNode", err)
	}

	// system 节点（persona 形态）：入树、可 Revise（D20）。
	sys := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: rootID, Role: RoleSystem,
		Content: textParts("persona"), CreatedAt: nowFunc(),
	})
	if _, err := c.Revise(sys.ID, textParts("persona'"), Fresh); err != nil {
		t.Fatalf("revise(system) = %v, want allowed", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
