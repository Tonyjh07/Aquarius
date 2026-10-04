package app

import (
	"context"
	"encoding/json"
	"fmt"
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
	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
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

// TestAssemblePathEchoThinking D42 回传开关两态：开 = 思考置入 PromptMessage.Reasoning
// （正文分片不含思考）；关 = 过滤不发，纯思考节点（如取消轮）整条不入请求。
func TestAssemblePathEchoThinking(t *testing.T) {
	c := convWithUser(t)
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "推理过程"},
		{Kind: conversation.PartText, Text: "答案"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := c.Append(conversation.RoleAssistant, []conversation.Part{
		{Kind: conversation.PartThinking, Text: "只有思考"},
	}); err != nil {
		t.Fatalf("append thinking-only: %v", err)
	}

	msgs, err := assemblePath(context.Background(), c.Path(), nil, true)
	if err != nil {
		t.Fatalf("assemble(echo): %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("echo on: msgs = %d, want 3（纯思考节点也在）", len(msgs))
	}
	if msgs[1].Reasoning != "推理过程" || msgs[2].Reasoning != "只有思考" {
		t.Fatalf("echo on: reasoning = %q / %q", msgs[1].Reasoning, msgs[2].Reasoning)
	}
	if len(msgs[1].Content) != 1 || msgs[1].Content[0].Kind != "text" || msgs[1].Content[0].Text != "答案" {
		t.Fatalf("echo on: 正文分片 = %+v, want 仅 text 答案", msgs[1].Content)
	}

	msgs, err = assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble(no-echo): %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("echo off: msgs = %d, want 2（纯思考节点整条不发）", len(msgs))
	}
	if msgs[1].Reasoning != "" {
		t.Fatalf("echo off: reasoning = %q, want 空", msgs[1].Reasoning)
	}
	if len(msgs[1].Content) != 1 || msgs[1].Content[0].Text != "答案" {
		t.Fatalf("echo off: 正文分片 = %+v, want 仅 text 答案", msgs[1].Content)
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

// TestAssemblePathToolPartProjectsCallAndResult D95 装配投影：assistant 节点的
// 工具分片拆为 1 条 assistant(tool_calls) + 1 条 tool 应答消息（调用与结果同片恒成对）。
func TestAssemblePathToolPartProjectsCallAndResult(t *testing.T) {
	c := convWithUser(t)
	user1 := c.Path()[1] // path[0] 是 Root
	// 手工提交带工具分片（含结果）的 assistant 节点。
	asst := conversation.Message{
		ID: "A1", Parent: user1.ID, Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "call_1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`),
			Result: &tool.Result{CallID: "call_1", OK: true, Output: "pong"},
		}}},
	}
	if err := c.AppendCommitted(asst); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 3 { // user + assistant(tool_calls) + tool
		t.Fatalf("msgs = %d, want 3: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant msg = %+v", msgs[1])
	}
	// 分片不应混进正文。
	if len(msgs[1].Content) != 0 {
		t.Fatalf("assistant content = %+v, want 空", msgs[1].Content)
	}
	// 拆出的 tool 应答紧随其后。
	if msgs[2].Role != "tool" || msgs[2].CallID != "call_1" || msgs[2].Content[0].Text != "pong" {
		t.Fatalf("tool msg = %+v", msgs[2])
	}
}

// TestAssemblePathSegmentedTurnNode D95 Turn 粒度：一节点多段（text1, tool, text2）
// 按工具边界切段投影——assistant(段1) → tool 应答 → assistant(段2)；多段 thinking
// 各随其段；echo_thinking=false 时思考丢弃、正文照常分段。
func TestAssemblePathSegmentedTurnNode(t *testing.T) {
	c := convWithUser(t)
	user1 := c.Path()[1]
	asst := conversation.Message{
		ID: "A1", Parent: user1.ID, Role: conversation.RoleAssistant,
		Content: []conversation.Part{
			{Kind: conversation.PartThinking, Text: "想一"},
			{Kind: conversation.PartText, Text: "先看"},
			{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
				CallID: "c1", Name: "echo",
				Result: &tool.Result{CallID: "c1", OK: true, Output: "pong"},
			}},
			{Kind: conversation.PartThinking, Text: "想二"},
			{Kind: conversation.PartText, Text: "答案"},
		},
		Outcome: conversation.OutcomeDone,
	}
	if err := c.AppendCommitted(asst); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	msgs, err := assemblePath(context.Background(), c.Path(), nil, true)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 4 { // user + assistant(段1) + tool + assistant(段2)
		t.Fatalf("msgs = %d, want 4: %+v", len(msgs), msgs)
	}
	seg1, reply, seg2 := msgs[1], msgs[2], msgs[3]
	if seg1.Role != "assistant" || seg1.Reasoning != "想一" || len(seg1.Content) != 1 ||
		seg1.Content[0].Text != "先看" || len(seg1.ToolCalls) != 1 || seg1.ToolCalls[0].ID != "c1" {
		t.Fatalf("段1 = %+v", seg1)
	}
	if reply.Role != "tool" || reply.CallID != "c1" || reply.Content[0].Text != "pong" {
		t.Fatalf("tool 应答 = %+v", reply)
	}
	if seg2.Role != "assistant" || seg2.Reasoning != "想二" || len(seg2.Content) != 1 ||
		seg2.Content[0].Text != "答案" || len(seg2.ToolCalls) != 0 {
		t.Fatalf("段2 = %+v", seg2)
	}

	// echo_thinking=false：思考丢弃，正文分段不变。
	msgs, err = assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble(off): %v", err)
	}
	if len(msgs) != 4 || msgs[1].Reasoning != "" || msgs[3].Reasoning != "" {
		t.Fatalf("echo off = %+v", msgs)
	}
}

