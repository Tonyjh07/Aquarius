package app

// session_inject_test.go D150（内核侧）：注入端口入队/校验、两个排空点、`/inject` 三态、
// 轮内注入不打断当前轮、装配投影矩阵（投影矩阵另见 prompt_test.go）。

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// injectableSession 造一个会话：可经 Inject 入队，也可让假工具在轮内调用 Inject。
func injectableSession(t *testing.T, store port.ConversationStore, streams ...*scriptStream) (*Session, *scriptLLM, *recorder) {
	t.Helper()
	s, llm, rec := newTestSession(t, store, streams...)
	return s, llm, rec
}

// TestInjectRejectsBadParts Inject 入队前校验（D150）：思考/工具分片与空内容当场拒，
// 坏包不占队列、不落树。
func TestInjectRejectsBadParts(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore())
	before := len(s.Current().Nodes)

	cases := []struct {
		name string
		in   port.Injection
	}{
		{"empty", port.Injection{Source: "user"}},
		{"thinking", port.Injection{Source: "tool:think", Parts: []conversation.Part{{Kind: conversation.PartThinking, Text: "t"}}}},
		{"tool", port.Injection{Source: "tool:think", Parts: []conversation.Part{{Kind: conversation.PartTool, Tool: &conversation.ToolPart{CallID: "c", Name: "n"}}}}},
	}
	for _, tc := range cases {
		if err := s.Inject(context.Background(), tc.in); err == nil {
			t.Fatalf("%s：坏包应被拒", tc.name)
		}
	}
	if len(s.Current().Nodes) != before {
		t.Fatal("坏包不得改树")
	}
	if len(s.injectCh) != 0 {
		t.Fatalf("坏包不得占队列，len = %d", len(s.injectCh))
	}
}

// TestInjectQueueNonBlockingQueueFull 队列满时 Inject 立即报错、不阻塞（§10 并发口径）。
func TestInjectQueueNonBlockingQueueFull(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore())
	in := port.Injection{Source: "user", Parts: []conversation.Part{{Kind: conversation.PartText, Text: "x"}}}
	for i := 0; i < injectQueueCap; i++ {
		if err := s.Inject(context.Background(), in); err != nil {
			t.Fatalf("第 %d 条入队失败: %v", i, err)
		}
	}
	if err := s.Inject(context.Background(), in); err == nil {
		t.Fatal("队列满应报错")
	}
}

// TestInjectDrainAtHandleEntry 排空点①（Handle 入口）：入队 → Handle（无参命令）落地注入，
// Trigger 记 false（只落地不起轮），且不花模型调用。
func TestInjectDrainAtHandleEntry(t *testing.T) {
	s, llm, _ := injectableSession(t, newMemStore())
	if err := s.Inject(context.Background(), port.Injection{
		Source: "job:j1", Parts: []conversation.Part{{Kind: conversation.PartText, Text: "任务完成"}},
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	// job: 默认该触发，但排空点①只落地（Trigger 记实际发生 = false）。
	if _, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "list"}}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	inject := lastNodeOfRole(t, s.Current(), conversation.RoleInject)
	if inject.Source != "job:j1" || inject.Trigger {
		t.Fatalf("inject 节点 = %+v, want source=job:j1 trigger=false", inject)
	}
	// 信任位随节点落树（D170）：没声明就是外部内容，装配时会被围栏。
	if inject.Trust != conversation.TrustExternal {
		t.Fatalf("Trust = %q, want 零值/外部（调用方未声明）", inject.Trust)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("llm requests = %d, want 0（排空点①不起轮）", len(llm.requests))
	}
}

