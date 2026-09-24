// Package repl 实现最简 REPL 式 UI 端口：Presenter + Prompter（标准输入输出）。
// bubbletea TUI 见 uitui（里程碑 M4）。
//
// 呈现约定：文本增量直写（流式观感），任何非增量行前先补换行断行；
// 命令的文本输出经 Say 呈现（Session 返回值由装配根转交）。
package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var (
	_ port.Presenter = (*UI)(nil)
	_ port.Prompter  = (*UI)(nil)
	_ port.Confirmer = (*UI)(nil)
)

// inputLineLimit 单行输入上限（防超长粘贴撑爆 Scanner）。
const inputLineLimit = 1 << 20

// toolPreviewLen 工具事件的预览截断长度（rune）。
const toolPreviewLen = 120

// UI 基于 Reader/Writer 的会话界面。
type UI struct {
	scanner *bufio.Scanner
	out     io.Writer
	inDelta bool // 上一事件是流式增量：写非增量行前先补换行
}

// New 创建 REPL UI。
func New(in io.Reader, out io.Writer) *UI {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), inputLineLimit)
	return &UI{scanner: sc, out: out}
}

// Next 读取一行输入：斜杠开头解析为 Command，其余为文本；io.EOF = 输入流结束。
// 空行返回零值 UserInput（调用方视为无操作）。
func (u *UI) Next(ctx context.Context) (port.UserInput, error) {
	if err := ctx.Err(); err != nil {
		return port.UserInput{}, err
	}
	if !u.scanner.Scan() {
		if err := u.scanner.Err(); err != nil {
			return port.UserInput{}, fmt.Errorf("repl: 读取输入: %w", err)
		}
		return port.UserInput{}, io.EOF
	}
	line := strings.TrimSpace(u.scanner.Text())
	if line == "" {
		return port.UserInput{}, nil
	}
	if strings.HasPrefix(line, "/") {
		fields := strings.Fields(line)
		return port.UserInput{Command: &port.Command{
			Name: strings.TrimPrefix(fields[0], "/"),
			Args: fields[1:],
		}}, nil
	}
	return port.UserInput{Text: line}, nil
}

// Prompt 打印输入提示符（先断开未完的流式行）。
func (u *UI) Prompt() {
	u.flushDelta()
	_, _ = fmt.Fprint(u.out, "> ")
}

// Say 输出一行会话文本（命令输出、启动提示等）。
func (u *UI) Say(text string) {
	if text == "" {
		return
	}
	u.flushDelta()
	_, _ = fmt.Fprint(u.out, text+"\n")
}

// Confirm 逐次确认（/rm 二次确认、Risk=Confirm 工具等）：
// 打印提示并读取一行回答；y/yes（大小写不敏感）为同意，其余与输入流结束均为拒绝。
func (u *UI) Confirm(ctx context.Context, prompt string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	u.flushDelta()
	if _, err := fmt.Fprint(u.out, prompt+" [y/N] "); err != nil {
		return false, fmt.Errorf("repl: 输出确认提示: %w", err)
	}
	if !u.scanner.Scan() {
		if err := u.scanner.Err(); err != nil {
			return false, fmt.Errorf("repl: 读取确认: %w", err)
		}
		return false, nil // 输入流结束：不替用户做破坏性决定
	}
	switch strings.ToLower(strings.TrimSpace(u.scanner.Text())) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Emit 呈现 Turn 事件。
func (u *UI) Emit(_ context.Context, ev port.Event) error {
	switch e := ev.(type) {
	case port.DeltaEvent:
		if e.Delta.Text == "" {
			return nil // 工具调用分片、用量分片不直接渲染
		}
		u.inDelta = true
		_, err := io.WriteString(u.out, e.Delta.Text)
		return err

	case port.ToolCallEvent:
		u.flushDelta()
		if p := preview(e.Call.Args); p != "" {
			_, err := fmt.Fprintf(u.out, "[tool] %s %s\n", e.Call.Name, p)
			return err
		}
		_, err := fmt.Fprintf(u.out, "[tool] %s\n", e.Call.Name)
		return err

	case port.ToolResultEvent:
		u.flushDelta()
		status, detail := "ok", e.Result.Output
		if !e.Result.OK {
			status, detail = "failed", e.Result.Err
		}
		_, err := fmt.Fprintf(u.out, "[tool %s] %s\n", status, preview([]byte(detail)))
		return err

	case port.CommittedEvent:
		u.flushDelta()
		var b strings.Builder
		if e.Message.Outcome != conversation.OutcomeDone {
			fmt.Fprintf(&b, "[%s]\n", e.Message.Outcome)
		}
		if u := e.Message.Usage; u.InputTokens > 0 || u.OutputTokens > 0 {
			fmt.Fprintf(&b, "[usage in=%d out=%d]\n", u.InputTokens, u.OutputTokens)
		}
		if b.Len() == 0 {
			return nil
		}
		_, err := io.WriteString(u.out, b.String())
		return err

	case port.ErrorEvent:
		u.flushDelta()
		_, err := fmt.Fprintf(u.out, "error: %v\n", e.Err)
		return err

	case port.NoticeEvent:
		u.flushDelta()
		_, err := fmt.Fprintf(u.out, "[notice] %s\n", e.Text)
		return err

	default:
		u.flushDelta()
		_, err := fmt.Fprintf(u.out, "%v\n", ev)
		return err
	}
}

// flushDelta 若刚写过流式增量则补换行断行。
func (u *UI) flushDelta() {
	if u.inDelta {
		_, _ = io.WriteString(u.out, "\n")
		u.inDelta = false
	}
}

// preview 截断工具参数/结果为单行预览（不可信数据只渲染，DESIGN §9）。
func preview(b []byte) string {
	s := strings.TrimSpace(string(b))
	r := []rune(s)
	if len(r) > toolPreviewLen {
		return string(r[:toolPreviewLen]) + "…"
	}
	return s
}