// TestAssemblePathNilResultPartSkipped D95：Result=nil 的工具分片（未执行/被取消）
// 不声明调用也不产出应答消息；纯 nil 分片节点整条不发。
func TestAssemblePathNilResultPartSkipped(t *testing.T) {
	c := convWithUser(t)
	user1 := c.Path()[1]
	asst := conversation.Message{
		ID: "A1", Parent: user1.ID, Role: conversation.RoleAssistant,
		Content: []conversation.Part{{Kind: conversation.PartTool, Tool: &conversation.ToolPart{
			CallID: "call_1", Name: "echo",
		}}},
	}
	if err := c.AppendCommitted(asst); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	// 仅 user；nil 分片节点无正文无调用，整条不发。
	if len(msgs) != 1 {
		t.Fatalf("msgs = %d, want 1: %+v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			t.Fatalf("nil 分片不应声明调用: %+v", m)
		}
		if m.Role == "tool" {
			t.Fatalf("nil 分片不应产出 tool 应答: %+v", m)
		}
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

// commitNode 手工提交组装态节点（persona/摘要等），ID 按树内序号生成避免冲突。
func commitNode(t *testing.T, c *conversation.Conversation, parent conversation.MessageID, role conversation.Role, text string) conversation.Message {
	t.Helper()
	m := conversation.Message{
		ID:      conversation.MessageID(fmt.Sprintf("n%d", len(c.Nodes)+1)),
		Parent:  parent,
		Role:    role,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: text}},
	}
	if err := c.AppendCommitted(m); err != nil {
		t.Fatalf("commit %s: %v", role, err)
	}
	return c.Nodes[m.ID]
}

// TestAssemblePathWatermarkDropsAboveSummary D21：persona 恒回传，摘要水位之上（persona 之外）不回传。
func TestAssemblePathWatermarkDropsAboveSummary(t *testing.T) {
	c := conversation.New(conversation.ID("w"), "w")
	root := conversation.MessageID(c.ID)
	persona := commitNode(t, c, root, conversation.RoleSystem, "人格")
	commitNode(t, c, persona.ID, conversation.RoleUser, "旧问题")
	commitNode(t, c, c.Head, conversation.RoleAssistant, "旧回答")
	sum := commitNode(t, c, c.Head, conversation.RoleSystem, "摘要内容")
	commitNode(t, c, sum.ID, conversation.RoleUser, "新问题")
	commitNode(t, c, c.Head, conversation.RoleAssistant, "新回答")

	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("msgs len = %d, want 4 (persona,摘要,新问题,新回答): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || msgs[0].Content[0].Text != "人格" {
		t.Fatalf("msgs[0] = %+v, want persona 恒回传", msgs[0])
	}
	if msgs[1].Role != "system" || msgs[1].Content[0].Text != "摘要内容" {
		t.Fatalf("msgs[1] = %+v, want 摘要", msgs[1])
	}
	if msgs[2].Content[0].Text != "新问题" || msgs[3].Content[0].Text != "新回答" {
		t.Fatalf("msgs[2:] = %+v", msgs[2:])
	}
}

// TestAssemblePathChainedSummariesKeepLatest 多次压缩链式吸收：只回传最新摘要。
func TestAssemblePathChainedSummariesKeepLatest(t *testing.T) {
	c := conversation.New(conversation.ID("w2"), "w")
	root := conversation.MessageID(c.ID)
	persona := commitNode(t, c, root, conversation.RoleSystem, "人格")
	commitNode(t, c, persona.ID, conversation.RoleUser, "q1")
	sum1 := commitNode(t, c, c.Head, conversation.RoleSystem, "摘要一")
	commitNode(t, c, sum1.ID, conversation.RoleUser, "q2")
	sum2 := commitNode(t, c, c.Head, conversation.RoleSystem, "摘要二")
	commitNode(t, c, sum2.ID, conversation.RoleUser, "q3")

	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("msgs len = %d, want 3 (人格,摘要二,q3): %+v", len(msgs), msgs)
	}
	if msgs[1].Content[0].Text != "摘要二" || msgs[2].Content[0].Text != "q3" {
		t.Fatalf("msgs = %+v, want 摘要一被吸收", msgs)
	}
}

// TestAssemblePathWatermarkWithoutPersona 无 persona 的会话：摘要即起点，其上全丢。
func TestAssemblePathWatermarkWithoutPersona(t *testing.T) {
	c := conversation.New(conversation.ID("w3"), "w")
	root := conversation.MessageID(c.ID)
	commitNode(t, c, root, conversation.RoleUser, "旧问题")
	commitNode(t, c, c.Head, conversation.RoleAssistant, "旧回答")
	sum := commitNode(t, c, c.Head, conversation.RoleSystem, "摘要")
	commitNode(t, c, sum.ID, conversation.RoleUser, "新问题")

	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[0].Content[0].Text != "摘要" ||
		msgs[1].Content[0].Text != "新问题" {
		t.Fatalf("msgs = %+v, want [摘要, 新问题]", msgs)
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
	msgs, err := assemblePath(context.Background(), c.Path(), nil, false)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("msgs = %+v, want 只剩首条 user", msgs)
	}
}
