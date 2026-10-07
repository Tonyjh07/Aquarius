//go:build windows

package main

import (
	"slices"
	"testing"
)

// swapFakeConsole 换入假 Win32 面并登记调用序列；随测试恢复函数变量与脱离标记
// （e2e 测试会真调 run() → hideSpawnedConsole，不能残留假态）。
func swapFakeConsole(t *testing.T, count int, hwnd uintptr) *[]string {
	t.Helper()
	origCount, origWin := consoleProcCount, consoleWindow
	origDetach, origAttach, origAlloc, origReopen := consoleDetach, consoleAttach, consoleAlloc, consoleReopenStd
	var calls []string
	consoleProcCount = func() int { return count }
	consoleWindow = func() uintptr { return hwnd }
	consoleDetach = func() bool { calls = append(calls, "detach"); return true }
	consoleAttach = func() bool { calls = append(calls, "attach"); return true }
	consoleAlloc = func() bool { calls = append(calls, "alloc"); return true }
	consoleReopenStd = func() { calls = append(calls, "reopen") }
	t.Cleanup(func() {
		consoleProcCount, consoleWindow = origCount, origWin
		consoleDetach, consoleAttach, consoleAlloc, consoleReopenStd = origDetach, origAttach, origAlloc, origReopen
		consoleDetached = false
	})
	return &calls
}

// TestHideSpawnedConsole 钉住 D108/D114 判定口径：仅「唯一挂载者 + 有控制台」才脱离。
func TestHideSpawnedConsole(t *testing.T) {
	tests := []struct {
		name        string
		count       int
		hwnd        uintptr
		preDetached bool
		wantDetach  bool
		wantCalls   []string
	}{
		{"终端启动：shell 同挂载（≥2）不脱离", 3, 0x1234, false, false, nil},
		{"无控制台：no-op", 0, 0, false, false, nil},
		{"自建控制台：脱离（双击，D114 = FreeConsole）", 1, 0x1234, false, true, []string{"detach"}},
		{"已脱离：重复调用 no-op", 1, 0x1234, true, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := swapFakeConsole(t, tt.count, tt.hwnd)
			consoleDetached = tt.preDetached
			if got := hideSpawnedConsole(); got != tt.wantDetach {
				t.Errorf("hideSpawnedConsole() = %v, want %v", got, tt.wantDetach)
			}
			if !slices.Equal(*calls, tt.wantCalls) {
				t.Errorf("console ops = %v, want %v", *calls, tt.wantCalls)
			}
		})
	}
}

// TestRestoreConsole 恢复语义（D114）：未脱离 no-op；脱离后先 AttachConsole(parent)，
// 失败 AllocConsole 兜底，重挂标准流，重复恢复 no-op。
func TestRestoreConsole(t *testing.T) {
	t.Run("未脱离：no-op", func(t *testing.T) {
		calls := swapFakeConsole(t, 1, 0x1234)
		consoleDetached = false
		if restoreConsole() {
			t.Error("未脱离不应执行恢复")
		}
		if len(*calls) != 0 {
			t.Errorf("console ops = %v, want none", *calls)
		}
	})
	t.Run("脱离后挂回父控制台并重挂流", func(t *testing.T) {
		calls := swapFakeConsole(t, 1, 0x1234)
		consoleDetached = true
		if !restoreConsole() {
			t.Fatal("脱离态应执行恢复")
		}
		if !slices.Equal(*calls, []string{"attach", "reopen"}) {
			t.Errorf("console ops = %v, want [attach reopen]", *calls)
		}
		if consoleDetached {
			t.Error("consoleDetached 应已清除")
		}
		restoreConsole() // 幂等
		if !slices.Equal(*calls, []string{"attach", "reopen"}) {
			t.Errorf("重复恢复应 no-op, ops = %v", *calls)
		}
	})
	t.Run("脱离后挂回失败 AllocConsole 兜底", func(t *testing.T) {
		calls := swapFakeConsole(t, 1, 0x1234)
		consoleAttach = func() bool { *calls = append(*calls, "attach"); return false }
		consoleDetached = true
		if !restoreConsole() {
			t.Fatal("脱离态应执行恢复")
		}
		if !slices.Equal(*calls, []string{"attach", "alloc", "reopen"}) {
			t.Errorf("console ops = %v, want [attach alloc reopen]", *calls)
		}
	})
}
