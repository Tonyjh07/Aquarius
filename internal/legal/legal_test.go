package legal

// 法务正文的完整性锁（D278 决策 5）：四份文件齐、中英节数对齐、英文不含汉字、版本常量与
// 目录号一致、解析器的四种标记按约定产出。全部不需要窗口，比界面先能验收——把正文做成
// 独立包的最大好处就在这里。

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// TestTextsComplete 当前版本下四份正文都在；中英两份的节数相等、标题都非空。
func TestTextsComplete(t *testing.T) {
	for _, d := range []Doc{Terms, Privacy} {
		zh, err := Load(d, i18n.LangZh)
		if err != nil {
			t.Fatalf("%s zh: %v", d, err)
		}
		en, err := Load(d, i18n.LangEn)
		if err != nil {
			t.Fatalf("%s en: %v", d, err)
		}
		if zh.Title == "" || en.Title == "" {
			t.Fatalf("%s 缺文档标题: zh=%q en=%q", d, zh.Title, en.Title)
		}
		if len(zh.Sections) != len(en.Sections) {
			t.Fatalf("%s 中英节数不等: zh=%d en=%d", d, len(zh.Sections), len(en.Sections))
		}
		if len(zh.Sections) == 0 {
			t.Fatalf("%s 没有任何节", d)
		}
		for i := range zh.Sections {
			if strings.TrimSpace(zh.Sections[i].Title) == "" || strings.TrimSpace(en.Sections[i].Title) == "" {
				t.Fatalf("%s 第 %d 节缺标题", d, i+1)
			}
			if len(zh.Sections[i].Blocks) == 0 {
				t.Fatalf("%s 第 %d 节没有正文", d, i+1)
			}
		}
		// 版本行在导言里：没有它，「同意的是哪一版」就只剩 config 里那个数。
		if len(zh.Lead) == 0 || len(en.Lead) == 0 {
			t.Fatalf("%s 缺导言（版本行）", d)
		}
		if !strings.Contains(zh.Lead[0].Text, strconv.Itoa(Version)) {
			t.Fatalf("%s zh 导言首行未注入版本号: %q", d, zh.Lead[0].Text)
		}
	}
}

// TestEnTextsNoCJK 英文正文不得残留汉字（`TestEnHasNoCJK` 只覆盖 en 表，覆盖不到 embed 的 md）。
func TestEnTextsNoCJK(t *testing.T) {
	cjk := regexp.MustCompile(`[\p{Han}]`)
	files := textFiles(t)
	n := 0
	for _, name := range files {
		if !strings.HasSuffix(name, ".en.md") {
			continue
		}
		n++
		raw, err := texts.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// 版本占位符是唯一允许的非英文记号，替换掉再扫。
		body := strings.ReplaceAll(string(raw), "{v}", strconv.Itoa(Version))
		for i, line := range strings.Split(body, "\n") {
			if loc := cjk.FindStringIndex(line); loc != nil {
				t.Fatalf("%s:%d 出现汉字 %q: %s", name, i+1, line[loc[0]:loc[1]], line)
			}
		}
	}
	if n == 0 {
		t.Fatal("没有找到任何英文正文")
	}
}

// TestVersionMatchesDir 存在的最高的 texts/vN 目录号必须等于 Version——改了正文忘了升常量，
// 用户就不会被要求重过一次同意。
func TestVersionMatchesDir(t *testing.T) {
	entries, err := texts.ReadDir("texts")
	if err != nil {
		t.Fatal(err)
	}
	highest := 0
	for _, e := range entries {
		v, ok := strings.CutPrefix(e.Name(), "v")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("目录名 %q 不是 v<N>", e.Name())
		}
		if n > highest {
			highest = n
		}
	}
	if highest != Version {
		t.Fatalf("最高的正文目录 = v%d，Version = %d（改正文就要同步常量）", highest, Version)
	}
}

