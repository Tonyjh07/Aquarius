package conversation

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

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

// TestCommitToolPartFlowAndRejectBadShapes D95：工具分片（调用+结果同片）随 assistant
// 节点提交；Result=nil 合法（未执行/被取消）；形态违规（user/system 携带、节点内 CallID
// 撞车、Result.CallID 不一致、空工具名）一律 ErrInvalidNode。无跨节点引用可悬挂。
func TestCommitToolPartFlowAndRejectBadShapes(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u := mustAppend(t, c, RoleUser, "run think")

	toolPart := func(callID string, res *tool.Result) Part {
		return Part{Kind: PartTool, Tool: &ToolPart{CallID: callID, Name: "think", Args: json.RawMessage(`{}`), Result: res}}
	}
	a := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u.ID, Role: RoleAssistant,
		Content: []Part{toolPart("c1", &tool.Result{CallID: "c1", OK: true, Output: "ok"})},
		Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})
	// Result=nil（未执行）同样是合法形态。
	mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleAssistant,
		Content: []Part{toolPart("c2", nil)},
		Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// user/system 携带工具分片必须被拒。
	err := c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleUser,
		Content: []Part{toolPart("c9", nil)}, CreatedAt: nowFunc(),
	})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("commit(user, tool part) = %v, want ErrInvalidNode", err)
	}
	err = c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleSystem,
		Content: []Part{toolPart("c9", nil)}, CreatedAt: nowFunc(),
	})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("commit(system, tool part) = %v, want ErrInvalidNode", err)
	}
	// 节点内 CallID 撞车必须被拒（全树唯一已随 D95 取消——另一节点复用 c1 合法）。
	dup := Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleAssistant,
		Content: []Part{toolPart("d1", nil), toolPart("d1", nil)},
		Outcome: OutcomeDone, CreatedAt: nowFunc(),
	}
	if err := c.AppendCommitted(dup); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("duplicate part call id = %v, want ErrInvalidNode", err)
	}
	// Result.CallID 与分片不一致必须被拒。
	err = c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleAssistant,
		Content: []Part{toolPart("e1", &tool.Result{CallID: "other", OK: true})},
		Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("mismatched result call id = %v, want ErrInvalidNode", err)
	}
	// 空工具名必须被拒。
	bad := Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleAssistant,
		Content: []Part{{Kind: PartTool, Tool: &ToolPart{CallID: "f1"}}},
		Outcome: OutcomeDone, CreatedAt: nowFunc(),
	}
	if err := c.AppendCommitted(bad); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("empty tool name = %v, want ErrInvalidNode", err)
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
	// 带 tool 分片的 assistant 节点可 Revise（D18 随 D95 取消）：内容整体替换，新版本不含旧分片。
	m, err := c.Revise(a.ID, textParts("a1'"), Fresh)
	if err != nil {
		t.Fatalf("revise(assistant with tool part) = %v, want allowed", err)
	}
	for _, p := range m.Content {
		if p.Kind == PartTool {
			t.Fatalf("revised node keeps old tool part: %+v", m.Content)
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate after rejects: %v", err)
	}
}

// TestThinkingPartOnlyOnAssistant D42：思考分片（PartThinking）仅 assistant 可携带——
// assistant 入树合法且整树校验通过；user/system/tool 与 Revise 路径一律 ErrInvalidNode。
func TestThinkingPartOnlyOnAssistant(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u := mustAppend(t, c, RoleUser, "explain")

	thinking := []Part{{Kind: PartThinking, Text: "先想一想"}, {Kind: PartText, Text: "答案"}}
	a, err := c.Append(RoleAssistant, thinking)
	if err != nil {
		t.Fatalf("append assistant with thinking: %v", err)
	}
	if a.Content[0].Kind != PartThinking || a.Content[1].Kind != PartText {
		t.Fatalf("assistant content = %v, want [thinking text]", a.Content)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// user / system 携带思考必须被拒。
	if _, err := c.Append(RoleUser, thinking); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("append(user, thinking) = %v, want ErrInvalidNode", err)
	}
	err = c.AppendCommitted(Message{
		ID: NewMessageID(), Parent: a.ID, Role: RoleSystem,
		Content: []Part{{Kind: PartThinking, Text: "x"}}, CreatedAt: nowFunc(),
	})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("commit(system, thinking) = %v, want ErrInvalidNode", err)
	}
	// Revise 同受形态约束（修订 user 消息塞思考）。
	if _, err := c.Revise(u.ID, thinking, Fresh); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("revise(user, thinking) = %v, want ErrInvalidNode", err)
	}
	// Root 恒为空消息（先于思考校验拒绝）。
	if err := c.AppendCommitted(Message{
		ID: MessageID(c.ID), Role: RoleRoot,
		Content: []Part{{Kind: PartThinking, Text: "x"}}, CreatedAt: nowFunc(),
	}); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("commit(root, thinking) = %v, want ErrInvalidNode", err)
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

