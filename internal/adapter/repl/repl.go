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
	"unicode/utf8"

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
	scanner  *bufio.Scanner
	out      io.Writer
	inDelta  bool // 上一事件是流式增量：写非增量行前先补换行
	thinking bool // 思维链行进行中（D34：[thinking] 前缀已打，等正文/事件收尾）
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

// SetInterrupt Ctrl+C 行为注入（前端接口同形，D33）：REPL 模式下 Ctrl+C 走
// 进程 os.Interrupt → signal ctx 取消，无需桥接（no-op）。
func (u *UI) SetInterrupt(func()) {}

// Close 前端收尾（前端接口同形）：REPL 无终端态需要恢复（no-op）。
func (u *UI) Close() error { return nil }

// Emit 呈现 Turn 事件。
func (u *UI) Emit(_ context.Context, ev port.Event) error {
	switch e := ev.(type) {
	case port.DeltaEvent:
		if e.Delta.Text == "" {
			return nil // 工具调用分片、用量分片不直接渲染
		}
		if e.Delta.Reasoning {
			// 思维链（D34）：单独通道——先关上一行（正文或旧思维链），首片打
			// [thinking] 前缀，连续分片续写同一行。
			if u.inDelta {
				if _, err := io.WriteString(u.out, "\n"); err != nil {
					return err
				}
				u.inDelta = false
			}
			if !u.thinking {
				if _, err := io.WriteString(u.out, "[thinking] "); err != nil {
					return err
				}
				u.thinking = true
			}
			_, err := io.WriteString(u.out, e.Delta.Text)
			return err
		}
		if u.thinking { // 正文首片：关思维链行
			if _, err := io.WriteString(u.out, "\n"); err != nil {
				return err
			}
			u.thinking = false
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
		_, err := fmt.Fprintf(u.out, "error: %s\n", sanitizeControl(e.Err.Error()))
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

// flushDelta 若刚写过流式增量或思维链行则补换行断行。
func (u *UI) flushDelta() {
	if u.inDelta || u.thinking {
		_, _ = io.WriteString(u.out, "\n")
		u.inDelta = false
		u.thinking = false
	}
}

// preview 截断工具参数/结果为单行预览（不可信数据只渲染，DESIGN §9）。
// 先剥控制序列与非法 UTF-8（审查修复：Windows cmd 输出常为 GBK——其中 0x9B 是
// 8 位 C1 CSI 引导符，直通终端会被解析成 ANSI 擦除序列，把转写区前几行清掉；
// 与 uitui.preview 同算法，两前端出口一致）。
func preview(b []byte) string {
	s := strings.TrimSpace(sanitizeControl(string(b)))
	r := []rune(s)
	if len(r) > toolPreviewLen {
		return string(r[:toolPreviewLen]) + "…"
	}
	return s
}

// sanitizeControl 剥除转义序列与控制字符（保留 \n\t；DESIGN §9 不可信数据只渲染）。
// 算法与 internal/adapter/uitui、notify 的同名函数一致——三处分别守护通知面与
// 两个前端的转写面。
func sanitizeControl(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b && i+1 < len(rs) {
			switch rs[i+1] {
			case '[': // CSI：吞至终结字节（0x40–0x7E）
				j := i + 2
				for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
					j++
				}
				if j < len(rs) {
					i = j
				} else {
					i = len(rs) - 1
				}
				continue
			case ']': // OSC：吞至 BEL 或 ESC\
				j := i + 2
				for j < len(rs) && rs[j] != 0x07 && !(rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\') {
					j++
				}
				if j < len(rs) {
					if rs[j] == 0x1b {
						i = j + 1 // ST 的 ESC\ 两字符都要跳过
					} else {
						i = j
					}
				} else {
					i = len(rs) - 1
				}
				continue
			default: // 其余两字符转义
				i++
				continue
			}
		}
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
