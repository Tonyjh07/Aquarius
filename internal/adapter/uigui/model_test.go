package uigui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newTestModel 无事件循环的模型 + 最小 UI（不经帧循环，直接驱动状态机）。
func newTestModel(t *testing.T) (*model, *UI) {
	t.Helper()
	u := &UI{
		inCh:  make(chan port.UserInput, inputCap),
		eofCh: make(chan struct{}),
	}
	return newModel(u), u
}

// allText 拼接全部可见文本（定稿块 + 流式草稿 + 实时思考），供注入面断言。
func allText(m *model) string {
	var b strings.Builder
	for _, blk := range m.blocks {
		b.WriteString(blk.text)
		b.WriteByte('\n')
	}
	b.WriteString(m.think.String())
	b.WriteByte('\n')
	b.WriteString(m.draft.String())
	return b.String()
}

// TestSubmitInputAndParse 提交入转写 + 输入投递解析（文本/斜杠命令/空行忽略）。
// GUI MVP 无输入历史（§15.2 未列），上下键历史留后续。
func TestSubmitInputAndParse(t *testing.T) {
	m, u := newTestModel(t)

	m.submit("你好")
	if len(m.blocks) != 1 || m.blocks[0].kind != blockUser {
		t.Fatalf("blocks = %+v, want 1 个 user 块", m.blocks)
	}
	select {
	case in := <-u.inCh:
		if in.Text != "你好" || in.Command != nil {
			t.Fatalf("in = %+v", in)
		}
	default:
		t.Fatal("输入未投递给 Next")
	}

	m.submit("/help extra")
	in := <-u.inCh
	if in.Command == nil || in.Command.Name != "help" || len(in.Command.Args) != 1 || in.Command.Args[0] != "extra" {
		t.Fatalf("command = %+v", in.Command)
	}

	before := len(m.blocks)
	m.submit("   ") // 空行：不入块不投递
	if len(m.blocks) != before {
		t.Fatalf("空行不应入块: %+v", m.blocks[before:])
	}
	select {
	case <-u.inCh:
		t.Fatal("空行不应投递")
	default:
	}
}

// TestSubmitOverflowMarksNotExecuted 缓冲满时明确标注"未执行"（不制造"已执行"
// 错觉，uitui 审查修复同款）；缓冲吸收后恢复。
func TestSubmitOverflowMarksNotExecuted(t *testing.T) {
	m, u := newTestModel(t)
	for i := 0; i < inputCap; i++ {
		m.submit("x")
	}
	m.submit("丢弃这行")
	got := allText(m)
	if !strings.Contains(got, "丢弃这行") {
		t.Fatalf("缺输入行: %q", got)
	}
	if !strings.Contains(got, "此行未执行") {
		t.Fatalf("缺未执行标注: %q", got)
	}
	<-u.inCh // 消费一行后恢复
	before := strings.Count(allText(m), "此行未执行")
	m.submit("恢复这行")
	if strings.Count(allText(m), "此行未执行") != before {
		t.Fatal("恢复后不应再标注未执行")
	}
}

// TestConfirmFlow 确认态：提示入转写、submit 路由应答（y/yes 语义）、拒答记录、
// 提示出口消毒（§9）。
func TestConfirmFlow(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan bool, 1)
	m.startConfirm("确认删除？", reply)
	if m.confirm == nil {
		t.Fatal("确认态未设置")
	}
	if !strings.Contains(allText(m), "确认删除？ [y/N]") {
		t.Fatalf("缺确认提示: %q", allText(m))
	}

	m.submit("y") // 确认态：submit 路由为应答（不进 inCh）
	select {
	case yes := <-reply:
		if !yes {
			t.Fatal("y 应为同意")
		}
	default:
		t.Fatal("应答应已投递")
	}
	if m.confirm != nil {
		t.Fatal("应答后确认态应清除")
	}
	if !strings.Contains(allText(m), "确认删除？ → true") {
		t.Fatalf("缺应答记录: %q", allText(m))
	}
	select {
	case <-m.u.inCh:
		t.Fatal("确认应答不应进输入队列")
	default:
	}

	// 拒答：非 y/yes 一律 false（与 repl 语义一致）。
	reply2 := make(chan bool, 1)
	m.startConfirm("再来？", reply2)
	m.submit("") // 空应答
	select {
	case yes := <-reply2:
		if yes {
			t.Fatal("空应答应为拒绝")
		}
	default:
		t.Fatal("应答应已投递")
	}
	if m.confirm != nil {
		t.Fatal("空应答后确认态应清除")
	}

	// 提示出口消毒：确认提示可携带注入序列（§9）。
	reply3 := make(chan bool, 1)
	m.startConfirm("危险\x1b[2J清屏？", reply3)
	if strings.Contains(allText(m), "\x1b") {
		t.Fatalf("确认提示泄漏控制序列: %q", allText(m))
	}
}

