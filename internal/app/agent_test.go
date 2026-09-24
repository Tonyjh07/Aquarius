package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func TestRunTextRound(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		withUsage(textStream("Hel", "lo"), conversation.Usage{InputTokens: 10, OutputTokens: 5}),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}

	path := c.Path()
	if len(path) != 3 {
		t.Fatalf("path len = %d, want 3 (root,user,assistant)", len(path))
	}
	if path[1].Role != conversation.RoleUser {
		t.Fatalf("path[1] = %+v, want user", path[1])
	}
	node := path[2]
	if node.Role != conversation.RoleAssistant || node.Parent != path[1].ID {
		t.Fatalf("node = %+v", node)
	}
	if len(node.Content) != 1 || node.Content[0].Text != "Hello" {
		t.Fatalf("content = %+v", node.Content)
	}
	if node.Outcome != conversation.OutcomeDone || node.Model != "test-model" {
		t.Fatalf("outcome/model = %q/%q", node.Outcome, node.Model)
	}
	if node.Usage.InputTokens != 10 || node.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", node.Usage)
	}
	if !node.CreatedAt.Equal(testTime) {
		t.Fatalf("CreatedAt = %v, want 固定时钟", node.CreatedAt)
	}
	if c.Head != node.ID {
		t.Fatalf("head = %s, want %s", c.Head, node.ID)
	}

	// 事件顺序：增量只进 UI（含流末尾用量分片），结束一次性提交。
	got := eventNames(rec.events)
	want := []string{"delta", "delta", "delta", "committed"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}

	// 请求装配：system 在首位 + 用户消息；关联 ID 即提交节点 ID。
	req := llm.requests[0]
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if req.Messages[1].Content[0].Text != "hi" || req.Model != "test-model" {
		t.Fatalf("request = %+v", req)
	}
	if deltaEv, ok := rec.events[0].(port.DeltaEvent); !ok || deltaEv.MessageID != node.ID {
		t.Fatalf("delta 关联 ID = %+v, want %s", rec.events[0], node.ID)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestRunToolRound(t *testing.T) {
	callDelta := port.Delta{ToolCalls: []port.ToolCallDelta{{
		Index: 0, ID: "call_1", Name: "echo", ArgsDelta: `{"m":`,
	}}}
	callDelta2 := port.Delta{ToolCalls: []port.ToolCallDelta{{Index: 0, ArgsDelta: "\"x\"}"}}}

	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: callDelta}, {delta: callDelta2}}},
		textStream("答案"),
	}}
	rec := &recorder{}
	runner := &fakeRunner{
		specs:   []tool.Spec{{Name: "echo", Description: "回声"}},
		results: map[tool.CallID]tool.Result{"call_1": {CallID: "call_1", OK: true, Output: "pong"}},
	}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}

	path := c.Path()
	if len(path) != 5 {
		t.Fatalf("path len = %d, want 5 (root/user/assistant/tool/assistant)", len(path))
	}
	asst, tnode, final := path[2], path[3], path[4]
	if asst.Role != conversation.RoleAssistant || len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant = %+v", asst)
	}
	if asst.ToolCalls[0].ID != "call_1" || asst.ToolCalls[0].Name != "echo" {
		t.Fatalf("call = %+v", asst.ToolCalls[0])
	}
	if string(asst.ToolCalls[0].Args) != `{"m":"x"}` {
		t.Fatalf("args 聚合 = %s", asst.ToolCalls[0].Args)
	}
	if tnode.Role != conversation.RoleTool || tnode.Parent != asst.ID {
		t.Fatalf("tool node = %+v", tnode)
	}
	if tnode.ToolResult == nil || !tnode.ToolResult.OK || tnode.ToolResult.Output != "pong" {
		t.Fatalf("tool result = %+v", tnode.ToolResult)
	}
	if final.Role != conversation.RoleAssistant || final.Content[0].Text != "答案" {
		t.Fatalf("final = %+v", final)
	}

	// 第二次请求带上了 assistant 调用与 tool 应答。
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(llm.requests))
	}
	msgs := llm.requests[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("second request msgs = %d, want 4", len(msgs))
	}
	last := msgs[3]
	if last.Role != "tool" || last.CallID != "call_1" || last.Content[0].Text != "pong" {
		t.Fatalf("tool msg = %+v", last)
	}
	if len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant msg = %+v", msgs[2])
	}

	got := eventNames(rec.events)
	want := "delta,delta,committed,tool_call,tool_result,delta,committed"
	if strings.Join(got, ",") != want {
		t.Fatalf("events = %v, want %s", got, want)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestRunToolErrorFeedsBack(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "call_e", Name: "boom"},
		}}}}},
		textStream("已恢复"),
	}}
	rec := &recorder{}
	runner := &fakeRunner{errs: map[tool.CallID]error{"call_e": errors.New("tool broke")}}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("工具失败不应中断 Turn: %v", err)
	}
	path := c.Path()
	if len(path) != 5 {
		t.Fatalf("path len = %d, want 5", len(path))
	}
	res := path[3].ToolResult
	if res == nil || res.OK || !strings.Contains(res.Err, "tool broke") {
		t.Fatalf("result = %+v, want OK=false 回填错误", res)
	}
	if path[4].Content[0].Text != "已恢复" {
		t.Fatalf("final = %+v", path[4])
	}
}

