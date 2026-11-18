//go:build windows

package platform

// 用系统默认处理程序打开一个链接或目录（设置窗「关于」页的项目主页、日志页的
// 「打开目录」）。走 ShellExecuteW 的 "open" 动词，与在资源管理器里双击同一条路径——
// 浏览器与资源管理器都由系统决定，本程序不碰进程创建与参数转义。

import (
	"fmt"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"syscall"
	"unsafe"
)

var procShellExecuteW = shell32.NewProc("ShellExecuteW")

// swShowNormal ShellExecute 的显示方式（SW_SHOWNORMAL）。
const swShowNormal = 1

// shellExecuteSuccess ShellExecuteW 返回值大于 32 才算成功（MSDN：≤32 是错误码）。
const shellExecuteSuccess = 32

// OpenURL 用系统默认浏览器打开 url。
func (p *Plat) OpenURL(url string) error {
	if url == "" {
		return fmt.Errorf("%s", i18n.T("ui.platform.url.empty"))
	}
	return shellOpen(url)
}

// OpenPath 打开一个本地路径（目录 → 资源管理器，文件 → 它的默认程序）。
func (p *Plat) OpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("%s", i18n.T("ui.platform.url.empty"))
	}
	return shellOpen(path)
}

// shellOpen ShellExecuteW("open", target)。两个入口只差空值文案，动词与错误处理同一份。
func shellOpen(target string) error {
	op, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(t)),
		0, 0, swShowNormal)
	if r <= shellExecuteSuccess {
		return fmt.Errorf("%s", i18n.Tf("ui.platform.url.not_opened", "code", r))
	}
	return nil
}
