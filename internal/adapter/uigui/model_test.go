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
		if c := blk.chip; c != nil { // D67：chip 的参数/问答/结果一并纳入消毒与合并断言
			b.WriteString(c.name)
			b.WriteByte('\n')
			b.WriteString(c.args)
			b.WriteByte('\n')
			b.WriteString(c.confirmQ)
			b.WriteByte('\n')
			b.WriteString(c.confirmA)
			b.WriteByte('\n')
			b.WriteString(c.result)
			b.WriteByte('\n')
		}
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

// TestConfirmReasonEcho D86：拒绝原因回显进 chip confirmA 与 plain 行（消毒同问句）。
func TestConfirmReasonEcho(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "term_exec"}})
	reply := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("允许执行 term_exec？参数: {}", reply)
	if m.confirmChip < 0 {
		t.Fatal("工具确认应并入 chip")
	}
	m.replyConfirm(port.ConfirmAnswer{Reason: "路径越界[2J了"})
	if got := m.blocks[0].chip.confirmA; got != "拒绝：路径越界了" {
		t.Fatalf("confirmA = %q, want 拒绝：路径越界了（CSI 序列整段吞除）", got)
	}

	// 非 chip 确认（/rm 类）：plain 行回显带原因。
	reply2 := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("确认删除 n4？", reply2)
	m.replyConfirm(port.ConfirmAnswer{Reason: "留错了"})
	if !strings.Contains(allText(m), "确认删除 n4？ → 拒绝：留错了") {
		t.Fatalf("缺回显: %q", allText(m))
	}

	// 干拒与允许文案不变。
	reply3 := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("再来？", reply3)
	m.replyConfirm(port.ConfirmAnswer{})
	if !strings.Contains(allText(m), "再来？ → 拒绝") {
		t.Fatalf("干拒回显缺失: %q", allText(m))
	}
}

// TestStartConfirmResetsReason D86：确认开启清空原因框并请求焦点让入。
func TestStartConfirmResetsReason(t *testing.T) {
	m, u := newTestModel(t)
	u.reasonEd.SetText("上一次的原因")
	m.startConfirm("确认删除？", make(chan port.ConfirmAnswer, 1))
	if got := u.reasonEd.Text(); got != "" {
		t.Fatalf("reasonEd = %q, want 清空", got)
	}
	if !u.reasonFocus {
		t.Fatal("reasonFocus 未武装（焦点不会让入原因框）")
	}
}

// TestNextPermLevel D86：权限档序列升一档；到顶/未知档不可升。
func TestNextPermLevel(t *testing.T) {
	cases := []struct {
		cur, next string
		ok        bool
	}{
		{"read-only", "strict", true},
		{"strict", "permissive", true},
		{"permissive", "full-access", true},
		{"full-access", "", false},
		{"unknown", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		next, ok := nextPermLevel(tc.cur)
		if next != tc.next || ok != tc.ok {
			t.Fatalf("nextPermLevel(%q) = %q, %v, want %q, %v", tc.cur, next, ok, tc.next, tc.ok)
		}
	}
}

// TestConfirmElevatable D86：🔑 仅工具确认且有下一档时可见。
func TestConfirmElevatable(t *testing.T) {
	u := newFrameUI() // 需要 u.m 双向就绪（confirmElevatable 读确认态）
	m := u.m
	u.opts.Status = func() Status { return Status{Level: "strict"} }

	// 非 chip 确认（/rm 类）：不可见。
	m.startConfirm("确认删除 n4？", make(chan port.ConfirmAnswer, 1))
	if u.confirmElevatable() {
		t.Fatal("非工具确认不应显示提升钮")
	}
	m.replyConfirm(port.ConfirmAnswer{})

	// 工具确认：strict 档可见。
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "term_exec"}})
	m.startConfirm("允许执行 term_exec？", make(chan port.ConfirmAnswer, 1))
	if !u.confirmElevatable() {
		t.Fatal("strict 档工具确认应显示提升钮")
	}

	// full-access 档：到顶不可见。
	u.opts.Status = func() Status { return Status{Level: "full-access"} }
	if u.confirmElevatable() {
		t.Fatal("full-access 档不应显示提升钮")
	}

	// Status 未配置：不可见（提档无据）。
	u.opts.Status = nil
	if u.confirmElevatable() {
		t.Fatal("无 Status 数据源不应显示提升钮")
	}
}

