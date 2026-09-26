package uigui

import "testing"

// TestParseHotkey 合法组合解析：修饰键位、VK 取值与规范化标签（§15.1）。
func TestParseHotkey(t *testing.T) {
	cases := []struct {
		in    string
		mods  uintptr
		vk    uintptr
		label string
	}{
		{"Alt+A", modAlt, vkA, "Alt+A"},
		{"", modAlt, vkA, "Alt+A"}, // 空 = 默认 Alt+A
		{"ctrl+alt+a", modCtrl | modAlt, vkA, "Ctrl+Alt+A"},
		{"Alt+Shift+F5", modAlt | modShift, vkF1 + 4, "Alt+Shift+F5"},
		{"Alt+Space", modAlt, vkSpace, "Alt+Space"},
		{"win+9", modWin, '9', "Win+9"},
		{"Shift+esc", modShift, vkEsc, "Shift+Esc"},
		{"Alt+f24", modAlt, vkF1 + 23, "Alt+F24"},
		{"Ctrl+Enter", modCtrl, vkEnter, "Ctrl+Enter"},
	}
	for _, c := range cases {
		got, err := parseHotkey(c.in)
		if err != nil {
			t.Errorf("parseHotkey(%q) 报错: %v", c.in, err)
			continue
		}
		if got.mods != c.mods || got.vk != c.vk || got.label != c.label {
			t.Errorf("parseHotkey(%q) = {%x,%x,%q}, want {%x,%x,%q}",
				c.in, got.mods, got.vk, got.label, c.mods, c.vk, c.label)
		}
	}
}

// TestParseHotkeyErrors 非法组合报错（调用方回落默认/回退，不误注册）。
func TestParseHotkeyErrors(t *testing.T) {
	for _, in := range []string{
		"Alt",            // 缺键
		"A",              // 缺修饰键
		"Foo+A",          // 未知修饰键
		"Alt+Shift",      // 末段是修饰键名而非键
		"Alt+X1",         // 非法键
		"Alt+Shift+Ctrl", // 无键
	} {
		if _, err := parseHotkey(in); err == nil {
			t.Errorf("parseHotkey(%q) 应报错", in)
		}
	}
}

// TestFallbackHotkey 回退组合固定为 Ctrl+Alt+A（spike 实测可用，§15.1）。
func TestFallbackHotkey(t *testing.T) {
	fb := fallbackHotkey()
	if fb.mods != modCtrl|modAlt || fb.vk != vkA || fb.label != "Ctrl+Alt+A" {
		t.Fatalf("fallbackHotkey = {%x,%x,%q}", fb.mods, fb.vk, fb.label)
	}
}
