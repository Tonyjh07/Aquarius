package uigui

// 主题令牌系统（§15.4/D61）：颜色令牌深浅两版**纯数据预设**——加色不改代码。
// 活动令牌槽仍是 window.go 的包级 var（自绘路径既有引用不动），palette 结构 =
// 预设与快照的集中形态；material 主题（th.Palette）经同一快照取默认文字/底色。
//
// 应用所有权（§15.7 并发模型——不共享裸字段）：
//   - 初始：newUI 在 goroutine 启动前调 applyTheme（无并发）；
//   - 运行时：仅事件循环 goroutine 经 themeMsg/sysThemeMsg 调用；
//   - 次窗：u.pal 原子快照供 spawn 读取，applyTheme 经 propagatePalette 广播，
//     各次窗帧内校正自身 material 主题并重绘（跨窗只经原子快照，无裸共享）。

import "image/color"

// palette 颜色令牌全集（§15.4：背景/文字/气泡双色/思考暗块/状态行/错误色/禁用态；
// fg/bg = material 默认文字/底——编辑器与助手/纯文本默认取色、次窗铺底）。
type palette struct {
	brandColor     color.NRGBA // 品牌色（logo/发送键/用户气泡底——两版同值，取自 assets/icon）
	pillBg         color.NRGBA // 输入胶囊/助手气泡/纯文本卡/状态 chip 底
	windowBg       color.NRGBA // 兜底背景（形裁前/整窗铺底）
	textDim        color.NRGBA // 思考块文字
	textMuted      color.NRGBA // 工具 chip/状态行文字
	textError      color.NRGBA // 错误文字
	textNotice     color.NRGBA // 提示文字
	textSystem     color.NRGBA // 系统行文字
	cardThinking   color.NRGBA
	cardTool       color.NRGBA
	cardNotice     color.NRGBA
	cardError      color.NRGBA
	cardSystem     color.NRGBA
	whiteText      color.NRGBA // 品牌底/深色 tips 上的文字
	iconDim        color.NRGBA // 图标槽占位灰
	disabledCircle color.NRGBA // 确认态置灰右圆
	tipBg          color.NRGBA // 悬浮 tips 底色
	fg, bg         color.NRGBA // material Palette（默认文字/底）
}

// lightPalette 浅色预设（= MVP 既有配色，逐值不变——§15.4 品牌色 + 浅白/浅灰）。
func lightPalette() palette {
	return palette{
		brandColor:     color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF},
		pillBg:         color.NRGBA{R: 0xFA, G: 0xFA, B: 0xFC, A: 0xFF},
		windowBg:       color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF3, A: 0xFF},
		textDim:        color.NRGBA{R: 0x8A, G: 0x8F, B: 0x98, A: 0xFF},
		textMuted:      color.NRGBA{R: 0x6B, G: 0x70, B: 0x78, A: 0xFF},
		textError:      color.NRGBA{R: 0xD9, G: 0x3A, B: 0x3A, A: 0xFF},
		textNotice:     color.NRGBA{R: 0xC0, G: 0x77, B: 0x00, A: 0xFF},
		textSystem:     color.NRGBA{R: 0x8E, G: 0x6B, B: 0xC4, A: 0xFF},
		cardThinking:   color.NRGBA{R: 0xE9, G: 0xEB, B: 0xF0, A: 0xFF},
		cardTool:       color.NRGBA{R: 0xE7, G: 0xEA, B: 0xEF, A: 0xFF},
		cardNotice:     color.NRGBA{R: 0xFD, G: 0xF2, B: 0xDC, A: 0xFF},
		cardError:      color.NRGBA{R: 0xFB, G: 0xE4, B: 0xE4, A: 0xFF},
		cardSystem:     color.NRGBA{R: 0xF1, G: 0xEB, B: 0xFA, A: 0xFF},
		whiteText:      color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
		iconDim:        color.NRGBA{R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF},
		disabledCircle: color.NRGBA{R: 0xD8, G: 0xDC, B: 0xE3, A: 0xFF},
		tipBg:          color.NRGBA{R: 0x26, G: 0x2A, B: 0x2E, A: 0xFF},
		fg:             color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xFF},
		bg:             color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
	}
}