// TestEventsAndCommit 事件流：流式草稿 → committed 定稿（替换草稿）、工具/通知/
// 错误行、用量在节点累计（Delta 用量分片不重复计）。
func TestEventsAndCommit(t *testing.T) {
	m, _ := newTestModel(t)

	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "流式中"}})
	if !strings.Contains(m.draft.String(), "流式中") || !m.drafting {
		t.Fatalf("draft = %q drafting=%v, want 流式草稿", m.draft.String(), m.drafting)
	}
	m.commit(conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeDone,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "## 标题\n\n正文 **加粗**"}},
		Usage:   conversation.Usage{InputTokens: 10, OutputTokens: 5},
	})
	if m.draft.Len() != 0 || m.drafting {
		t.Fatalf("草稿未清: %q", m.draft.String())
	}
	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockAssistant || !strings.Contains(last.text, "标题") || !strings.Contains(last.text, "正文") {
		t.Fatalf("committed 块 = %+v", last)
	}
	if m.usage.InputTokens != 10 || m.usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", m.usage)
	}
	// Delta 里的用量分片不重复累计（节点是权威）。
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Usage: &conversation.Usage{InputTokens: 99}}})
	if m.usage.InputTokens != 10 {
		t.Fatalf("usage 双重累计: %+v", m.usage)
	}

	m.handleEvent(port.ToolCallEvent{Call: tool.Call{Name: "file_read", Args: []byte(`{"path":"a.txt"}`)}})
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{OK: true, Output: "内容"}})
	m.handleEvent(port.NoticeEvent{Text: "提示行"})
	m.handleEvent(port.ErrorEvent{Err: errors.New("boom")})
	m.commit(conversation.Message{
		Role:    conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "压缩摘要"}},
	})
	got := allText(m)
	for _, want := range []string{"[tool] file_read", "[tool ok]", "[notice] 提示行", "error: boom", "压缩摘要"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺 %q: %q", want, got)
		}
	}
}

// TestHistoryReplay D40/§7.4：历史节点按角色定稿渲染；空文本的取消给 [cancelled]
// 标记；回放不累计用量。
func TestHistoryReplay(t *testing.T) {
	m, _ := newTestModel(t)

	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "你好"}},
	}})
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:      conversation.RoleAssistant,
		Outcome:   conversation.OutcomeDone,
		ToolCalls: []tool.Call{{Name: "echo", Args: []byte(`{"m":"x"}`)}},
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "## 结论\n最终答案"}},
	}})
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:       conversation.RoleTool,
		ToolResult: &tool.Result{CallID: "c", OK: true, Output: "pong"},
	}})
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "压缩摘要"}},
	}})
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeCancelled,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "半截话"}},
	}})
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeCancelled,
	}})

	wantKinds := []blockKind{blockUser, blockTool, blockAssistant, blockTool,
		blockSystem, blockAssistant, blockAssistant}
	if len(m.blocks) != len(wantKinds) {
		t.Fatalf("blocks = %d, want %d", len(m.blocks), len(wantKinds))
	}
	for i, want := range wantKinds {
		if m.blocks[i].kind != want {
			t.Fatalf("blocks[%d].kind = %d, want %d", i, m.blocks[i].kind, want)
		}
	}
	for i, want := range []string{
		"你好", "[tool] echo", "最终答案", "[tool ok] pong",
		"压缩摘要", "[cancelled]", "[cancelled]",
	} {
		if !strings.Contains(m.blocks[i].text, want) {
			t.Fatalf("blocks[%d].text = %q, 缺 %q", i, m.blocks[i].text, want)
		}
	}
	if m.usage.InputTokens != 0 || m.usage.OutputTokens != 0 {
		t.Fatalf("回放不应累计用量: %+v", m.usage)
	}
}

