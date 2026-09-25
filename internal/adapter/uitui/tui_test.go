package uitui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// newTestModel 无事件循环的模型 + 最小 UI（不经 Program，直接驱动消息）。
func newTestModel(t *testing.T) (*model, *UI) {
	t.Helper()
	u := &UI{
		inCh:  make(chan port.UserInput, inputCap),
		eofCh: make(chan struct{}, 1),
		opts:  Options{History: 50},
	}
	return newModel(u), u
}

// TestSubmitInputAndParse 提交入转写 + 输入投递解析（文本/斜杠命令/空行忽略）+ 历史。
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
	if len(m.history) != 2 {
		t.Fatalf("history = %v, want 2 条", m.history)
	}
}

// TestHistoryKeys 上下键翻历史（首末边界与清空）。
func TestHistoryKeys(t *testing.T) {
	m, _ := newTestModel(t)
	m.submit("one")
	m.submit("two")

	m.key(tea.KeyMsg{Type: tea.KeyUp})
	if string(m.input) != "two" {
		t.Fatalf("input = %q, want two", m.input)
	}
	m.key(tea.KeyMsg{Type: tea.KeyUp})
	if string(m.input) != "one" {
		t.Fatalf("input = %q, want one", m.input)
	}
	m.key(tea.KeyMsg{Type: tea.KeyDown})
	if string(m.input) != "two" {
		t.Fatalf("input = %q, want two", m.input)
	}
	m.key(tea.KeyMsg{Type: tea.KeyDown})
	if len(m.input) != 0 {
		t.Fatalf("input = %q, want 清空", m.input)
	}
}

// TestConfirmFlow 确认对话：提示入转写、应答缓冲提交、y/yes 语义与拒答记录。
func TestConfirmFlow(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan bool, 1)
	m.Update(confirmMsg{prompt: "确认删除？", reply: reply})
	if m.confirm == nil {
		t.Fatal("确认态未设置")
	}
	if got := m.View(); !strings.Contains(got, "确认删除？ [y/N]") {
		t.Fatalf("View 缺确认提示: %q", got)
	}

	// 输入应答进 answer 缓冲（不是输入框），回车提交。
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(m.input) != 0 || string(m.confirm.answer) != "y" {
		t.Fatalf("input=%q answer=%q", m.input, m.confirm.answer)
	}
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
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
	if got := m.View(); !strings.Contains(got, "确认删除？ → true") {
		t.Fatalf("转写缺应答记录: %q", got)
	}

	// 拒答：非 y/yes 一律 false（与 repl 语义一致）。
	reply2 := make(chan bool, 1)
	m.Update(confirmMsg{prompt: "再来？", reply: reply2})
	m.key(tea.KeyMsg{Type: tea.KeyEnter}) // 空应答
	select {
	case yes := <-reply2:
		if yes {
			t.Fatal("空应答应为拒绝")
		}
	default:
		t.Fatal("应答应已投递")
	}
}

