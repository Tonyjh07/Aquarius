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

// TestLogoMenuItems D73 logo 菜单契约（项序/文案固定）：新对话 / 权限（二级菜单 =
// settings.go permLevels 四档、当前档打勾）/ 消息历史 / 设置 / 置顶（勾选）/ 隐藏 /
// 分隔 / 退出；档位空（Status 未就绪）则四档均不勾。
func TestLogoMenuItems(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	items := logoMenuItems("strict")
	want := []struct {
		id    uintptr
		label string
	}{
		{cmdNew, "新对话"},
		{0, "权限"},
		{cmdHistory, "消息历史"},
		{cmdSettings, "设置"},
		{cmdTopMost, "置顶"},
		{cmdToggle, "隐藏"},
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
	if !items[4].checked {
		t.Fatal("置顶项应勾选（无句柄 topMostQuery 缺省置顶）")
	}
	// 权限二级菜单：四档 id = cmdPermBase+i、文案同 permLevels，当前档打勾。
	sub := items[1].sub
	if len(sub) != len(permLevels) {
		t.Fatalf("权限子项数 = %d, want %d", len(sub), len(permLevels))
	}
	for i, lv := range permLevels {
		if sub[i].id != cmdPermBase+uintptr(i) || sub[i].label != lv {
			t.Fatalf("sub[%d] = {id:%d label:%q}, want {id:%d label:%q}",
				i, sub[i].id, sub[i].label, cmdPermBase+uintptr(i), lv)
		}
		if want := lv == "strict"; sub[i].checked != want {
			t.Fatalf("sub[%d](%s) 勾选 = %v, want %v", i, lv, sub[i].checked, want)
		}
	}
	for _, it := range logoMenuItems("") {
		for _, s := range it.sub {
			if s.checked {
				t.Fatalf("空档位时 %q 不应勾选", s.label)
			}
		}
	}
}

// TestMenuDispatchPerm D73 权限子菜单选中 = 注入 /permission <档>（与键入同路径，
// D22 写回 + 热切换走内核单一口径）。
func TestMenuDispatchPerm(t *testing.T) {
	u := newHeadless(t, Options{})
	menuDispatch(u, cmdPermBase+2) // permLevels[2] = permissive
	drainSync(t, u)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "permission" ||
			len(in.Command.Args) != 1 || in.Command.Args[0] != "permissive" {
			t.Fatalf("inCh 输入 = %+v, want /permission permissive", in)
		}
	default:
		t.Fatal("inCh 未收到 /permission")
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

// TestShowMainGateBeforeFirstFrame D78 呼出门：首帧 ULW 就绪前（revealPending）托盘/
// 快捷键呼出只投展开消息、**不显示窗口**（位图未提交 → 防闪现未定制窗口）；就绪后呼出
// 正常揭示（显示 + 激活前台）。揭示经 revealMain 槽注入、假句柄不落真 Win32。
func TestShowMainGateBeforeFirstFrame(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	oldReveal := revealMain
	defer func() { revealMain = oldReveal }()
	reveals := 0
	revealMain = func(uintptr) { reveals++ }

	mainHWND = 0x1234 // 非 0 过 h 门（headless 无真窗；揭示经槽注入不落真 ShowWindow）
	defer func() { mainHWND = 0 }()

	u := newFrameUI()
	u.inbox = make(chan uiMsg, 8) // post 落点（无事件循环，缓冲可见）
	u.done = make(chan struct{})

	u.revealPending.Store(true)
	u.showMain()
	if reveals != 0 {
		t.Fatalf("首帧前呼出不应显示窗口, reveals=%d", reveals)
	}
	if n := len(u.inbox); n != 1 {
		t.Fatalf("呼出应仍投一条展开消息（只置展开态）, got %d", n)
	}

	u.revealPending.Store(false)
	u.showMain()
	if reveals != 1 {
		t.Fatalf("就绪后呼出应显示一次, got %d", reveals)
	}
	if n := len(u.inbox); n != 2 {
		t.Fatalf("展开消息照投, got %d", n)
	}
}
