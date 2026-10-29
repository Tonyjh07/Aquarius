package main

// 注释里的决策编号是**溯源锚**，不是结论：读者不翻 decisions.md 也得能读懂这一行在说什么。
//
// 与 userfacing_test.go 互补——那条守「编号不进用户面」，这条守「编号别在注释里替说明」。
// 反例（真实出现过，动因就是一整块注释只写了编号）：
//
//	// D202/D203：技能页。            ← 去掉编号什么也没说
//	//
//	// ---------------------------------------------
//	// 思考签名（D175 口径三）         ← 只有编号，没有「签名要干什么」
//
// 正例（说明在前，编号放句末作溯源）：
//
//	// 权限判定：工具风险与路径档两列分工，互不叠加（D22）。
//
// 判据是机械的：一段连续注释去掉编号与标点后，剩余可见字符不得少于 minCommentBody。

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// commentIDPattern 注释里的内部标记：决策编号、内部步骤标记、设计文档节号、里程碑。
var commentIDPattern = regexp.MustCompile(
	`\bD\d{2,}\b` + // D22 / D145
		`|\bS\d{1,2}(?:-[0-9]+)?[①-⑳]?[a-z]?` + // S6 / S11-7① / S4d
		`|§\d+(?:\.\d+)*` + // §15.7
		`|\bTier-[0-9]\b` + // Tier-1
		`|\bM[0-9]\b`) // M5

// minCommentBody 一段注释去掉编号后至少要剩这么多可见字符，否则编号就是全部内容。
const minCommentBody = 12

// TestCommentsExplainNotJustCite 扫 cmd/ 与 internal/ 的非测试源码，按「连续 // 行」成块检查。
func TestCommentsExplainNotJustCite(t *testing.T) {
	root := filepath.Join("..", "..")
	var bad []string
	for _, top := range []string{"cmd", "internal", "pluginapi"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue // 该目录尚未建立（如 pluginapi/v1 后移出里程碑）
		}
		werr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", "spike", "temp":
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			rel, _ := filepath.Rel(root, path)
			bad = append(bad, shortCommentBlocks(filepath.ToSlash(rel), string(raw))...)
			return nil
		})
		if werr != nil {
			t.Fatalf("遍历 %s: %v", base, werr)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("注释里的编号代替了说明（去掉编号后不足 %d 字）：\n  %s\n"+
			"写清这一行在做什么，编号放句末作溯源。",
			minCommentBody, strings.Join(bad, "\n  "))
	}
}

// shortCommentBlocks 找出「含编号但去掉编号后过短」的注释块，返回 `<文件>:<行>  <摘要>` 列表。
func shortCommentBlocks(rel, src string) []string {
	var out []string
	var buf []string
	start := 0
	flush := func() {
		if len(buf) == 0 {
			return
		}
		raw := strings.Join(buf, " ")
		if commentIDPattern.MatchString(raw) && visibleLen(commentBody(raw)) < minCommentBody {
			out = append(out, rel+":"+strconv.Itoa(start)+"  "+raw)
		}
		buf, start = nil, 0
	}
	for i, line := range strings.Split(src, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "//") {
			if isRule(s) {
				continue // 纯分隔线（`// ----`）不算注释内容，不撑大计数
			}
			if len(buf) == 0 {
				start = i + 1
			}
			buf = append(buf, strings.TrimSpace(strings.TrimPrefix(s, "//")))
			continue
		}
		flush()
	}
	flush()
	return out
}

// commentBody 去掉编号与标点，只留词，用于判断「这一行到底说了什么」。
func commentBody(s string) string {
	return commentIDPattern.ReplaceAllString(s, " ")
}

// visibleLen 可见字符数（字母与数字；中文按字计）。
func visibleLen(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

// isRule 是否整行只有分隔符（`// ------`）。
func isRule(s string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(s, "//"))
	return visibleLen(body) == 0 && strings.ContainsAny(body, "-=_*")
}

// TestShortCommentBlocksSanity 守门测试自检：判定函数喂正反例，避免「规则写错 / 扫描根找错 /
// 分隔线吃掉整块」把这条守卫变成永远绿。
func TestShortCommentBlocksSanity(t *testing.T) {
	caught := map[string]string{
		"只有编号":      "package x\n\n// D202/D203：技能页。\nfunc f() {}\n",
		"编号+两个字的结论": "package x\n\n// D22 两列分工。\nfunc f() {}\n",
		"分隔线包着短标题":  "package x\n\n// ----\n// 思考签名（D175）\n// ----\nfunc f() {}\n",
		"多行也没说清":    "package x\n\n// D86\n// 确认态。\nfunc f() {}\n",
	}
	for name, src := range caught {
		if got := shortCommentBlocks("x.go", src); len(got) == 0 {
			t.Errorf("%s：应判为「编号代替说明」，实际放过", name)
		}
	}
	passing := map[string]string{
		"编号在句末":  "package x\n\n// 权限判定：工具风险与路径档各管一列、取更严者（D22）。\nfunc f() {}\n",
		"无编号短注释": "package x\n\n// 向前推进一帧。\nfunc f() {}\n",
		"长说明":    "package x\n\n// D204：采样参数热生效——从刚写回的 config 现算一份快照换上去，下一次请求就用新值。\nfunc f() {}\n",
		"纯分隔线":   "package x\n\n// ---------------------------------------------------------------------------\nfunc f() {}\n",
	}
	for name, src := range passing {
		if got := shortCommentBlocks("x.go", src); len(got) > 0 {
			t.Errorf("%s：不应判失败，实际 %v", name, got)
		}
	}
}