// TestConfirmInterruptCtrlC Ctrl+C：调用中断回调 + 拒答 + 清输入（TUI 无 SIGINT，须桥接）。
func TestConfirmInterruptCtrlC(t *testing.T) {
	m, u := newTestModel(t)
	var interrupted bool
	u.SetInterrupt(func() { interrupted = true })

	reply := make(chan bool, 1)
	m.Update(confirmMsg{prompt: "p", reply: reply})
	m.input = []rune("typed")
	m.key(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !interrupted {
		t.Fatal("Ctrl+C 应触发中断回调")
	}
	select {
	case yes := <-reply:
		if yes {
			t.Fatal("中断应拒答")
		}
	default:
		t.Fatal("拒答应已投递")
	}
	if len(m.input) != 0 || m.confirm != nil {
		t.Fatalf("input=%q confirm=%v, want 清空", m.input, m.confirm)
	}
}

// TestCtrlDEof 空输入 Ctrl+D → Next 收到 io.EOF（等价输入流结束）。
func TestCtrlDEof(t *testing.T) {
	m, u := newTestModel(t)
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m.key(tea.KeyMsg{Type: tea.KeyCtrlD})
	select {
	case <-u.eofCh:
		t.Fatal("非空输入的 Ctrl+D 不应触发 EOF")
	default:
	}
	m.input = nil
	m.key(tea.KeyMsg{Type: tea.KeyCtrlD})
	if _, err := u.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

// TestNextDrainsQueuedInputBeforeEOF 审查修复：EOF 是 close 广播（粘滞+同时可见），
// 排队输入必须先于 EOF 交付——旧实现的一次性 channel 标记会被随机 select 吞掉，
// 此后 Next 永久阻塞。
func TestNextDrainsQueuedInputBeforeEOF(t *testing.T) {
	m, u := newTestModel(t)
	m.submit("第一行")
	m.submit("/quit")
	u.signalEOF(nil) // 事件循环已在处理完输入行后广播

	in, err := u.Next(context.Background())
	if err != nil || in.Text != "第一行" {
		t.Fatalf("first = %+v, %v, want 第一行", in, err)
	}
	in, err = u.Next(context.Background())
	if err != nil || in.Command == nil || in.Command.Name != "quit" {
		t.Fatalf("second = %+v, %v, want /quit", in, err)
	}
	// EOF 粘滞：之后每次 Next 都返回 EOF（不再阻塞）。
	for i := 0; i < 3; i++ {
		if _, err := u.Next(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("第 %d 次 Next = %v, want 持续 EOF", i+3, err)
		}
	}
}

// TestNextSurfacesScanError 审查修复：pump 扫描错误（超长行等）随 eofMsg 上抛，
// 不再静默当 EOF（否则 TUI 以退出码 0 掩盖输入故障）。
func TestNextSurfacesScanError(t *testing.T) {
	_, u := newTestModel(t)
	boom := errors.New("读取输入故障")
	u.signalEOF(boom)
	if _, err := u.Next(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want 扫描错误原样上抛", err)
	}
}

// TestConfirmRejectsOnEOF 审查修复：输入流结束时确认对话不得永久挂起——
// 对齐 repl"不替用户做破坏性决定"语义，返回拒绝。
func TestConfirmRejectsOnEOF(t *testing.T) {
	u := New(Options{In: strings.NewReader(""), Out: io.Discard})
	defer func() { _ = u.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	yes, err := u.Confirm(ctx, "危险操作？")
	if err != nil || yes {
		t.Fatalf("confirm = %v, %v, want (false, nil)", yes, err)
	}
	// EOF 标志保留：随后 Next 仍见 EOF。
	if _, err := u.Next(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("Next = %v, want EOF", err)
	}
}

// TestPumpMultiLineThenEOF 端到端：管道多行输入全部交付后稳定返回 EOF
// （B-M1 复现——旧实现第一或第二次 Next 就可能把 EOF 吞掉直接退出，跳过整轮对话）。
func TestPumpMultiLineThenEOF(t *testing.T) {
	u := New(Options{In: strings.NewReader("第一行\n/quit\n"), Out: io.Discard})
	defer func() { _ = u.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	in, err := u.Next(ctx)
	if err != nil || in.Text != "第一行" {
		t.Fatalf("first = %+v, %v", in, err)
	}
	in, err = u.Next(ctx)
	if err != nil || in.Command == nil || in.Command.Name != "quit" {
		t.Fatalf("second = %+v, %v", in, err)
	}
	if _, err := u.Next(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("third = %v, want EOF", err)
	}
	if _, err := u.Next(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("fourth = %v, want EOF（粘滞）", err)
	}
}

// TestPumpScanErrorPropagates 扫描错误端到端：首行正常交付，随后错误上抛。
func TestPumpScanErrorPropagates(t *testing.T) {
	boom := errors.New("scanner 故障")
	in := io.MultiReader(strings.NewReader("line\n"), iotest.ErrReader(boom))
	u := New(Options{In: in, Out: io.Discard})
	defer func() { _ = u.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := u.Next(ctx)
	if err != nil || got.Text != "line" {
		t.Fatalf("first = %+v, %v", got, err)
	}
	if _, err := u.Next(ctx); err == nil || !strings.Contains(err.Error(), "scanner 故障") {
		t.Fatalf("second = %v, want 扫描错误上抛", err)
	}
}

// TestEventsAndCommit 事件流：流式草稿 → committed 定稿（glamour/原文）、
// 工具/通知/错误行、用量在节点累计（Delta 用量不重复计）。
func TestEventsAndCommit(t *testing.T) {
	m, _ := newTestModel(t)

	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "流式中"}})
	if !strings.Contains(m.View(), "流式中") {
		t.Fatal("View 缺流式草稿")
	}
	m.commit(conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeDone,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "## 标题\n\n正文 **加粗**"}},
		Usage:   conversation.Usage{InputTokens: 10, OutputTokens: 5},
	})
	got := m.View()
	for _, want := range []string{"标题", "正文"} {
		if !strings.Contains(got, want) {
			t.Fatalf("committed 块缺 %q: %q", want, got)
		}
	}
	if m.usage.InputTokens != 10 || m.usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", m.usage)
	}
	// Delta 里的用量分片不再重复累计（节点是权威）。
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Usage: &conversation.Usage{InputTokens: 99}}})
	if m.usage.InputTokens != 10 {
		t.Fatalf("usage 双重累计: %+v", m.usage)
	}
	// 草稿已定稿清空。
	if m.draft.Len() != 0 {
		t.Fatalf("草稿未清: %q", m.draft.String())
	}

	m.handleEvent(port.ToolCallEvent{Call: tool.Call{Name: "file_read", Args: []byte(`{"path":"a.txt"}`)}})
	m.handleEvent(port.ToolResultEvent{Result: tool.Result{OK: true, Output: "内容"}})
	m.handleEvent(port.NoticeEvent{Text: "提示行"})
	m.handleEvent(port.ErrorEvent{Err: errors.New("boom")})
	m.commit(conversation.Message{
		Role:    conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "压缩摘要"}},
	})
	got = m.View()
	for _, want := range []string{"[tool] file_read", "[tool ok]", "[notice] 提示行", "error: boom", "压缩摘要"} {
		if !strings.Contains(got, want) {
			t.Fatalf("View 缺 %q: %q", want, got)
		}
	}
}

