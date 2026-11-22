package uigui

// markdown_test.go D65 解析与展开测试：结构块映射、行内剥标记、转义/实体出口语义、
// 流式未闭合围栏退化、缓存与回退。

import (
	"fmt"
	"gioui.org/io/input"
	"testing"
)

// blocksOf 便捷：parseMarkdown 后取 kind/text 序列便于断言。
func blocksOf(t *testing.T, src string) []mdBlock {
	t.Helper()
	blocks := parseMarkdown(src)
	if len(blocks) == 0 {
		t.Fatalf("parseMarkdown(%q) 为空", src)
	}
	return blocks
}

// TestParseMarkdownStructural 结构块映射：标题/段落/代码/列表/引用/分隔线（D65）。
func TestParseMarkdownStructural(t *testing.T) {
	t.Parallel()
	blocks := blocksOf(t, "# 标题一\n\n## 二级\n\n正文段落，两行：\n续行\n\n```go\nx := 1\n```\n\n---")
	want := []struct {
		kind  mdKind
		text  string
		level int
		lang  string
	}{
		{mdHeading, "标题一", 1, ""},
		{mdHeading, "二级", 2, ""},
		{mdPara, "正文段落，两行：\n续行", 0, ""}, // 软换行保 \n（聊天规则）
		{mdCode, "x := 1", 0, "go"},
		{mdRule, "", 0, ""},
	}
	if len(blocks) != len(want) {
		t.Fatalf("块数 = %d, want %d: %+v", len(blocks), len(want), blocks)
	}
	for i, w := range want {
		got := blocks[i]
		if got.kind != w.kind || got.text != w.text || got.level != w.level || got.lang != w.lang {
			t.Fatalf("blocks[%d] = %+v, want %+v", i, got, w)
		}
	}
}

// TestParseMarkdownLists 列表平铺：无序/有序前缀、嵌套缩进紧随父项、续行对齐（D65）。
func TestParseMarkdownLists(t *testing.T) {
	t.Parallel()
	blocks := blocksOf(t, "- 甲\n- 乙\n  - 丙\n\n1. 第一\n2. 第二")
	want := []string{"• 甲", "• 乙", "  • 丙", "1. 第一", "2. 第二"}
	if len(blocks) != len(want) {
		t.Fatalf("块数 = %d, want %d: %+v", len(blocks), len(want), blocks)
	}
	for i, w := range want {
		if blocks[i].kind != mdListItem || blocks[i].text != w {
			t.Fatalf("blocks[%d] = %+v, want %q", i, blocks[i], w)
		}
	}
	if blocks[4].order != 2 {
		t.Fatalf("有序编号 = %d, want 2", blocks[4].order)
	}
}

// TestParseMarkdownTable 表格解析（D325）：表头 + 表体入网格、单元格行内剥标记、
// 缺列补空；不是表格的伪表（缺分隔行）仍是段落原文。
func TestParseMarkdownTable(t *testing.T) {
	t.Parallel()
	blocks := blocksOf(t, "| 名称 | 数量 |\n| --- | --: |\n| **甲** | 1 |\n| 乙 |  |")
	if len(blocks) != 1 || blocks[0].kind != mdTable || blocks[0].grid == nil {
		t.Fatalf("blocks = %+v, want 单个表格块", blocks)
	}
	g := blocks[0].grid
	if len(g.head) != 2 || g.head[0] != "名称" || g.head[1] != "数量" {
		t.Fatalf("head = %v, want [名称 数量]", g.head)
	}
	if len(g.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(g.rows))
	}
	if g.rows[0][0] != "甲" || g.rows[0][1] != "1" {
		t.Fatalf("row0 = %v, want 行内标记已剥", g.rows[0])
	}
	if g.rows[1][1] != "" { // 缺列补空
		t.Fatalf("row1 = %v, want 空单元格补齐", g.rows[1])
	}
	if n := mdKeys(blocks[0]); n != 6 { // (2 行体 + 1 表头) × 2 列
		t.Fatalf("mdKeys = %d, want 6", n)
	}
	// 伪表（缺分隔行）不进网格：仍是段落原文。
	blocks = blocksOf(t, "| 甲 | 乙 |\n| 丙 | 丁 |")
	if len(blocks) != 1 || blocks[0].kind != mdPara || blocks[0].text == "" {
		t.Fatalf("伪表 blocks = %+v, want 段落", blocks)
	}
}

