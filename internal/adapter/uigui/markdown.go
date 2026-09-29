package uigui

// markdown.go 助手正文 markdown 渲染（D65/D66/§15.3）：goldmark（CommonMark，依赖树
// 既有）定稿解析为结构块，渲染期作为消息级复合行进单气泡（D66）——行选（D63）/滚动
// 手势/淡出合成（D62）等行机制零改动复用。行内剥标记保文本（material.Label 单一样式；
// 行内富样式留 richtext 后续增量）；live 草稿不解析（D33「流式原样、定稿渲染」）。

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	gmast "github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mdParser CommonMark 解析器（默认无扩展：表格按原文段落呈现、可读，D65 否决 GFM）。
var mdParser = goldmark.New()

// mdKind markdown 结构块种类（解析层中间表示；渲染映射见 UI.mdViews）。
type mdKind int

const (
	mdPara     mdKind = iota // 段落（含 HTML 块的原文行）
	mdHeading                // 标题（level = 1–6）
	mdCode                   // 代码块（围栏/缩进；lang = 语言标签）
	mdListItem               // 列表项（前缀已在 text；level = 嵌套深度，order = 有序编号）
	mdQuote                  // 引用（前缀已在 text）
	mdRule                   // 分隔线
)

// mdBlock markdown 结构块。
type mdBlock struct {
	kind  mdKind
	text  string
	level int    // 标题级别 / 列表嵌套深度
	order int    // 有序列表编号（0 = 无序）
	lang  string // 代码块语言标签
}

// parseMarkdown 源文本 → 结构块（只遍历顶层与列表/引用子树；解析无错误路径，
// 对抗性输入的兜底在 UI.mdViews 的 recover）。
func parseMarkdown(src string) []mdBlock {
	source := []byte(src)
	doc := mdParser.Parser().Parse(gmtext.NewReader(source))
	var blocks []mdBlock
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		blocks = appendBlock(blocks, c, source)
	}
	return blocks
}

// appendBlock 单个顶层块 → 结构块（列表/引用就地展开为平铺块）。
func appendBlock(blocks []mdBlock, n gmast.Node, src []byte) []mdBlock {
	switch n := n.(type) {
	case *gmast.Heading:
		return append(blocks, mdBlock{kind: mdHeading, level: n.Level,
			text: mdTrimNL(mdInlineText(n, src))})
	case *gmast.Paragraph, *gmast.TextBlock:
		return append(blocks, mdBlock{kind: mdPara,
			text: mdTrimNL(mdInlineText(n, src))})
	case *gmast.FencedCodeBlock:
		return append(blocks, mdBlock{kind: mdCode, lang: string(n.Language(src)),
			text: mdTrimNL(mdCodeLines(n, src))})
	case *gmast.CodeBlock:
		return append(blocks, mdBlock{kind: mdCode, text: mdTrimNL(mdCodeLines(n, src))})
	case *gmast.ThematicBreak:
		return append(blocks, mdBlock{kind: mdRule})
	case *gmast.Blockquote:
		start := len(blocks)
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			blocks = appendBlock(blocks, c, src)
		}
		for i := start; i < len(blocks); i++ {
			blocks[i].kind = mdQuote
			blocks[i].text = "▏ " + strings.ReplaceAll(blocks[i].text, "\n", "\n▏ ")
		}
		return blocks
	case *gmast.List:
		return mdAppendList(blocks, n, src, 0)
	case *gmast.HTMLBlock: // HTML 块按原文文本行呈现（§9 只渲染不执行）
		return append(blocks, mdBlock{kind: mdPara, text: mdTrimNL(mdCodeLines(n, src))})
	default:
		return blocks
	}
}

// mdAppendList 列表 → 平铺列表项块（嵌套列表紧随父项、深度缩进；软换行的续行对齐前缀）。
func mdAppendList(blocks []mdBlock, n *gmast.List, src []byte, depth int) []mdBlock {
	num := n.Start
	indent := strings.Repeat("  ", depth)
	for li := n.FirstChild(); li != nil; li = li.NextSibling() {
		var parts []string
		var nested []*gmast.List
		for c := li.FirstChild(); c != nil; c = c.NextSibling() {
			if sub, ok := c.(*gmast.List); ok {
				nested = append(nested, sub)
				continue
			}
			if s := mdTrimNL(mdInlineText(c, src)); s != "" {
				parts = append(parts, s)
			}
		}
		text := strings.Join(parts, "\n")
		if n.IsOrdered() {
			cont := indent + "   "
			blocks = append(blocks, mdBlock{kind: mdListItem, level: depth, order: num,
				text: fmt.Sprintf("%s%d. %s", indent, num, strings.ReplaceAll(text, "\n", "\n"+cont))})
			num++
		} else {
			blocks = append(blocks, mdBlock{kind: mdListItem, level: depth,
				text: indent + "• " + strings.ReplaceAll(text, "\n", "\n"+indent+"  ")})
		}
		for _, sub := range nested {
			blocks = mdAppendList(blocks, sub, src, depth+1)
		}
	}
	return blocks
}

