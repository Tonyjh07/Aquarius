package uigui

// theme_paper.go 次窗的 Paper 视觉方向（D148）：白面 + hairline 分层 + 单层柔阴影 +
// 明确的圆角阶梯，强调色只给主操作与焦点环。
//
// **为什么跟主窗令牌分开**：主窗是 ULW 形裁窗（悬浮胶囊 / 球），它的配色与形状
// 位图、亚帧透明度耦合在一起；次窗是普通装饰窗，白面与阴影在系统窗体里才成立。
// 两者共用一个包级令牌槽时，只能取交集——那会让主窗的输入胶囊变白、次窗的卡片
// 仍是窗灰底。故次窗改用**自己的快照**：`palette.paper` 随原子快照一起传播，
// 主窗的包级令牌（brandColor/windowBg/...）完全不动。
//
// **强调色为什么分成两个**：品牌色 #00AEEF 上放白字只有 2.53:1（不达 AA 4.5:1），
// 而按钮必须写字。故：
//   - `accent`（#0A76AE）= 承载白字的实心按钮底与链接文字，4.98:1；
//   - `brand`（#00AEEF）= 纯标识：焦点环、选中态描边、进度、图标——不与文字叠。
// 两者同色相（198°），观感连续，不会读成两套配色。

import (
	"image/color"
	"sync/atomic"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// paperPalette 次窗 Paper 令牌（浅色）。深色版见 paperDarkPalette。
// 字段与 palette 有意重叠一部分：次窗读自己的令牌，主窗读包级槽。
type paperPalette struct {
	// 面阶：win = 窗体底，card = 卡片/行面，sunken = 次级面（输入框/只读块）。
	win, card, sunken color.NRGBA
	// 描边：hairline 分层（1px）。次窗无形状位图，分层只能靠描边与面阶。
	hairline color.NRGBA
	// 文字三档（层级主力是字号与颜色，不是字重——CJK 只有 Regular/Bold）。
	text, text2, text3 color.NRGBA
	// 强调：accent 承载白字（按钮底），brand 只做标识（焦点环/选中描边）。
	accent, accentFg, brand, brandSoft color.NRGBA
	// 选中项底（导航栏/分段控件）。
	sel color.NRGBA
	// 语义色。
	danger, ok, warn color.NRGBA
	// 浮层（tips / 弹窗遮罩）。
	scrim, tipBg color.NRGBA
	// depth 关系图 4 档深度色带（A2 可解释性：色带就是把「离根多远」画出来）。
	// 4 档必须都在画布上真的出现——图例列了 4 个而画布只现 3 档，读者会把第 4 档
	// 读成「另一种节点类型」，而不是「更深的层级」。
	depth [4]color.NRGBA
	// 圆角阶梯（dp；经 gtx.Dp 消费）。窗体 16 / 卡 12 / 控件 10 / chip 8。
	rWin, rCard, rCtl, rChip int
	// 柔阴影开关与强度：次窗是常规窗，阴影给「浮层/卡片抬升」用；主窗不用。
	shadow bool
	// dark 供关系图色带等需要判明暗的路径使用。
	dark bool
}

// paperLight 次窗浅色 Paper。
func paperLight() paperPalette {
	return paperPalette{
		win:      paperRGB(0xFF, 0xFF, 0xFF),
		card:     paperRGB(0xFF, 0xFF, 0xFF),
		sunken:   paperRGB(0xF4, 0xF6, 0xF9),
		hairline: paperRGB(0xE4, 0xE7, 0xEC),

		text:  paperRGB(0x0F, 0x17, 0x2A),
		text2: paperRGB(0x5A, 0x64, 0x74),
		// 第三档（说明/微标）必须仍能读清：白面 5.02:1、下沉面 4.64:1。
		// 早先取 #8A93A3 只有 3.1:1——灰得很"干净"，但低视力与高亮屏下就是读不动。
		text3: paperRGB(0x66, 0x70, 0x7E),

		accent:    paperRGB(0x0A, 0x76, 0xAE), // 白字 4.98:1（AA）
		accentFg:  paperRGB(0xFF, 0xFF, 0xFF),
		brand:     paperRGB(0x00, 0xAE, 0xEF), // 只做标识，不与文字叠
		brandSoft: paperRGB(0xE7, 0xF3, 0xFA),

		sel: paperRGB(0xF2, 0xF5, 0xF8),

		// 语义色也按 AA 定档：它们在白面与下沉面上都要能读清（原 ok/warn 只有 3.1/3.6）。
		danger: paperRGB(0xC6, 0x2F, 0x2F), // 白面 5.46:1
		ok:     paperRGB(0x1B, 0x7F, 0x3F), // 白面 5.06:1
		warn:   paperRGB(0x9A, 0x5B, 0x00), // 白面 5.43:1

		scrim: paperRGB(0x10, 0x14, 0x18),
		tipBg: paperRGB(0x26, 0x2A, 0x2E),

		depth: [4]color.NRGBA{
			paperRGB(0x0A, 0x76, 0xAE), paperRGB(0x2E, 0x9E, 0xC4),
			paperRGB(0x46, 0xB0, 0x93), paperRGB(0xA8, 0x8F, 0x2E),
		},

		rWin: 16, rCard: 12, rCtl: 10, rChip: 8,
		shadow: true,
	}
}

// paperDark 次窗深色 Paper：几何与浅色**完全一致**，只换颜色数据——深色不依赖
// 系统材质（主窗 ULW 吃不到材质，次窗也不该假设有）。
func paperDark() paperPalette {
	return paperPalette{
		win:      paperRGB(0x16, 0x19, 0x1E),
		card:     paperRGB(0x1B, 0x1F, 0x26),
		sunken:   paperRGB(0x22, 0x26, 0x2E),
		hairline: paperRGB(0x2C, 0x31, 0x3A),

		text:  paperRGB(0xE8, 0xEA, 0xEE),
		text2: paperRGB(0xA2, 0xAA, 0xB6),
		// 深色版同样要过 AA：卡面 5.34:1、下沉面 4.9:1（原 #747C8A 只有 4.19／3.8）。
		text3: paperRGB(0x8A, 0x93, 0xA3),

		// 深色底上白字对比度反过来：亮青更合适，白字仍达标。
		accent:    paperRGB(0x2E, 0x9B, 0xD6),
		accentFg:  paperRGB(0x06, 0x12, 0x1B),
		brand:     paperRGB(0x4A, 0xA8, 0xE0),
		brandSoft: paperRGB(0x1B, 0x30, 0x3F),

		sel: paperRGB(0x22, 0x26, 0x2E),

		danger: paperRGB(0xE8, 0x6B, 0x6B), // 卡面 5.31:1（原 #E05A5A 在中灰面上 4.17）
		ok:     paperRGB(0x3E, 0xB5, 0x6B),
		warn:   paperRGB(0xE0, 0xA3, 0x3C),

		scrim: paperRGB(0x06, 0x08, 0x0A),
		tipBg: paperRGB(0x2E, 0x32, 0x38),

		depth: [4]color.NRGBA{
			paperRGB(0x4A, 0xA8, 0xE0), paperRGB(0x5C, 0xC0, 0xC8),
			paperRGB(0x6E, 0xC8, 0x9B), paperRGB(0xC8, 0xB0, 0x5A),
		},

		rWin: 16, rCard: 12, rCtl: 10, rChip: 8,
		shadow: true,
		dark:   true,
	}
}

// resolvePaper 次窗 Paper 预设（跟随系统深浅；与 palette 同一套解析口径）。
func resolvePaper(mode string, dark bool) paperPalette {
	switch mode {
	case "light":
		return paperLight()
	case "dark":
		return paperDark()
	}
	if dark {
		return paperDark()
	}
	return paperLight()
}

// paperRGB 按十六进制字面量构造不透明色（与既有 palette 的写法同观感）。
func paperRGB(r, g, b uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: 0xFF} }

