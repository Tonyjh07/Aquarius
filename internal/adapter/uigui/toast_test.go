package uigui

// toast_test.go 轻提示组件（toast.go）单元测试：显示/到期可见性判定（零依赖、
// 不涉窗）。到期的**擦除**由渲染帧的 op.InvalidateCmd{At} 驱动（见 toast.go 注释），
// 故这里只钉住帧态的可见性口径——那正是渲染分支的判据。

import (
	"testing"
	"time"
)

// TestToastAutoExpires 轻提示到期自动失效（不依赖下一次用户输入）：到期后 active=false。
func TestToastAutoExpires(t *testing.T) {
	var ts toastState
	now := time.Now()
	if ts.active(now) {
		t.Fatal("零值不应 active")
	}
	ts.show("已复制会话 ID", now)
	if !ts.active(now) {
		t.Fatal("刚显示应 active")
	}
	if ts.active(now.Add(toastLifetime)) {
		t.Fatal("到期应失效")
	}
	if ts.active(now.Add(toastLifetime + time.Millisecond)) {
		t.Fatal("过期后仍应失效")
	}
}

// TestToastEmptyTextIgnored show 空文案 = 不显示（防止误触发时闪出空卡）。
func TestToastEmptyTextIgnored(t *testing.T) {
	var ts toastState
	ts.show("", time.Now())
	if ts.active(time.Now()) {
		t.Fatal("空文案不应显示")
	}
}
