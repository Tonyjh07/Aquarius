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

// ctxProbeRunner 记录工具执行上下文里能否拿到会话 ID（port）与会话对象（app 内部）。
type ctxProbeRunner struct {
	sid    conversation.ID
	sidOK  bool
	convOK bool
	specs  []tool.Spec
}

func (r *ctxProbeRunner) Specs(context.Context) ([]tool.Spec, error) { return r.specs, nil }

func (r *ctxProbeRunner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	r.sid, r.sidOK = port.SessionIDFrom(ctx)
	_, r.convOK = conversationFrom(ctx)
	return tool.Result{CallID: call.ID, OK: true, Output: "ok"}, nil
}

// TestRunInjectsSessionContext 工具执行上下文带会话 ID（port）与会话对象（app 内部）。
func TestRunInjectsSessionContext(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "c1", Name: "probe"},
		}}}}},
		textStream("done"),
	}}
	rec := &recorder{}
	probe := &ctxProbeRunner{}
	a := newAgent(t, llm, rec, Deps{Tools: probe}, Config{})
	c := newConv(t) // id = "conv"

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !probe.sidOK || probe.sid != conversation.ID("conv") {
		t.Fatalf("SessionID = %q/%v, want conv", probe.sid, probe.sidOK)
	}
	if !probe.convOK {
		t.Fatal("会话对象未注入（context_compact 依赖）")
	}
}