// TestParseMarkdownQuote 引用块逐行前缀（D65）。
func TestParseMarkdownQuote(t *testing.T) {
	t.Parallel()
	blocks := blocksOf(t, "> 引用一行\n> 续行\n\n普通段")
	if len(blocks) != 2 {
		t.Fatalf("块数 = %d, want 2: %+v", len(blocks), blocks)
	}
	if blocks[0].kind != mdQuote || blocks[0].text != "▏ 引用一行\n▏ 续行" {
		t.Fatalf("引用块 = %+v", blocks[0])
	}
}

// TestParseMarkdownStreaming 流式未闭合围栏：吞到 EOF 成代码块（D33 定稿渲染前的
// 过程态允许退化呈现）。
func TestParseMarkdownStreaming(t *testing.T) {
	t.Parallel()
	blocks := blocksOf(t, "```go\nx := 1")
	if blocks[0].kind != mdCode || blocks[0].lang != "go" || blocks[0].text != "x := 1" {
		t.Fatalf("未闭合围栏 = %+v", blocks[0])
	}
}

// TestMdInlineStrip 行内剥标记：emphasis/链接取文字/图片取 alt/行内代码逐字/
// autolink/行内原始 HTML 逐字呈现（§9 只渲染不执行）。
func TestMdInlineStrip(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, want string }{
		{"**粗**体", "粗体"},
		{"*斜* 与 _斜2_", "斜 与 斜2"},
		{"`c:\\path`", "c:\\path"},                       // 行内代码逐字（反斜杠不解）
		{"[Google](https://google.com)", "Google"},       // 链接取文字
		{"![截图](img.png)", "截图"},                         // 图片取 alt
		{"\\*不是强调\\*", "*不是强调*"},                         // 反斜杠转义
		{"&amp;&copy;&#65;", "&©A"},                      // 命名/数字实体
		{"<https://example.com>", "https://example.com"}, // autolink
		{"a <b>x</b> c", "a <b>x</b> c"},                 // 原始 HTML 逐字
		{"`&amp;`", "&amp;"},                             // code span 内容不解实体
	}
	for _, c := range cases {
		blocks := parseMarkdown(c.src)
		if len(blocks) != 1 || blocks[0].kind != mdPara {
			t.Fatalf("parseMarkdown(%q) = %+v, want 单段落", c.src, blocks)
		}
		if blocks[0].text != c.want {
			t.Fatalf("parseMarkdown(%q).text = %q, want %q", c.src, blocks[0].text, c.want)
		}
	}
}

