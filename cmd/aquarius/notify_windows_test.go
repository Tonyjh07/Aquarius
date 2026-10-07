//go:build windows

package main

import (
	"os/exec"
	"testing"
)

// TestNotifyDetachedChildHidden 通知子进程不得外露窗口（D117，roadmap「已知缺陷」复现）：
// Windows 通知 = 拉起 powershell 跑气泡脚本（子进程存活 ≈6s）；D114 双击启动 FreeConsole
// 后父进程无控制台可继承 → Windows 为该子进程**新建可见控制台**，直到气泡结束才消失
// （D114 前继承的是已隐藏的自建控制台，不可见）。分离启动前必须挂
// HideWindow + CREATE_NO_WINDOW。
func TestNotifyDetachedChildHidden(t *testing.T) {
	var got *exec.Cmd
	send := notifyWith("windows",
		func(c *exec.Cmd) error { got = c; return nil },
		func(*exec.Cmd) error { t.Fatal("windows 分支不应同步执行"); return nil })
	if err := send("Aquarius", "你好"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got == nil {
		t.Fatal("分离启动未收到命令")
	}
	if got.SysProcAttr == nil {
		t.Fatal("windows 分离启动应挂 SysProcAttr（D117：子进程窗口不外露）")
	}
	if !got.SysProcAttr.HideWindow {
		t.Error("HideWindow = false, want true")
	}
	if got.SysProcAttr.CreationFlags&0x08000000 == 0 { // CREATE_NO_WINDOW
		t.Errorf("CreationFlags = %#x, want 携带 CREATE_NO_WINDOW(0x8000000)",
			got.SysProcAttr.CreationFlags)
	}
}
