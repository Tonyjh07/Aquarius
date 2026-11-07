package acpgate

// 执行控制类文件：写这些位置要单独问一次。
//
// 协议点名的是三类——版本控制钩子、代理与编辑器设置、MCP 配置。共同点是**它们不是
// 项目内容，而是「之后谁会跑起来」的开关**：往 .git/hooks 放一个脚本，下次 commit 就
// 按它的意思跑；改 .mcp.json，下次启动就多一个外部服务。
//
// 清单是代码常量：不进配置、界面上不可调。能调就等于把这条防线交给「当时想图省事的人」
// （与 D280 的硬名单同一口径）。

import (
	"path/filepath"
	"strings"
)

// executionControlRules 匹配规则：segments 是**连续的路径段**；单元素规则对目录名与
// 文件名都成立（`.mcp.json` 命中的是路径末段）。
var executionControlRules = []struct {
	label    string
	segments []string
}{
	{".git/hooks", []string{".git", "hooks"}},
	{".git/config", []string{".git", "config"}},
	{".gitmodules", []string{".gitmodules"}},
	{".github/workflows", []string{".github", "workflows"}},
	{".vscode", []string{".vscode"}},
	{".idea", []string{".idea"}},
	{".cursor", []string{".cursor"}},
	{".claude", []string{".claude"}},
	{".codex", []string{".codex"}},
	{".mcp.json", []string{".mcp.json"}},
}

// executionControl 命中的规则名（空 = 不是执行控制类文件）。
//
// 逐段比较且大小写不敏感：Windows 上 `.GIT/Hooks` 与 `.git/hooks` 是同一个目录，
// 而在 Unix 上把 `.GIT` 也问一次只是多问一句——这个方向的误差可以接受。
func executionControl(path string) string {
	segs := pathSegments(path)
	for _, rule := range executionControlRules {
		if hasSequence(segs, rule.segments) {
			return rule.label
		}
	}
	return ""
}

// pathSegments 按两种分隔符切段（代理写的路径在 Windows 上也可能用 `/`）。
func pathSegments(p string) []string {
	return strings.FieldsFunc(filepath.ToSlash(p), func(r rune) bool { return r == '/' })
}

// hasSequence 连续子序列匹配（大小写不敏感）。
func hasSequence(segs, want []string) bool {
	if len(want) == 0 || len(segs) < len(want) {
		return false
	}
	for i := 0; i+len(want) <= len(segs); i++ {
		hit := true
		for j, w := range want {
			if !strings.EqualFold(segs[i+j], w) {
				hit = false
				break
			}
		}
		if hit {
			return true
		}
	}
	return false
}
