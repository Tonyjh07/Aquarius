//go:build !windows

package i18n

import "testing"

// TestLangFromLocale POSIX locale → Lang：只取语言段，编码与修饰不影响；C / POSIX / 空串
// 不认识（由调用方回落 zh）。
func TestLangFromLocale(t *testing.T) {
	cases := []struct {
		loc  string
		want Lang
	}{
		{loc: "zh_CN.UTF-8", want: LangZh},
		{loc: "zh_TW.Big5", want: LangZh},
		{loc: "zh", want: LangZh},
		{loc: "en_US.UTF-8", want: LangEn},
		{loc: "en_GB@euro", want: LangEn},
		{loc: "EN", want: LangEn},
		{loc: "C", want: ""},
		{loc: "POSIX", want: ""},
		{loc: "ja_JP.UTF-8", want: ""},
		{loc: "", want: ""},
	}
	for _, c := range cases {
		if got := langFromLocale(c.loc); got != c.want {
			t.Errorf("langFromLocale(%q) = %q, want %q", c.loc, got, c.want)
		}
	}
}
