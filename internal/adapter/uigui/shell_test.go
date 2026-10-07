package uigui

// 外壳与菜单 headless 测试（S4b 起中性：菜单项集/分发与显隐策略与平台无关；平台能力经
// 假平台注入，见 fakeplat_test.go）。原生实现自身的测试随面迁入 platform 包。

import (
	"image"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// TestLogoMenuItems D73 logo 菜单契约（项序/文案固定）：新对话 / 权限（二级菜单 =
// settings.go permLevels 四档、当前档打勾）/ 消息历史 / 设置 / 置顶（勾选）/ 隐藏 /
// 分隔 / 退出；档位空（Status 未就绪）则四档均不勾。
func TestLogoMenuItems(t *testing.T) {
	items := logoMenuItems("strict", true)
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
		t.Fatal("置顶项应勾选（平台 TopMost 传入为真）")
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
	for _, it := range logoMenuItems("", true) {
		for _, s := range it.sub {
			if s.checked {
				t.Fatalf("空档位时 %q 不应勾选", s.label)
			}
		}
	}
}

// TestMenuItemsViaHost 菜单项集经宿主回调现取（平台呈现前调用；S4b 口径：平台只拿
// 中性 DTO，值随出口处数据源变化——顶置勾选态来自平台查询）。
func TestMenuItemsViaHost(t *testing.T) {
	u := newFrameUI()
	fp := fakeOf(u)
	fp.TopNow = false
	items := u.menuItems(platform.MenuTray)
	if len(items) == 0 {
		t.Fatal("托盘项集不应为空")
	}
	for _, it := range items {
		if it.ID == cmdTopMost && it.Checked {
			t.Fatal("平台 TopMost=false 时置顶项不应勾选")
		}
	}
	// 气泡菜单无上下文（原子槽空）= 空项集（防平台呈现出误导菜单）。
	if got := u.menuItems(platform.MenuBubble); len(got) != 0 {
		t.Fatalf("无气泡上下文时项集应为空, got %+v", got)
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

// TestMenuDispatchBubble D92/D97/D98/D99 气泡菜单分发：重新生成 = /regen（与键入同
// 路径）、复制 = copyMsg 记账 → 帧内落剪贴板、编辑三方式 = editMsg（mode 随项而设）
// 进编辑态（预填原文）、引用 = quoteMsg 追加引用前缀、查看原文 = rawView 原子槽 +
// 次窗；项集 user/assistant 同集七项（呈现契约，测试锁定）。
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

	// 查看原文（D99/D100）：ctx.raw（bubbleCtx 按块角色组装）入原子槽 + 开窗
	// （headless spawn=nil = no-op 不 panic）。
	menuDispatchBubble(u, &bubbleMenuCtx{
		id: "a1b2c3d4e5f6", kind: blockAssistant, edit: "**原始** markdown",
		raw: rawContent{title: "原文 · a1b2c3d4", text: "**原始** markdown"},
	}, cmdBubbleRaw)
	drainSync(t, u)
	c := u.rawView.Load()
	if c == nil || c.title != "原文 · a1b2c3d4" || c.text != "**原始** markdown" {
		t.Fatalf("rawView = %+v, want 标题含 8 字符前缀 + 原文", c)
	}

	// 项集 user/assistant 同集七项（D97 三方式 + D98 引用 + D99 查看原文；kind 参数
	// 留 thinking/chip 差异化）。
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
		{cmdBubbleRaw, "查看原文"},
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
	u := newFrameUI()
	fp := fakeOf(u)
	fp.Main = 0x1234 // 有主窗句柄（呼出门）；矩形查不到 → 回退 u.x/u.y
	fp.VisibleNow = true

	u.inbox = make(chan uiMsg, 8)
	u.done = make(chan struct{})
	u.frameSize = image.Pt(100, 80)
	u.fadeBuf = make([]byte, 100*80*4)

	u.showMain()
	if fp.Reveals != 1 {
		t.Fatalf("呼出应揭示一次, got %d", fp.Reveals)
	}
	if fp.Presents != 1 {
		t.Fatalf("呼出揭示后应补提交一次, got %d", fp.Presents)
	}
}

// TestShowMainGateBeforeFirstFrame D78 呼出门：首帧 ULW 就绪前（revealPending）托盘/
// 快捷键呼出只投展开消息、**不显示窗口**（位图未提交 → 防闪现未定制窗口）；就绪后呼出
// 正常揭示（显示 + 激活前台）。揭示经假平台记账、假句柄不落真 Win32。
func TestShowMainGateBeforeFirstFrame(t *testing.T) {
	u := newFrameUI()
	fp := fakeOf(u)
	fp.Main = 0x1234              // 非 0 过 h 门（headless 无真窗；揭示经假平台记账）
	u.inbox = make(chan uiMsg, 8) // post 落点（无事件循环，缓冲可见）
	u.done = make(chan struct{})

	u.revealPending.Store(true)
	u.showMain()
	if fp.Reveals != 0 {
		t.Fatalf("首帧前呼出不应显示窗口, reveals=%d", fp.Reveals)
	}
	if n := len(u.inbox); n != 1 {
		t.Fatalf("呼出应仍投一条展开消息（只置展开态）, got %d", n)
	}

	u.revealPending.Store(false)
	u.showMain()
	if fp.Reveals != 1 {
		t.Fatalf("就绪后呼出应显示一次, got %d", fp.Reveals)
	}
	if n := len(u.inbox); n != 2 {
		t.Fatalf("展开消息照投, got %d", n)
	}
}

// TestToggleWindowAndHotkey 显隐策略（§15.1，S4b 中性化）：托盘左键切换显隐——可见 →
// 平台隐藏；隐藏 → 呼出（揭示 + 展开）；快捷键在可见时投展开↔收起互切。
func TestToggleWindowAndHotkey(t *testing.T) {
	u := newFrameUI()
	fp := fakeOf(u)
	fp.Main = 0x1234
	u.inbox = make(chan uiMsg, 8)
	u.done = make(chan struct{})

	fp.VisibleNow = true
	u.toggleWindow()
	if fp.Hides != 1 {
		t.Fatalf("可见时托盘切换应隐藏窗口, hides=%d", fp.Hides)
	}
	fp.VisibleNow = false
	u.toggleWindow()
	if fp.Reveals != 1 {
		t.Fatalf("隐藏时托盘切换应呼出（揭示）, reveals=%d", fp.Reveals)
	}

	// 快捷键：可见 → 投 toggleExpandMsg（展开↔收起互切）。
	u.inbox = make(chan uiMsg, 8)
	fp.VisibleNow = true
	u.hotkeyToggle()
	if n := len(u.inbox); n != 1 {
		t.Fatalf("可见态快捷键应投一条互切消息, got %d", n)
	}
	if _, ok := <-u.inbox; !ok {
		t.Fatal("互切消息未入队")
	}
	fp.VisibleNow = false
	u.hotkeyToggle()
	if fp.Reveals != 2 {
		t.Fatalf("隐藏态快捷键应呼出, reveals=%d", fp.Reveals)
	}
}

// TestExitViaShell 退出策略（§15.1/D64）：注销热键 + 清托盘 + 隐藏主窗 + 中断进行中轮次
// + EOF 收尾（装配根退出）；平台侧动作经假平台记账。
func TestExitViaShell(t *testing.T) {
	u := newHeadless(t, Options{}) // 需 eofCh/事件循环就绪（EOF 收尾是退出路径的一半）
	fp := fakeOf(u)
	fp.Main = 0x1234
	interrupted := false
	u.SetInterrupt(func() { interrupted = true })

	u.exitViaShell()
	if fp.ShellStops != 1 {
		t.Fatalf("退出应注销外壳（热键+托盘）, shellStops=%d", fp.ShellStops)
	}
	if fp.Hides != 1 {
		t.Fatalf("退出应隐藏主窗, hides=%d", fp.Hides)
	}
	if !interrupted {
		t.Fatal("退出应中断进行中轮次（D64）")
	}
	select {
	case <-u.eofCh:
	default:
		t.Fatal("退出应走 EOF 收尾（装配根 Next → io.EOF）")
	}
}

// TestToggleTopMost 置顶开关（§15.1/D72）：翻转主窗置顶态 + 位置记忆落盘（含停靠边）。
func TestToggleTopMost(t *testing.T) {
	dir := t.TempDir()
	posFile := dir + "/gui_pos.json"
	u := newFrameUI()
	fp := fakeOf(u)
	u.opts.PosFile = posFile
	fp.TopNow = true
	fp.Rect = platform.Rect{Left: 10, Top: 20, Right: 110, Bottom: 120}
	fp.RectOK = true

	u.toggleTopMost()
	if fp.TopNow {
		t.Fatal("置顶开关应翻转为 false")
	}
	p, ok := loadPos(posFile)
	if !ok || p.X != 10 || p.Y != 20 || p.TopMost == nil || *p.TopMost {
		t.Fatalf("位置记忆 = %+v ok=%v, want X=10 Y=20 TopMost=false", p, ok)
	}
}
