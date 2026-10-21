package uitui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Ask 提问通道的 TUI 降级实现（D184）：逐题在转写区打印题干与编号选项，
// 然后等输入行给一行回答——没有页签与键位，语义与 repl 同口径：
//   - 数字 = 选第 N 项（1 起；多选用逗号/空格分隔）；
//   - 其他文本 = 自由答案；
//   - 空行 = 本题不答（TUI 的空行被输入行吞掉，等价于直接回车）；
//   - `/dismiss` = 放弃全部（不是跳过本题）。
//
// 中断本轮仍靠 Ctrl+C（ctx 取消，与停止生成同路径）。
func (u *UI) Ask(ctx context.Context, req port.AskRequest) (port.AskAnswer, error) {
	if err := ctx.Err(); err != nil {
		return port.AskAnswer{}, err
	}
	if len(req.Questions) == 0 {
		return port.AskAnswer{}, nil
	}
	ans := port.AskAnswer{Answers: make([][]string, len(req.Questions))}
	for i, q := range req.Questions {
		picked, dismissed, err := u.askOne(ctx, i, len(req.Questions), q)
		if err != nil {
			return port.AskAnswer{}, err
		}
		if dismissed {
			return port.AskAnswer{Dismissed: true}, nil
		}
		ans.Answers[i] = picked
	}
	return ans, nil
}

// say 投递一行文本到转写区；无 Program（单测直驱）时静默丢弃。
func (u *UI) say(text string) {
	if u.prog == nil {
		return
	}
	u.prog.Send(sayMsg{text: text})
}

// askOne 打印一题并等一行回答（只读 inCh，不读 wakeCh——后台任务完成不能替用户答题）。
func (u *UI) askOne(ctx context.Context, idx, total int, q port.AskQuestion) (picked []string, dismissed bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	u.say(askPromptText(idx, total, q))
	for {
		select {
		case in := <-u.inCh:
			return u.parseAskLine(in, q)
		case <-u.eofCh:
			// 输入流结束：不替用户编答案，按「放弃全部」收场。
			return nil, true, nil
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

// parseAskLine 解析一行回答（与 repl 同口径）。
func (u *UI) parseAskLine(in port.UserInput, q port.AskQuestion) ([]string, bool, error) {
	text := strings.TrimSpace(in.Text)
	if in.Command != nil {
		if in.Command.Name == "dismiss" {
			return nil, true, nil
		}
		// 其他命令在提问期间没有语义：当作自由答案原样回传（不执行）。
		text = "/" + in.Command.Name + strings.Join(append([]string{""}, in.Command.Args...), " ")
	}
	switch {
	case text == "":
		return nil, false, nil
	case strings.EqualFold(text, "/dismiss") || strings.EqualFold(text, "dismiss"):
		return nil, true, nil
	}
	if picks, ok := parseAskChoice(text, q.Options); ok {
		if !q.Multiple && len(picks) > 1 {
			u.say(fmt.Sprintf("（本题为单选，只取「%s」）", picks[0]))
			picks = picks[:1]
		}
		return picks, false, nil
	}
	return []string{text}, false, nil
}

// askPromptText 提问块文本（题干 + 编号选项 + 一行约定说明）。
func askPromptText(idx, total int, q port.AskQuestion) string {
	var b strings.Builder
	if total > 1 {
		fmt.Fprintf(&b, "（第 %d/%d 题）", idx+1, total)
	}
	b.WriteString(q.Question)
	for i, o := range q.Options {
		fmt.Fprintf(&b, "\n  %d) %s", i+1, o.Label)
		if o.Description != "" {
			b.WriteString(" — " + o.Description)
		}
	}
	switch {
	case len(q.Options) == 0:
		b.WriteString("\n  （直接输入答案；/dismiss = 放弃全部）")
	case q.Multiple:
		b.WriteString("\n  （可多选：编号用逗号或空格分隔；直接输入文字 = 自己的答案；/dismiss = 放弃全部）")
	default:
		b.WriteString("\n  （输入编号；直接输入文字 = 自己的答案；/dismiss = 放弃全部）")
	}
	return b.String()
}

// parseAskChoice 把一行回答解析成选项标签（按编号 1 起取 label）；不是纯编号则 ok=false。
func parseAskChoice(line string, opts []port.AskOption) ([]string, bool) {
	if len(opts) == 0 {
		return nil, false
	}
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(fields))
	seen := map[int]bool{}
	for _, f := range fields {
		v, err := strconv.Atoi(strings.TrimSuffix(f, ")"))
		if err != nil || v < 1 || v > len(opts) {
			return nil, false
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, opts[v-1].Label)
	}
	return out, true
}
