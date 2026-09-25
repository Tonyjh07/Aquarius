package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func TestNextParsesInput(t *testing.T) {
	in := "  hello world  \n/new 项目 X\n/quit\n\n"
	ui := New(strings.NewReader(in), io.Discard)
	ctx := context.Background()

	got, err := ui.Next(ctx)
	if err != nil || got.Text != "hello world" || got.Command != nil {
		t.Fatalf("#1 = %+v, %v", got, err)
	}
	got, err = ui.Next(ctx)
	if err != nil || got.Command == nil || got.Command.Name != "new" ||
		strings.Join(got.Command.Args, " ") != "项目 X" {
		t.Fatalf("#2 = %+v, %v", got, err)
	}
	got, err = ui.Next(ctx)
	if err != nil || got.Command == nil || got.Command.Name != "quit" {
		t.Fatalf("#3 = %+v, %v", got, err)
	}
	got, err = ui.Next(ctx) // 空行
	if err != nil || got.Text != "" || got.Command != nil {
		t.Fatalf("#4 = %+v, %v, want 零值", got, err)
	}
	if _, err := ui.Next(ctx); err != io.EOF {
		t.Fatalf("#5 err = %v, want io.EOF", err)
	}
}

func TestNextCanceledContext(t *testing.T) {
	ui := New(strings.NewReader("x\n"), io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ui.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestEmitStreamThenCommitted(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)

	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "Hel"}})
	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "lo"}})
	emit(t, ui, port.CommittedEvent{Message: conversation.Message{
		Outcome: conversation.OutcomeDone,
		Usage:   conversation.Usage{InputTokens: 1, OutputTokens: 2},
	}})
	if got, want := buf.String(), "Hello\n[usage in=1 out=2]\n"; got != want {
		t.Fatalf("buf = %q, want %q", got, want)
	}
}

// TestEmitReasoningLine D34：思维链独立成行（[thinking] 前缀，分片续写），
// 正文/工具/提交事件到达时先关思维链行。
func TestEmitReasoningLine(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)

	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "先想", Reasoning: true}})
	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "一步", Reasoning: true}})
	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "答案"}})
	emit(t, ui, port.CommittedEvent{Message: conversation.Message{Outcome: conversation.OutcomeDone}})
	if got, want := buf.String(), "[thinking] 先想一步\n答案\n"; got != want {
		t.Fatalf("buf = %q, want %q", got, want)
	}

	// 思维链 → 工具事件：先关思维链行再打工具行。
	buf.Reset()
	ui = New(strings.NewReader(""), &buf)
	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "打算调用", Reasoning: true}})
	emit(t, ui, port.ToolCallEvent{Call: tool.Call{Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}})
	if got, want := buf.String(), "[thinking] 打算调用\n[tool] echo {\"m\":\"x\"}\n"; got != want {
		t.Fatalf("buf = %q, want %q", got, want)
	}
}

func TestEmitCancelledOutcome(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)
	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "一半"}})
	emit(t, ui, port.CommittedEvent{Message: conversation.Message{Outcome: conversation.OutcomeCancelled}})
	if got, want := buf.String(), "一半\n[cancelled]\n"; got != want {
		t.Fatalf("buf = %q, want %q", got, want)
	}
}

func TestEmitToolEventsAndError(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)

	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "调用中"}})
	emit(t, ui, port.ToolCallEvent{Call: tool.Call{Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}})
	emit(t, ui, port.ToolResultEvent{Result: tool.Result{CallID: "c", OK: true, Output: "pong"}})
	emit(t, ui, port.ToolResultEvent{Result: tool.Result{CallID: "c", OK: false, Err: "boom"}})
	emit(t, ui, port.ErrorEvent{Err: errors.New("conn reset")})

	want := "调用中\n" +
		"[tool] echo {\"m\":\"x\"}\n" +
		"[tool ok] pong\n" +
		"[tool failed] boom\n" +
		"error: conn reset\n"
	if buf.String() != want {
		t.Fatalf("buf = %q, want %q", buf.String(), want)
	}
}

func TestSayAndPrompt(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)
	ui.Say("") // 空输出无副作用
	ui.Say("列表如下")
	ui.Prompt()
	if got, want := buf.String(), "列表如下\n> "; got != want {
		t.Fatalf("buf = %q, want %q", got, want)
	}
}

// TestConfirmReadsAnswer /rm 等二次确认：提示后读一行；y/yes 同意，其余、空行与 EOF 一律拒绝。
func TestConfirmReadsAnswer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"y", "y\n", true},
		{"YES 大小写", "  YES \n", true},
		{"n", "n\n", false},
		{"空行默认拒绝", "\n", false},
		{"EOF 不替用户决定", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			ui := New(strings.NewReader(tc.in), &buf)
			got, err := ui.Confirm(context.Background(), "确认删除 n4？")
			if err != nil || got != tc.want {
				t.Fatalf("confirm = %v, %v, want %v", got, err, tc.want)
			}
			if want := "确认删除 n4？ [y/N] "; buf.String() != want {
				t.Fatalf("buf = %q, want %q", buf.String(), want)
			}
		})
	}
}

