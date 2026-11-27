package main

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// uienv_test.go 环境快照的装配（D352）：转换保真，以及「还没造出前端」时不编状态。

// TestToV1UIEnvironmentKeepsEveryField 逐字段抄：漏一个就是插件侧静默少一块信息。
func TestToV1UIEnvironmentKeepsEveryField(t *testing.T) {
	t.Parallel()
	in := port.UIEnvironment{
		Frontend: port.FrontendGUI,
		Dark:     true,
		Scale:    1.5,
		Tokens: port.UIThemeTokens{
			BG: "F7F7F8", FG: "1B1B1F", Muted: "6B6B73",
			Accent: "3E63DD", Border: "D9D9E0", Sel: "ECECF1",
		},
		Fonts:       []string{"a.ttc"},
		SymbolFonts: []string{"s.ttf"},
	}
	got := toV1UIEnvironment(in)
	if got.Frontend != v1.FrontendGUI || !got.Dark || got.Scale != 1.5 {
		t.Fatalf("基本字段丢了：%+v", got)
	}
	if got.Tokens != (v1.ThemeTokens{
		BG: "F7F7F8", FG: "1B1B1F", Muted: "6B6B73",
		Accent: "3E63DD", Border: "D9D9E0", Sel: "ECECF1",
	}) {
		t.Fatalf("令牌丢了：%+v", got.Tokens)
	}
	if len(got.Fonts) != 1 || len(got.SymbolFonts) != 1 {
		t.Fatalf("字体面丢了：%+v / %+v", got.Fonts, got.SymbolFonts)
	}
}

// TestUIEnvironmentBeforeFrontend 前端还没造出来（启动早期）时给零值：插件会读到
// 「什么都不知道」，而不是被误导成一个具体前端。
func TestUIEnvironmentBeforeFrontend(t *testing.T) {
	t.Parallel()
	w := &wiring{}
	got := w.uiEnvironment()
	if got.Frontend != "" || got.Dark || got.Scale != 0 || got.Tokens != (v1.ThemeTokens{}) {
		t.Fatalf("无前端时快照应为零值：%+v", got)
	}
	if len(got.Fonts) != 0 || len(got.SymbolFonts) != 0 {
		t.Fatalf("无前端时不该有字体面：%+v", got)
	}
}
