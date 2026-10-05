//go:build windows

package uigui

import (
	"image"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

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

// TestMenuDispatchBubble D92/D97/D98 气泡菜单分发：重新生成 = /regen（与键入同路径）、
// 复制 = copyMsg 记账 → 帧内落剪贴板、编辑三方式 = editMsg（mode 随项而设）进编辑态
// （预填原文）、引用 = quoteMsg 追加引用块；项集 user/assistant 同集六项（呈现契约，
// 测试锁定）。
func TestMenuDispatchBubble(t *testing.T) {
	u := newHeadless(t, Options{})

	// 重新生成：/regen <id> 进 inCh（与键入同路径）。
	menuDispatchBubble(u, &bubbleMenuCtx{id: "a1", kind: blockAssistant}, cmdBubbleRegen)
	drainSync(t, u)
	select {
	case in := <-u.inCh:
		if in.Command == nil || in.Command.Name != "regen" || len(in.Command.Args) != 1 || in.Command.Args[0] != "a1" {
			t.Fatalf("inCh 输入 = %+v, want /regen a1", in)
		}
	default:
		t.Fatal("inCh 未收到 /regen")
	}

	// 复制：copyMsg 记账 pendingCopy → flushCopy（Gio 帧）写系统剪贴板。
	menuDispatchBubble(u, &bubbleMenuCtx{id: "a1", kind: blockAssistant, copy: "回答文本"}, cmdBubbleCopy)
	drainSync(t, u)
	if u.pendingCopy != "回答文本" {
		t.Fatalf("pendingCopy = %q, want 已记账", u.pendingCopy)
	}
	q := new(input.Router)
	gtx, ops := frameGtx(q.Source())
	u.flushCopy(gtx)
	q.Frame(ops)
	_, content, ok := q.WriteClipboard()
	if !ok || string(content) != "回答文本" {
		t.Fatalf("剪贴板 = %q ok=%v, want 回答文本", content, ok)
	}

	// 编辑三方式（D97）：editMsg mode 随项而设，进编辑态（预填原文 + 焦点待入）。
	for _, tc := range []struct {
		cmd  uintptr
		mode conversation.KeepMode
	}{{cmdBubbleEdit, conversation.Fresh}, {cmdBubbleEditKeep, conversation.Carry}, {cmdBubbleEditCopy, conversation.Clone}} {
		menuDispatchBubble(u, &bubbleMenuCtx{id: "u1", kind: blockUser, edit: "问"}, tc.cmd)
		drainSync(t, u)
		if u.m.editTarget != "u1" || u.editor.Text() != "问" || u.m.editMode != tc.mode {
			t.Fatalf("编辑态(cmd %d) = target %q editor %q mode %v, want u1/问/%v",
				tc.cmd, u.m.editTarget, u.editor.Text(), u.m.editMode, tc.mode)
		}
	}

	// 引用（D98）：quoteMsg → 输入框追加单行引用前缀（不进编辑态）。先清上一段编辑态残留。
	u.editor.SetText("")
	u.m.editTarget = ""
	menuDispatchBubble(u, &bubbleMenuCtx{id: "a1", kind: blockAssistant, copy: "一\n二"}, cmdBubbleQuote)
	drainSync(t, u)
	if u.m.editTarget != "" {
		t.Fatalf("引用不应进编辑态: target=%q", u.m.editTarget)
	}
	if got := u.editor.Text(); got != "> 一 二" {
		t.Fatalf("编辑框 = %q, want > 一 二（换行压平）", got)
	}

	// 项集 user/assistant 同集六项（D97 三方式 + D98 引用；kind 参数留 thinking/chip 差异化）。
	want := []struct {
		id    uintptr
		label string
	}{
		{cmdBubbleEdit, "编辑"},
		{cmdBubbleEditKeep, "编辑并转移历史"},
		{cmdBubbleEditCopy, "编辑并复制历史"},
		{cmdBubbleRegen, "重新生成"},
		{cmdBubbleCopy, "复制"},
		{cmdBubbleQuote, "引用"},
	}
	for _, kind := range []blockKind{blockUser, blockAssistant} {
		items := bubbleMenuItems(kind)
		if len(items) != len(want) {
			t.Fatalf("%v 菜单项数 = %d, want %d", kind, len(items), len(want))
		}
		for i, w := range want {
			if items[i].id != w.id || items[i].label != w.label {
				t.Fatalf("%v 菜单项[%d] = {%d %q}, want {%d %q}", kind, i, items[i].id, items[i].label, w.id, w.label)
			}
		}
	}
}

// TestShowMainRepresentsAfterReveal D88 呼出补提交：隐藏期间 presentable=false
// 不提交 ULW，重显瞬间 DWM 可能露 Gio 不透明表面——showMain 揭示后应立即以最后
// 一次合成位图补提交一次（与揭示帧补提交同一不变量）。
func TestShowMainRepresentsAfterReveal(t *testing.T) {
	if mainHWND != 0 {
		t.Skip("测试进程内已有主窗句柄")
	}
	oldReveal := revealMain
	revealMain = func(uintptr) {}
	defer func() { revealMain = oldReveal }()
	calls := 0
	presentMain = func(x, y, w, h int32, bits []byte, alpha byte) bool {
		calls++
		return true
	}
	defer func() { presentMain = mainPresent }()

	mainHWND = 0x1234 // windowRectPx 拿不到真矩形 → 回退 u.x/u.y（同 TestFadePresentSubmitsBitmap 口径）
	defer func() { mainHWND = 0 }()

	u := newFrameUI()
	u.inbox = make(chan uiMsg, 8)
	u.done = make(chan struct{})
	u.frameSize = image.Pt(100, 80)
	u.fadeBuf = make([]byte, 100*80*4)

	u.showMain()
	if calls != 1 {
		t.Fatalf("呼出揭示后应补提交一次, got %d", calls)
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