// TestConfirmFlow 确认态：提示入转写、submit 路由应答（y/yes 语义）、拒答记录、
// 提示出口消毒（§9）。
func TestConfirmFlow(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("确认删除？", reply)
	if m.confirm == nil {
		t.Fatal("确认态未设置")
	}
	if !strings.Contains(allText(m), "确认删除？") {
		t.Fatalf("缺确认提示: %q", allText(m))
	}

	m.submit("y") // 确认态：submit 路由为应答（不进 inCh）
	select {
	case ans := <-reply:
		if !ans.Allow {
			t.Fatal("y 应为同意")
		}
	default:
		t.Fatal("应答应已投递")
	}
	if m.confirm != nil {
		t.Fatal("应答后确认态应清除")
	}
	if !strings.Contains(allText(m), "确认删除？ → 允许") {
		t.Fatalf("缺应答记录: %q", allText(m))
	}
	select {
	case <-m.u.inCh:
		t.Fatal("确认应答不应进输入队列")
	default:
	}

	// 拒答：非 y/yes 一律 false（与 repl 语义一致）。
	reply2 := make(chan port.ConfirmAnswer, 1)
	m.startConfirm("再来？", reply2)
	m.submit("") // 空应答
	select {
	case ans := <-reply2:
		if ans.Allow {
			t.Fatal("空应答应为拒绝")
		}
	default:
		t.Fatal("应答应已投递")
	}
	if m.confirm != nil {
		t.Fatal("空应答后确认态应清除")
	}

	// 提示出口消毒：确认提示可携带注入序列（§9）。
	reply3 := make(chan port.ConfirmAnswer, 1)
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

	m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "file_read", Args: []byte(`{"path":"a.txt"}`)}})
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "c1", OK: true, Output: "内容"}})
	m.handleEvent(port.NoticeEvent{Text: "提示行"})
	m.handleEvent(port.ErrorEvent{Err: errors.New("boom")})
	m.commit(conversation.Message{
		Role:    conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "压缩摘要"}},
	})
	// D67：调用+结果合并为单个 chip 块（文本行只剩 notice/error/system；前面已有
	// committed 助手块）。
	if len(m.blocks) != 5 {
		t.Fatalf("blocks = %d, want 5: %+v", len(m.blocks), m.blocks)
	}
	c := m.blocks[1].chip
	if c == nil || c.name != "file_read" || !c.done || !c.ok || c.result != "内容" || c.args != `{"path":"a.txt"}` {
		t.Fatalf("chip = %+v", c)
	}
	got := allText(m)
	for _, want := range []string{"file_read", "内容", "[notice] 提示行", "error: boom", "压缩摘要"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺 %q: %q", want, got)
		}
	}
}

// TestClearEvent D75：/switch、/new 的清屏——blocks/草稿/思维链/用量归零，
// 并重置转写滚动与行选态（contentH 归零、跟随贴底）。
func TestClearEvent(t *testing.T) {
	m, u := newTestModel(t)
	m.handleEvent(port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "旧会话的内容"}},
	}})
	m.handleEvent(port.NoticeEvent{Text: "旧提示"})
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "未完成草稿"}})
	m.usage = conversation.Usage{InputTokens: 7, OutputTokens: 3}
	u.scrollPx, u.contentH, u.followTail = 120, 800, false
	u.selRows = append(u.selRows, nil)
	if len(m.blocks) == 0 {
		t.Fatal("前置：应已有转写块")
	}

	m.handleEvent(port.ClearEvent{})
	if len(m.blocks) != 0 {
		t.Fatalf("清屏后 blocks = %d, want 0", len(m.blocks))
	}
	if m.draft.Len() != 0 || m.drafting || m.think.Len() != 0 {
		t.Fatalf("草稿/思维链未清: draft=%q think=%q", m.draft.String(), m.think.String())
	}
	if m.usage.InputTokens != 0 || m.usage.OutputTokens != 0 {
		t.Fatalf("用量未清: %+v", m.usage)
	}
	if u.scrollPx != 0 || u.contentH != 0 {
		t.Fatalf("滚动状态未清: scrollPx=%d contentH=%d", u.scrollPx, u.contentH)
	}
	if !u.followTail {
		t.Fatal("清屏后应恢复跟随贴底")
	}
	if len(u.selRows) != 0 {
		t.Fatalf("行选态未清: %d", len(u.selRows))
	}
	if got := allText(m); strings.Contains(got, "旧会话的内容") || strings.Contains(got, "旧提示") {
		t.Fatalf("转写仍含旧内容: %q", got)
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
		ToolCalls: []tool.Call{{ID: "c", Name: "echo", Args: []byte(`{"m":"x"}`)}},
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

	// D67：调用与结果按 CallID 合并为单个 chip 块（tool 节点不再独立成块）。
	wantKinds := []blockKind{blockUser, blockTool, blockAssistant,
		blockSystem, blockAssistant, blockAssistant}
	if len(m.blocks) != len(wantKinds) {
		t.Fatalf("blocks = %d, want %d: %+v", len(m.blocks), len(wantKinds), m.blocks)
	}
	for i, want := range wantKinds {
		if m.blocks[i].kind != want {
			t.Fatalf("blocks[%d].kind = %d, want %d", i, m.blocks[i].kind, want)
		}
	}
	c := m.blocks[1].chip
	if c == nil || c.name != "echo" || !c.done || !c.ok || c.result != "pong" {
		t.Fatalf("回放 chip = %+v", c)
	}
	for i, want := range []string{
		"你好", "", "最终答案", "压缩摘要", "[cancelled]", "[cancelled]",
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
