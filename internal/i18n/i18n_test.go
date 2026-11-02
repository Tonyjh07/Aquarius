package i18n

import (
	"strings"
	"testing"
	"unicode"
)

// TestParse ui.lang 的写法：空与 auto 等价，大小写与空白宽容，其余一律报错（启动 fail-fast）。
func TestParse(t *testing.T) {
	cases := []struct {
		raw  string
		want Selection
		bad  bool
	}{
		{raw: "", want: SelectionAuto},
		{raw: "auto", want: SelectionAuto},
		{raw: " AUTO ", want: SelectionAuto},
		{raw: "zh", want: SelectionZh},
		{raw: "ZH", want: SelectionZh},
		{raw: "en", want: SelectionEn},
		{raw: "en-US", bad: true},
		{raw: "fr", bad: true},
	}
	for _, c := range cases {
		got, err := Parse(c.raw)
		if c.bad {
			if err == nil {
				t.Errorf("Parse(%q) 应报错，得到 %q", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) 报错: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestTablesIsomorphic 两份表必须同构：key 集合相等、每条模板的占位符名集合相等、没有空值。
//
// 这是「两套界面说同一件事」唯一的机器判据（§7.8）。少了它，漏一条 key 不会被任何人发现，
// 只会让英文界面上突然冒出一句中文。
func TestTablesIsomorphic(t *testing.T) {
	if len(zhTexts) == 0 {
		t.Fatal("zh 表是空的：取词口形同虚设")
	}
	for key, zhText := range zhTexts {
		enText, ok := enTexts[key]
		if !ok {
			t.Errorf("en 缺 key %q（中文: %q）", key, zhText)
			continue
		}
		if strings.TrimSpace(enText) == "" {
			t.Errorf("en[%q] 是空串", key)
		}
		if zhWant, enGot := PlaceholderNames(zhText), PlaceholderNames(enText); strings.Join(zhWant, ",") != strings.Join(enGot, ",") {
			t.Errorf("%q 的占位符不一致: zh %v, en %v", key, zhWant, enGot)
		}
	}
	for key := range enTexts {
		if _, ok := zhTexts[key]; !ok {
			t.Errorf("en 多出 key %q（zh 是母版，只许它先有）", key)
		}
	}
}

// TestKeyShape key 是标识不是文案：点号分层、小写、无空白。首段是区域，末段是用途。
func TestKeyShape(t *testing.T) {
	for key := range zhTexts {
		if !strings.Contains(key, ".") {
			t.Errorf("key %q 没有区域前缀（形如 boot.config_written）", key)
		}
		if strings.TrimSpace(key) != key || strings.ContainsAny(key, " \t\n") {
			t.Errorf("key %q 含空白", key)
		}
		if strings.ToLower(key) != key {
			t.Errorf("key %q 不是全小写", key)
		}
	}
}

// TestEnHasNoCJK 英文表不许留中文：抄中文原文改词时最容易漏掉半句。
func TestEnHasNoCJK(t *testing.T) {
	for key, text := range enTexts {
		if strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("en[%q] 含中文: %q", key, text)
		}
	}
}

// TestFallbackToZh 缺 en 的 key 回落母版；两边都没有就把 key 露出来（空串看不出哪里坏）。
func TestFallbackToZh(t *testing.T) {
	prev := current.Load()
	defer current.Store(prev)

	// 造一张缺词的 en 表：正常构建下同构测试会拦住，这里模拟的是它与测试同时失守的兜底。
	current.Store(&catalog{lang: LangEn, texts: map[string]string{}})

	key := "boot.config_written"
	if got, want := T(key), zhTexts[key]; got != want {
		t.Errorf("缺 en 时应回落 zh，得到 %q", got)
	}
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("两边都没有时应返回 key，得到 %q", got)
	}
}

// TestUseSelectsLanguage 选语：en 表生效后取到英文；未知语言给母版（宁可口径旧，也不给空表）。
func TestUseSelectsLanguage(t *testing.T) {
	defer Use(SelectionZh)

	if got := Use(SelectionEn); got != LangEn {
		t.Fatalf("Use(en) = %q, want en", got)
	}
	if got := Current(); got != LangEn {
		t.Fatalf("Current() = %q, want en", got)
	}
	if got, want := T("boot.config_written"), enTexts["boot.config_written"]; got != want {
		t.Errorf("en 下取词 = %q, want %q", got, want)
	}

	Use(SelectionZh)
	if got, want := T("boot.config_written"), zhTexts["boot.config_written"]; got != want {
		t.Errorf("zh 下取词 = %q, want %q", got, want)
	}
}

// TestTfFillsPlaceholders 具名占位符：按名取、缺参原样留、没参数不动、非字符串名忽略。
func TestTfFillsPlaceholders(t *testing.T) {
	defer Use(SelectionZh)
	Use(SelectionZh)

	cases := []struct {
		name string
		tmpl string
		kv   []any
		want string
	}{
		{name: "全部命中", tmpl: "{a}-{b}", kv: []any{"a", 1, "b", "x"}, want: "1-x"},
		{name: "缺参原样留", tmpl: "{a}/{b}", kv: []any{"a", 1}, want: "1/{b}"},
		{name: "重复占位符", tmpl: "{a}{a}", kv: []any{"a", 2}, want: "22"},
		{name: "无参数", tmpl: "原样 {a}", kv: nil, want: "原样 {a}"},
		{name: "非字符串名忽略", tmpl: "{a}", kv: []any{7, 8}, want: "{a}"},
		{name: "半个花括号", tmpl: "{a", kv: []any{"a", 1}, want: "{a"},
	}
	for _, c := range cases {
		if got := fill(c.tmpl, c.kv); got != c.want {
			t.Errorf("%s: fill(%q) = %q, want %q", c.name, c.tmpl, got, c.want)
		}
	}
}

// TestPlaceholderNames 占位符名去重、升序，认不出的花括号不算名字。
func TestPlaceholderNames(t *testing.T) {
	got := PlaceholderNames("{b} {a} {b} {} {")
	want := []string{"a", "b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PlaceholderNames = %v, want %v", got, want)
	}
}
