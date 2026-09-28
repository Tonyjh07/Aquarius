package uigui

// 主题令牌 headless 测试（§15.5/D61）：预设完整性与同步完整性、档位解析、
// 消息热切换路径——全部无窗口（真实窗口路径由手工验收覆盖）。

import (
	"image/color"
	"testing"
)

// slots 预设中活动令牌槽覆盖的部分：material 的 fg/bg 不落包级槽（经 u.th 快照
// 校正），currentPalette 回读恒零值——对比时统一剥离。
func slots(p palette) palette {
	p.fg, p.bg = color.NRGBA{}, color.NRGBA{}
	return p
}

// restoreLight 测试后把活动令牌槽复位浅色（包级活动槽跨测试共享，保持确定性）。
func restoreLight(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { applyPalette(lightPalette()) })
}

// TestPalettePresetsTwoVersions 两版预设同构异值：底/文字/卡片全套成对换值，
// 品牌色与 tips 白字恒定（§15.4 品牌色两版同值）。
func TestPalettePresetsTwoVersions(t *testing.T) {
	l, d := lightPalette(), darkPalette()
	if l.pillBg == d.pillBg || l.windowBg == d.windowBg || l.fg == d.fg {
		t.Fatal("深浅预设底色应不同")
	}
	if l.cardThinking == d.cardThinking || l.textDim == d.textDim {
		t.Fatal("深浅预设卡片/文字应不同")
	}
	if l.brandColor != d.brandColor {
		t.Errorf("品牌色两版应同值: %v vs %v", l.brandColor, d.brandColor)
	}
	if l.whiteText != d.whiteText {
		t.Errorf("tips 白字两版应同值: %v vs %v", l.whiteText, d.whiteText)
	}
	// 深色底必须配浅色文字（fg 亮于底）：防手滑把深色 fg 配深色 bg。
	if !isLighter(d.fg, d.bg) || isLighter(l.fg, l.bg) {
		t.Error("fg/bg 亮度配对错误（深版 fg 应亮于底，浅版 fg 应暗于底）")
	}
}

// isLighter a 的感知亮度（近似 = 平均通道）是否高于 b。
func isLighter(a, b color.NRGBA) bool {
	return int(a.R)+int(a.G)+int(a.B) > int(b.R)+int(b.G)+int(b.B)
}

// TestApplyPaletteSyncsAllTokens 活动槽 ↔ 预设同步完整性：applyPalette 后
// currentPalette 回读必须等于预设（新增令牌漏配 apply/current 任一侧即失败）。
func TestApplyPaletteSyncsAllTokens(t *testing.T) {
	restoreLight(t)
	for _, p := range []palette{lightPalette(), darkPalette()} {
		applyPalette(p)
		if got := currentPalette(); got != slots(p) {
			t.Errorf("活动槽与预设不同步:\n got %#v\nwant %#v", got, slots(p))
		}
	}
}

// TestResolvePalette 档位解析：light/dark 直取预设；""/system/未识别 = 跟随系统。
func TestResolvePalette(t *testing.T) {
	if got := resolvePalette("light"); got != lightPalette() {
		t.Error("light 应直取浅色预设")
	}
	if got := resolvePalette("dark"); got != darkPalette() {
		t.Error("dark 应直取深色预设")
	}
	sys := lightPalette()
	if systemDark() {
		sys = darkPalette()
	}
	for _, mode := range []string{"", "system", "bogus"} {
		if got := resolvePalette(mode); got != sys {
			t.Errorf("mode %q 应跟随系统解析", mode)
		}
	}
}

// TestThemeMessagesHeadless 消息热切换路径（与运行时同一条 apply 链）：
// 初始档、themeMsg 切换、sysThemeMsg 仅 system 档重解析。
func TestThemeMessagesHeadless(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{Theme: "dark"})

	if currentPalette() != slots(darkPalette()) {
		t.Error("初始 Theme=dark 未生效")
	}
	if u.themeMode != "dark" {
		t.Errorf("themeMode = %q, want dark", u.themeMode)
	}

	if !u.post(themeMsg{mode: "light"}) {
		t.Fatal("事件循环已退出")
	}
	drainSync(t, u)
	if currentPalette() != slots(lightPalette()) || u.themeMode != "light" {
		t.Errorf("themeMsg light 未生效 (mode=%q)", u.themeMode)
	}

	// 固定档下系统广播 = 忽略。
	if !u.post(sysThemeMsg{}) {
		t.Fatal("事件循环已退出")
	}
	drainSync(t, u)
	if u.themeMode != "light" || currentPalette() != slots(lightPalette()) {
		t.Errorf("固定 light 档不应被系统广播改动 (mode=%q)", u.themeMode)
	}

	// system 档：广播后重解析（结果 = 当前系统档，允许与切换前相同）。
	if !u.post(themeMsg{mode: "system"}) {
		t.Fatal("事件循环已退出")
	}
	drainSync(t, u)
	if u.themeMode != "system" {
		t.Errorf("themeMode = %q, want system", u.themeMode)
	}
	if !u.post(sysThemeMsg{}) {
		t.Fatal("事件循环已退出")
	}
	drainSync(t, u)
	if currentPalette() != slots(resolvePalette("system")) {
		t.Error("system 档系统广播后未按系统解析")
	}
}