// TestHistoryReplayThinking D42：回放口径恒含思考——思考分片渲染为独立思考块、
// 不并入正文；实时流场景提交不重复渲染（恰好 1 个思考块）。
func TestHistoryReplayThinking(t *testing.T) {
	parts := []conversation.Part{
		{Kind: conversation.PartThinking, Text: "先想一想"},
		{Kind: conversation.PartText, Text: "答案"},
	}

	m, _ := newTestModel(t)
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role: conversation.RoleAssistant, Outcome: conversation.OutcomeDone, Content: parts,
	}})
	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2（思考块 + 正文块）", len(m.blocks))
	}
	if m.blocks[0].kind != blockThinking || !strings.Contains(m.blocks[0].text, "先想一想") {
		t.Fatalf("blocks[0] = %+v, want 思考块", m.blocks[0])
	}
	if m.blocks[0].secs != -1 {
		t.Fatalf("回放思考块 secs = %d, want -1（无耗时）", m.blocks[0].secs)
	}
	if m.blocks[1].kind != blockAssistant || !strings.Contains(m.blocks[1].text, "答案") {
		t.Fatalf("blocks[1] = %+v, want 正文块", m.blocks[1])
	}
	if strings.Contains(m.blocks[1].text, "先想一想") {
		t.Fatalf("思考并入正文: %q", m.blocks[1].text)
	}

	// 实时流：思考 delta 已由 flushThink 落块，提交节点的思考分片不重复渲染。
	live, _ := newTestModel(t)
	live.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "先想一想", Reasoning: true}})
	live.handleEvent(port.CommittedEvent{Message: conversation.Message{
		Role: conversation.RoleAssistant, Outcome: conversation.OutcomeDone, Content: parts,
	}})
	thinking := 0
	for _, b := range live.blocks {
		if b.kind == blockThinking {
			thinking++
		}
	}
	if thinking != 1 {
		t.Fatalf("实时流思考块 = %d, want 恰好 1（提交不重复渲染）", thinking)
	}
	if len(live.blocks) != 2 || !strings.Contains(live.blocks[1].text, "答案") {
		t.Fatalf("live blocks = %+v, want 思考块 + 正文块", live.blocks)
	}
}

// TestReasoningFlow D34/§15.3：思维链流式实时可见（think 草稿）→ 正文到达落
// 定稿块（带耗时秒数）→ 不进正文草稿。
func TestReasoningFlow(t *testing.T) {
	m, _ := newTestModel(t)

	// 流中：实时可见于 think 草稿。
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "先想一想", Reasoning: true}})
	if !strings.Contains(m.think.String(), "先想一想") {
		t.Fatalf("think 草稿 = %q", m.think.String())
	}
	if m.draft.Len() != 0 {
		t.Fatalf("思维链不应进正文草稿: %q", m.draft.String())
	}
	if m.thinkAt.IsZero() {
		t.Fatal("首个思考增量应记录时刻（定稿耗时来源）")
	}

	// 回拨起点 2s：正文到达落块带耗时。
	m.thinkAt = time.Now().Add(-2 * time.Second)
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "答案"}})
	if m.think.Len() != 0 {
		t.Fatal("正文到达应把思维链落块")
	}
	if len(m.blocks) != 1 || m.blocks[0].kind != blockThinking {
		t.Fatalf("blocks = %+v, want 思考块先行", m.blocks)
	}
	if got := m.blocks[0].secs; got < 1 || got > 3 {
		t.Fatalf("思考耗时 secs = %d, want ≈2", got)
	}
	if !strings.Contains(m.draft.String(), "答案") {
		t.Fatalf("draft = %q", m.draft.String())
	}
}

