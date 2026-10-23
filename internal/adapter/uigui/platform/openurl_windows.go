//go:build windows

package platform

// 用系统默认处理程序打开一个链接（设置窗「关于」页的项目主页）。走 ShellExecuteW 的
// "open" 动词，与在资源管理器里双击链接同一条路径——浏览器由系统决定，本程序不碰
// 进程创建与参数转义。

import (
	"fmt"
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
		return fmt.Errorf("链接为空")
	}
	op, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(target)),
		0, 0, swShowNormal)
	if r <= shellExecuteSuccess {
		return fmt.Errorf("系统未打开链接（错误码 %d）", r)
	}
	return nil
}
