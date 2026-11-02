//go:build windows

package i18n

import "syscall"

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")
)

// systemLanguage 读 Windows 的界面语言（D275）：只区分中文与英文，其余返回空串让调用方
// 回落 zh。用 GetUserDefaultUILanguage 而不是 LANG 环境变量——Windows 上后者常常根本没有。
func systemLanguage() Lang {
	lcid, _, _ := procGetUserDefaultUILanguage.Call()
	return langFromLCID(uint32(lcid))
}

// langFromLCID 把 LCID 的主语言 ID（低 10 位）映射到 Lang：0x04 = 中文（简繁都算），
// 0x09 = 英文；其余不认识。主语言 ID 口径见 Win32 的 MAKELANGID/PRIMARYLANGID。
func langFromLCID(lcid uint32) Lang {
	switch lcid & 0x3ff {
	case 0x04:
		return LangZh
	case 0x09:
		return LangEn
	}
	return ""
}