// TestSanitizeControlStripsSequences 出口面：CSI、OSC（BEL 与 ST 收尾）、两字符
// 转义、截断序列、裸尾 ESC、C0/C1/DEL 全部剥除；\n\t 保留（§9 三处同算法）。
func TestSanitizeControlStripsSequences(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a\x1b[31mb", "ab"},
		{"a\x1b]0;title\x07b", "ab"},
		{"a\x1b]0;title\x1b\\b", "ab"},
		{"a\x1bMb", "ab"},
		{"a\x07b\x7fb\x85c", "abbc"},
		{"行1\n行2\t制表", "行1\n行2\t制表"},
		{"截断\x1b[3", "截断"},
		{"尾部\x1b", "尾部"},
	}
	for _, tc := range cases {
		if got := sanitizeControl(tc.in); got != tc.want {
			t.Errorf("sanitizeControl(% x) = % x, want % x", []byte(tc.in), []byte(got), []byte(tc.want))
		}
	}
}

// TestTranscriptStripsControlSequences GUI 出口面统一消毒（§15.3）：
// notice/错误行、流式草稿、工具预览、committed 内容都不得带注入序列进渲染。
func TestTranscriptStripsControlSequences(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.NoticeEvent{Text: "注意\x1b[2J清屏"})
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "流式\x07"}})
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{Name: "t", Args: []byte(`{"a":"b\x1b]52;c;?\x07"}`)}})
	got := allText(m)
	for _, bad := range []string{"\x1b", "\x07"} {
		if strings.Contains(got, bad) {
			t.Fatalf("泄漏控制序列 %q: %q", bad, got)
		}
	}
	for _, want := range []string{"注意", "清屏", "流式"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺 %q: %q", want, got)
		}
	}

	m.commit(conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeDone,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "正文\x1b[1m加粗"}},
	})
	got = allText(m)
	if strings.Contains(got, "\x1b[1m") || !strings.Contains(got, "正文加粗") {
		t.Fatalf("committed 内容消毒异常: %q", got)
	}
}

// TestCommitEmptyCancelledShowsMarker 首个 token 前取消的空内容节点显示
// [cancelled]；done 的空节点不入块。
func TestCommitEmptyCancelledShowsMarker(t *testing.T) {
	m, _ := newTestModel(t)
	m.commit(conversation.Message{Role: conversation.RoleAssistant, Outcome: conversation.OutcomeCancelled})
	if !strings.Contains(allText(m), "[cancelled]") {
		t.Fatalf("缺 [cancelled]: %q", allText(m))
	}
	before := len(m.blocks)
	m.commit(conversation.Message{Role: conversation.RoleAssistant, Outcome: conversation.OutcomeDone})
	if len(m.blocks) != before {
		t.Fatalf("done 空节点不应入块: %d → %d", before, len(m.blocks))
	}
}

// TestCommitSystemUsageAccumulated system 节点（/compact 摘要）的用量计入累计。
func TestCommitSystemUsageAccumulated(t *testing.T) {
	m, _ := newTestModel(t)
	m.commit(conversation.Message{
		Role:  conversation.RoleSystem,
		Usage: conversation.Usage{InputTokens: 100, OutputTokens: 50},
		Content: []conversation.Part{
			{Kind: conversation.PartText, Text: "压缩摘要"},
		},
	})
	if m.usage.InputTokens != 100 || m.usage.OutputTokens != 50 {
		t.Fatalf("usage = %+v, want {100 50}", m.usage)
	}
}

// TestSayFlushesThink 时序：外部输出先落进行中的思维链（与 uitui 口径一致）。
func TestSayFlushesThink(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "推理中", Reasoning: true}})
	m.say("命令输出")
	if m.think.Len() != 0 {
		t.Fatal("say 应先落思维链")
	}
	if len(m.blocks) != 2 || m.blocks[0].kind != blockThinking || m.blocks[1].kind != blockPlain {
		t.Fatalf("blocks = %+v, want 思考块 + plain", m.blocks)
	}
}