// TestScrollWindow PgUp/PgDn 滚动转写窗口（尾随跟随与上限收敛）。
func TestScrollWindow(t *testing.T) {
	m, _ := newTestModel(t)
	m.height = 6 // avail = 6-2-1 = 3… blocks 5 行时可滚
	for _, s := range []string{"L1", "L2", "L3", "L4", "L5"} {
		m.add(blockPlain, s)
	}
	m.key(tea.KeyMsg{Type: tea.KeyPgUp})
	m.key(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.scroll == 0 {
		t.Fatal("PgUp 应产生滚动")
	}
	view := m.View()
	if strings.Contains(view, "L5") {
		t.Fatalf("滚回后窗口不应含尾行: %q", view)
	}
	for i := 0; i < 10; i++ {
		m.key(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if m.scroll != 0 {
		t.Fatalf("PgDn 应回到尾随: %d", m.scroll)
	}
	if !strings.Contains(m.View(), "L5") {
		t.Fatalf("尾随窗口应含尾行: %q", m.View())
	}
}

// TestStatusLine 状态行：模型/权限（回调现取）+ 用量累计 + 提示。
func TestStatusLine(t *testing.T) {
	m, u := newTestModel(t)
	u.opts.Status = func() Status { return Status{Model: "gpt-x", Level: "strict"} }
	m.usage = conversation.Usage{InputTokens: 7, OutputTokens: 3}
	line := m.statusLine()
	for _, want := range []string{"模型 gpt-x", "权限 strict", "↑7 ↓3", "Ctrl+C"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status 缺 %q: %q", want, line)
		}
	}
}

// TestSubmitOverflowMarksNotExecuted 审查修复：缓冲满时明确标注"未执行"
// （旧行为先入转写再静默丢弃，制造"已执行"错觉）；缓冲吸收后恢复。
func TestSubmitOverflowMarksNotExecuted(t *testing.T) {
	m, u := newTestModel(t)
	for i := 0; i < inputCap; i++ {
		m.submit("x")
	}
	m.submit("丢弃这行")
	got := m.View()
	if !strings.Contains(got, "丢弃这行") {
		t.Fatalf("View 缺输入行: %q", got)
	}
	if !strings.Contains(got, "此行未执行") {
		t.Fatalf("View 缺未执行标注: %q", got)
	}
	// 消费一行后恢复。
	<-u.inCh
	before := strings.Count(m.View(), "此行未执行")
	m.submit("恢复这行")
	if strings.Count(m.View(), "此行未执行") != before {
		t.Fatal("恢复后不应再标注未执行")
	}
}

// TestConfirmAnswerEditing 审查修复：确认态的退格/Ctrl+U 编辑应答缓冲
// （旧实现误改输入框），应答缓冲同受上限约束。
func TestConfirmAnswerEditing(t *testing.T) {
	m, _ := newTestModel(t)
	m.Update(confirmMsg{prompt: "p", reply: make(chan bool, 1)})
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("yes")})
	m.key(tea.KeyMsg{Type: tea.KeyBackspace})
	if string(m.confirm.answer) != "ye" {
		t.Fatalf("answer = %q, want ye（退格作用于应答缓冲）", m.confirm.answer)
	}
	if len(m.input) != 0 {
		t.Fatalf("input = %q, want 不受影响", m.input)
	}
	m.key(tea.KeyMsg{Type: tea.KeyCtrlU})
	if len(m.confirm.answer) != 0 {
		t.Fatalf("answer = %q, want Ctrl+U 清空应答", m.confirm.answer)
	}
	// 超长粘贴截断在上限内。
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("z", inputRuneCap+100))})
	if len(m.confirm.answer) > inputRuneCap {
		t.Fatalf("answer len = %d, want <= %d", len(m.confirm.answer), inputRuneCap)
	}
}