// TestRunMemoryInjection 记忆索引（全局+当前会话白名单）与会话记忆全文注入首条 system。
func TestRunMemoryInjection(t *testing.T) {
	mem := newFakeMemory()
	mem.docs[port.GlobalMemoryDoc] = "## 用户偏好\n喜欢简洁回答"
	mem.docs[port.SessionMemoryDoc("conv")] = "本会话要点：先给结论"
	mem.docs[port.SessionMemoryDoc("other")] = "别的会话，不该出现"

	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("ok")}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{Memory: mem}, Config{})
	c := newConv(t)

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	sys := llm.requests[0].Messages[0]
	if sys.Role != "system" {
		t.Fatalf("首条 = %+v", sys)
	}
	var text strings.Builder
	for _, p := range sys.Content {
		text.WriteString(p.Text)
	}
	got := text.String()
	for _, want := range []string{"记忆索引", port.GlobalMemoryDoc, "用户偏好",
		port.SessionMemoryDoc("conv"), "本会话要点"} {
		if !strings.Contains(got, want) {
			t.Fatalf("system 缺 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "别的会话") {
		t.Fatalf("泄漏其他会话记忆:\n%s", got)
	}
	// 树内 persona/节点未被改写（注入只发生在装配副本）。
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestRunMemoryFailureSilent 记忆读取失败静默跳过，不阻断对话。
func TestRunMemoryFailureSilent(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{textStream("ok")}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{Memory: &brokenMemory{}}, Config{})
	c := newConv(t)
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("记忆失败不应阻断: %v", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("requests = %d", len(llm.requests))
	}
}

type brokenMemory struct{}

func (brokenMemory) Index(context.Context) ([]port.MemoryIndexEntry, error) {
	return nil, errors.New("disk broken")
}
func (brokenMemory) Read(context.Context, string) (port.MemoryDoc, error) {
	return port.MemoryDoc{}, errors.New("disk broken")
}
func (brokenMemory) Write(context.Context, port.MemoryDoc) error { return errors.New("disk broken") }
func (brokenMemory) Remove(context.Context, string) error        { return errors.New("disk broken") }
func (brokenMemory) Search(context.Context, string) ([]port.MemoryHit, error) {
	return nil, errors.New("disk broken")
}

// TestRunAutoCompact 触发阈值 → 先压缩（轨2）再生成：摘要入树、NoticeEvent、水位裁掉长文。
func TestRunAutoCompact(t *testing.T) {
	longText := strings.Repeat("长", 300) // CJK ≈200 tokens ≫ 阈值
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		textStream("这是自动摘要"),
		textStream("最终回答"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{MaxContextTokens: 100, CompactThreshold: 0.7})
	c := conversation.New(conversation.ID("ac"), "ac")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: longText}}); err != nil {
		t.Fatal(err)
	}

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2（压缩 + 生成）", len(llm.requests))
	}
	// 请求0 = 压缩指令。
	if txt := reqText(llm.requests[0]); !strings.Contains(txt, "压缩") {
		t.Fatalf("req0 非压缩请求: %.100s", txt)
	}
	// 请求1 = 水位生效：含摘要、不含被压长文。
	txt1 := reqText(llm.requests[1])
	if !strings.Contains(txt1, "这是自动摘要") {
		t.Fatalf("req1 缺摘要: %.200s", txt1)
	}
	if strings.Contains(txt1, longText) {
		t.Fatal("req1 仍带被压缩长文（水位未生效）")
	}
	// 树：root,user,summary,assistant；摘要为 system 水位节点。
	path := c.Path()
	if len(path) != 4 || path[2].Role != conversation.RoleSystem || path[2].Content[0].Text != "这是自动摘要" {
		t.Fatalf("path roles = %+v", roles(path))
	}
	if path[3].Content[0].Text != "最终回答" {
		t.Fatalf("final = %+v", path[3])
	}
	if !hasNotice(rec.events, "自动压缩") {
		t.Fatalf("缺 NoticeEvent: %v", eventNames(rec.events))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestRunAutoCompactFailureTrims 压缩失败 → 回退最旧裁剪 + 提示省略条数，Turn 照常。
func TestRunAutoCompactFailureTrims(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{err: errors.New("摘要服务挂了")}}}, // 压缩请求失败
		textStream("仍然回答"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{MaxContextTokens: 100, CompactThreshold: 0.7})
	c := conversation.New(conversation.ID("af"), "af")
	// 4 条中等消息 → 估算超阈值，裁剪有操作空间。
	for i := 0; i < 4; i++ {
		if _, err := c.Append(conversation.RoleUser,
			[]conversation.Part{{Kind: conversation.PartText, Text: strings.Repeat("问", 60)}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("压缩失败不应中断 Turn: %v", err)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(llm.requests))
	}
	// 树无摘要节点（压缩失败树无损）。
	for _, m := range c.Path() {
		if m.Role == conversation.RoleSystem && m.ID != conversation.MessageID(c.ID) {
			// 本树无 persona，出现 system 即摘要
			t.Fatalf("失败不应入摘要: %+v", m)
		}
	}
	// 请求1 被裁剪：消息数少于原树路径数。
	if len(llm.requests[1].Messages) >= len(c.Path()) {
		t.Fatalf("req1 未裁剪: %d msgs vs path %d", len(llm.requests[1].Messages), len(c.Path()))
	}
	if !hasNotice(rec.events, "自动压缩失败") {
		t.Fatalf("缺失败 NoticeEvent: %v", eventNames(rec.events))
	}
	if !hasNotice(rec.events, "已省略") {
		t.Fatalf("缺省略条数提示: %v", eventNames(rec.events))
	}
	if path := c.Path(); path[len(path)-1].Content[0].Text != "仍然回答" {
		t.Fatalf("final = %+v", path[len(path)-1])
	}
}

// TestRunAutoCompactOncePerRun 每次 Run 至多自动压缩一次（多轮工具循环不反复尝试）。
func TestRunAutoCompactOncePerRun(t *testing.T) {
	toolStep := func(id string) *scriptStream {
		return &scriptStream{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: id, Name: "n"},
		}}}}}
	}
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{err: errors.New("压缩失败")}}}, // 第一次尝试失败
		toolStep("c1"),
		textStream("答案"),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{Tools: &fakeRunner{}},
		Config{MaxContextTokens: 100, CompactThreshold: 0.7})
	c := conversation.New(conversation.ID("ao"), "ao")
	// 单条巨大消息：裁剪保底（至少留 1 条非 system）压不下估算 → 每轮都"够阈值"。
	if _, err := c.Append(conversation.RoleUser,
		[]conversation.Part{{Kind: conversation.PartText, Text: strings.Repeat("文", 2000)}}); err != nil {
		t.Fatal(err)
	}

	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	compactReqs := 0
	for _, req := range llm.requests {
		if strings.Contains(reqText(req), "你是上下文压缩器") {
			compactReqs++
		}
	}
	if compactReqs != 1 {
		t.Fatalf("压缩请求 = %d, want 1（每次 Run 至多一次）", compactReqs)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(llm.requests))
	}
}

