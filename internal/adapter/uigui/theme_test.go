package uigui

// 主题令牌 headless 测试（§15.5/D61）：预设完整性与同步完整性、档位解析、
// 消息热切换路径——全部无窗口（真实窗口路径由手工验收覆盖）。

import (
	"image/color"
	"testing"
)

// slots 只剥离来源标注（themeMode/dark）：resolvePalette 写入它们而预设本身不带，
// 其余字段一律逐一比对——这就是「预设 ↔ 活动快照同步完整」的判据。
func slots(p palette) palette {
	p.themeMode, p.dark = "", false
	return p
}

// storeTokens 测试写入活动快照（与 applyTheme 同一个原子写；store 的值是副本，
// 调用方之后改自己的变量不会污染活动值）。
func storeTokens(p palette) { activeTokens.Store(&p) }

// restoreLight 测试后把活动快照复位浅色（快照跨测试共享，保持确定性）。Paper 令牌
// 是另一份快照（次窗），一并复位——否则前一个用例切到深色会让后面按浅色断言的
// 像素测试读错档。
func restoreLight(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		storeTokens(lightPalette())
		applyPaper(paperLight())
	})
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

// TestTokensHaveOneSource 令牌只有一份活动值：写进快照的预设，读侧必须原样取回
// （含 fg/bg 与来源标注）。这条替代了原先「applyPalette 写包级槽、currentPalette
// 反向抄回」的双向同步检查——不再有两套事实源，也就没有「漏配另一半」这种失败模式。
func TestTokensHaveOneSource(t *testing.T) {
	restoreLight(t)
	for _, p := range []palette{lightPalette(), darkPalette()} {
		storeTokens(p)
		if got := currentPalette(); got != p {
			t.Errorf("快照与预设不一致:\n got %#v\nwant %#v", got, p)
		}
	}
}

// TestTokensAreSnapshots 快照是值不是引用：写入后改调用方那份变量，活动值不受影响
// （把「跨 goroutine 只读快照」落成可断言的性质）。
func TestTokensAreSnapshots(t *testing.T) {
	restoreLight(t)
	p := lightPalette()
	storeTokens(p)
	p.pillBg = color.NRGBA{R: 0xFF, G: 0x00, B: 0xFF, A: 0xFF}
	if got := currentPalette(); got.pillBg != lightPalette().pillBg {
		t.Errorf("改调用方变量污染了活动快照: %v", got.pillBg)
	}
}

// TestTokensFallBackToLight 未初始化时取令牌回落浅色预设（宁可浅色，不要零值全黑）。
func TestTokensFallBackToLight(t *testing.T) {
	activeTokens.Store(nil)
	t.Cleanup(func() { storeTokens(lightPalette()) })
	if got := tok(); got != lightPalette() {
		t.Errorf("空快照应回落浅色: %#v", got)
	}
}

// TestResolvePalette 档位解析：light/dark 直取预设；""/system/未识别 = 跟随系统
// （系统深浅由平台层给，测试两档都走一遍——不再依赖跑测试的机器当前主题）。
func TestResolvePalette(t *testing.T) {
	for _, dark := range []bool{false, true} {
		if got := slots(resolvePalette("light", dark)); got != slots(lightPalette()) {
			t.Error("light 应直取浅色预设")
		}
		if got := slots(resolvePalette("dark", dark)); got != slots(darkPalette()) {
			t.Error("dark 应直取深色预设")
		}
		sys := lightPalette()
		if dark {
			sys = darkPalette()
		}
		for _, mode := range []string{"", "system", "bogus"} {
			if got := slots(resolvePalette(mode, dark)); got != slots(sys) {
				t.Errorf("mode %q（系统深色=%v）应跟随系统解析", mode, dark)
			}
		}
	}
}

// TestThemeMessagesHeadless 消息热切换路径（与运行时同一条 apply 链）：
// 初始档、themeMsg 切换、sysThemeMsg 仅 system 档重解析。
func TestThemeMessagesHeadless(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{Theme: "dark"})

	if slots(currentPalette()) != slots(darkPalette()) {
		t.Error("初始 Theme=dark 未生效")
	}
	if u.themeMode != "dark" {
		t.Errorf("themeMode = %q, want dark", u.themeMode)
	}

	if !u.post(themeMsg{mode: "light"}) {
		t.Fatal("事件循环已退出")
	}
	drainSync(t, u)
	if slots(currentPalette()) != slots(lightPalette()) || u.themeMode != "light" {
		t.Errorf("themeMsg light 未生效 (mode=%q)", u.themeMode)
	}

	// 固定档下系统广播 = 忽略。
	if !u.post(sysThemeMsg{}) {
		t.Fatal("事件循环已退出")
	}
	drainSync(t, u)
	if u.themeMode != "light" || slots(currentPalette()) != slots(lightPalette()) {
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
	if slots(currentPalette()) != slots(resolvePalette("system", u.plat.DarkMode())) {
		t.Error("system 档系统广播后未按系统解析")
	}
}
