package uigui

import (
	"fmt"
	"image/color"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// uienv.go 界面环境快照（D352）。给插件「自己开窗」那条路回答「宿主现在长什么样」：
// 前端种类、深浅、缩放、六个语义色、字体候选。只读，不交句柄也不交事件。
//
// 取的是当前值：pal 是原子快照（设置窗切主题会换），DPI 与字体也每次现问平台——
// 插件在自建窗里轮询这个函数就能跟住深浅色变化。

// Environment 当前环境快照。
//
// 未应用主题（pal 为空）时返回除 Frontend 外的零值快照，而不是编一个「浅色」出来：
// 「不知道」和「浅色」对插件是不同的意思。
func (u *UI) Environment() port.UIEnvironment {
	env := port.UIEnvironment{Frontend: port.FrontendGUI, Scale: 1.0}
	if p := u.pal.Load(); p != nil {
		env.Dark = p.isDark()
		env.Tokens = port.UIThemeTokens{
			BG:     hexColor(p.bg),
			FG:     hexColor(p.fg),
			Muted:  hexColor(p.textMuted),
			Accent: hexColor(p.brandColor),
			Border: hexColor(p.popEdge),
			Sel:    hexColor(p.popSel),
		}
	}
	if u.plat != nil {
		if s := u.plat.MainDPI(); s > 0 {
			env.Scale = s
		}
		env.Fonts = u.plat.SystemFontCandidates()
		env.SymbolFonts = u.plat.SymbolFontCandidates()
	}
	return env
}

// hexColor 转 RRGGBB（丢掉 alpha：语义色是面/字，透明度是宿主版式内部的事）。
func hexColor(c color.NRGBA) string {
	return fmt.Sprintf("%02X%02X%02X", c.R, c.G, c.B)
}