// TestReviseCloneDeepCopiesSubtree D96：Clone = 新节点 + 整棵子树深拷贝（全新 ID、
// 原子树字节级不动、CreatedAt 保真）；旧 Head 在原子树内则平移到拷贝对应节点；
// RevisedFrom 仅记新根一条。
func TestReviseCloneDeepCopiesSubtree(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "q1")
	a1 := mustAppend(t, c, RoleAssistant, "a1")
	u2 := mustAppend(t, c, RoleUser, "q2")
	a2 := mustAppend(t, c, RoleAssistant, "a2") // Head，在 u1 子树内

	before := map[MessageID]Message{}
	for nid, m := range c.Nodes {
		before[nid] = m.Clone()
	}

	m, err := c.Revise(u1.ID, textParts("q1'"), Clone)
	if err != nil {
		t.Fatalf("revise clone: %v", err)
	}
	if m.Parent != u1.Parent || m.Role != RoleUser {
		t.Fatalf("new node = %+v, want 同父同角色", m)
	}
	if c.RevisedFrom[m.ID] != u1.ID || len(c.RevisedFrom) != 1 {
		t.Fatalf("revised_from = %v, want 仅 新根→旧根 一条", c.RevisedFrom)
	}
	// 原子树字节级不动（内容 + Parent 边）。
	for nid, want := range before {
		got, ok := c.Nodes[nid]
		if !ok {
			t.Fatalf("原节点 %s 消失", nid)
		}
		if got.Parent != want.Parent || !sameContent(want, got) {
			t.Fatalf("原节点 %s 被改写", nid)
		}
	}
	// 拷贝镜像结构：a1 的全新拷贝挂在 m 下，内容与 CreatedAt 保真。
	kids := c.Children[m.ID]
	if len(kids) != 1 || kids[0] == a1.ID {
		t.Fatalf("children[new] = %v, want a1 的全新拷贝", kids)
	}
	ca1 := c.Nodes[kids[0]]
	if ca1.Parent != m.ID || !sameCopy(before[a1.ID], ca1) {
		t.Fatalf("拷贝 a1 = %+v, want 内容保真、挂到新节点", ca1)
	}
	// 节点数 = 原 5 + 新版本 1 + 拷贝 3。
	if len(c.Nodes) != len(before)+1+3 {
		t.Fatalf("nodes = %d, want %d", len(c.Nodes), len(before)+1+3)
	}
	// Head 平移到拷贝对应节点：Head → a1 拷贝 → m 的链路成立，且不是原子树节点。
	if c.Head == a2.ID || c.Head == a1.ID || c.Head == u2.ID {
		t.Fatalf("head = %q, 不应留在原子树", c.Head)
	}
	hops := 0
	for cur := c.Head; cur != m.ID; hops++ {
		n, ok := c.Nodes[cur]
		if !ok || hops > 3 {
			t.Fatalf("Head 链断裂于 %s", cur)
		}
		cur = n.Parent
	}
	if hops != 3 { // 自 Head 经 a2拷贝、u2拷贝、a1拷贝 到达新根 m
		t.Fatalf("Head 到新根 hops = %d, want 3", hops)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// 旧 Head 不在原子树（回 Root 后修订）：Head 移到新节点。
	if err := c.Checkout(MessageID(c.ID)); err != nil {
		t.Fatalf("checkout root: %v", err)
	}
	m2, err := c.Revise(a1.ID, textParts("a1'"), Clone)
	if err != nil {
		t.Fatalf("revise clone 2: %v", err)
	}
	if c.Head != m2.ID {
		t.Fatalf("head = %q, want 新节点 %q", c.Head, m2.ID)
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

// TestTail D94 版本末端：叶子即自身；版本子树末端 = CreatedAt 最新的叶子（平局取 ID 序
// 最大）；id 不在树中 ok=false。
func TestTail(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "问题")
	mustAppend(t, c, RoleAssistant, "答一") // a1
	u2 := mustAppend(t, c, RoleUser, "追问")
	a2 := mustAppend(t, c, RoleAssistant, "答二")

	// 叶子即自身。
	if got, ok := c.Tail(a2.ID); !ok || got != a2.ID {
		t.Fatalf("Tail(叶子) = %q %v, want 自身", got, ok)
	}
	// 线性链的唯一叶子。
	if got, ok := c.Tail(u1.ID); !ok || got != a2.ID {
		t.Fatalf("Tail(u1) = %q %v, want a2", got, ok)
	}

	// 分叉：编辑 u2 → 新版本 + 新回答；Tail(u1) 应取 CreatedAt 最新的叶子（新分支末端）。
	if _, err := c.Revise(u2.ID, textParts("追问（改）"), Fresh); err != nil {
		t.Fatalf("revise: %v", err)
	}
	a2n := mustAppend(t, c, RoleAssistant, "答二（新）")
	if got, ok := c.Tail(u1.ID); !ok || got != a2n.ID {
		t.Fatalf("Tail(u1) = %q %v, want 新分支末端 a2'", got, ok)
	}

	// 旧叶子时钟推后 → 它成为末端（CreatedAt 优先于 ID 序）。
	old := c.Nodes[a2.ID]
	old.CreatedAt = old.CreatedAt.Add(time.Hour)
	c.Nodes[a2.ID] = old
	if got, _ := c.Tail(u1.ID); got != a2.ID {
		t.Fatalf("Tail(u1) = %q, want 被推后的旧叶子 a2", got)
	}

	// 时钟全部相同 → 按 ID 序最大（确定性）。
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for id, n := range c.Nodes {
		n.CreatedAt = base
		c.Nodes[id] = n
	}
	want := a2.ID
	if a2n.ID > want {
		want = a2n.ID
	}
	if got, _ := c.Tail(u1.ID); got != want {
		t.Fatalf("Tail(u1) = %q, want 平局 ID 序最大 %q", got, want)
	}

	// 不在树中。
	if got, ok := c.Tail("nope"); ok || got != "" {
		t.Fatalf("Tail(缺失) = %q %v, want ok=false", got, ok)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestRootGuardsAndSystemRole D19/D20：Root 不可 Append/Commit/Revise/Prune 且必须为空；
// system 节点经 AppendCommitted 入树且可 Revise；非 Root 不得无父。
func TestRootGuardsAndSystemRole(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	rootID := MessageID(c.ID)

	root, ok := c.Find(rootID)
	if !ok || root.Role != RoleRoot || root.Parent != "" || len(root.Content) != 0 {
		t.Fatalf("root = %+v, want 空内容 RoleRoot", root)
	}
	if top := c.Branches(rootID); len(top) != 0 {
		t.Fatalf("fresh top-level = %v, want none", ids(top))
	}

	for _, role := range []Role{RoleSystem, RoleRoot} {
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

// TestReviseCarriesOutcome 修订节点 Outcome 随原节点（D107 补）：修订 = 同一轮的改写版，
// 终态语义随行——缺省零值会在呈现层命中「非 done 终态标记」（修订消息顶部多出 `[]`）。
func TestReviseCarriesOutcome(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	mustAppend(t, c, RoleUser, "q")
	a := Message{ID: NewMessageID(), Parent: c.Head, Role: RoleAssistant,
		Content: textParts("回答"), Outcome: OutcomeDone}
	if err := c.AppendCommitted(a); err != nil {
		t.Fatalf("append committed: %v", err)
	}
	m, err := c.Revise(a.ID, textParts("改写"), Fresh)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if m.Outcome != OutcomeDone {
		t.Fatalf("revised outcome = %q, want %q（随原节点）", m.Outcome, OutcomeDone)
	}
}
