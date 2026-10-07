package uigui

// 平台接口（消费方定义，D111 修订②）：`uigui` 主体只经本接口触达平台能力，具体实现见
// `internal/adapter/uigui/platform`（纯 Go、不依赖 gio，三平台可各自交叉编译与单测）。
//
// 接口随 S4b 分批增长：每迁入一面方法，Windows 实现与非 Windows 降级实现必须同时给出
// ——差一个方法 `*platform.Plat` 就满足不了本接口，`platformImpl` 断言即编译失败。
//
// 测试替身：headless 测试可把 `u.plat` 换成假实现（原包级注入槽 presentMain/revealMain/
// visMain 随平台面迁入后退役）。

import (
	"github.com/Tonyjh07/Aquarius/assets"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// platformImpl 当前实现满足消费方接口的编译期断言。
var _ platformAPI = (*platform.Plat)(nil)

// platformAPI 平台能力（uigui → 平台）。
type platformAPI interface {
	// DarkMode 系统深浅色（§15.4/D61）。
	DarkMode() bool
	// SystemFontCandidates 系统中文字体候选路径（无 = 回落 gofont）。
	SystemFontCandidates() []string
}

// newPlatform 构造平台实现（组装点：`uigui` 内唯一 import 具体实现处）。
func newPlatform(opts Options, window bool) platformAPI {
	cfg := platform.Config{Hotkey: opts.Hotkey}
	if window {
		// 托盘图标：.ico 内嵌资源 → 16px 档（平台无关解析在此完成，平台只负责呈现）。
		if b, err := icoImage(assets.TrayICO, 16); err == nil {
			cfg.TrayIcon = b
		}
	}
	return platform.New(cfg)
}
