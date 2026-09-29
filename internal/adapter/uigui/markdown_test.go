package uigui

// markdown_test.go D65 解析与展开测试：结构块映射、行内剥标记、转义/实体出口语义、
// 流式未闭合围栏退化、缓存与回退。

import (
	"fmt"
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
	blocks := blocksOf(t, "# 标题一\n\n## 二级\n\n正文段落，两行：\n续行\n\n```go\nx := 1\n```\n\n---")
	want := []struct {
		kind  mdKind
		text  string
		level int
		lang  string
	}{
		{mdHeading, "标题一", 1, ""},
		{mdHeading, "二级", 2, ""},
		{mdPara, "正文段落，两行：\n续行", 0, ""}, // 软换行保 \n（聊天口径）
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

// TestParseMarkdownQuote 引用块逐行前缀（D65）。
func TestParseMarkdownQuote(t *testing.T) {
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
	blocks := blocksOf(t, "```go\nx := 1")
	if blocks[0].kind != mdCode || blocks[0].lang != "go" || blocks[0].text != "x := 1" {
		t.Fatalf("未闭合围栏 = %+v", blocks[0])
	}
}

// TestMdInlineStrip 行内剥标记：emphasis/链接取文字/图片取 alt/行内代码逐字/
// autolink/行内原始 HTML 逐字呈现（§9 只渲染不执行）。
func TestMdInlineStrip(t *testing.T) {
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
	u := &UI{} // mdCache 惰性初始化（不依赖 newUI）
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
	u := &UI{}
	for i := 0; i < mdCacheLimit+1; i++ {
		u.mdBlocks(fmt.Sprintf("消息 %d", i))
	}
	if len(u.mdCache) > mdCacheLimit {
		t.Fatalf("缓存条目 = %d, 应在重建后 ≤ %d", len(u.mdCache), mdCacheLimit)
	}
}

// TestFrameItemsMarkdown 助手定稿块 = 单条目携结构块（D66 单回复单气泡）；live 草稿
// 不解析（D33 口径）。
func TestFrameItemsMarkdown(t *testing.T) {
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

// TestMdHeadingSp 标题字号阶梯单调递减且不低于正文（D65）。
func TestMdHeadingSp(t *testing.T) {
	th := newTheme()
	prev := th.TextSize * 2 // 哨兵：大于 h1（阶梯自 h1 递减、不低于正文）
	for level := 1; level <= 6; level++ {
		sp := mdHeadingSp(th, level)
		if sp > prev {
			t.Fatalf("h%d 字号 %v 大于上一级 %v", level, sp, prev)
		}
		prev = sp
	}
}