// TestParseMarks 解析口径：** 段整段判 Tip 且星号去掉、反引号去掉、分隔行不产出块、
// 表格首行是表头且数据行进 Cols、导言与节分流。
func TestParseMarks(t *testing.T) {
	// 样例故意用 ASCII：这是解析器的输入，不是界面文案（中文样例会被 i18n 的
	// TestNoCJKExpectations 当成写死的期望值拦下）。
	raw := strings.Join([]string{
		"# Title",
		"",
		"Version {v} · effective sometime",
		"",
		"## 5. Permissions",
		"",
		"A paragraph with `code` and **emphasis** mixed in.",
		"",
		"| Key | Value |",
		"|---|---|",
		"| a | b |",
		"",
		"- one bullet",
	}, "\n")
	doc, err := parse(strings.ReplaceAll(raw, "{v}", "7"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Title" {
		t.Fatalf("标题 = %q", doc.Title)
	}
	if len(doc.Lead) != 1 || !strings.Contains(doc.Lead[0].Text, "Version 7") {
		t.Fatalf("导言 = %+v", doc.Lead)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].Title != "5. Permissions" {
		t.Fatalf("节 = %+v", doc.Sections)
	}
	var kinds []BlockKind
	for _, b := range doc.Sections[0].Blocks {
		kinds = append(kinds, b.Kind)
	}
	want := []BlockKind{Tip, TableRow, TableRow, Bullet}
	if len(kinds) != len(want) {
		t.Fatalf("块类型 = %v, want %v（分隔行不该产出块）", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("块 %d = %v, want %v", i, kinds[i], want[i])
		}
	}
	tip := doc.Sections[0].Blocks[0]
	if strings.Contains(tip.Text, "**") || strings.Contains(tip.Text, "`") {
		t.Fatalf("标记没去干净: %q", tip.Text)
	}
	head, row := doc.Sections[0].Blocks[1], doc.Sections[0].Blocks[2]
	if !head.Head || row.Head {
		t.Fatalf("表头标记 = %v / %v", head.Head, row.Head)
	}
	if len(head.Cols) != 2 || head.Cols[0] != "Key" || row.Cols[1] != "b" {
		t.Fatalf("表格列 = %+v / %+v", head.Cols, row.Cols)
	}
}

// TestParseClauses 条款号行各成一段：正文里 1.1、1.2、1.3 连着写、中间不空行，
// 按 markdown 软换行去拼会把一节的六条糊成一大段。
func TestParseClauses(t *testing.T) {
	doc, err := parse("## 1. Scope\n\n1.1 first clause\n1.2 second clause\n1.3 third clause\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 1 {
		t.Fatalf("节数 = %d", len(doc.Sections))
	}
	blocks := doc.Sections[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("块数 = %d, want 3（条款号行必须各自成段）: %+v", len(blocks), blocks)
	}
	if blocks[1].Text != "1.2 second clause" {
		t.Fatalf("第 2 块 = %q", blocks[1].Text)
	}
}

// TestLoadAtFallsBack 英文缺文件时回落中文母版（与 i18n.T 同口径：界面不会空着）。
func TestLoadAtFallsBack(t *testing.T) {
	doc, err := LoadAt(Terms, i18n.Lang("fr"), Version)
	if err != nil {
		t.Fatalf("未知语言应回落中文而不是报错: %v", err)
	}
	zh, err := Load(Terms, i18n.LangZh)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != zh.Title || len(doc.Sections) != len(zh.Sections) {
		t.Fatalf("回落的不是中文母版: %q / %d 节", doc.Title, len(doc.Sections))
	}
}

// textFiles 列出 embed 里的全部正文文件名（测试用）。
func textFiles(t *testing.T) []string {
	t.Helper()
	dirs, err := texts.ReadDir("texts")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range dirs {
		names, err := texts.ReadDir("texts/" + d.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			out = append(out, "texts/"+d.Name()+"/"+n.Name())
		}
	}
	return out
}
