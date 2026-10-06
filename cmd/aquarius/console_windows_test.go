//go:build windows

package main

import (
	"slices"
	"testing"
)

// swapFakeConsole 换入假 Win32 面并登记 ShowWindow 调用；随测试恢复函数变量
// 与隐藏标记（e2e 测试会真调 run() → hideSpawnedConsole，不能残留假态）。
func swapFakeConsole(t *testing.T, count int, hwnd uintptr) *[]string {
	t.Helper()
	origCount, origWin, origShow := consoleProcCount, consoleWindow, consoleShow
	var calls []string
	consoleProcCount = func() int { return count }
	consoleWindow = func() uintptr { return hwnd }
	consoleShow = func(h uintptr, cmd int) {
		if h != hwnd {
			t.Errorf("ShowWindow hwnd = %#v, want %#v", h, hwnd)
		}
		switch cmd {
		case swHide:
			calls = append(calls, "hide")
		case swShow:
			calls = append(calls, "show")
		}
	}
	t.Cleanup(func() {
		consoleProcCount, consoleWindow, consoleShow = origCount, origWin, origShow
		consoleHidden = false
	})
	return &calls
}

// TestHideSpawnedConsole 钉住 D108 判定口径：仅「唯一挂载者 + 有控制台」才隐藏。
func TestHideSpawnedConsole(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		hwnd       uintptr
		preHidden  bool
		wantHidden bool
		wantCalls  []string
	}{
		{"终端启动：shell 同挂载（≥2）不隐藏", 3, 0x1234, false, false, nil},
		{"无控制台：no-op", 0, 0, false, false, nil},
		{"自建控制台：隐藏（双击）", 1, 0x1234, false, true, []string{"hide"}},
		{"已隐藏：重复调用 no-op", 1, 0x1234, true, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := swapFakeConsole(t, tt.count, tt.hwnd)
			consoleHidden = tt.preHidden
			if got := hideSpawnedConsole(); got != tt.wantHidden {
				t.Errorf("hideSpawnedConsole() = %v, want %v", got, tt.wantHidden)
			}
			if !slices.Equal(*calls, tt.wantCalls) {
				t.Errorf("ShowWindow calls = %v, want %v", *calls, tt.wantCalls)
			}
		})
	}
}

// TestRestoreConsole 恢复语义：非隐藏态 no-op；隐藏态恢复（SW_SHOW）并清标记，
// 重复恢复 no-op。
func TestRestoreConsole(t *testing.T) {
	t.Run("未隐藏：no-op", func(t *testing.T) {
		calls := swapFakeConsole(t, 1, 0x1234)
		consoleHidden = false
		restoreConsole()
		if len(*calls) != 0 {
			t.Errorf("ShowWindow calls = %v, want none", *calls)
		}
	})
	t.Run("隐藏后恢复并清标记", func(t *testing.T) {
		calls := swapFakeConsole(t, 1, 0x1234)
		consoleHidden = true
		restoreConsole()
		restoreConsole() // 幂等
		if !slices.Equal(*calls, []string{"show"}) {
			t.Errorf("ShowWindow calls = %v, want [show]", *calls)
		}
		if consoleHidden {
			t.Error("consoleHidden 应已清除")
		}
	})
}
