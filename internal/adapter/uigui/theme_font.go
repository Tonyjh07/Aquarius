package uigui

import (
	"os"
	"strings"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// monoFace 代码块等宽字体面（newTheme 从集合挑 Mono 系；零值 = 回落正文体，D65）。
// 与 brandColor 等同槽风格：主题组装期写定，帧循环只读。
var monoFace font.Font

// newTheme 主题组装（§15.4 骨架：系统中文字体优先——gofont 无 CJK，spike 实证路径；
// 失败回落 gofont）。字体集合恒补 Go Mono 系面（markdown 代码块等宽，D65；CJK 缺字
// 由 typesetting FontMap 自动回落中文字面）。
func newTheme() *material.Theme {
	th := material.NewTheme()
	faces := loadCJKFaces()
	if len(faces) == 0 {
		faces = gofont.Collection() // 自带 Go Mono
	} else {
		faces = append(faces, monoFontFaces()...)
	}
	monoFace = pickMonoFace(faces)
	th.Shaper = text.NewShaper(text.WithCollection(faces))
	return th
}

// monoFontFaces gofont 集合中的 Mono 系面（等宽拉丁；正体在前优先）。
func monoFontFaces() []text.FontFace {
	var out []text.FontFace
	for _, f := range gofont.Collection() {
		if strings.Contains(strings.ToLower(string(f.Font.Typeface)), "mono") {
			out = append(out, f)
		}
	}
	return out
}

// pickMonoFace 从字体集合挑等宽正体面（未找到 = 零值回落）。
func pickMonoFace(faces []text.FontFace) font.Font {
	for _, f := range faces {
		if strings.Contains(strings.ToLower(string(f.Font.Typeface)), "mono") &&
			f.Font.Weight == font.Normal && f.Font.Style == font.Regular {
			return f.Font
		}
	}
	for _, f := range faces {
		if strings.Contains(strings.ToLower(string(f.Font.Typeface)), "mono") {
			return f.Font
		}
	}
	return font.Font{}
}

// mdHeadingSp 标题字号阶梯（D65）：h1 20sp → h2 17sp → h3 15sp，h4 以下与正文同大
// （Weight 求粗仍区分；CJK 粗体面缺省时回落常规渲染）。
func mdHeadingSp(th *material.Theme, level int) unit.Sp {
	switch level {
	case 1:
		return th.TextSize * 20.0 / 16.0
	case 2:
		return th.TextSize * 17.0 / 16.0
	case 3:
		return th.TextSize * 15.0 / 16.0
	default:
		return th.TextSize * 14.0 / 16.0
	}
}

// loadCJKFaces 加载 Windows 系统中文字体（§15.6 spike 实证：msyh.ttc → opentype）。
func loadCJKFaces() []text.FontFace {
	for _, p := range []string{
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
		`C:\Windows\Fonts\simsun.ttc`,
	} {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(src)
		if err != nil || len(faces) == 0 {
			continue
		}
		return faces
	}
	return nil
}