// TestContextCompactToolRound 模型经 context_compact 触发压缩（轨3）：
// 摘要 mid-turn 入树、tool 结果回填、下一次装配水位生效（失联 tool 内联标注）。
func TestContextCompactToolRound(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{{delta: port.Delta{ToolCalls: []port.ToolCallDelta{
			{Index: 0, ID: "cc1", Name: "context_compact"},
		}}}}},
		textStream("自答摘要"),
		textStream("压缩后的回答"),
	}}
	rec := &recorder{}
	runner := &fakeRunner{specs: []tool.Spec{{Name: "context_compact"}}}
	a := newAgent(t, llm, rec, Deps{Tools: runner}, Config{})
	runner2 := &ctxlessRunner{inner: runner, a: a}
	// context_compact 工具要真正接到 agent：经 runner 注册表没有它——直接包一层。
	a.tools = &delegatingRunner{inner: runner2, extra: a.ContextCompactTool()}
	_ = runner2

	c := conversation.New(conversation.ID("ct"), "ct")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "长历史"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	path := c.Path()
	// root,user,assistant(call),summary,tool,final
	if len(path) != 6 {
		t.Fatalf("path len = %d, want 6: %v", len(path), roles(path))
	}
	if path[3].Role != conversation.RoleSystem || path[3].Content[0].Text != "自答摘要" {
		t.Fatalf("summary = %+v", path[3])
	}
	if path[4].Role != conversation.RoleTool || path[4].ToolResult == nil || !path[4].ToolResult.OK {
		t.Fatalf("tool result = %+v", path[4])
	}
	if !strings.Contains(path[4].ToolResult.Output, "已压缩") {
		t.Fatalf("output = %q", path[4].ToolResult.Output)
	}
	// 第三次请求：水位生效 + 失联 tool 内联标注（不带孤立 tool_call_id）。
	last := llm.requests[2]
	txt := reqText(last)
	if !strings.Contains(txt, "自答摘要") {
		t.Fatalf("req2 缺摘要: %.200s", txt)
	}
	if !strings.Contains(txt, historyToolMarker) {
		t.Fatalf("req2 缺失联 tool 内联标注: %.300s", txt)
	}
	for _, m := range last.Messages {
		if m.Role == "tool" {
			t.Fatalf("水位之上不应出现孤立 tool 消息: %+v", m)
		}
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// delegatingRunner 把额外工具（context_compact）并进现有 runner 的注册表。
type delegatingRunner struct {
	inner port.ToolRunner
	extra port.Tool
}

func (r *delegatingRunner) Specs(ctx context.Context) ([]tool.Spec, error) {
	specs, err := r.inner.Specs(ctx)
	if err != nil {
		return nil, err
	}
	return append(specs, r.extra.Spec()), nil
}

func (r *delegatingRunner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if call.Name == r.extra.Spec().Name {
		return r.extra.Execute(ctx, call)
	}
	return r.inner.Execute(ctx, call)
}

// ctxlessRunner 占位（保留给未来注入场景）。
type ctxlessRunner struct {
	inner port.ToolRunner
	a     *Agent
}

func (r *ctxlessRunner) Specs(ctx context.Context) ([]tool.Spec, error) { return r.inner.Specs(ctx) }
func (r *ctxlessRunner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	return r.inner.Execute(ctx, call)
}

// TestContextCompactToolDirect 直接调用：无会话上下文报 OK=false；无可压缩历史短路。
func TestContextCompactToolDirect(t *testing.T) {
	llm := &scriptLLM{t: t}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	ct := a.ContextCompactTool()

	// 无上下文。
	res, err := ct.Execute(context.Background(), mustCall("cc"))
	if err != nil || res.OK || !strings.Contains(res.Err, "会话上下文") {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	// 无可压缩（仅 persona）。
	c := conversation.New(conversation.ID("p"), "p")
	commitNode(t, c, conversation.MessageID(c.ID), conversation.RoleSystem, "人格")
	res, err = ct.Execute(withConversation(context.Background(), c), mustCall("cc"))
	if err != nil || !res.OK || !strings.Contains(res.Output, "没有可压缩") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("短路不应花生成: %d", len(llm.requests))
	}
	if ct.Spec().Risk != tool.Safe {
		t.Fatalf("risk = %v", ct.Spec().Risk)
	}
}

// mustCall 构造一次无参工具调用。
func mustCall(name string) tool.Call {
	return tool.Call{ID: tool.CallID("t1"), Name: name, Args: json.RawMessage(`{}`)}
}

// TestUsageReport 用量汇总：估算（三级链）+ 树上实测累计。
func TestUsageReport(t *testing.T) {
	llm := &scriptLLM{t: t, streams: []*scriptStream{
		withUsage(textStream("答"), conversation.Usage{InputTokens: 100, OutputTokens: 20}),
	}}
	rec := &recorder{}
	a := newAgent(t, llm, rec, Deps{}, Config{})
	c := newConv(t)
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}

	rep, err := a.UsageReport(context.Background(), c)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if rep.SumIn != 100 || rep.SumOut != 20 || rep.Gen != 1 {
		t.Fatalf("sum = %+v", rep)
	}
	if rep.LastIn != 100 || rep.LastOut != 20 {
		t.Fatalf("last = %+v", rep)
	}
	if rep.Exact {
		t.Fatal("scriptLLM 无 TokenCounter 不应精确")
	}
	if rep.Estimate <= 0 || rep.Estimate > rep.MaxCtx {
		t.Fatalf("estimate = %d（max %d）", rep.Estimate, rep.MaxCtx)
	}
	thr := defaultCompactThreshold // 经变量打破常量折叠（int(常量小数) 非法）
	wantCompactAt := int(thr*float64(defaultMaxContextTokens) + 0.5)
	if rep.CompactAt != wantCompactAt {
		t.Fatalf("compactAt = %d, want %d（默认 0.7×64000）", rep.CompactAt, wantCompactAt)
	}
	if rep.Ratio < calMin || rep.Ratio > calMax {
		t.Fatalf("ratio = %v, want clamp 内", rep.Ratio)
	}
}

