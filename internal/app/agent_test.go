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

// TestRunToolFailureFeedsBack 工具级失败（Runner 转 OK=false 结果）照常回填、
// 不中断 Turn，模型拿到错误自行恢复（§10）。
func TestRunToolFailureFeedsBack(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "call_e", Name: "boom"},
		}}}}},
		textStream("已恢复"),
	}}
	rec := &recorder{}
	runner := &fakeRunner{results: map[tool.CallID]tool.Result{
		"call_e": {OK: false, Err: "tool broke"},
	}}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("工具级失败不应中断 Turn: %v", err)
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

// TestRunInfraErrorAbortsTurn 装配级错误（确认器缺失等基础设施故障）快速失败上抛：
// 不再发起后续生成（不空转 MaxTurns），剩余调用补 OK=false 中断结果——
// assistant.tool_calls 与 tool 结果一一配对，树仍满足不变量可继续装配。
func TestRunInfraErrorAbortsTurn(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "call_0", Name: "alpha"},
			{Index: 1, ID: "call_1", Name: "beta"},
		}}}}},
		// 只给一个脚本：若发生第二次 Generate，scriptLLM 会 t.Fatalf。
	}}
	rec := &recorder{}
	runner := &fakeRunner{errs: map[tool.CallID]error{"call_0": errors.New("未配置 Confirmer")}}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	err := a.Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "未配置 Confirmer") {
		t.Fatalf("err = %v, want 装配级错误快速上抛", err)
	}
	if !strings.Contains(err.Error(), "执行工具 alpha") {
		t.Fatalf("err = %v, want 指明首个失败调用", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d, want 1（不应空转生成）", len(llm.requests))
	}
	path := c.Path()
	if len(path) != 5 { // root + persona + assistant(2 calls) + 2 个中断结果
		t.Fatalf("path len = %d, want 5", len(path))
	}
	for _, idx := range []int{3, 4} {
		res := path[idx].ToolResult
		if res == nil || res.OK || !strings.Contains(res.Err, "未配置 Confirmer") {
			t.Fatalf("tool result = %+v, want OK=false 中断结果", res)
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := eventNames(rec.events)
	want := "delta,committed,tool_call,tool_call,tool_result,tool_result"
	if strings.Join(got, ",") != want {
		t.Fatalf("events = %v, want %s", got, want)
	}
}

// TestRunCancelDuringToolReturnsNil 工具阶段取消：补齐中断结果后返回 nil
// （与生成阶段取消一致，§10——不作为错误呈现），树仍满足不变量，不再发起后续生成。
func TestRunCancelDuringToolReturnsNil(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "call_c", Name: "slow"},
		}}}}},
	}}
	rec := &recorder{}
	runner := &fakeRunner{errs: map[tool.CallID]error{"call_c": context.Canceled}}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("工具阶段取消应返回 nil: %v", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(llm.requests))
	}
	path := c.Path()
	if len(path) != 4 { // root + persona + assistant + 中断结果
		t.Fatalf("path len = %d, want 4", len(path))
	}
	res := path[3].ToolResult
	if res == nil || res.OK || !strings.Contains(res.Err, "context canceled") {
		t.Fatalf("result = %+v", res)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestRunInfraErrorAfterFirstTool 首个调用成功、第二个装配级失败（i>0 分支）：
// 只为剩余调用补中断结果，已成功的照常入树，事件序成对。
func TestRunInfraErrorAfterFirstTool(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "call_ok", Name: "alpha"},
			{Index: 1, ID: "call_bad", Name: "beta"},
		}}}}},
	}}
	rec := &recorder{}
	runner := &fakeRunner{errs: map[tool.CallID]error{"call_bad": errors.New("未配置 Confirmer")}}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	c := newConv(t)

	err := a.Run(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "执行工具 beta") {
		t.Fatalf("err = %v", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(llm.requests))
	}
	path := c.Path()
	if len(path) != 5 {
		t.Fatalf("path len = %d, want 5", len(path))
	}
	if path[3].ToolResult == nil || !path[3].ToolResult.OK {
		t.Fatalf("首个成功结果 = %+v", path[3].ToolResult)
	}
	if path[4].ToolResult == nil || path[4].ToolResult.OK ||
		!strings.Contains(path[4].ToolResult.Err, "未配置 Confirmer") {
		t.Fatalf("中断结果 = %+v", path[4].ToolResult)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	want := "delta,committed,tool_call,tool_result,tool_call,tool_result"
	if got := strings.Join(eventNames(rec.events), ","); got != want {
		t.Fatalf("events = %v, want %s", got, want)
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

// TestAgentCompactSummarizesAndSetsWatermark D21 手动轨：摘要 system 节点入树、水位生效、记账完整。
func TestAgentCompactSummarizesAndSetsWatermark(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		withUsage(textStream("这是摘要"), conversation.Usage{InputTokens: 30, OutputTokens: 12}),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t) // root + user "hi"
	prevHead := c.Head

	node, absorbed, err := a.Compact(context.Background(), c)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if node.Role != conversation.RoleSystem || node.Content[0].Text != "这是摘要" {
		t.Fatalf("node = %+v", node)
	}
	if node.Parent != prevHead {
		t.Fatalf("parent = %s, want %s", node.Parent, prevHead)
	}
	if node.Usage.InputTokens != 30 || node.Usage.OutputTokens != 12 {
		t.Fatalf("usage = %+v", node.Usage)
	}
	if node.Outcome != conversation.OutcomeDone || node.Model != "test-model" {
		t.Fatalf("outcome/model = %q/%q", node.Outcome, node.Model)
	}
	if absorbed != 1 {
		t.Fatalf("absorbed = %d, want 1", absorbed)
	}
	if c.Head != node.ID {
		t.Fatalf("head = %s, want 摘要节点", c.Head)
	}

	// 请求：压缩指令在首位，历史紧随，尾部输出提示。
	req := llm.requests[0]
	if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].Content[0].Text, "压缩") {
		t.Fatalf("messages[0] = %+v, want 压缩指令", req.Messages[0])
	}
	if len(req.Messages) != 3 || req.Messages[1].Role != "user" || req.Messages[1].Content[0].Text != "hi" {
		t.Fatalf("messages = %+v, want [指令, hi, 提示]", req.Messages)
	}
	last := req.Messages[2]
	if last.Role != "user" || !strings.Contains(last.Content[0].Text, "摘要") {
		t.Fatalf("last = %+v, want 输出提示", last)
	}

	// 水位生效：再装配只剩摘要（user 被裁掉）。
	msgs, err := assemblePath(context.Background(), c.Path(), nil)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "system" || msgs[0].Content[0].Text != "这是摘要" {
		t.Fatalf("msgs = %+v, want 水位裁剪后仅 [摘要]", msgs)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if names := eventNames(rec.events); names[len(names)-1] != "committed" {
		t.Fatalf("events = %v, want 以 committed 收尾", names)
	}
}