// activePaper 次窗 Paper 令牌活动槽。
//
// 用**进程级**快照而不是每窗一份：全进程只有一档主题（ui.theme 是全局配置），
// 所有次窗必然同值；每窗一份只会让装配前的独立向导窗（没有 *UI）读不到。
// 写入点只有两处，都在窗口事件循环启动之前或由主窗事件循环独占：
//   - newUI / RunSetupWindow 装配期（goroutine 启动前，无并发）；
//   - applyTheme（仅主窗事件循环 goroutine 调用）。
//
// 读侧是渲染 goroutine，故用原子指针：**只读快照，不共享可变态**。
var activePaper atomic.Pointer[paperPalette]

// applyPaper 写入次窗 Paper 令牌（调用点同 applyTheme 的并发约束）。
func applyPaper(p paperPalette) { activePaper.Store(&p) }

// ---------------------------------------------------------------------------
// 字级阶梯（次窗）
// ---------------------------------------------------------------------------

// Paper 的字号阶梯按**用途**定档，不按 material 的 Body1/Body2 定档。原因：material
// 的比例尺是相对 th.TextSize 的（默认 16），H6=20/16、Subtitle1=16/16、Body1=16/16
// ——分区标题与正文**完全同大**，页面标题只大 25%。设置页此前直接取这三档，渲染出来
// 「标题、分区、正文、说明」四层挤成两层，层级丢失。
//
// 定档口径（相对 th.TextSize 线性缩放，用户调字号时整体跟着走）：
//
//	页标题   +25%   加粗
//	分区标题  −8%   加粗 + 字距微开（靠颜色与"小一号的粗体"与正文分开）
//	正文       0
//	说明      −15%
//	微标      −23%  （磁贴第二行、图例、字数这类辅助信息）
const (
	paperTitleR   = 1.25
	paperSectionR = 0.92
	paperBodyR    = 1.00
	paperNoteR    = 0.85
	paperMicroR   = 0.77
)

// paperTitleSp 页标题字号。
func paperTitleSp(th *material.Theme) unit.Sp { return th.TextSize * paperTitleR }

