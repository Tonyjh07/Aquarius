package app

// session_part_test.go 按分片编辑：/edit --part N 的 flag 位识别，以及助手
// 多分片编辑后其余分片原样保留。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// TestSessionEditPartFlag --part N 只在 flag 位识别（D107②）：与 --keep/--copy 同区、
// 任意顺序同用；非数字/缺序号/负数按用法拒绝；字面 "--part" 之外的 "--x" 仍归文本。
func TestSessionEditPartFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		part int
		mode conversation.KeepMode
		text string
		fail string
	}{
		{"part 紧跟 id", []string{"a1", "--part", "1", "新文本"}, 1, conversation.Fresh, "新文本", ""},
		{"part 与 keep 同用", []string{"a1", "--keep", "--part", "0", "x"}, 0, conversation.Carry, "x", ""},
		{"part 在 keep 前", []string{"a1", "--part", "2", "--copy", "x"}, 2, conversation.Clone, "x", ""},
		{"part 缺序号", []string{"a1", "--part"}, -1, conversation.Fresh, "", "用法"},
		{"part 非数字", []string{"a1", "--part", "x", "t"}, -1, conversation.Fresh, "", "用法"},
		{"part 为负", []string{"a1", "--part", "-1", "t"}, -1, conversation.Fresh, "", "用法"},
		{"空参数", nil, -1, conversation.Fresh, "", "用法"},
		{"字面未知 flag 归文本", []string{"a1", "--partx", "t"}, -1, conversation.Fresh, "--partx t", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := parseEditArgs(tc.args)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("err = %v, want 含 %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if opt.target != "a1" || opt.part != tc.part || opt.mode != tc.mode || opt.text != tc.text {
				t.Fatalf("got %q/%d/%v/%q, want a1/%d/%v/%q",
					opt.target, opt.part, opt.mode, opt.text, tc.part, tc.mode, tc.text)
			}
		})
	}
}

// TestSessionEditAssistantPartFidelity 带工具的助手 Turn 编辑分片保真（D107，用户实测
// 「前半段丢失」的回归测试）：[思考, 前半段, 工具, 后半段] 编辑 --part 1 只换后半段；
// --part 0 只换前半段；缺省并段但思考/工具保留；越界报错。
func TestSessionEditAssistantPartFidelity(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	c := buildTree(t, s)

	mk, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "思考过程"},
		{Kind: conversation.PartText, Text: "前半段"},
		{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "t9", Name: "file_read", Args: json.RawMessage(`{"path":"x"}`),
			Result: &tool.Result{CallID: "t9", OK: true, Output: "内容"},
		}},
		{Kind: conversation.PartText, Text: "后半段"},
	})
	if err != nil {
		t.Fatalf("append turn: %v", err)
	}
	origTool := func(msg conversation.Message) *conversation.ToolPart {
		for _, p := range msg.Content {
			if p.Kind == conversation.PartTool {
				return p.Tool
			}
		}
		return nil
	}

	// --part 1：只换后半段，思考/前半段/工具逐字节保留。
	if _, err := handleCmd(s, "edit", string(mk.ID), "--part", "1", "改写后半段"); err != nil {
		t.Fatalf("edit --part 1: %v", err)
	}
	n1 := revisedID(c, mk.ID)
	nm1 := nodeOf(t, c, n1)
	if len(nm1.Content) != 4 ||
		nm1.Content[0].Kind != conversation.PartThinking || nm1.Content[0].Text != "思考过程" ||
		nm1.Content[1].Text != "前半段" ||
		nm1.Content[2].Kind != conversation.PartTool || nm1.Content[2].Tool.CallID != "t9" ||
		nm1.Content[3].Text != "改写后半段" {
		t.Fatalf("--part 1 内容 = %+v", nm1.Content)
	}
	if got, want := nm1.Content[2].Tool, origTool(mk); got.Args == nil || string(got.Args) != string(want.Args) {
		t.Fatalf("工具分片未逐字节保留: %+v vs %+v", got, want)
	}

	// --part 0（对上一修订节点链式再修，避免 revisedID 多笔歧义）：只换前半段。
	cur := revisedID(c, mk.ID)
	if _, err := handleCmd(s, "edit", string(cur), "--part", "0", "改写前半段"); err != nil {
		t.Fatalf("edit --part 0: %v", err)
	}
	nm0 := nodeOf(t, c, revisedID(c, cur))
	if len(nm0.Content) != 4 || nm0.Content[1].Text != "改写前半段" || nm0.Content[3].Text != "改写后半段" {
		t.Fatalf("--part 0 内容 = %+v", nm0.Content)
	}

	// 缺省：正文并为一条、置于首个正文位，思考/工具保留（D107③）。
	cur = revisedID(c, cur)
	if _, err := handleCmd(s, "edit", string(cur), "合并正文"); err != nil {
		t.Fatalf("edit 缺省: %v", err)
	}
	nmD := nodeOf(t, c, revisedID(c, cur))
	if len(nmD.Content) != 3 ||
		nmD.Content[0].Kind != conversation.PartThinking ||
		nmD.Content[1].Text != "合并正文" ||
		nmD.Content[2].Kind != conversation.PartTool || nmD.Content[2].Tool.CallID != "t9" {
		t.Fatalf("缺省内容 = %+v", nmD.Content)
	}

	// 越界：报错（不产生可续修状态，链上最后节点即当前 Head）。
	cur = revisedID(c, cur)
	if _, err := handleCmd(s, "edit", string(cur), "--part", "5", "x"); err == nil ||
		!strings.Contains(err.Error(), "越界") {
		t.Fatalf("越界 err = %v, want 含 越界", err)
	}
}