// countingLLM 带 TokenCounter 的脚本 LLM（②覆盖③的接线验证）。
type countingLLM struct {
	*scriptLLM
	n int
}

func (l *countingLLM) CountTokens(context.Context, string) (int, error) { return l.n, nil }

// TestUsageReportExactCounter 适配器实现 TokenCounter → /usage 走精确计数②（D26 覆盖语义）。
func TestUsageReportExactCounter(t *testing.T) {
	script := &scriptLLM{t: t, streams: []*scriptStream{
		withUsage(textStream("答"), conversation.Usage{InputTokens: 50, OutputTokens: 5}),
	}}
	rec := &recorder{}
	llm := &countingLLM{scriptLLM: script, n: 777}
	a, err := New(Deps{LLM: llm, UI: rec, IDs: &seqIDs{}, Clock: fixedClock{testTime}}, Config{Model: "m"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	c := newConv(t) // root + user → 2 条消息
	if err := a.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	rep, err := a.UsageReport(context.Background(), c)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if !rep.Exact {
		t.Fatal("实现 TokenCounter 应走精确计数②")
	}
	// Run 后 path = root,user,assistant → 装配 3 条（system 兜底 + user + assistant）。
	if want := 777 + 3*perMessageOverhead; rep.Estimate != want {
		t.Fatalf("estimate = %d, want %d（777 + 3×结构开销）", rep.Estimate, want)
	}
	if rep.SumIn != 50 || rep.SumOut != 5 {
		t.Fatalf("sum = %+v", rep)
	}
}

// reqText 拼接请求全部文本（断言用）。
func reqText(req port.GenerateRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		for _, p := range m.Content {
			b.WriteString(p.Text)
			b.WriteByte('\n')
		}
		for _, c := range m.ToolCalls {
			b.Write(c.Args)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// roles 提取角色序列（断言用）。
func roles(path []conversation.Message) []string {
	out := make([]string, len(path))
	for i, m := range path {
		out[i] = string(m.Role)
	}
	return out
}

// hasNotice 事件中是否含指定文本的通知。
func hasNotice(events []port.Event, sub string) bool {
	for _, ev := range events {
		if n, ok := ev.(port.NoticeEvent); ok && strings.Contains(n.Text, sub) {
			return true
		}
	}
	return false
}
