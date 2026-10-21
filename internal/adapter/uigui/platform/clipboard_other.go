//go:build !windows

package platform

// 非 Windows 剪贴板位图（降级，D187）：Gio 的剪贴板只走文本，各桌面环境的位图读取
// 又要按工具链分叉（xclip / wl-paste / pngpaste……），本期不做——声明「没有图」，
// 入口就把 Ctrl+V 原样交给编辑器做文本粘贴，行为与升级前完全一致（不静默弄坏粘贴）。

// ClipboardHasImage 非 Windows 恒 false：不消费 Ctrl+V。
func (p *Plat) ClipboardHasImage() bool { return false }

// ClipboardImage 非 Windows 无位图剪贴板通路。
func (p *Plat) ClipboardImage() ([]byte, error) { return nil, ErrNoClipboardImage }
