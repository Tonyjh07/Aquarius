package perm

// 不可覆盖的硬名单（D280 第 6 条）：策略表唯一说了不算的地方。
//
// 三类目标不参与「自动放行」：
//  1. **执行控制类文件**——它们不是项目内容，而是「之后谁会跑起来」的开关
//     （.git/hooks、.vscode、.mcp.json 等）；
//  2. 装配层给的**敏感根**（密钥目录、配置目录、系统目录）；
//  3. 目标无法判定时（空串）保守处理：不算硬名单，按常规路径走。
//
// 清单是代码常量，不进配置、界面不可调。能调就等于把这条防线交给当时想图省事的人。

import (
	"path/filepath"
	"strings"
)

// hardRules 匹配规则：Segments 是**连续的路径段**；单元素规则对目录名与文件名
// 都成立（`.mcp.json` 命中的是路径末段）。
var hardRules = []struct {
	Label    string
	Segments []string
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

// HardLabel 命中的执行控制类规则名（空 = 不是这一类）。
//
// 逐段比较且大小写不敏感：Windows 上 `.GIT/Hooks` 与 `.git/hooks` 是同一个目录，
// 在 Unix 上把 `.GIT` 也问一次只是多问一句——这个方向的误差可以接受。
func HardLabel(path string) string {
	segs := pathSegments(path)
	for _, rule := range hardRules {
		if hasSequence(segs, rule.Segments) {
			return rule.Label
		}
	}
	return ""
}

// HardTarget 目标是否命中硬名单：执行控制类文件，或落在任一敏感根之下。
// 命中后即便该工具设了自动放行，也要照问。
func HardTarget(target string, sensitive []string) bool {
	if strings.TrimSpace(target) == "" {
		return false
	}
	if HardLabel(target) != "" {
		return true
	}
	for _, root := range sensitive {
		if withinRoot(target, root) {
			return true
		}
	}
	return false
}

// withinRoot 目标是否在 root 之内（含 root 本身）。纯字符串比较：
// 大小写不敏感、两种分隔符都认；不做 symlink 展开——这是「宁可多问」的一侧，
// 展开由调用方在需要严格判定的地方另做（pathres）。
func withinRoot(target, root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	t := strings.ToLower(filepath.ToSlash(filepath.Clean(target)))
	b := strings.ToLower(filepath.ToSlash(filepath.Clean(root)))
	if b == "." || b == "/" {
		return false
	}
	if t == b {
		return true
	}
	if !strings.HasSuffix(b, "/") {
		b += "/"
	}
	return strings.HasPrefix(t, b)
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
