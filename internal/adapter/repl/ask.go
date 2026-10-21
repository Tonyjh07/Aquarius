package repl

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Ask 提问通道的命令行降级实现（D184）：逐题打印编号选单、读一行回答。
//
// 输入约定（与 GUI 面板同语义，只是没有页签与键位）：
//   - 数字 = 选第 N 项（1 起；多选用逗号/空格分隔，如 `1,3`）；
//   - 其他文本 = 自由答案（含逗号也按一条处理——只有纯编号才算多选）；
//   - 空行 = 本题不答；
//   - `/dismiss` = 放弃全部（不是跳过本题）。
//
// 命令行没有 Esc 输入通道，中断本轮仍靠 Ctrl+C（ctx 取消）。
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

// askOne 问一题：打印题干与选项，读一行并解析。
func (u *UI) askOne(ctx context.Context, idx, total int, q port.AskQuestion) (picked []string, dismissed bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	u.flushDelta()
	var b strings.Builder
	if total > 1 {
		fmt.Fprintf(&b, "（第 %d/%d 题）", idx+1, total)
	}
	b.WriteString(q.Question)
	b.WriteString("\n")
	for i, o := range q.Options {
		fmt.Fprintf(&b, "  %d) %s", i+1, o.Label)
		if o.Description != "" {
			b.WriteString(" — " + o.Description)
		}
		b.WriteString("\n")
	}
	switch {
	case len(q.Options) == 0:
		b.WriteString("  （直接输入答案；空行 = 不答；/dismiss = 放弃全部）\n")
	case q.Multiple:
		b.WriteString("  （可多选：编号用逗号或空格分隔；直接输入文字 = 自己的答案；空行 = 不答；/dismiss = 放弃全部）\n")
	default:
		b.WriteString("  （输入编号；直接输入文字 = 自己的答案；空行 = 不答；/dismiss = 放弃全部）\n")
	}
	b.WriteString("> ")
	if _, err := fmt.Fprint(u.out, b.String()); err != nil {
		return nil, false, fmt.Errorf("repl: 输出提问: %w", err)
	}
	u.prompted = true // 提示符已打，等这一行回答
	for {
		select {
		case r, ok := <-u.lines:
			if !ok {
				// 输入流结束：不替用户编答案，按「放弃全部」收场。
				return nil, true, nil
			}
			if r.err != nil {
				return nil, false, fmt.Errorf("repl: 读取提问回答: %w", r.err)
			}
			u.prompted = false
			line := strings.TrimSpace(r.text)
			switch {
			case line == "/dismiss" || strings.EqualFold(line, "dismiss"):
				return nil, true, nil
			case line == "":
				return nil, false, nil
			}
			if picks, ok := parseAskChoice(line, q.Options); ok {
				if !q.Multiple && len(picks) > 1 {
					// 单选给了多个编号：只认第一个，并说清（不静默吞掉）。
					fmt.Fprintf(u.out, "（本题为单选，只取「%s」）\n", picks[0])
					picks = picks[:1]
				}
				return picks, false, nil
			}
			// 非编号文本 = 用户自己写的答案（有选项时同样成立）。
			return []string{line}, false, nil
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

// parseAskChoice 把一行回答解析成选项标签（按编号 1 起取 label）；不是纯编号则 ok=false。
// 越界编号整体视为「不是编号」——用户可能只是想写带数字的文本。
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