// TestAgentCompactNothingToCompact 空会话 / 仅 persona / 重复压缩均短路，不花生成费用。
func TestAgentCompactNothingToCompact(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})

	empty := conversation.New(conversation.ID("e"), "e")
	if _, _, err := a.Compact(context.Background(), empty); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("empty = %v, want ErrNothingToCompact", err)
	}

	c := conversation.New(conversation.ID("p"), "p")
	commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleSystem, "人格")
	if _, _, err := a.Compact(context.Background(), c); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("persona only = %v", err)
	}

	// 已有摘要且其后无新内容：重复压缩短路。
	c2 := conversation.New(conversation.ID("c2"), "c2")
	u := commitNode(t, c2, conversation.MessageID(c2.ID), conversation.RoleUser, "q")
	commitNode(t, c2, u.ID, conversation.RoleSystem, "旧摘要")
	if _, _, err := a.Compact(context.Background(), c2); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("double compact = %v", err)
	}

	if len(llm.requests) != 0 {
		t.Fatalf("不应发起生成: %d", len(llm.requests))
	}
}

// TestAgentCompactFailureLeavesTreeIntact 生成/断流失败：树无损、不发 Committed。
func TestAgentCompactFailureLeavesTreeIntact(t *testing.T) {
	cases := []struct {
		name string
		llm  *scriptLLM
	}{
		{"generate", &scriptLLM{t: nil, genErr: errors.New("boom")}},
		{"mid-stream", &scriptLLM{t: nil, streams: []*scriptStream{{steps: []scriptStep{
			{delta: port.Delta{Text: "半截"}},
			{err: errors.New("conn reset")},
		}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.llm.t = t
			rec := &recorder{}
			a := newAgent(t, tc.llm, rec, Deps{}, Config{})
			c := newConv(t)
			before := len(c.Nodes)

			_, _, err := a.Compact(context.Background(), c)
			if err == nil {
				t.Fatal("want error")
			}
			if len(c.Nodes) != before {
				t.Fatalf("nodes = %d, want 树无损 %d", len(c.Nodes), before)
			}
			for _, ev := range rec.events {
				if _, ok := ev.(port.CommittedEvent); ok {
					t.Fatal("失败不应发 CommittedEvent")
				}
			}
		})
	}
}

// TestBuildRequestConfigFallbackWithoutPersona 无 persona 但有水位：config system 兜底注入 + 摘要照常裁剪。
func TestBuildRequestConfigFallbackWithoutPersona(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := conversation.New(conversation.ID("np"), "np")
	u := commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleUser, "旧问题")
	commitNode(t, c, u.ID, conversation.RoleAssistant, "旧回答")
	sum := commitNode(t, c, c.Head, conversation.RoleSystem, "摘要")
	commitNode(t, c, sum.ID, conversation.RoleUser, "新问题")

	req, err := a.buildRequest(context.Background(), c)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (config system, 摘要, 新问题): %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content[0].Text != defaultSystem {
		t.Fatalf("messages[0] = %+v, want config/内置 system 兜底", req.Messages[0])
	}
	if req.Messages[1].Role != "system" || req.Messages[1].Content[0].Text != "摘要" {
		t.Fatalf("messages[1] = %+v, want 摘要水位", req.Messages[1])
	}
	if req.Messages[2].Role != "user" || req.Messages[2].Content[0].Text != "新问题" {
		t.Fatalf("messages[2] = %+v", req.Messages[2])
	}
}

// TestBuildRequestTreePersonaReplacesConfig D20：树内 persona 在位时不再注入 config 兜底 system。
func TestBuildRequestTreePersonaReplacesConfig(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := conversation.New(conversation.ID("p"), "p")
	persona := conversation.Message{
		ID:      "P1",
		Parent:  conversation.MessageID(c.ID),
		Role:    conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "树内人格"}},
	}
	if err := c.AppendCommitted(persona); err != nil {
		t.Fatalf("commit persona: %v", err)
	}
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	req, err := a.buildRequest(context.Background(), c)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (persona system + user)", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content[0].Text != "树内人格" {
		t.Fatalf("messages[0] = %+v, want 树内 persona", req.Messages[0])
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
