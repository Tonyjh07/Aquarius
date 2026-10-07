//go:build !windows

package platform

import "runtime"

// DarkMode 系统深浅检测（非 Windows 降级，D111③）：经桌面环境命令查询，不引 cgo——
//   - Linux：GNOME `gsettings`（color-scheme 含 "dark"）→ KDE `kreadconfig6/5`（已试）；
//   - macOS：`defaults read -g AppleInterfaceStyle`（深色时输出 "Dark"）。
//
// 命令缺失/查询失败 = 浅色保守回落（与 Windows 侧「键缺失 = 浅色」同口径）。变化事件
// 依赖桌面环境广播：非 Windows 侧暂无监听（首帧与设置窗切换时重查），属已知降级。
func (p *Plat) DarkMode() bool {
	switch runtime.GOOS {
	case "darwin":
		return darkFromCommands([][]string{
			{"defaults", "read", "-g", "AppleInterfaceStyle"},
		})
	default:
		return darkFromCommands([][]string{
			{"gsettings", "get", "org.gnome.desktop.interface", "color-scheme"},
			{"gsettings", "get", "org.gnome.desktop.interface", "gtk-theme"},
			{"kreadconfig6", "--group", "General", "--key", "ColorScheme"},
			{"kreadconfig5", "--group", "General", "--key", "ColorScheme"},
		})
	}
}

// SystemFontCandidates 系统中文字体候选路径（非 Windows 降级）：优先常见 CJK 字体包
// （Noto CJK / 文泉驿 / Droid Fallback），macOS 取苹方；全部缺失时 uigui 回落 gofont
// （无 CJK 字面，属已知降级）。
func (p *Plat) SystemFontCandidates() []string {
	if runtime.GOOS == "darwin" {
		return []string{
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/Library/Fonts/Arial Unicode.ttf",
		}
	}
	return []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
	}
}