func TestRunGenerateErrorCommitsErrorNode(t *testing.T) {
	llm := &scriptLLM{t: t, genErr: errors.New("boom")}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)

	err := a.Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want 含 boom", err)
	}
	path := c.Path()
	if len(path) != 3 {
		t.Fatalf("path len = %d, want 3（error 占位节点已提交）", len(path))
	}
	node := path[2]
	if node.Outcome != conversation.OutcomeError || len(node.Content) != 0 || len(node.ToolCalls) != 0 {
		t.Fatalf("node = %+v, want 空内容 error 节点", node)
	}
	got := eventNames(rec.events)
	if strings.Join(got, ",") != "committed" {
		t.Fatalf("events = %v, want committed（错误经返回值上抛，ErrorEvent 由装配根发）", got)
	}
}

func TestRunStreamErrorCommitsPartialText(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{
			{delta: port.Delta{Text: "写到一半"}},
			{delta: port.Delta{ToolCalls: []port.ToolCallDelta{{Index: 0, ID: "call_x", Name: "n"}}}},
			{err: errors.New("conn reset")},
		}},
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)

	err := a.Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "conn reset") {
		t.Fatalf("err = %v", err)
	}
	node := c.Path()[2]
	if node.Outcome != conversation.OutcomeError {
		t.Fatalf("outcome = %q, want error", node.Outcome)
	}
	if len(node.Content) != 1 || node.Content[0].Text != "写到一半" {
		t.Fatalf("content = %+v", node.Content)
	}
	if len(node.ToolCalls) != 0 {
		t.Fatalf("半截工具调用不应进树: %+v", node.ToolCalls)
	}
	got := eventNames(rec.events)
	if strings.Join(got, ",") != "delta,delta,committed" {
		t.Fatalf("events = %v", got)
	}
}

func TestRunCancelCommitsCancelledAndReturnsNil(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{
			{delta: port.Delta{Text: "一半"}},
			{err: context.Canceled},
		}},
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("取消不应视为失败: %v", err)
	}
	node := c.Path()[2]
	if node.Outcome != conversation.OutcomeCancelled || node.Content[0].Text != "一半" {
		t.Fatalf("node = %+v, want cancelled + 部分文本", node)
	}
	got := eventNames(rec.events)
	if strings.Join(got, ",") != "delta,committed" {
		t.Fatalf("events = %v, want delta,committed（无 error 事件）", got)
	}
}

