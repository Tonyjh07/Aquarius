package exchange

// md.go 人读与可粘贴的导出（D314）：`md` 与 `html`。
//
// 两者**不是新的互通格式**，是 openai 摊平结果的另一种呈现：同一份 headPath、同一套
// linearText 丢弃计数、同一份 branchNote——同一个会话两种导出丢的东西必须一样，否则回执
// 在撒谎。呈现层的差异：
//   - md：角色标题 + 正文**原样**（不转义不加工——「整份文件拷走就能当上下文」是本条的存在
//     理由），文件头一行标题一行时间戳。
//   - html：同一份摊平内容包一个单文件壳（内联样式、无外链、目录带锚点），文本 HTML 转义、
//     按换行分段。不做 Markdown 渲染（D314 否决 1）：转义是安全的下限，不是可读性的让步。

import (
	"context"
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// mdEntry 摊平链里的一条：线性角色 + 可带走的正文（linearText 的结果）。
type mdEntry struct {
	role string // linearRole 的结果：user / assistant / system
	text string
}

// flattenForHuman openai 同一套摊平（D314）：Head 路径、无正文节点跳过并计数、旁支注记。
// 返回的条目按 Head 路径顺序排列。
func flattenForHuman(c *conversation.Conversation, counts map[string]int) ([]mdEntry, string) {
	var out []mdEntry
	for _, m := range headPath(c) {
		if m.Role == conversation.RoleRoot {
			continue
		}
		text, ok := linearText(m, counts)
		if !ok {
			continue
		}
		out = append(out, mdEntry{role: linearRole(m.Role), text: text})
	}
	return out, branchNote(c)
}

// mdRoleHeading 线性角色 → 二级标题（跟随界面语言；导出文件是给用户的成品）。
func mdRoleHeading(role string) string {
	switch role {
	case "user":
		return i18n.T("exchange.md.role_user")
	case "assistant":
		return i18n.T("exchange.md.role_assistant")
	default:
		return i18n.T("exchange.md.role_system")
	}
}

// exportMD 导出 Head 路径为 Markdown 文档（D314）。
func (e *Exchange) exportMD(ctx context.Context, c *conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	return exportHuman(ctx, c, w, "md", renderMD)
}

// exportHTML 导出同一份摊平内容的单文件网页（D314）。
func (e *Exchange) exportHTML(ctx context.Context, c *conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	return exportHuman(ctx, c, w, "html", renderHTML)
}

// renderFn 摊平条目 → 文本（md 与 html 各一个实现，共用收集与回执）。
type renderFn func(title string, updated time.Time, entries []mdEntry) string

// exportHuman 两种人读格式共用的收集与回执骨架。
func exportHuman(ctx context.Context, c *conversation.Conversation, w io.Writer, format string, render renderFn) (port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return port.ExchangeReport{Format: format}, err
	}
	rep := port.ExchangeReport{Format: format}
	counts := map[string]int{}
	entries, note := flattenForHuman(c, counts)
	// 时间戳取会话的 UpdatedAt 而不是生成时刻：导出应可复现（golden 依赖它），「这份历史
	// 更新到什么时候」对读者也比「文件何时写出」更有用。
	text := render(c.Title, c.UpdatedAt, entries)
	if _, err := io.WriteString(w, text); err != nil {
		return rep, fmt.Errorf("%s: %w", i18n.T("exchange.err.write_messages"), err)
	}
	rep.Bytes = int64(len(text))
	rep.Nodes = len(entries)
	rep.Dropped = droppedLines(counts)
	if note != "" {
		rep.Dropped = append(rep.Dropped, note)
	}
	return rep, nil
}

// renderMD Markdown 呈现：`# 标题` + 时间戳，其后每条消息一个二级标题 + 原样正文。
// 正文不转义不加工（D314 决策 2）；消息之间空一行，粘出去即是干净的历史段落。
func renderMD(title string, gen time.Time, entries []mdEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", title, i18n.Tf("exchange.md.generated", "time", gen.Format(time.RFC3339)))
	for _, en := range entries {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", mdRoleHeading(en.role), en.text)
	}
	return b.String()
}

// htmlShell 单文件网页的壳：样式内联、无外链（D314 决策 3）。
const htmlShell = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>%s</title>
<style>
body{font-family:system-ui,sans-serif;max-width:52em;margin:2em auto;padding:0 1em;line-height:1.6;color:#1a1a1a}
h1{border-bottom:2px solid #ccc;padding-bottom:.3em}
h2{font-size:1.05em;margin:1.6em 0 .4em;color:#555}
p{white-space:pre-wrap;margin:.4em 0}
nav{font-size:.9em;color:#555;border:1px solid #ddd;border-radius:6px;padding:.8em 1.2em;background:#f7f7f7}
nav a{color:#0366d6;text-decoration:none;margin-right:1em}
.gen{color:#888;font-size:.85em}
</style>
</head>
<body>
<h1>%s</h1>
<p class="gen">%s</p>
<nav>%s</nav>
%s
</body>
</html>
`

// renderHTML 单文件网页呈现：目录带每条消息的锚点，正文按角色分节、转义、按换行分段。
func renderHTML(title string, gen time.Time, entries []mdEntry) string {
	var nav, body strings.Builder
	for i, en := range entries {
		heading := mdRoleHeading(en.role)
		fmt.Fprintf(&nav, `<a href="#m%d">%s</a>`, i, html.EscapeString(heading))
		fmt.Fprintf(&body, "\n<h2 id=\"m%d\">%s</h2>\n", i, html.EscapeString(heading))
		for _, para := range strings.Split(en.text, "\n") {
			fmt.Fprintf(&body, "<p>%s</p>\n", html.EscapeString(para))
		}
	}
	return fmt.Sprintf(htmlShell,
		html.EscapeString(title),
		html.EscapeString(title),
		html.EscapeString(i18n.Tf("exchange.md.generated", "time", gen.Format(time.RFC3339))),
		nav.String(),
		body.String())
}