// mdInlineText 行内子树 → 纯文本（行内剥标记，D65）：Text 段原文（Raw 段逐字，如
// code span/行内标签，§9 只渲染不执行）、软/硬换行保 \n、autolink 取可读文本；
// 转义与实体在 mdUnescape 解出。
func mdInlineText(n gmast.Node, src []byte) string {
	var b strings.Builder
	gmast.Walk(n, func(c gmast.Node, entering bool) (gmast.WalkStatus, error) {
		if !entering {
			return gmast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *gmast.Text:
			if t.IsRaw() {
				b.Write(t.Segment.Value(src))
				return gmast.WalkContinue, nil
			}
			b.WriteString(mdUnescape(t.Segment.Value(src)))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte('\n')
			}
		case *gmast.RawHTML: // 行内原始 HTML：逐字呈现为文本（§9 只渲染不执行）
			for i := 0; i < t.Segments.Len(); i++ {
				seg := t.Segments.At(i)
				b.Write(seg.Value(src))
			}
			return gmast.WalkSkipChildren, nil
		case *gmast.String:
			if t.IsRaw() {
				return gmast.WalkSkipChildren, nil // 原始行内 HTML 不渲染
			}
			b.Write(t.Value)
			return gmast.WalkSkipChildren, nil
		case *gmast.AutoLink:
			if l := t.Label(src); len(l) > 0 {
				b.WriteString(mdUnescape(l))
			} else {
				b.Write(t.URL(src))
			}
			return gmast.WalkSkipChildren, nil
		}
		return gmast.WalkContinue, nil
	})
	return b.String()
}

// mdUnescape 复刻 goldmark html defaultWriter.Write 的转义/实体出口语义（不做 HTML
// 转义——GUI 直接吃文本）：反斜杠转义优先于实体（`\&amp;` → 字面 `&amp;`）、命名与
// 十/十六进制数字实体解出。Raw 段（code span/行内标签）不走此处，内容逐字保真。
func mdUnescape(src []byte) string {
	var b strings.Builder
	b.Grow(len(src))
	n := 0 // 输出水位：src[:n] 已拷出，遇转义/实体先 flush 再写替身
	limit := len(src)
	flush := func(end int) { b.Write(src[n:end]) }
	escaped := false
	for i := 0; i < limit; i++ {
		c := src[i]
		if escaped && util.IsPunct(c) {
			flush(i - 1)
			b.WriteByte(c)
			n = i + 1
			escaped = false
			continue
		}
		if c == '&' {
			next := i + 1
			if next < limit && src[next] == '#' {
				if nnext := next + 1; nnext < limit {
					nc := src[nnext]
					if nc == 'x' || nc == 'X' { // &#x22;
						start := nnext + 1
						j, ok := util.ReadWhile(src, [2]int{start, limit}, util.IsHexDecimal)
						if ok && j < limit && src[j] == ';' && j-start < 7 {
							v, _ := strconv.ParseUint(string(src[start:j]), 16, 32)
							flush(i)
							b.WriteRune(rune(v))
							i, n = j, j+1
							continue
						}
					} else if nc >= '0' && nc <= '9' { // &#1234;
						start := nnext
						j, ok := util.ReadWhile(src, [2]int{start, limit}, util.IsNumeric)
						if ok && j < limit && j-start < 8 && src[j] == ';' {
							v, _ := strconv.ParseUint(string(src[start:j]), 10, 32)
							flush(i)
							b.WriteRune(rune(v))
							i, n = j, j+1
							continue
						}
					}
				}
			} else { // &name;
				start := next
				j, ok := util.ReadWhile(src, [2]int{start, limit}, util.IsAlphaNumeric)
				if ok && j < limit && src[j] == ';' {
					if entity, ok := util.LookUpHTML5EntityByName(string(src[start:j])); ok {
						flush(i)
						b.Write(entity.Characters)
						i, n = j, j+1
						continue
					}
				}
			}
			i = next - 1 // '&' 字面：越过实体名扫描，对齐 Writer.Write
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		escaped = false
	}
	flush(limit)
	return b.String()
}

// mdCodeLines 代码/HTML 块行原值拼接（逐字保真，不解转义）。
func mdCodeLines(n gmast.Node, src []byte) string {
	var b strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		l := n.Lines().At(i)
		b.Write(l.Value(src))
	}
	return b.String()
}

// mdTrimNL 去尾部换行（行尾硬换行/围栏收尾）。
func mdTrimNL(s string) string { return strings.TrimRight(s, "\n") }

// mdCacheLimit 展开缓存上限（条）：超限整表重建——长会话防膨胀，重解析代价可忽略。
const mdCacheLimit = 256

// mdBlocks 助手定稿文本 → markdown 结构块（D65/D66）：按原文缓存（解析结果不可变）；
// 空文本块剔除（空围栏等不占气泡空间）；解析 panic 或空结果回退单段落原文
// （帧循环不可挂，goldmark 虽经模糊测试仍兜底）。
func (u *UI) mdBlocks(src string) (blocks []mdBlock) {
	if u.mdCache == nil {
		u.mdCache = map[string][]mdBlock{}
	}
	if v, ok := u.mdCache[src]; ok {
		return v
	}
	defer func() {
		if recover() != nil || len(blocks) == 0 {
			blocks = []mdBlock{{kind: mdPara, text: src}}
		}
		if len(u.mdCache) >= mdCacheLimit {
			u.mdCache = map[string][]mdBlock{}
		}
		u.mdCache[src] = blocks
	}()
	parsed := parseMarkdown(src)
	blocks = parsed[:0]
	for _, b := range parsed {
		if b.kind == mdRule || strings.TrimSpace(b.text) != "" {
			blocks = append(blocks, b)
		}
	}
	return blocks
}