func TestRunMaxTurns(t *testing.T) {
	toolStep := func(id tool.CallID) *scriptStream {
		return &scriptStream{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: string(id), Name: "n"},
		}}}}}
	}
	llm := &scriptLLM{t: t, streams: []*scriptStream{toolStep("c1"), toolStep("c2")}}
	rec := &recorder{}
	runner := &fakeRunner{}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{MaxTurns: 2})
	c := newConv(t)

	err := a.Run(context.Background(), c)
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("err = %v, want ErrMaxTurns", err)
	}
	if len(runner.ran) != 2 {
		t.Fatalf("tool executions = %d, want 2", len(runner.ran))
	}
	if names := eventNames(rec.events); len(names) > 0 && names[len(names)-1] == "error" {
		t.Fatalf("events = %v, agent 不直接发 ErrorEvent（装配根统一上抛）", names)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestRunNormalizesMissingCallIDAndArgs(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, Name: "echo"}, // 个别兼容服务不回传 ID/参数分片
		}}}}},
		textStream("done"),
	}}
	rec := &recorder{}
	runner := &fakeRunner{}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	asst := c.Path()[2]
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID == "" {
		t.Fatalf("call = %+v, want 补齐 ID", asst.ToolCalls)
	}
	if string(asst.ToolCalls[0].Args) != "{}" {
		t.Fatalf("args = %s, want {}", asst.ToolCalls[0].Args)
	}
	tnode := c.Path()[3]
	if tnode.ToolResult.CallID != asst.ToolCalls[0].ID {
		t.Fatalf("result call id = %s, want %s", tnode.ToolResult.CallID, asst.ToolCalls[0].ID)
	}
}

func TestRunEmptyConversationRejected(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := conversation.New(conversation.ID("empty"), "e")

	if err := a.Run(context.Background(), c); err == nil {
		t.Fatal("空会话应报错")
	}
	if len(llm.requests) != 0 || len(rec.events) != 0 {
		t.Fatalf("不应发起生成或发事件: %d/%d", len(llm.requests), len(rec.events))
	}
}

func TestNewValidatesDeps(t *testing.T) {
	if _, err := New(Deps{}, Config{Model: "m"}); err == nil {
		t.Fatal("缺少端口依赖应报错")
	}
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	if _, err := New(Deps{LLM: llm, UI: rec, IDs: &seqIDs{}, Clock: fixedClock{testTime}}, Config{}); err == nil {
		t.Fatal("model 为空应报错")
	}
}

// TestCallAssemblerIndexOrder 验证 Index 分片聚合与乱序到达的升序输出。
func TestCallAssemblerIndexOrder(t *testing.T) {
	var a callAssembler
	a.add([]port.ToolCallDelta{{Index: 1, ID: "b", Name: "nb", ArgsDelta: `{"x"`}})
	a.add([]port.ToolCallDelta{{Index: 0, ID: "a", Name: "na"}})
	a.add([]port.ToolCallDelta{{Index: 1, ArgsDelta: ":1}"}})
	a.add([]port.ToolCallDelta{{Index: 0, ArgsDelta: `{}`}})
	got := a.finalize()
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("finalize = %+v, want 按 Index 升序", got)
	}
	if got[1].Name != "nb" || string(got[1].Args) != `{"x":1}` {
		t.Fatalf("聚合结果 = %+v", got[1])
	}
}

// TestBuildRequestTools 无工具时不带 tools；有工具时透传 Specs。
func TestBuildRequestTools(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("x")}}
	rec := &recorder{}
	specs := []tool.Spec{{Name: "echo", Schema: json.RawMessage(`{"type":"object"}`)}}
	a := newAgent(t, llm, rec, Deps{Tools: &fakeRunner{specs: specs}}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(llm.requests[0].Tools) != 1 || llm.requests[0].Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", llm.requests[0].Tools)
	}
}
