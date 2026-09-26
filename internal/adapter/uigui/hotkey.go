package uigui

import (
	"fmt"
	"strconv"
	"strings"
)

// defaultHotkey 全局呼出快捷键默认值（§15.1；D43 修订 2026-09：原 Alt+Space
// 与输入法/开始菜单冲突面大，改 Alt+A）。
const defaultHotkey = "Alt+A"

// 组合键常量（Win32 MOD_* / VK_* 取值；中性定义——解析与日志各平台可用，
// 注册仅 Windows 调用）。
const (
	modAlt   = 0x0001
	modCtrl  = 0x0002
	modShift = 0x0004
	modWin   = 0x0008

	vkTab   = 0x09
	vkEnter = 0x0D
	vkEsc   = 0x1B
	vkSpace = 0x20
	vkA     = 0x41
	vkF1    = 0x70
)

// hotkeyCombo 解析后的组合键。
type hotkeyCombo struct {
	mods  uintptr // Win32 MOD_*：Alt=1 Ctrl=2 Shift=4 Win=8
	vk    uintptr // Win32 VK_*
	label string  // 规范化显示（日志用）
}

// fallbackHotkey 注册失败的回退组合（spike 实测可用）。
func fallbackHotkey() hotkeyCombo {
	return hotkeyCombo{
		mods:  modCtrl | modAlt,
		vk:    vkA,
		label: "Ctrl+Alt+A",
	}
}

// parseHotkey 解析 "Alt+A" / "ctrl+alt+shift+f5" 形式的组合键。
// 规则：至少一个修饰键（alt/ctrl/shift/win），末段为键——字母、数字、Space/Tab/
// Esc/Enter 或 F1–F24；非法输入报错（调用方回落默认/回退组合）。
func parseHotkey(s string) (hotkeyCombo, error) {
	var c hotkeyCombo
	s = strings.TrimSpace(s)
	if s == "" {
		return parseHotkey(defaultHotkey)
	}
	parts := strings.Split(s, "+")
	if len(parts) < 2 {
		return c, fmt.Errorf("需要修饰键+键（如 Alt+A）: %q", s)
	}
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "alt":
			c.mods |= modAlt
		case "ctrl", "control":
			c.mods |= modCtrl
		case "shift":
			c.mods |= modShift
		case "win", "super":
			c.mods |= modWin
		default:
			return c, fmt.Errorf("未知修饰键 %q: %q", p, s)
		}
	}
	if c.mods == 0 {
		return c, fmt.Errorf("至少需要一个修饰键: %q", s)
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	vk, label, err := parseHotkeyKey(key)
	if err != nil {
		return c, fmt.Errorf("hotkey %q: %w", s, err)
	}
	c.vk = vk
	// 规范化顺序：Ctrl+Alt+Shift+Win+键。
	var b strings.Builder
	for _, m := range []struct {
		bit  uintptr
		name string
	}{{modCtrl, "Ctrl"}, {modAlt, "Alt"}, {modShift, "Shift"}, {modWin, "Win"}} {
		if c.mods&m.bit != 0 {
			if b.Len() > 0 {
				b.WriteByte('+')
			}
			b.WriteString(m.name)
		}
	}
	b.WriteByte('+')
	b.WriteString(label)
	c.label = b.String()
	return c, nil
}

// parseHotkeyKey 末段键 → VK 与规范名。
func parseHotkeyKey(key string) (uintptr, string, error) {
	if len(key) == 1 {
		ch := key[0]
		switch {
		case ch >= 'a' && ch <= 'z':
			return uintptr(ch - 'a' + 'A'), strings.ToUpper(key), nil
		case ch >= 'A' && ch <= 'Z':
			return uintptr(ch - 'A' + 'A'), key, nil
		case ch >= '0' && ch <= '9':
			return uintptr(ch), key, nil
		}
		return 0, "", fmt.Errorf("不支持的键 %q", key)
	}
	low := strings.ToLower(key)
	switch low {
	case "space":
		return vkSpace, "Space", nil
	case "tab":
		return vkTab, "Tab", nil
	case "esc", "escape":
		return vkEsc, "Esc", nil
	case "enter", "return":
		return vkEnter, "Enter", nil
	}
	if strings.HasPrefix(low, "f") {
		if n, err := strconv.Atoi(low[1:]); err == nil && n >= 1 && n <= 24 {
			return vkF1 + uintptr(n-1), "F" + strconv.Itoa(n), nil
		}
	}
	return 0, "", fmt.Errorf("不支持的键 %q", key)
}
