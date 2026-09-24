package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// convWithUser 造带一条 user 消息的会话。
func convWithUser(t *testing.T) *conversation.Conversation {
	t.Helper()
	return newConv(t)
}

func TestAssemblePathBasicRoles(t *testing.T) {
	c := convWithUser(t)
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{{Kind: conversation.PartText, Text: "你好"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	msgs, err := assemblePath(context.Background(), c.Path(), nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("roles = %+v", msgs)
	}
	if msgs[0].Content[0].Text != "hi" || msgs[1].Content[0].Text != "你好" {
		t.Fatalf("texts = %+v", msgs)
	}
}

func TestAssemblePathMediaParts(t *testing.T) {
	parts := []conversation.Part{
		{Kind: conversation.PartText, Text: "看这个"},
		{Kind: conversation.PartAudio, Transcript: "语音转写"},
		{Kind: conversation.PartDoc, Text: "文档提取文本"},
		{Kind: conversation.PartImage, Ref: &conversation.BlobRef{Name: "a.png", MIME: "image/png", Hash: "H"}},
	}
	got, err := assembleParts(context.Background(), parts, nil)
	if err != nil {
		t.Fatalf("assembleParts: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("parts = %d, want 4", len(got))
	}
	if got[1].Text != "语音转写" || got[2].Text != "文档提取文本" {
		t.Fatalf("media texts = %+v", got)
	}
	if got[3].Kind != "text" || !strings.Contains(got[3].Text, "a.png") {
		t.Fatalf("image 占位 = %+v, want 附件库未接入时的占位文本", got[3])
	}
}

func TestAssemblePathImageInlinesBytes(t *testing.T) {
	blobs := &fakeBlobs{data: map[string][]byte{"H": []byte{0x89, 'P', 'N', 'G'}}}
	parts := []conversation.Part{
		{Kind: conversation.PartImage, Ref: &conversation.BlobRef{Name: "a.png", MIME: "image/png", Hash: "H"}},
	}
	got, err := assembleParts(context.Background(), parts, blobs)
	if err != nil {
		t.Fatalf("assembleParts: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "image" || string(got[0].Data) != "\x89PNG" || got[0].MIME != "image/png" {
		t.Fatalf("image part = %+v", got)
	}
}

func TestAssemblePathNormalToolResult(t *testing.T) {
	c := convWithUser(t)
	user1 := c.Path()[0]
	// 手工提交带调用的 assistant 节点 + tool 应答。
	asst := conversation.Message{
		ID: "A1", Parent: user1.ID, Role: conversation.RoleAssistant,
		ToolCalls: []tool.Call{{ID: "call_1", Name: "echo"}},
	}
	if err := c.AppendCommitted(asst); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	tnode := conversation.Message{
		ID: "T1", Parent: "A1", Role: conversation.RoleTool,
		ToolResult: &tool.Result{CallID: "call_1", OK: true, Output: "pong"},
	}
	if err := c.AppendCommitted(tnode); err != nil {
		t.Fatalf("append tool: %v", err)
	}

	msgs, err := assemblePath(context.Background(), c.Path(), nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.CallID != "call_1" || last.Content[0].Text != "pong" {
		t.Fatalf("tool msg = %+v", last)
	}
}

func TestAssemblePathFailedToolResultText(t *testing.T) {
	text := toolResultText(&tool.Result{CallID: "c", OK: false, Err: "权限拒绝", Output: "partial"})
	if !strings.HasPrefix(text, "错误: 权限拒绝") || !strings.Contains(text, "partial") {
		t.Fatalf("failed result text = %q", text)
	}
	if got := toolResultText(&tool.Result{CallID: "c", OK: true}); got != "（无输出）" {
		t.Fatalf("empty ok text = %q", got)
	}
	if got := toolResultText(nil); got != "（无输出）" {
		t.Fatalf("nil text = %q", got)
	}
}

// TestAssemblePathOrphanToolInlined 失联 tool 结果（声明其调用的 assistant 不在本路径）
// 必须按文本内联并标注——Revise(Carry) 隔离旧 assistant 是产生场景（DESIGN §4.1 不变量 2）。
func TestAssemblePathOrphanToolInlined(t *testing.T) {
	c := convWithUser(t)
	user1 := c.Path()[0]
	asst := conversation.Message{
		ID: "A1", Parent: user1.ID, Role: conversation.RoleAssistant,
		ToolCalls: []tool.Call{{ID: "call_1", Name: "echo"}},
	}
	if err := c.AppendCommitted(asst); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	tnode := conversation.Message{
		ID: "T1", Parent: "A1", Role: conversation.RoleTool,
		ToolResult: &tool.Result{CallID: "call_1", OK: true, Output: "pong"},
	}
	if err := c.AppendCommitted(tnode); err != nil {
		t.Fatalf("append tool: %v", err)
	}
	// Carry Revise 旧 assistant：tool 子树随行，但调用声明者掉出路径 → 失联。
	if _, err := c.Revise("A1", []conversation.Part{{Kind: conversation.PartText, Text: "重写"}}, conversation.Carry); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	msgs, err := assemblePath(context.Background(), c.Path(), nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" || last.CallID != "" {
		t.Fatalf("orphan 应内联为 assistant 文本，got %+v", last)
	}
	if !strings.Contains(last.Content[0].Text, historyToolMarker) || !strings.Contains(last.Content[0].Text, "pong") {
		t.Fatalf("orphan text = %q, want 含标注与结果", last.Content[0].Text)
	}
	// 新 assistant 不应带调用（否则与内联结果配不上）。
	for _, m := range msgs {
		if m.Role == "assistant" && m.Content[0].Text == "重写" && len(m.ToolCalls) != 0 {
			t.Fatalf("重写节点不应带 tool_calls: %+v", m)
		}
	}
}

func TestAssemblePathSkipsEmptyNodes(t *testing.T) {
	c := convWithUser(t)
	if _, err := c.Append(conversation.RoleAssistant, nil); err != nil { // error 终态空占位
		t.Fatalf("append empty assistant: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, nil); err != nil {
		t.Fatalf("append empty user: %v", err)
	}
	msgs, err := assemblePath(context.Background(), c.Path(), nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("msgs = %+v, want 只剩首条 user", msgs)
	}
}
