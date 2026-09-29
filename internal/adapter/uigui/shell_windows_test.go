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

// TestLogoMenuItems D72 logo 右键菜单六项（项序/文案固定）：新对话/消息历史/设置/
// 置顶/隐藏悬浮球/分隔/退出；置顶项勾选随 topMostQuery（无句柄缺省置顶 = 勾选）。
func TestLogoMenuItems(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	items := logoMenuItems()
	want := []struct {
		id    uintptr
		label string
	}{
		{cmdNew, "新对话"},
		{cmdHistory, "消息历史"},
		{cmdSettings, "设置"},
		{cmdTopMost, "置顶"},
		{cmdToggle, "隐藏悬浮球"},
		{0, ""},
		{cmdExit, "退出"},
	}
	if len(items) != len(want) {
		t.Fatalf("项数 = %d, want %d（items=%+v）", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i].id != w.id || items[i].label != w.label {
			t.Fatalf("items[%d] = {id:%d label:%q}, want {id:%d label:%q}",
				i, items[i].id, items[i].label, w.id, w.label)
		}
	}
	if !items[3].checked {
		t.Fatal("置顶项应勾选（无句柄 topMostQuery 缺省置顶）")
	}
}

// TestMenuDispatchNew D72 新对话 = 注入 /new（与键入同路径：parseInput → inCh）。
func TestMenuDispatchNew(t *testing.T) {
	u := newHeadless(t, Options{})
	menuDispatch(u, cmdNew)
	drainSync(t, u)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "new" || len(in.Command.Args) != 0 {
			t.Fatalf("inCh 输入 = %+v, want /new 命令", in)
		}
	default:
		t.Fatal("inCh 未收到 /new")
	}
}
