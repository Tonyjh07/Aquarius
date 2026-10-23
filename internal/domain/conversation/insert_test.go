package conversation

// insert_test.go D152：衬垫节点插入（InsertBefore/InsertCommitted）的位置、边转移、
// Head 三态、子树零拷贝与拒绝路径。

import (
	"errors"
	"testing"
)

// insertFixture 建带分叉的树（D152 测试用）：
//
//	root ─ u1 ─ a1 ─ a2
//	          └─ u1b（u1 的第二个孩子 = 分叉）
//
// Head 结束时在 u1b（目标子树之外）。
func insertFixture(t *testing.T) (c *Conversation, u1, a1, a2, u1b Message) {
	t.Helper()
	useFixedClock(t)
	c = New(NewID(), "t")
	u1 = mustAppend(t, c, RoleUser, "u1")
	a1 = mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u1.ID, Role: RoleAssistant,
		Content: textParts("a1"), Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})
	a2 = mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: a1.ID, Role: RoleAssistant,
		Content: textParts("a2"), Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})
	u1b = mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u1.ID, Role: RoleUser,
		Content: textParts("u1b"), CreatedAt: nowFunc(),
	})
	if err := c.Validate(); err != nil {
		t.Fatalf("fixture validate: %v", err)
	}
	return c, u1, a1, a2, u1b
}

// TestInsertBeforeTakesTargetSlot 摘要占据目标在原父下的**原位置**，目标改挂其下，
// 兄弟不动、子树零拷贝、Head 不变、整树仍然合法。
func TestInsertBeforeTakesTargetSlot(t *testing.T) {
	c, u1, a1, a2, u1b := insertFixture(t)
	headBefore := c.Head

	pad, err := c.InsertBefore(a1.ID, RoleSystem, textParts("摘要"))
	if err != nil {
		t.Fatalf("insert before %s: %v", a1.ID, err)
	}
	if pad.Role != RoleSystem || pad.Parent != u1.ID {
		t.Fatalf("pad = {%s %s parent %s}, want system under %s", pad.ID, pad.Role, pad.Parent, u1.ID)
	}
	if got := c.children[u1.ID]; len(got) != 2 || got[0] != pad.ID || got[1] != u1b.ID {
		t.Fatalf("children[%s] = %v, want [%s %s]（原位置 + 兄弟不动）", u1.ID, got, pad.ID, u1b.ID)
	}
	if got := c.children[pad.ID]; len(got) != 1 || got[0] != a1.ID {
		t.Fatalf("children[%s] = %v, want [%s]", pad.ID, got, a1.ID)
	}
	if moved := c.nodes[a1.ID]; moved.Parent != pad.ID {
		t.Fatalf("target parent = %s, want %s", moved.Parent, pad.ID)
	}
	// 子树零拷贝：下游节点 ID 与内容不变，边照旧。
	if got := c.children[a1.ID]; len(got) != 1 || got[0] != a2.ID {
		t.Fatalf("children[%s] = %v, want [%s]", a1.ID, got, a2.ID)
	}
	if got := c.nodes[a2.ID]; got.Content[0].Text != "a2" || got.Parent != a1.ID {
		t.Fatalf("descendant mutated: %+v", got)
	}
	if c.Head != headBefore {
		t.Fatalf("head = %s, want unchanged %s", c.Head, headBefore)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate after insert: %v", err)
	}
}