// TestConfirmCanceledContext 取消的 ctx 直接报错，不读输入。
func TestConfirmCanceledContext(t *testing.T) {
	ui := New(strings.NewReader("y\n"), io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ui.Confirm(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestEmitNotice NoticeEvent：断流式行后以 [notice] 呈现（裁剪/自动压缩提示，D21）。
func TestEmitNotice(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)
	emit(t, ui, port.DeltaEvent{Delta: port.Delta{Text: "生成中"}})
	emit(t, ui, port.NoticeEvent{Text: "已省略 3 条较早消息"})
	if got, want := buf.String(), "生成中\n[notice] 已省略 3 条较早消息\n"; got != want {
		t.Fatalf("buf = %q, want %q", got, want)
	}
}

func TestEmitLongPreviewTruncated(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)
	emit(t, ui, port.ToolResultEvent{Result: tool.Result{OK: true, Output: strings.Repeat("あ", toolPreviewLen+50)}})
	line := buf.String()
	if !strings.HasSuffix(line, "…\n") {
		t.Fatalf("line = %q, want 截断结尾", line)
	}
	if r := []rune(strings.TrimSuffix(strings.TrimPrefix(line, "[tool ok] "), "\n")); len(r) > toolPreviewLen+1 {
		t.Fatalf("preview too long: %d runes", len(r))
	}
}

// TestEmitHistoryReplay D40/§7.4：历史回放按角色逐行打印，与 TUI 同语义。
func TestEmitHistoryReplay(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)
	emit(t, ui, port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleUser,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "你好"}},
	}})
	emit(t, ui, port.HistoryEvent{Message: conversation.Message{
		Role:      conversation.RoleAssistant,
		Outcome:   conversation.OutcomeDone,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: "回答"}},
		ToolCalls: []tool.Call{{Name: "echo", Args: json.RawMessage(`{"m":"x"}`)}},
	}})
	emit(t, ui, port.HistoryEvent{Message: conversation.Message{
		Role:       conversation.RoleTool,
		ToolResult: &tool.Result{CallID: "c", OK: true, Output: "pong"},
	}})
	emit(t, ui, port.HistoryEvent{Message: conversation.Message{
		Role:       conversation.RoleTool,
		ToolResult: &tool.Result{CallID: "c", OK: false, Err: "boom"},
	}})
	emit(t, ui, port.HistoryEvent{Message: conversation.Message{
		Role:    conversation.RoleSystem,
		Content: []conversation.Part{{Kind: conversation.PartText, Text: "压缩摘要"}},
	}})
	want := "> 你好\n" +
		"[tool] echo {\"m\":\"x\"}\n" +
		"回答\n" +
		"[tool ok] pong\n" +
		"[tool failed] boom\n" +
		"压缩摘要\n"
	if buf.String() != want {
		t.Fatalf("buf = %q, want %q", buf.String(), want)
	}
}

// TestHistoryStripsControlSequences 回放内容同样只渲染不执行（§9）：
// GBK 乱码字节（0x9B = C1 CSI）与 ANSI 序列不得直通终端——复现"输出清掉前几行"。
func TestHistoryStripsControlSequences(t *testing.T) {
	var buf bytes.Buffer
	ui := New(strings.NewReader(""), &buf)
	emit(t, ui, port.HistoryEvent{Message: conversation.Message{
		Role: conversation.RoleUser,
		Content: []conversation.Part{{
			Kind: conversation.PartText,
			Text: "os: Windows\x9b2J\rnext\x1b[31mred\x1b[0m",
		}},
	}})
	got := buf.String()
	for _, bad := range []string{"\x9b", "\r", "\x1b"} {
		if strings.Contains(got, bad) {
			t.Fatalf("回放直通控制字节 %q: %q", bad, got)
		}
	}
	if !strings.Contains(got, "Windows") || !strings.Contains(got, "next") {
		t.Fatalf("正常文本被误删: %q", got)
	}
}

// TestPreviewStripsGBKInvalidUTF8 工具结果预览剥非法 UTF-8（Windows cmd 默认
// OEM 代码页输出），与 uitui.preview 同口径。
func TestPreviewStripsGBKInvalidUTF8(t *testing.T) {
	in := []byte{'d', 'i', 'r', ' ', 0xc4, 0xe3, 0xba, 0xc3, '\r', '\n', 'o', 'k'}
	got := preview(in)
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, 0xfffd) {
		t.Fatalf("preview = %q, want 无 CR/无替换符", got)
	}
	if !strings.Contains(got, "dir") || !strings.Contains(got, "ok") {
		t.Fatalf("preview = %q, 正常 ASCII 应保留", got)
	}
}

// emit 触发 Emit 并断言无错。
func emit(t *testing.T, ui *UI, ev port.Event) {
	t.Helper()
	if err := ui.Emit(context.Background(), ev); err != nil {
		t.Fatalf("emit %T: %v", ev, err)
	}
}
