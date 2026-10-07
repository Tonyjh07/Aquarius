//go:build windows

// 通知子进程窗口隐藏（D117，§7.2）：Windows 通知 = 拉起 `powershell` 跑气泡脚本
// （子进程存活 ≈6s）。D114 双击启动 FreeConsole 后父进程无控制台可继承，Windows 会为
// 该子进程新建**可见控制台**（D114 前继承的是已被隐藏的自建控制台，不可见；终端启动
// 同样会新开，只是原本不显眼）——故分离启动前显式压制子进程窗口。
package main

import (
	"os/exec"
	"syscall"
)

// createNoWindow CREATE_NO_WINDOW（0x08000000）：console 子系统子进程不创建控制台窗。
const createNoWindow = 0x08000000

// hideNotifyChild 压制通知子进程的窗口（D117）。两旗并挂、互不冲突：
// CREATE_NO_WINDOW 断掉控制台窗创建（console 子进程正解），HideWindow 兜住任何顶层窗
// （GUI 子进程只有它管用）。调用时机 = 命令构造后、Start 前；已有的 SysProcAttr 字段
// 不覆盖（按位或 / 条件赋值，幂等可重入）。
func hideNotifyChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
