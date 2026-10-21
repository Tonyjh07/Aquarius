package platform

// 剪贴板位图入口的共同约定（D187）：平台实现各异，但「剪贴板里没有图」必须能与
// 「读图失败」区分开——前者是常态（用户复制了一段文字），入口要静默把按键让回编辑器；
// 后者才值得提示一次。

import "errors"

// ErrNoClipboardImage 剪贴板里没有可用位图（不是错误，是常态）。
var ErrNoClipboardImage = errors.New("no image in clipboard")

// NoClipboardImage 判定「只是没有图」（入口据此决定不消费按键、不提示）。
func NoClipboardImage(err error) bool { return errors.Is(err, ErrNoClipboardImage) }
