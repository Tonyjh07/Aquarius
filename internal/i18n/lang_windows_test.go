//go:build windows

package i18n

import "testing"

// TestLangFromLCID Windows 界面语言映射：简体 0x0804 / 繁体 0x0404 都算中文，0x0409 英文，
// 其余（这里取日文 0x0411）不认识，由调用方回落 zh。
func TestLangFromLCID(t *testing.T) {
	cases := []struct {
		lcid uint32
		want Lang
	}{
		{lcid: 0x0804, want: LangZh}, // zh-CN
		{lcid: 0x0404, want: LangZh}, // zh-TW
		{lcid: 0x0409, want: LangEn}, // en-US
		{lcid: 0x0809, want: LangEn}, // en-GB
		{lcid: 0x0411, want: ""},     // ja-JP：不认识
		{lcid: 0, want: ""},
	}
	for _, c := range cases {
		if got := langFromLCID(c.lcid); got != c.want {
			t.Errorf("langFromLCID(%#x) = %q, want %q", c.lcid, got, c.want)
		}
	}
}
