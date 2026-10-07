package uigui

// 窗口外壳**策略**（中性，S4b）：显隐/呼出/置顶/退出这些决策留在 uigui；平台能力
// （托盘、全局热键、原生菜单呈现、原生显隐/置顶）经 u.plat 触达（D111 修订②）。
// 原先这些方法只存在于 shell_windows.go；迁入中性文件后非 Windows 也能编译同一套
// 策略（只是平台侧能力降级：呼出/常驻缺失，见 platform 包）。

import (
	"fmt"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
)

// toggleTopMost 置顶开关（托盘/logo 菜单，§15.1/D72）：切换主窗置顶态并持久化（与位置
// 记忆同文件）。D62：单窗单像素层，无 overlay 跟随步。
func (u *UI) toggleTopMost() {
	on := !u.plat.TopMost()
	u.plat.SetTopMost(on) // 主窗断言（平台内部经窗口线程，铁律 1）
	if u.opts.PosFile != "" {
		if rc, ok := u.windowRect(); ok { // 查询类：跨线程直接调
			tm := on
			var docked string // D50：停靠边随记忆保存（dockHint 原子镜像，跨线程读）
			if c := u.dockHint.Load(); c != dockNoneInt {
				docked = edgeName(c)
			}
			savePos(u.opts.PosFile, posRec{X: rc.left, Y: rc.top, TopMost: &tm, Docked: docked})
		}
	}
	fmt.Printf("[tray] 窗口置顶 → %v\n", on)
}

// toggleWindow 托盘显隐（§15.1：隐藏只经托盘；呼出 = 显示 + 展开 + 焦点入栏）。
func (u *UI) toggleWindow() {
	h := u.mainHandle()
	if h == 0 {
		return // 主窗未就绪（Win32ViewEvent 晚到）：忽略本次触发
	}
	if u.plat.Visible() {
		u.plat.HideMain(h)
		return
	}
	u.showMain()
}

// hotkeyToggle 快捷键（§15.1）：隐藏 → 呼出（showMain）；可见 → 展开 ↔ 收起互切。
func (u *UI) hotkeyToggle() {
	h := u.mainHandle()
	if h == 0 {
		return
	}
	if !u.plat.Visible() {
		u.showMain()
		return
	}
	u.post(toggleExpandMsg{})
}

// showMain 呼出：显示 + 前台 + 展开输入栏 + 焦点入栏（§15.1 呼出 = 展开）。
// D78：首帧 ULW 未提交前（revealPending）**不显示窗口**——只投展开态，窗口由首帧
// 揭示（防提前 ShowWindow 闪现未定制窗口）；就绪后经 RevealWindow 揭示（显示 + 激活前台）。
func (u *UI) showMain() {
	h := u.mainHandle()
	if h == 0 {
		return
	}
	if u.revealPending.Load() {
		u.post(showExpandMsg{}) // 只置展开/焦点态（拍板：首帧前呼出忽略显示）
		return
	}
	u.plat.RevealWindow(h)
	u.representAfterShow() // D88：呼出重显后补提交（隐藏期间无 ULW，防裸 Gio 表面）
	u.post(showExpandMsg{})
}

// exitViaShell 菜单退出（托盘/logo 右键菜单共用，§15.1/D72：退出只经菜单）——注销快捷键、
// 清托盘图标、隐藏主窗，**中断进行中轮次**（D64：退出即终止，不等待 Agent 完成——装配根
// 阻塞在 Turn 内时 EOF 不可见，先经停止键同款取消通道解卷）再走 EOF 收尾（装配根 Next →
// io.EOF → Close → 事件循环退出，退出码 0）。
func (u *UI) exitViaShell() {
	u.plat.ShutdownShell() // 注销全局热键 + 清托盘图标（幂等）
	if h := u.mainHandle(); h != 0 {
		u.plat.HideMain(h)
	}
	fmt.Println("[tray] 菜单退出")
	u.interruptNow() // D64：进行中的 Turn 立即取消（无轮进行 = nop，空闲退出不受影响）
	u.signalEOF(nil)
}

// mainHandle 主窗句柄（平台侧原子读；跨 goroutine 安全——设置窗线程也会用到）。
func (u *UI) mainHandle() platform.Handle { return u.plat.MainHandle() }

// startShell 启动外壳（托盘 + 全局热键；仅窗口模式）：宿主回调先绑定，平台按需起线程。
func (u *UI) startShell() {
	u.plat.SetHost(&platHost{u: u})
	u.plat.StartShell()
}
