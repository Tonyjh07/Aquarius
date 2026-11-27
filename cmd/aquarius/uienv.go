package main

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/repl"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uitui"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// uienv.go 界面环境快照的装配（D352）：把前端各自的快照转成插件契约形状。
//
// 三个前端的差别只在「能回答多少」：GUI 有主题、DPI、字体；TUI 与 repl 只有种类——
// 终端里没有宿主的语义色可给（背景是用户自己设的），硬编一个「深色」比说「不知道」更坏。
//
// 每次调用现取：深浅色、DPI 会在运行中变，插件轮询这个函数才跟得住。

// uiEnvironment 当前前端的快照（零值 = 还没造出前端）。
func (w *wiring) uiEnvironment() v1.UIEnvironment {
	switch ui := w.ui.(type) {
	case *uigui.UI:
		return toV1UIEnvironment(ui.Environment())
	case *uitui.UI:
		return v1.UIEnvironment{Frontend: v1.FrontendTUI, Scale: 1}
	case *repl.UI:
		return v1.UIEnvironment{Frontend: v1.FrontendREPL, Scale: 1}
	default:
		return v1.UIEnvironment{}
	}
}

// toV1UIEnvironment 中性快照 → 插件契约。字段逐个抄，不做加工：加工意味着两个前端
// 各有一套解释规则，插件就得猜自己在哪套上。
func toV1UIEnvironment(e port.UIEnvironment) v1.UIEnvironment {
	return v1.UIEnvironment{
		Frontend: e.Frontend,
		Dark:     e.Dark,
		Scale:    e.Scale,
		Tokens: v1.ThemeTokens{
			BG:     e.Tokens.BG,
			FG:     e.Tokens.FG,
			Muted:  e.Tokens.Muted,
			Accent: e.Tokens.Accent,
			Border: e.Tokens.Border,
			Sel:    e.Tokens.Sel,
		},
		Fonts:       e.Fonts,
		SymbolFonts: e.SymbolFonts,
	}
}
