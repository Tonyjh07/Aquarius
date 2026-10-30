package app

// character_report.go 角色卡导入 / 查看回执的正文（§5.17 / D247）。
//
// 「装成完整支持比少说更糟」：用户拿一张依赖正则脚本的卡来，会以为脚本在跑。所以回执按
// 四类分列「读了什么、没读什么」——世界书 / 资产 / 本版不执行的机制 / 只展示字段——再补
// 两类提示：界面 HTML 降级、开场白偏长。渲染只读事实，不改写卡文本（落树即终稿的铁律）。

import (
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// htmlTags 判「卡的文本里成规模用了 HTML」的标签集（§5.17 界面卡降级提示）。
// 只认常见块级 / 标记标签：`<START>` / `<BOT>` / `<USER>` 这些卡方言不在此列，不会被误报。
var htmlTags = map[string]bool{
	"div": true, "span": true, "style": true, "img": true, "br": true, "p": true,
	"a": true, "font": true, "table": true, "tr": true, "td": true, "th": true,
	"ul": true, "ol": true, "li": true, "script": true, "pre": true, "code": true,
	"blockquote": true, "hr": true, "section": true, "iframe": true, "svg": true,
	"video": true, "audio": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "em": true, "strong": true,
}

// characterReportLines 回执正文（§5.17 四类 + 两类提示），不含首行标题。
//
// greetingBudget = 压缩阈值的四分之一（tokens），来自 agent 的上下文预算；<=0 时不判开场白长度。
func characterReportLines(card port.CharacterCard, opts CharacterOptions, greetingBudget int) []string {
	info, profile := card.Info, card.Profile
	var lines []string

	// 一、世界书：总数 + 其中 constant 且 enabled 的条数（按开关说明注没注入）。
	if info.BookEntries > 0 {
		constN := countConstantEnabled(profile.BookEntries)
		if opts.InjectConstantBook {
			lines = append(lines, fmt.Sprintf(
				"世界书：共 %d 条，其中 %d 条常驻条目已注入人格（每轮都付 token）", info.BookEntries, constN))
		} else {
			lines = append(lines, fmt.Sprintf(
				"世界书：共 %d 条全部保留未注入，其中 %d 条是常驻条目（当前开关下不注入）", info.BookEntries, constN))
		}
	}

	// 二、资产：图标作头像，其余随原包保留、不内联不下载。
	if info.Assets > 0 || info.HasAvatar {
		iconUsed := 0
		if info.HasAvatar && info.Assets > 0 {
			iconUsed = 1
		}
		line := "资产："
		if info.HasAvatar {
			line += "图标已用作头像"
		} else {
			line += "没有图标"
		}
		if extra := info.Assets - iconUsed; extra > 0 {
			line += fmt.Sprintf("；其余 %d 个资产随原包保留，本版不内联、不下载", extra)
		}
		lines = append(lines, line)
	}

	// 三、本版不执行的机制：明说保留、不运行，别让用户以为脚本在跑。
	if len(info.Unsupported) > 0 {
		lines = append(lines, fmt.Sprintf(
			"不执行的机制：%s —— 已随原卡保留，本版一律不运行", strings.Join(info.Unsupported, "、")))
	}

	// 四、只展示字段：说明它们只作展示、不进模型上下文。
	lines = append(lines, "只展示字段：作者备注、标签、作者、版本、导入日期只作展示，不进模型上下文。")

	// 提示一、界面 HTML 降级：不净化、不改写，标签会原样显示。
	if cardHasHTML(profile) {
		lines = append(lines, "界面提示：卡的文本里有 HTML 标签，界面会按纯文本显示，标签会原样出现。")
	}

	// 提示二、开场白偏长：给一行 token 估算与占位提醒（不截断、不改写卡）。
	if greetingBudget > 0 {
		lines = append(lines, openingLengthHints(profile, greetingBudget)...)
	}
	return lines
}

// countConstantEnabled 世界书里 constant 且 enabled 的条数（默认开注入时真正进人格的条数）。
func countConstantEnabled(entries []port.BookEntry) int {
	n := 0
	for _, e := range entries {
		if e.Constant && e.Enabled {
			n++
		}
	}
	return n
}

// cardHasHTML 卡的投影字段（含常驻世界书正文）里是否成规模用了 HTML 标签。
func cardHasHTML(p port.CharacterProfile) bool {
	texts := []string{p.Description, p.Personality, p.Scenario, p.SystemPrompt, p.Examples, p.FirstMes}
	texts = append(texts, p.Greetings...)
	for _, e := range p.BookEntries {
		texts = append(texts, e.Content)
	}
	return containsHTMLBlock(texts...)
}

// containsHTMLBlock 文本里是否有 `<tag` 形式的已知标签（大小写不敏感，容许 `< / div`）。
func containsHTMLBlock(texts ...string) bool {
	for _, s := range texts {
		for i := 0; i < len(s); i++ {
			if s[i] != '<' {
				continue
			}
			j := i + 1
			if j < len(s) && s[j] == '/' {
				j++
			}
			for j < len(s) && s[j] == ' ' {
				j++
			}
			k := j
			for k < len(s) && isASCIILetter(s[k]) {
				k++
			}
			if k == j {
				continue
			}
			if htmlTags[strings.ToLower(s[j:k])] {
				return true
			}
		}
	}
	return false
}

// isASCIILetter 只判参数字节是否为英文字母（标签名只含字母）。
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// openingLengthHints 超长开场白的提示（§5.17）：估算超压缩阈值四分之一的开场白逐条报
// token 与占位提醒。开场白合并列表 [first_mes, ...alternate_greetings]，序号从 1 起。
func openingLengthHints(p port.CharacterProfile, budget int) []string {
	openings := make([]string, 0, 1+len(p.Greetings))
	openings = append(openings, p.FirstMes)
	openings = append(openings, p.Greetings...)
	var hints []string
	for i, o := range openings {
		if tokens := estimateTokens(o); tokens > budget {
			hints = append(hints, fmt.Sprintf(
				"开场白偏长：第 %d 条约 %d tokens（压缩阈值四分之一约 %d），会占掉首轮可用上下文的一大块；卡文本原样使用，不截断。",
				i+1, tokens, budget))
		}
	}
	return hints
}