// paperSectionSp 分区标题字号。
func paperSectionSp(th *material.Theme) unit.Sp { return th.TextSize * paperSectionR }

// paperBodySp 正文字号（= 主题基准；显式取回而不是靠 material 默认，便于一处调档）。
func paperBodySp(th *material.Theme) unit.Sp { return th.TextSize * paperBodyR }

// paperNoteSp 说明文字字号。
func paperNoteSp(th *material.Theme) unit.Sp { return th.TextSize * paperNoteR }

// paperMicroSp 辅助微标字号。
func paperMicroSp(th *material.Theme) unit.Sp { return th.TextSize * paperMicroR }

// paperTitle 页标题：大一号 + 加粗 + 主文字色。
func paperTitle(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperTitleSp(th), text)
		l.Font.Weight = font.Bold
		l.Color = paperOf().text
		return l.Layout(gtx)
	}
}

// paperSection 分区标题：小一号的粗体 + 次级色。**不用全大写**：命令名与缩写
// （MCP / TTS / provider）大写后更难扫读，中文也没有大小写这层信息可用。
func paperSection(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperSectionSp(th), text)
		l.Font.Weight = font.Bold
		l.Color = paperOf().text2
		return l.Layout(gtx)
	}
}

// paperNote 说明文字：比正文小一档、第三档颜色（不抢正文，但仍要能读清）。
func paperNote(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperNoteSp(th), text)
		l.Color = paperOf().text3
		return l.Layout(gtx)
	}
}

// paperBody 正文：主题基准字号 + 主文字色。
func paperBody(th *material.Theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperBodySp(th), text)
		l.Color = paperOf().text
		return l.Layout(gtx)
	}
}

// paperOf 次窗帧内取 Paper 令牌。未初始化时回落浅色预设——宁可白面，也不要零值黑。
func paperOf() paperPalette {
	if pp := activePaper.Load(); pp != nil {
		return *pp
	}
	return paperLight()
}

// applyPaperTheme 把 Paper 令牌投影到 material 主题实例（次窗各自持有 th）。
//
// 只改次窗自己的 th：主窗的 th 与包级令牌槽都不动，所以主窗配色完全保持原样。
// **必须设 ContrastBg/ContrastFg**：material.Button 默认取 Gio 的靛蓝 #3f51b5，
// 不投影的话次窗里每个未显式配色的按钮都会是蓝底白字，与白面 Paper 完全冲突。
func applyPaperTheme(th *material.Theme, pp paperPalette) {
	th.Palette.Fg, th.Palette.Bg = pp.text, pp.win
	th.Palette.ContrastBg, th.Palette.ContrastFg = pp.accent, pp.accentFg
}

// paperCheckBox 次窗复选框：勾选框用品牌色而非 material 默认的 ContrastBg。
//
// 为什么不直接 material.CheckBox：它的 IconColor 固定取 th.Palette.ContrastBg——
// 也就是「主按钮底色」。在 Paper 里主按钮底是 accent（要压白字），而勾选框是**标识性
// 图形**，按分工该用 brand。两者混用会让勾选框比主按钮还暗一档，与「图示用亮色、
// 文字底用深色」的口径打架（实测：复选框残留 Gio 靛蓝 #3F51B5）。
func paperCheckBox(th *material.Theme, c *widget.Bool, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		b := material.CheckBox(th, c, label)
		b.Color = pp.text
		b.IconColor = pp.brand
		b.TextSize = paperBodySp(th)
		return b.Layout(gtx)
	}
}

// paperBtnKind 次窗按钮的三档语义（Paper 里只有这三种，不设第四档灰按钮）。
type paperBtnKind int

const (
	paperBtnSecondary paperBtnKind = iota // 次要：下沉面 + 主文字（默认形态）
	paperBtnPrimary                       // 主操作：深一档强调底 + 白字（AA 达标）
	paperBtnDanger                        // 破坏性：白面 + 危险色文字
)

// paperButton 次窗通用按钮：统一圆角、内衬与三档配色。
//
// 为什么不用 material.Button 的默认外观：它的默认底色是 Gio 的靛蓝 ContrastBg，
// 圆角 4dp 也与 Paper 的阶梯（控件 10dp）不符。这里只覆盖样式、点击语义仍走
// material.Button（焦点环、涟漪、禁用态都由它提供）。
func paperButton(th *material.Theme, c *widget.Clickable, label string, kind paperBtnKind, enabled bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		pp := paperOf()
		b := material.Button(th, c, label)
		b.TextSize = paperNoteSp(th)
		b.CornerRadius = unit.Dp(pp.rCtl)
		b.Inset = layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(14), Right: unit.Dp(14)}
		switch kind {
		case paperBtnPrimary:
			b.Background, b.Color = pp.accent, pp.accentFg
		case paperBtnDanger:
			b.Background, b.Color = pp.card, pp.danger
		default:
			b.Background, b.Color = pp.sunken, pp.text
		}
		if !enabled {
			gtx = gtx.Disabled()
		}
		return b.Layout(gtx)
	}
}
