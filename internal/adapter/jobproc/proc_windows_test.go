//go:build windows

package jobproc

import (
	"os/exec"
	"testing"
)

// TestSetupProcHidesConsoleWindow D118 复现（D114 连带回归）：双击启动 FreeConsole 后
// 父进程无控制台可继承，Windows 会为每个作业子进程新建**可见控制台窗**（每起一个弹一个）
// ——而作业 I/O 全程写日志文件/缓冲、stdin 置空，窗口纯属噪音。spawn 前须压
// CREATE_NO_WINDOW；**只挂这一旗**：job 跑的是用户命令，GUI 子进程（notepad 等）要照常可见。
func TestSetupProcHidesConsoleWindow(t *testing.T) {
	t.Run("cmd /c 形态：原始命令行与压制共存", func(t *testing.T) {
		cmd := exec.Command("cmd", "/c", "echo hi")
		setupProc(cmd)
		if cmd.SysProcAttr == nil {
			t.Fatal("windows 分支应挂 SysProcAttr（D118）")
		}
		if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 { // CREATE_NO_WINDOW
			t.Errorf("CreationFlags = %#x, want 携带 CREATE_NO_WINDOW(0x8000000)",
				cmd.SysProcAttr.CreationFlags)
		}
		if cmd.SysProcAttr.CmdLine == "" {
			t.Fatal("CmdLine 不应丢（含引号命令的原始命令行直传，既有行为）")
		}
	})
	t.Run("直连可执行：同样压制", func(t *testing.T) {
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "1")
		setupProc(cmd)
		if cmd.SysProcAttr == nil {
			t.Fatal("直连形态也应挂 SysProcAttr（D118）")
		}
		if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 { // CREATE_NO_WINDOW
			t.Errorf("CreationFlags = %#x, want 携带 CREATE_NO_WINDOW(0x8000000)",
				cmd.SysProcAttr.CreationFlags)
		}
		if cmd.SysProcAttr.CmdLine != "" {
			t.Errorf("非 cmd /c 形态不应设 CmdLine（回落默认 argv 拼装）：%q", cmd.SysProcAttr.CmdLine)
		}
	})
}
