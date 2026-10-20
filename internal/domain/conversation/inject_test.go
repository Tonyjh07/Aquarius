package conversation

// inject_test.go D150：注入节点（RoleInject）的形态、入树、修订保留字段、
// 衬垫角色放开与「非 inject 不得带 Source/Trigger」的拒绝路径。

import (
	"errors"
	"testing"
)

// TestInjectNodeCommitShape 注入节点经 AppendCommitted 入树：Source/Trigger 原样保留、
// 可携带图片/音频/文档分片（与 user 同规则），Head 移到新节点。
func TestInjectNodeCommitShape(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "u1")
	a1 := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u1.ID, Role: RoleAssistant,
		Content: textParts("a1"), Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})

	in := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: a1.ID, Role: RoleInject,
		Content: []Part{
			{Kind: PartText, Text: "任务完成"},
			{Kind: PartImage, Ref: &BlobRef{Hash: "h1", MIME: "image/png", Name: "s.png", Size: 3}},
			{Kind: PartDoc, Text: "提取文本", Ref: &BlobRef{Hash: "h2", MIME: "text/plain"}},
		},
		Source:    "job:j1",
		Trigger:   true,
		CreatedAt: nowFunc(),
	})
	if in.Role != RoleInject || in.Source != "job:j1" || !in.Trigger {
		t.Fatalf("inject node lost fields: %+v", in)
	}
	if c.Head != in.ID {
		t.Fatalf("head = %s, want %s", c.Head, in.ID)
	}
	if len(in.Content) != 3 {
		t.Fatalf("content = %+v, want 3 parts", in.Content)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestInjectNodeRejectsThinkingAndTool inject 与 user 同规则：拒思考分片与工具分片
// （D95 后两者仅 assistant 可携带）。
func TestInjectNodeRejectsThinkingAndTool(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "u1")
	before := len(c.Nodes)

	cases := []struct {
		name string
		part Part
	}{
		{"thinking", Part{Kind: PartThinking, Text: "t"}},
		{"tool", Part{Kind: PartTool, Tool: &ToolPart{CallID: "c1", Name: "think"}}},
	}
	for _, tc := range cases {
		err := c.AppendCommitted(Message{
			ID: NewMessageID(), Parent: u1.ID, Role: RoleInject,
			Content: []Part{tc.part}, Source: "tool:think", CreatedAt: nowFunc(),
		})
		if !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("%s in inject: err = %v, want ErrInvalidNode", tc.name, err)
		}
		if len(c.Nodes) != before {
			t.Fatalf("%s in inject: 失败不得入树", tc.name)
		}
	}
}

// TestNonInjectRejectsSourceTrigger 非 inject 节点带 Source 或 Trigger 一律拒——
// 防未来某条路径把注入专属字段写到别的角色上（D150 领域校验）。
func TestNonInjectRejectsSourceTrigger(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "u1")
	before := len(c.Nodes)

	bad := []Message{
		{ID: NewMessageID(), Parent: u1.ID, Role: RoleUser, Content: textParts("x"), Source: "tool:x", CreatedAt: nowFunc()},
		{ID: NewMessageID(), Parent: u1.ID, Role: RoleAssistant, Content: textParts("x"), Trigger: true, CreatedAt: nowFunc()},
		{ID: NewMessageID(), Parent: u1.ID, Role: RoleSystem, Content: textParts("x"), Source: "user", CreatedAt: nowFunc()},
		{ID: NewMessageID(), Parent: u1.ID, Role: RoleUser, Content: textParts("x"), Trust: TrustHost, CreatedAt: nowFunc()},
	}
	for _, m := range bad {
		if err := c.AppendCommitted(m); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("role %s with source/trigger: err = %v, want ErrInvalidNode", m.Role, err)
		}
		if len(c.Nodes) != before {
			t.Fatalf("role %s: 失败不得入树", m.Role)
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestReviseInjectKeepsSourceTrigger 修订注入节点只换内容：Source/Trust/Trigger 随原节点
// （否则改写后就成了「无来源注入」，或宿主事实被降级成外部数据）。
func TestReviseInjectKeepsSourceTrigger(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "u1")
	in := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u1.ID, Role: RoleInject,
		Content: textParts("旧"), Source: "poller:p1", Trust: TrustHost, Trigger: true, CreatedAt: nowFunc(),
	})
	rev, err := c.Revise(in.ID, textParts("新"), Fresh)
	if err != nil {
		t.Fatalf("revise inject: %v", err)
	}
	if rev.Role != RoleInject || rev.Source != "poller:p1" || !rev.Trigger {
		t.Fatalf("revised inject lost source/trigger: %+v", rev)
	}
	if rev.Trust != TrustHost {
		t.Fatalf("revised trust = %q, want host（改内容不改信任级）", rev.Trust)
	}
	if rev.Content[0].Text != "新" {
		t.Fatalf("revised content = %q, want 新", rev.Content[0].Text)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestInsertInjectPadAllowed D152 留的扣：衬垫角色放开 inject（与 system 并列）。
func TestInsertInjectPadAllowed(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	u1 := mustAppend(t, c, RoleUser, "u1")
	a1 := mustCommit(t, c, Message{
		ID: NewMessageID(), Parent: u1.ID, Role: RoleAssistant,
		Content: textParts("a1"), Outcome: OutcomeDone, CreatedAt: nowFunc(),
	})

	pad, err := c.InsertCommitted(a1.ID, Message{
		Role: RoleInject, Content: textParts("注入上下文"),
		Source: "tool:think", CreatedAt: nowFunc(),
	})
	if err != nil {
		t.Fatalf("insert inject pad: %v", err)
	}
	if pad.Role != RoleInject || pad.Source != "tool:think" || pad.Parent != u1.ID {
		t.Fatalf("inject pad = %+v, want inject under %s", pad, u1.ID)
	}
	path := c.Path()
	if len(path) != 4 || path[2].ID != pad.ID || path[3].ID != a1.ID {
		t.Fatalf("path = %v, want [root u1 inj a1]（插入加一层，Head 仍在目标 a1）", ids(path))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestAppendStillRejectsInject Append 仍只收 user|assistant——注入走 AppendCommitted。
func TestAppendStillRejectsInject(t *testing.T) {
	useFixedClock(t)
	c := New(NewID(), "t")
	if _, err := c.Append(RoleInject, textParts("x")); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("append(inject) = %v, want ErrInvalidNode", err)
	}
}
