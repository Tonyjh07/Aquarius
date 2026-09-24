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

// emit 触发 Emit 并断言无错。
func emit(t *testing.T, ui *UI, ev port.Event) {
	t.Helper()
	if err := ui.Emit(context.Background(), ev); err != nil {
		t.Fatalf("emit %T: %v", ev, err)
	}
}