// TestMdUnescape 转义优先于实体（对齐 goldmark Writer.Write）：`\&amp;` 保持字面。
func TestMdUnescape(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, want string }{
		{"\\&amp;", "&amp;"}, // 转义的 & 字面，amp; 留原文
		{"a\\*b\\_c", "a*b_c"},
		{"&#x41;&#66;", "AB"},
		{"&nbsp;x", "\u00a0x"},
		{"&unknown;x", "&unknown;x"}, // 未知实体原样
		{"纯文本", "纯文本"},
	}
	for _, c := range cases {
		if got := mdUnescape([]byte(c.src)); got != c.want {
			t.Fatalf("mdUnescape(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}

// TestMdBlocksFallback 空解析结果回退单段落原文；缓存命中返回同一实例（D65/D66）。
func TestMdBlocksFallback(t *testing.T) {
	t.Parallel()
	u := &UI{plat: newFakePlat()} // mdCache 惰性初始化（不依赖 newUI）
	for _, src := range []string{"   ", "\n\n"} {
		blocks := u.mdBlocks(src)
		if len(blocks) != 1 || blocks[0].kind != mdPara || blocks[0].text != src {
			t.Fatalf("mdBlocks(%q) = %+v, want 原文单段落", src, blocks)
		}
	}
	first := u.mdBlocks("# 标题")
	again := u.mdBlocks("# 标题")
	if len(first) != 1 || first[0].kind != mdHeading || first[0].level != 1 {
		t.Fatalf("mdBlocks 解析 = %+v", first)
	}
	if &first[0] != &again[0] {
		t.Fatal("缓存命中应返回同一实例")
	}
}

// TestMdBlocksCacheLimit 缓存上限：超限整表重建（长会话防膨胀，D65）。
func TestMdBlocksCacheLimit(t *testing.T) {
	t.Parallel()
	u := &UI{plat: newFakePlat()}
	for i := 0; i < mdCacheLimit+1; i++ {
		u.mdBlocks(fmt.Sprintf("消息 %d", i))
	}
	if len(u.mdCache) > mdCacheLimit {
		t.Fatalf("缓存条目 = %d, 应在重建后 ≤ %d", len(u.mdCache), mdCacheLimit)
	}
}

// TestFrameItemsMarkdown 助手定稿块 = 单条目携结构块（D66 单回复单气泡）；live 草稿
// 不解析（D33 规则）。
func TestFrameItemsMarkdown(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	u.m.add(blockUser, "给我列个清单")
	u.m.add(blockAssistant, "好的：\n\n- 一项\n- 二项\n\n```go\ndone\n```")
	u.m.drafting = true
	u.m.draft.WriteString("**草稿**不解析")

	items := u.frameItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3: %+v", len(items), items)
	}
	if items[0].kind != blockUser || items[0].selCount() != 1 {
		t.Fatalf("items[0] = %+v（用户行恒 1 键）", items[0])
	}
	a := items[1]
	if a.kind != blockAssistant || a.text != "" {
		t.Fatalf("items[1] = %+v（复合行正文在 md 块里）", a)
	}
	wantKinds := []mdKind{mdPara, mdListItem, mdListItem, mdCode}
	wantTexts := []string{"好的：", "• 一项", "• 二项", "done"}
	if len(a.md) != len(wantKinds) {
		t.Fatalf("md 块数 = %d, want %d: %+v", len(a.md), len(wantKinds), a.md)
	}
	for k := range wantKinds {
		if a.md[k].kind != wantKinds[k] || a.md[k].text != wantTexts[k] {
			t.Fatalf("md[%d] = %+v, want %v %q", k, a.md[k], wantKinds[k], wantTexts[k])
		}
	}
	if a.selCount() != len(wantKinds) {
		t.Fatalf("selCount = %d, want %d", a.selCount(), len(wantKinds))
	}
	if items[2].kind != blockAssistant || !items[2].live || items[2].text != "**草稿**不解析" || items[2].md != nil {
		t.Fatalf("items[2] = %+v（live 草稿应原样且不解析）", items[2])
	}
}

// TestFrameItemsMarkdownTable 含表格的助手块：表格入复合行、选键按单元格累计
// （键数 = 行 × 列 + 其余块，D325），整帧布局不炸（网格渲染走量期宏）。
func TestFrameItemsMarkdownTable(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	u.m.add(blockUser, "画个表格")
	u.m.add(blockAssistant, "表格：\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |")
	items := u.frameItems()
	a := items[1]
	if a.kind != blockAssistant || len(a.md) != 2 || a.md[1].kind != mdTable {
		t.Fatalf("items[1].md = %+v, want 段落 + 表格", a.md)
	}
	// 段落 1 键 + 表格 (1 表头 + 2 行) × 2 列 = 7。
	if n := a.selCount(); n != 7 {
		t.Fatalf("selCount = %d, want 7（D325 按单元格计键）", n)
	}
	// 整帧跑一遍（网格宏 + 细线 + 文本重放全路径），只验不炸与几何落格。
	q := new(input.Router)
	gtx, ops := frameGtx(q.Source())
	u.layout(gtx)
	q.Frame(ops)
}

// TestMdTableColWidths 列宽分配（D325）：权重占比 × 可用宽，窄列保底，总宽超限按比例压回。
func TestMdTableColWidths(t *testing.T) {
	t.Parallel()
	// 均权 → 均分。
	w := mdTableColWidths(300, []float64{1, 1}, 48)
	if w[0] != 150 || w[1] != 150 {
		t.Fatalf("均权 = %v, want [150 150]", w)
	}
	// 权重 3:1 → 225:75。
	w = mdTableColWidths(300, []float64{3, 1}, 48)
	if w[0] != 225 || w[1] != 75 {
		t.Fatalf("3:1 = %v, want [225 75]", w)
	}
	// 窄列保底：权重悬殊时小列不低于 minW。
	w = mdTableColWidths(300, []float64{9, 0.05}, 48)
	if w[1] != 48 {
		t.Fatalf("保底 = %v, want 小列 48", w)
	}
	// 超限压回：保底把总宽顶过可用时按比例缩。
	w = mdTableColWidths(60, []float64{1, 1, 1, 1}, 48)
	if got := w[0] + w[1] + w[2] + w[3]; got > 60 {
		t.Fatalf("总宽 %d 超可用 60: %v", got, w)
	}
}

// TestMdHeadingSp 标题字号阶梯单调递减且不低于正文（D65）。
func TestMdHeadingSp(t *testing.T) {
	t.Parallel()
	th := testTheme()
	prev := th.TextSize * 2 // 哨兵：大于 h1（阶梯自 h1 递减、不低于正文）
	for level := 1; level <= 6; level++ {
		sp := mdHeadingSp(th, level)
		if sp > prev {
			t.Fatalf("h%d 字号 %v 大于上一级 %v", level, sp, prev)
		}
		prev = sp
	}
}
