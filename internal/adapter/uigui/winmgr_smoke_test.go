package uigui

// 次窗真实窗口冒烟（需显示环境；AQUARIUS_GUI_SMOKE=1 启用——§15.5 GUI 不进 CI
// 图形路径，常规门禁只跑 headless 注册表套件）。验证 §15.7/D60 的关键假设：
// 同进程第二个 app.Window（未调 app.Main）能创建并运转事件循环 → HWND 挂接 →
// 收编投递 WM_CLOSE → Gio 默认销毁 → DestroyEvent → 循环退出、登记摘除。
// 设置窗一并开：真实窗口下跑通 settingsFrame 表单渲染（headless 不触帧路径）。

import (
	"os"
	"testing"
	"time"
)

func TestSecondaryWindowSmoke(t *testing.T) {
	if os.Getenv("AQUARIUS_GUI_SMOKE") == "" {
		t.Skip("次窗真实窗口冒烟（AQUARIUS_GUI_SMOKE=1 启用）")
	}

	u := newUI(Options{}, false) // headless 状态机 + 手动注入开窗器（不起托盘/主窗）
	defer u.Close()
	u.wins.spawn = u.spawnSecondary

	u.wins.openWin(winHistory)
	u.wins.openWin(winSettings)
	u.wins.mu.Lock()
	hd := u.wins.live[winHistory]
	hs := u.wins.live[winSettings]
	u.wins.mu.Unlock()
	if hd == nil || hs == nil {
		t.Fatal("开窗后登记缺失")
	}

	// HWND 就绪 = 窗口创建 + 事件循环在跑（Win32ViewEvent 已投递）。
	waitHandle := func(hd *winHandle, what string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for hd.hwnd.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if hd.hwnd.Load() == 0 {
			t.Fatalf("%s HWND 未在时限内就绪（窗口创建/事件循环假设不成立）", what)
		}
	}
	waitHandle(hd, "会话历史窗")
	waitHandle(hs, "设置窗")

	// 收编 → WM_CLOSE → DestroyEvent → 循环退出（closeAll 已清空登记，
	// isOpen 立即为 false——销毁进度只能看实例自身的 done）。
	if n := u.wins.closeAll(); n != 2 {
		t.Fatalf("收编数 = %d, want 2", n)
	}
	for _, c := range []struct {
		hd   *winHandle
		what string
	}{{hd, "会话历史窗"}, {hs, "设置窗"}} {
		deadline := time.Now().Add(10 * time.Second)
		for !c.hd.doneClosed() && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if !c.hd.doneClosed() {
			t.Fatalf("收编后%s未在时限内销毁（WM_CLOSE → DestroyEvent 路径不成立）", c.what)
		}
	}
	if u.wins.isOpen(winHistory) || u.wins.isOpen(winSettings) {
		t.Fatal("销毁后登记应已摘除")
	}
}
