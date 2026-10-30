package app

import (
	"regexp"
	"strings"
)

// cardMacroRe 卡文本宏的单趟匹配（§7.7）：`{{ name }}` 与 `<name>` 两种写法。
//
// 名字限定 ASCII 字母：实测只出现 char / user / date / original / start 这些词。
// 带冒号或下划线的写法（`{{roll:d14}}`、`{{user_name}}`）因此整段不匹配、原样保留——
// 契约要的是「认不出的不动」，不是「尽力拆一半」。
var cardMacroRe = regexp.MustCompile(`\{\{\s*([A-Za-z]+)\s*\}\}|<\s*([A-Za-z]+)\s*>`)

// expandMacros 替换卡文本里的宏（§7.7）：`{{char}}` / `{{user}}` / `{{date}}` /
// `{{original}}` 与 `<BOT>` / `<USER>` / `<START>` 三种别名。
//
// 单趟、不递归：替换进去的值里的 `{{...}}` 不再扫第二遍——否则 `user_name` 里写个宏
// 就能自我放大。认不出的写法原样保留；大小写不敏感（实测 `{{Char}}` 213 次、
// `{{User}}` 40 次，按大小写敏感处理等于让两成引用漏过去）；`<start>` 归一为 `<START>`
// （它在示例里是「换一段对话」的分块标记，切块时保留它本身）。
//
// user 为空时取 "User"；original 只有 `@char.system` 与 `Tail` 两处传 U，其余处传空。
func expandMacros(text, char, user, date, original string) string {
	if !strings.Contains(text, "{{") && !strings.Contains(text, "<") {
		return text
	}
	if user == "" {
		user = "User"
	}
	return cardMacroRe.ReplaceAllStringFunc(text, func(m string) string {
		name := strings.ToLower(macroName(m))
		angle := strings.HasPrefix(strings.TrimSpace(m), "<")
		switch name {
		case "char":
			if angle { // 只认 {{char}}；尖括号形式里 char 不是契约里的别名
				return m
			}
			return char
		case "bot":
			if !angle { // 只认 <BOT>
				return m
			}
			return char
		case "user":
			return user
		case "date":
			if angle {
				return m
			}
			return date
		case "original":
			if angle {
				return m
			}
			return original
		case "start":
			if !angle { // 分块符只有 <START> 这一种写法
				return m
			}
			return "<START>"
		default:
			return m
		}
	})
}

// macroName 从命中的宏串里取出名字（去掉 `{{ }}` 或 `< >` 与两侧空白）。
func macroName(m string) string {
	m = strings.TrimSpace(m)
	switch {
	case strings.HasPrefix(m, "{{") && strings.HasSuffix(m, "}}"):
		return strings.TrimSpace(m[2 : len(m)-2])
	case strings.HasPrefix(m, "<") && strings.HasSuffix(m, ">"):
		return strings.TrimSpace(m[1 : len(m)-1])
	}
	return ""
}