// TestSanitizeControlStripsSequences 终端注入面：CSI、OSC（BEL 与 ST 收尾）、
// 两字符转义、截断序列、裸尾 ESC、C0/C1/DEL 全部剥除；\n\t 保留。
func TestSanitizeControlStripsSequences(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a\x1b[31mb", "ab"},
		{"a\x1b]0;title\x07b", "ab"},
		{"a\x1b]0;title\x1b\\b", "ab"},
		{"a\x1bMb", "ab"},
		{"a\x07b\x7fb\x85c", "abbc"}, // 夹缝字符剥除；0x85 非法 UTF-8 → RuneError 同样剔除
		{"ab", "ab"},                // 合法 UTF-8 编码的 C1（U+0085）同样剔除
		{"行1\n行2\t制表", "行1\n行2\t制表"},
		{"截断\x1b[3", "截断"},
		{"尾部\x1b", "尾部"},
	}
	for _, tc := range cases {
		if got := sanitizeControl(tc.in); got != tc.want {
			t.Errorf("sanitizeControl(% x) = % x, want % x（%q → %q）", []byte(tc.in), []byte(got), []byte(tc.want), tc.in, got)
		}
	}
}

// TestTranscriptStripsControlSequences 转写各入口统一消毒（§9）：
// notice/错误行、流式草稿、工具预览、committed 内容都不得把注入序列带进终端。
func TestTranscriptStripsControlSequences(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleEvent(port.NoticeEvent{Text: "注意\x1b[2J清屏"})
	m.handleEvent(port.DeltaEvent{Delta: port.Delta{Text: "流式\x07"}})
	m.handleEvent(port.ToolCallEvent{Call: tool.Call{Name: "t", Args: []byte(`{"a":"b\x1b]52;c;?\x07"}`)}})
	got := m.View() // 流式草稿仍在（未提交）
	for _, bad := range []string{"\x1b", "\x07"} {
		if strings.Contains(got, bad) {
			t.Fatalf("View 泄漏控制序列 %q: %q", bad, got)
		}
	}
	for _, want := range []string{"注意", "清屏", "流式"} {
		if !strings.Contains(got, want) {
			t.Fatalf("View 缺 %q: %q", want, got)
		}
	}

	// committed 内容（glamour 未装配时回退原文，仍须已消毒）。
	m.commit(conversation.Message{
		Role:    conversation.RoleAssistant,
		Outcome: conversation.OutcomeDone,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "正文\x1b[1m加粗"}},
	})
	got = m.View()
	if strings.Contains(got, "\x1b[1m") || !strings.Contains(got, "正文加粗") {
		t.Fatalf("committed 内容消毒异常: %q", got)
	}
}

// TestCommitEmptyCancelledShowsMarker 审查修复：首个 token 前取消的空内容节点
// 显示 [cancelled]（旧实现什么都不显示）；done 的空节点不入块。
func TestCommitEmptyCancelledShowsMarker(t *testing.T) {
	m, _ := newTestModel(t)
	m.commit(conversation.Message{Role: conversation.RoleAssistant, Outcome: conversation.OutcomeCancelled})
	if !strings.Contains(m.View(), "[cancelled]") {
		t.Fatalf("View 缺 [cancelled]: %q", m.View())
	}
	before := len(m.blocks)
	m.commit(conversation.Message{Role: conversation.RoleAssistant, Outcome: conversation.OutcomeDone})
	if len(m.blocks) != before {
		t.Fatalf("done 空节点不应入块: %d → %d", before, len(m.blocks))
	}
}

// TestCommitSystemUsageAccumulated 审查修复：system 节点（/compact 摘要）的用量
// 计入状态行——旧实现只累计 assistant，压缩开销从状态行消失。
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
	if !strings.Contains(m.statusLine(), "↑100 ↓50") {
		t.Fatalf("status = %q", m.statusLine())
	}
}

// TestRenderMDFallback 无渲染器时原文回退。
func TestRenderMDFallback(t *testing.T) {
	m, _ := newTestModel(t)
	if got := m.renderMD("**x**"); got != "**x**" {
		t.Fatalf("renderMD = %q", got)
	}
}
