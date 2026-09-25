package uitui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

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

// TestRenderMDFallback 无渲染器时原文回退。
func TestRenderMDFallback(t *testing.T) {
	m, _ := newTestModel(t)
	if got := m.renderMD("**x**"); got != "**x**" {
		t.Fatalf("renderMD = %q", got)
	}
}