// darkPalette 深色预设（同构异值：底压暗、文字提亮、卡片同阶降亮；
// 品牌色与 tips 白字恒定——§15.4 两版预设）。
func darkPalette() palette {
	return palette{
		brandColor:     color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF},
		pillBg:         color.NRGBA{R: 0x26, G: 0x2A, B: 0x2F, A: 0xFF},
		windowBg:       color.NRGBA{R: 0x15, G: 0x18, B: 0x1C, A: 0xFF},
		textDim:        color.NRGBA{R: 0x9A, G: 0xA1, B: 0xAB, A: 0xFF},
		textMuted:      color.NRGBA{R: 0x90, G: 0x96, B: 0xA0, A: 0xFF},
		textError:      color.NRGBA{R: 0xF0, G: 0x6A, B: 0x6A, A: 0xFF},
		textNotice:     color.NRGBA{R: 0xE0, G: 0xA3, B: 0x3C, A: 0xFF},
		textSystem:     color.NRGBA{R: 0xB4, G: 0x9B, B: 0xE8, A: 0xFF},
		cardThinking:   color.NRGBA{R: 0x23, G: 0x26, B: 0x2B, A: 0xFF},
		cardTool:       color.NRGBA{R: 0x21, G: 0x24, B: 0x2A, A: 0xFF},
		cardNotice:     color.NRGBA{R: 0x34, G: 0x2D, B: 0x1E, A: 0xFF},
		cardError:      color.NRGBA{R: 0x35, G: 0x24, B: 0x24, A: 0xFF},
		cardSystem:     color.NRGBA{R: 0x2B, G: 0x27, B: 0x35, A: 0xFF},
		whiteText:      color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
		iconDim:        color.NRGBA{R: 0x7C, G: 0x87, B: 0x98, A: 0xFF},
		disabledCircle: color.NRGBA{R: 0x3A, G: 0x3F, B: 0x47, A: 0xFF},
		tipBg:          color.NRGBA{R: 0x2E, G: 0x32, B: 0x38, A: 0xFF},
		fg:             color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF3, A: 0xFF},
		bg:             color.NRGBA{R: 0x15, G: 0x18, B: 0x1C, A: 0xFF},
	}
}

// resolvePalette 档位 → 预设：light/dark 直取；其余（""/"system"/未识别）
// = 跟随系统（§15.4 默认）。
func resolvePalette(mode string) palette {
	switch mode {
	case "light":
		return lightPalette()
	case "dark":
		return darkPalette()
	default:
		if systemDark() {
			return darkPalette()
		}
		return lightPalette()
	}
}

// applyPalette 把预设写入活动令牌槽（包级 var；仅事件循环 goroutine 或
// goroutine 启动前调用——与帧读取不并发）。
func applyPalette(p palette) {
	brandColor = p.brandColor
	pillBg = p.pillBg
	windowBg = p.windowBg
	textDim = p.textDim
	textMuted = p.textMuted
	textError = p.textError
	textNotice = p.textNotice
	textSystem = p.textSystem
	cardThinking = p.cardThinking
	cardTool = p.cardTool
	cardNotice = p.cardNotice
	cardError = p.cardError
	cardSystem = p.cardSystem
	whiteText = p.whiteText
	iconDim = p.iconDim
	disabledCircle = p.disabledCircle
	tipBg = p.tipBg
}

// currentPalette 从活动令牌槽回读（与 applyPalette 成对；供测试断言同步完整性）。
// material 的 fg/bg 不落包级槽——回读为零值，校正走 u.th/次窗快照。
func currentPalette() palette {
	return palette{
		brandColor:     brandColor,
		pillBg:         pillBg,
		windowBg:       windowBg,
		textDim:        textDim,
		textMuted:      textMuted,
		textError:      textError,
		textNotice:     textNotice,
		textSystem:     textSystem,
		cardThinking:   cardThinking,
		cardTool:       cardTool,
		cardNotice:     cardNotice,
		cardError:      cardError,
		cardSystem:     cardSystem,
		whiteText:      whiteText,
		iconDim:        iconDim,
		disabledCircle: disabledCircle,
		tipBg:          tipBg,
	}
}

// applyTheme 应用主题档（初始：newUI goroutine 启动前；运行时：仅事件循环
// goroutine——themeMsg/sysThemeMsg）。解析预设 → 写活动槽与 material 默认色 →
// 原子快照传播次窗（propagatePalette 广播 + Invalidate）。
func (u *UI) applyTheme(mode string) {
	if mode == "" {
		mode = "system"
	}
	p := resolvePalette(mode)
	applyPalette(p)
	u.themeMode = mode
	u.pal.Store(&p)
	if u.th != nil { // headless 无主题（测试自装）；runWindow 创建后自会按快照校正
		u.th.Palette.Fg, u.th.Palette.Bg = p.fg, p.bg
	}
	u.wins.propagatePalette(&p)
}