// TestInjectDrainAtTurnEndWithFakeTool 排空点② + 轮内注入不打断当前轮：假工具在轮到它的
// 那一刻经 Inject 入队，注入落在**该轮 assistant 节点之后**，且本轮请求里不含注入内容。
func TestInjectDrainAtTurnEndWithFakeTool(t *testing.T) {
	s, llm, _ := injectableSession(t, newMemStore(), toolCallStream("call_1", "probe"), textStream("收场"))
	holder := &injectTool{sess: s}
	newSetTools(t, s, holder)

	if _, err := s.Handle(context.Background(), port.UserInput{Text: "跑个任务"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	path := s.Current().Path()
	// [root persona user assistant(turn) inject]——注入落在轮末的 assistant 之后。
	last := path[len(path)-1]
	if last.Role != conversation.RoleInject || last.Source != "tool:probe" {
		t.Fatalf("last = %+v (%v)，want 轮末注入", last, roles(path))
	}
	if last.Parent != path[len(path)-2].ID {
		t.Fatalf("注入的父应为该轮 assistant 节点")
	}
	if last.Trigger {
		t.Fatal("轮内注入 Trigger 应记 false（未起响应）")
	}
	// 本轮第二个请求（工具后收场轮）里不含注入文本——注入在轮末才落地。
	for _, m := range llm.requests[len(llm.requests)-1].Messages {
		for _, p := range m.Content {
			if strings.Contains(p.Text, "轮内注入内容") {
				t.Fatalf("本轮请求不应含轮内注入: %+v", m)
			}
		}
	}
	if err := s.Current().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestInjectCommandTriggersTurn /inject 缺省（来源 user）= 触发：落地触发型注入 + 跑一轮，
// 产物是该注入节点的 assistant 子节点；清屏回放。
func TestInjectCommandTriggersTurn(t *testing.T) {
	store := newMemStore()
	s, llm, rec := injectableSession(t, store, textStream("收到"), textStream("收到注入"))
	s.Handle(context.Background(), port.UserInput{Text: "hi"})

	out, err := handleCmd(s, "inject", "补充一条背景")
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if !strings.Contains(out, "已注入一条 手动 上下文") || !strings.Contains(out, "已触发响应") {
		t.Fatalf("out = %q", out)
	}
	path := s.Current().Path()
	// [root persona user assistant inject assistant]
	if len(path) != 6 {
		t.Fatalf("path = %v，want 6 段", roles(path))
	}
	inject := path[4]
	if inject.Role != conversation.RoleInject || !inject.Trigger || inject.Source != "user" {
		t.Fatalf("注入 = %+v, want user + 已触发", inject)
	}
	// 手动注入是用户自己的动作 → 宿主信任（D170），装配为 user 且不套围栏。
	if inject.Trust != conversation.TrustHost {
		t.Fatalf("Trust = %q, want host", inject.Trust)
	}
	if path[5].Role != conversation.RoleAssistant || path[5].Parent != inject.ID {
		t.Fatalf("产物 = %+v, want 注入的子节点", path[5])
	}
	if len(llm.requests) != 2 {
		t.Fatalf("llm requests = %d, want 2（首轮 + 注入轮）", len(llm.requests))
	}
	names := eventNames(rec.events)
	if !containsSeq(names, "clear", "notice", "history") {
		t.Fatalf("应清屏回放: %v", names)
	}
	if saved := store.convs[s.Current().ID]; saved == nil || len(saved.Nodes) != 6 {
		t.Fatalf("落盘 = %+v", saved)
	}
}

// TestInjectCommandNoTrigger /inject --no-trigger：只落地、不起响应，不花模型调用。
func TestInjectCommandNoTrigger(t *testing.T) {
	s, llm, _ := injectableSession(t, newMemStore(), textStream("收到"))
	s.Handle(context.Background(), port.UserInput{Text: "hi"})
	baseReqs := len(llm.requests)

	out, err := handleCmd(s, "inject", "--no-trigger", "只当背景")
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if !strings.Contains(out, "未触发响应") {
		t.Fatalf("out = %q", out)
	}
	last := s.Current().Path()[len(s.Current().Path())-1]
	if last.Role != conversation.RoleInject || last.Trigger {
		t.Fatalf("注入 = %+v, want 未触发", last)
	}
	if len(llm.requests) != baseReqs {
		t.Fatalf("--no-trigger 不应起轮：requests = %d, want %d", len(llm.requests), baseReqs)
	}
}

// TestInjectCommandSourceDefaults 来源默认表（D150）：tool: 默认不触发、job: 默认触发；
// 显式 --no-trigger 覆盖 job: 的默认触发。
func TestInjectCommandSourceDefaults(t *testing.T) {
	// tool: 默认不触发。
	s, llm, _ := injectableSession(t, newMemStore(), textStream("a"))
	s.Handle(context.Background(), port.UserInput{Text: "hi"})
	before := len(llm.requests)
	if _, err := handleCmd(s, "inject", "--source", "tool:think", "工具上下文"); err != nil {
		t.Fatalf("inject tool: %v", err)
	}
	last := s.Current().Path()[len(s.Current().Path())-1]
	if last.Source != "tool:think" || last.Trigger || len(llm.requests) != before {
		t.Fatalf("tool 注入 = %+v, requests %d，want 不触发", last, len(llm.requests))
	}

	// job: 默认触发。
	s2, llm2, _ := injectableSession(t, newMemStore(), textStream("a"), textStream("b"))
	s2.Handle(context.Background(), port.UserInput{Text: "hi"})
	out, err := handleCmd(s2, "inject", "--source", "job:j1", "任务完成")
	if err != nil {
		t.Fatalf("inject job: %v", err)
	}
	if !strings.Contains(out, "已触发响应") {
		t.Fatalf("out = %q", out)
	}
	path := s2.Current().Path()
	if path[len(path)-2].Source != "job:j1" || !path[len(path)-2].Trigger {
		t.Fatalf("job 注入 = %+v, want 触发", path[len(path)-2])
	}
	if len(llm2.requests) != 2 {
		t.Fatalf("requests = %d, want 2（job 触发轮）", len(llm2.requests))
	}

	// 显式 --no-trigger 覆盖 job: 默认。
	s3, llm3, _ := injectableSession(t, newMemStore(), textStream("a"))
	s3.Handle(context.Background(), port.UserInput{Text: "hi"})
	before3 := len(llm3.requests)
	if _, err := handleCmd(s3, "inject", "--source", "job:j1", "--no-trigger", "只记录"); err != nil {
		t.Fatalf("inject job --no-trigger: %v", err)
	}
	last3 := s3.Current().Path()[len(s3.Current().Path())-1]
	if last3.Trigger || len(llm3.requests) != before3 {
		t.Fatalf("--no-trigger 应覆盖默认: %+v requests %d", last3, len(llm3.requests))
	}
}

// TestInjectCommandUsageAndParse /inject 用法错误与参数解析（--source 缺参、空文本 →
// 报用法；选项顺序自由）。
func TestInjectCommandUsageAndParse(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore())
	if _, err := handleCmd(s, "inject"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("空参 err = %v", err)
	}
	if _, err := handleCmd(s, "inject", "--source"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("--source 缺参 err = %v", err)
	}
	if _, err := handleCmd(s, "inject", "--no-trigger"); err == nil || !strings.Contains(err.Error(), "用法") {
		t.Fatalf("只有选项无文本 err = %v", err)
	}
	src, nt, text, err := parseInjectArgs([]string{"前置文本", "--source", "tool:x", "--no-trigger"})
	if err != nil || src != "tool:x" || !nt || text != "前置文本" {
		t.Fatalf("parse = %q/%v/%q/%v", src, nt, text, err)
	}
	src, nt, text, err = parseInjectArgs([]string{"--no-trigger", "多", "词", "文本"})
	if err != nil || src != "user" || !nt || text != "多 词 文本" {
		t.Fatalf("parse 多词 = %q/%v/%q/%v", src, nt, text, err)
	}
}

// TestInjectCommandInHelpAndCatalog /inject 入 /help 与命令清单（单一事实源）。
func TestInjectCommandInHelpAndCatalog(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore())
	out, err := s.Handle(context.Background(), port.UserInput{Command: &port.Command{Name: "help"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/inject [--source <名>] [--no-trigger] <文本>") {
		t.Fatalf("help 缺 /inject:\n%s", out)
	}
	found := false
	for _, ci := range s.Commands() {
		if ci.Names[0] == "inject" {
			found = true
		}
	}
	if !found {
		t.Fatal("命令清单缺 /inject")
	}
}

// ---------------------------------------------------------------------------
// 测试替身与助手
// ---------------------------------------------------------------------------

// injectTool 假工具：执行时经持有会话的 Inject 端口入队一条注入（模拟工具轮内注入）。
type injectTool struct {
	sess *Session
}

func (r *injectTool) Specs(context.Context) ([]tool.Spec, error) {
	return []tool.Spec{{Name: "probe", Description: "探针"}}, nil
}

func (r *injectTool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if r.sess != nil {
		_ = r.sess.Inject(ctx, port.Injection{
			Source: "tool:probe",
			Parts:  []conversation.Part{{Kind: conversation.PartText, Text: "轮内注入内容"}},
		})
	}
	return tool.Result{CallID: call.ID, OK: true, Output: "探测完成"}, nil
}

// toolCallStream 造一个只发一次工具调用的脚本流（参数为空对象）。
func toolCallStream(id, name string) *scriptStream {
	return &scriptStream{steps: []scriptStep{
		{delta: port.Delta{ToolCalls: []port.ToolCallDelta{{Index: 0, ID: id, Name: name, ArgsDelta: "{}"}}}},
	}}
}

// newSetTools 替换会话内 Agent 的工具执行器（给轮内注入测试挂上持有会话的假工具）。
// 只改测试私有字段：app 内测试同包可直达（生产路径的工具在构造时定死，不进产品代码）。
func newSetTools(t *testing.T, s *Session, tr port.ToolRunner) {
	t.Helper()
	s.agent.tools = tr
}

// lastNodeOfRole 取 Path 上最后一个指定角色的节点。
func lastNodeOfRole(t *testing.T, c *conversation.Conversation, role conversation.Role) conversation.Message {
	t.Helper()
	path := c.Path()
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Role == role {
			return path[i]
		}
	}
	t.Fatalf("no node with role %s in path %v", role, roles(path))
	return conversation.Message{}
}

// containsSeq 判断 names 中是否存在按序出现的相邻子序列。
func containsSeq(names []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(names); i++ {
		ok := true
		for j, s := range seq {
			if names[i+j] != s {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
