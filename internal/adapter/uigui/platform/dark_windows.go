//go:build windows

package platform

import (
	"syscall"
	"unsafe"
)

var advapi32 = syscall.NewLazyDLL("advapi32.dll")

var (
	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

// 系统深浅检测相关常量（Win32 头文件取值）。
const (
	hkeyCurrentUser = 0x80000001 // HKEY_CURRENT_USER（预定义句柄，与 x/sys 同口径零扩展）
	keyRead         = 0x20019    // KEY_READ
	regDWORD        = 4          // REG_DWORD
	personalizePath = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	appsUseLight    = "AppsUseLightTheme" // 0 = 深色应用主题，1 = 浅色
)

// DarkMode 系统深浅检测（§15.4/D61：Win32 注册表 `AppsUseLightTheme`，键缺失/读取失败
// = 浅色保守回落）。查询类调用可跨线程直调（§15.6 铁律 1 不限）；运行中变化经主窗
// WM_SETTINGCHANGE 广播（见 shell_windows.go 的窗口过程）触发 Host.SystemThemeChanged。
func (p *Plat) DarkMode() bool {
	key, err := syscall.UTF16PtrFromString(personalizePath)
	if err != nil {
		return false
	}
	name, err := syscall.UTF16PtrFromString(appsUseLight)
	if err != nil {
		return false
	}
	var hkey uintptr
	r, _, _ := procRegOpenKeyExW.Call(hkeyCurrentUser,
		uintptr(unsafe.Pointer(key)), 0, keyRead,
		uintptr(unsafe.Pointer(&hkey)))
	if r != 0 || hkey == 0 {
		return false
	}
	defer procRegCloseKey.Call(hkey)
	var val, typ, size uint32
	q, _, _ := procRegQueryValueExW.Call(hkey,
		uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)))
	if q != 0 || typ != regDWORD || size < 4 {
		return false
	}
	return val == 0
}

// SystemFontCandidates 系统中文字体候选路径（§15.6 spike 实证：msyh.ttc → opentype）。
// 平台无关的解析/回落（gofont）留在 uigui 侧：本方法只给路径。
func (p *Plat) SystemFontCandidates() []string {
	return []string{
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
		`C:\Windows\Fonts\simsun.ttc`,
	}
}
