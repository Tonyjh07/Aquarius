package legal

// md → []Block 的最小解析：只认 `# ` / `## ` / `|` 表格 / `- ` 列表 / `**` 整段加粗 /
// 反引号去标记。不做行内混排、不认引用块与嵌套列表——遇到不认识的记号当普通段落原样
// 保留，宁可少强调，也不能让解析失败把向导卡住或把原始 md 泄漏到法律文本上。

import (
	"regexp"
	"strings"
	"unicode"
)

// parse 解析一份正文。
func parse(raw string) (Document, error) {
	var doc Document
	var buf []string // 当前段落累积的行
	cur := -1        // 当前节下标（-1 = 还在导言里）
	inTable := false

	appendBlock := func(b Block) {
		if cur < 0 {
			doc.Lead = append(doc.Lead, b)
			return
		}
		doc.Sections[cur].Blocks = append(doc.Sections[cur].Blocks, b)
	}
	flush := func() {
		if len(buf) == 0 {
			return
		}
		text := joinLines(buf)
		buf = buf[:0]
		b := Block{Kind: Para, Text: stripMarks(text)}
		// 一段里只要出现 ** 就整段判为提示段：半句加粗变整段加粗是「多强调」，
		// 不会漏掉提示义务（行内混排要另一套排版模型，不值当）。
		if strings.Contains(text, "**") {
			b.Kind = Tip
		}
		if strings.TrimSpace(b.Text) == "" {
			return
		}
		appendBlock(b)
	}

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flush()
			inTable = false
		case strings.HasPrefix(trimmed, "# "):
			flush()
			inTable = false
			doc.Title = strings.TrimSpace(trimmed[2:])
		case strings.HasPrefix(trimmed, "## "):
			flush()
			inTable = false
			doc.Sections = append(doc.Sections, Section{Title: strings.TrimSpace(trimmed[3:])})
			cur = len(doc.Sections) - 1
		case strings.HasPrefix(trimmed, "|"):
			flush()
			cols := splitRow(trimmed)
			if isSeparator(cols) {
				continue // |---|---| 只是排版线，不产出内容
			}
			head := !inTable // 一张表的第一行是表头
			inTable = true
			appendBlock(Block{Kind: TableRow, Cols: cols, Head: head})
		case strings.HasPrefix(trimmed, "- "):
			flush()
			inTable = false
			appendBlock(Block{Kind: Bullet, Text: stripMarks(trimmed[2:])})
		default:
			inTable = false
			// 数字编号行另起一段：正文的约定是「一行为一条款」，款项之间不空行（1.1、1.2、
			// 1.3 连着写）。按 markdown 的软换行去拼，一节的六条会糊成一大段。
			if isClauseStart(trimmed) {
				flush()
			}
			buf = append(buf, trimmed)
		}
	}
	flush()
	return doc, nil
}

// clauseStart 数字编号行（`1.1 ` / `2.` 这类条款号）——每行独立成段。
var clauseStart = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*\.?[\s、]`)

// isClauseStart 判断一行是否以条款号开头。
func isClauseStart(line string) bool { return clauseStart.MatchString(line) }

// stripMarks 去掉行内标记（反引号、加粗星号）：第一版不做等宽字体，也不做行内混排。
func stripMarks(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	return strings.ReplaceAll(s, "`", "")
}

// splitRow 拆一行表格：去掉首尾竖线，逐列去空白。
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cols := make([]string, 0, len(parts))
	for _, p := range parts {
		cols = append(cols, strings.TrimSpace(p))
	}
	return cols
}

// isSeparator 判断表格的分隔行（每列只由 `-` 与 `:` 组成，且至少有一个 `-`）。
func isSeparator(cols []string) bool {
	if len(cols) == 0 {
		return false
	}
	dash := false
	for _, c := range cols {
		if c == "" {
			return false
		}
		for _, r := range c {
			if r == '-' {
				dash = true
				continue
			}
			if r != ':' {
				return false
			}
		}
	}
	return dash
}

// joinLines 段内软换行 → 一行：两侧都是汉字（或一侧是中文标点）时不加空格，
// 否则补一个空格，免得英文单词粘在一起。
func joinLines(lines []string) string {
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 && needSpace(lines[i-1], ln) {
			b.WriteByte(' ')
		}
		b.WriteString(ln)
	}
	return b.String()
}

// needSpace 上一行末字符与本行首字符之间要不要补空格。
func needSpace(prev, next string) bool {
	p, n := lastRune(prev), firstRune(next)
	if p == 0 || n == 0 {
		return false
	}
	if isCJK(p) || isCJK(n) {
		return false
	}
	if unicode.IsPunct(n) && unicode.Is(unicode.Han, p) {
		return false
	}
	return true
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

// isCJK 汉字与全角标点（这些字符之间插空格会被看成排版错误）。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3000 && r <= 0x303F) || // CJK 标点
		(r >= 0xFF00 && r <= 0xFFEF) // 全角形式
}
