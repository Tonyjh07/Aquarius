//go:build windows

package uigui

import "testing"

// TestTopMostHandle HWND_TOPMOST(-1)/HWND_NOTOPMOST(-2) 的补码取值
// （置顶开关与 overlay 同步共用，§15.1）。
func TestTopMostHandle(t *testing.T) {
	if got := topMostHandle(true); got != ^uintptr(0) {
		t.Fatalf("topMostHandle(true) = %d, want -1", int64(got))
	}
	if got := topMostHandle(false); got != ^uintptr(1) {
		t.Fatalf("topMostHandle(false) = %d, want -2", int64(got))
	}
}

// TestTopMostQueryDefault 无句柄（headless/窗口未就绪）= 缺省置顶。
func TestTopMostQueryDefault(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	if !topMostQuery() {
		t.Fatal("hwnd=0 时应缺省置顶")
	}
}
