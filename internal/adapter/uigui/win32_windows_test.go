//go:build windows

package uigui

import "testing"

// TestHideUntilFirstPresentRoutesWindowThread D78 挂接即隐藏必须经窗口线程下发
// （§15.6 铁律 1）：onHWND 跑在事件循环客户端协程（runWindow 读 w.Event()），此际
// 窗口线程常停在 deliverEvent 的 select 把事件发给客户端、不泵消息——跨线程 ShowWindow
// 内部回投窗口过程会永久互锁，实机表现为启动即「未响应」（日志断在 logo、WM_CLOSE
// 钩子与 [ulw] 皆未打出）。经 win32Run 槽注入断言：hide 走 Window.Run，不得在调用方
// 协程直调 ShowWindow。
func TestHideUntilFirstPresentRoutesWindowThread(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	oldRun := win32Run.Load()
	defer func() { win32Run.Store(oldRun) }()
	routed := false
	fakeRun := func(f func()) { routed = true; f() } // 假槽：记录后执行（假句柄 ShowWindow 落空无副作用）
	win32Run.Store(&fakeRun)

	hideUntilFirstPresent(0x1234)

	if !routed {
		t.Fatal("hideUntilFirstPresent 未走窗口线程槽（直调 ShowWindow 违反铁律 1）")
	}
}