// TestInsertBeforeKeepsHeadInSubtree Head 在目标自身或其后代时也保持不变——
// 插入只加一层路径，不把 Head 挪走（D152）。
func TestInsertBeforeKeepsHeadInSubtree(t *testing.T) {
	c, u1, a1, _, _ := insertFixture(t)
	if err := c.Checkout(a1.ID); err != nil { // Head = 目标自身
		t.Fatalf("checkout: %v", err)
	}
	if _, err := c.InsertBefore(a1.ID, RoleSystem, textParts("摘要")); err != nil {
		t.Fatalf("insert before %s: %v", a1.ID, err)
	}
	if c.Head != a1.ID {
		t.Fatalf("head = %s, want target %s", c.Head, a1.ID)
	}
	path := c.Path()
	if len(path) != 4 || path[2].Role != RoleSystem || path[3].ID != a1.ID {
		t.Fatalf("path = %v, want [root u1 摘要 a1]", ids(path))
	}
	// Head 在后代：插入后仍在该子树内（路径长度 +1，终点不变）。
	if err := c.Checkout(a1.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := c.InsertBefore(u1.ID, RoleSystem, textParts("摘要二")); err != nil {
		t.Fatalf("insert before %s: %v", u1.ID, err)
	}
	if c.Head != a1.ID {
		t.Fatalf("head = %s, want descendant %s", c.Head, a1.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestInsertBeforeWaterlineSummaryIsSystemNode 摘要节点落在路径上的目标之前——
// 装配口径（水位 = 路径上最后一个 system）由此成立：其上历史不再回传。
func TestInsertBeforeWaterlineSummaryIsSystemNode(t *testing.T) {
	c, _, a1, _, _ := insertFixture(t)
	if err := c.Checkout(a1.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	pad, err := c.InsertBefore(a1.ID, RoleSystem, textParts("## Objective\n- 摘要"))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	path := c.Path()
	idx := -1
	for i, m := range path {
		if m.ID == pad.ID {
			idx = i
		}
	}
	if idx < 0 || idx+1 >= len(path) || path[idx+1].ID != a1.ID {
		t.Fatalf("path = %v: pad 应紧邻目标之前", ids(path))
	}
}

// TestInsertCommittedKeepsAssembledNode 模型生成的摘要走 InsertCommitted：预分配 ID、
// Model/Usage/终态原样入树，Parent 由目标决定（调用方给的值不作数），CreatedAt 为零则补。
func TestInsertCommittedKeepsAssembledNode(t *testing.T) {
	c, u1, a1, _, _ := insertFixture(t)
	padID := NewMessageID()
	pad, err := c.InsertCommitted(a1.ID, Message{
		ID:      padID,
		Parent:  u1.ID, // 故意给父：位置仍由目标决定
		Role:    RoleSystem,
		Content: textParts("模型摘要"),
		Outcome: OutcomeDone,
		Model:   "m1",
		Usage:   Usage{InputTokens: 7, OutputTokens: 3, CostUSD: 0.01},
	})
	if err != nil {
		t.Fatalf("insert committed: %v", err)
	}
	if pad.ID != padID || pad.Model != "m1" || pad.Usage.InputTokens != 7 ||
		pad.Outcome != OutcomeDone || pad.CreatedAt.IsZero() {
		t.Fatalf("pad lost assembled fields: %+v", pad)
	}
	if pad.Parent != u1.ID {
		t.Fatalf("pad parent = %s, want %s", pad.Parent, u1.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestInsertBeforeRejects 非法插入一律报错且树毫无改动（失败不落半截变更）。
func TestInsertBeforeRejects(t *testing.T) {
	c, u1, a1, _, _ := insertFixture(t)
	root := MessageID(c.ID)
	before := len(c.nodes)
	kidsBefore := len(c.children[u1.ID])

	cases := []struct {
		name string
		ins  func() (Message, error)
		want error
	}{
		{"root", func() (Message, error) {
			return c.InsertBefore(root, RoleSystem, textParts("x"))
		}, ErrInvalidNode},
		{"user role", func() (Message, error) {
			return c.InsertBefore(a1.ID, RoleUser, textParts("x"))
		}, ErrInvalidNode},
		{"assistant role", func() (Message, error) {
			return c.InsertBefore(a1.ID, RoleAssistant, textParts("x"))
		}, ErrInvalidNode},
		{"root role", func() (Message, error) {
			return c.InsertBefore(a1.ID, RoleRoot, textParts("x"))
		}, ErrInvalidNode},
		{"no content", func() (Message, error) {
			return c.InsertBefore(a1.ID, RoleSystem, nil)
		}, ErrInvalidNode},
		{"unknown target", func() (Message, error) {
			return c.InsertBefore("nope", RoleSystem, textParts("x"))
		}, ErrNotFound},
		{"duplicate pad id", func() (Message, error) {
			return c.InsertCommitted(a1.ID, Message{
				ID: a1.ID, Role: RoleSystem, Content: textParts("x"),
			})
		}, ErrInvalidNode},
		{"tool part in pad", func() (Message, error) {
			return c.InsertCommitted(a1.ID, Message{
				Role:    RoleSystem,
				Content: []Part{{Kind: PartTool, Tool: &ToolPart{CallID: "c1", Name: "x"}}},
			})
		}, ErrInvalidNode},
	}
	for _, tc := range cases {
		if _, err := tc.ins(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if len(c.nodes) != before {
			t.Fatalf("%s: node count = %d, want %d（失败不得入树）", tc.name, len(c.nodes), before)
		}
		if len(c.children[u1.ID]) != kidsBefore {
			t.Fatalf("%s: children[%s] 被改动", tc.name, u1.ID)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: validate: %v", tc.name, err)
		}
	}
}
