package uigui

// cmd_test.go D265 命令折叠条：命令回显与命令返回都是运行输出——不进会话树，也不
// 冒充用户气泡；连续的命令合成一条淡灰圆角条，默认只留一行「折叠 N 条命令 · 点击展开」。

import (
	"strings"
	"testing"

	"gioui.org/io/input"
)

// TestCmdHeaderText 折叠行文案：条数 + 动作提示，展开态换动作词。
func TestCmdHeaderText(t *testing.T) {
	cases := []struct {
		n    int
		open bool
		want string
	}{
		{1, false, "折叠 1 条命令 · 点击展开"},
		{3, false, "折叠 3 条命令 · 点击展开"},
		{3, true, "折叠 3 条命令 · 点击收起"},
	}
	for _, c := range cases {
		if got := cmdHeaderText(c.n, c.open); got != c.want {
			t.Fatalf("cmdHeaderText(%d, %v) = %q, want %q", c.n, c.open, got, c.want)
		}
	}
}

// TestCommandEchoFolds 连续命令合成一条：命令行回显与随后的返回文本归进同一条目；
// 第二条命令追加到同一条折叠条，而不是各占一行。
func TestCommandEchoFolds(t *testing.T) {
	m, _ := newTestModel(t)

	m.submit("/help")
	if len(m.blocks) != 1 || m.blocks[0].kind != blockCommand {
		t.Fatalf("blocks = %+v, want 1 个命令块（命令行不再进用户气泡）", m.blocks)
	}
	m.say("帮助正文")
	if got := m.blocks[0].cmds; len(got) != 1 || got[0].cmd != "/help" || got[0].out != "帮助正文" {
		t.Fatalf("cmds = %+v, want 一条 /help + 输出", got)
	}

	m.submit("/list")
	m.say("会话列表")
	if len(m.blocks) != 1 {
		t.Fatalf("连续命令应共用一条折叠条，blocks = %+v", m.blocks)
	}
	if got := m.blocks[0].cmds; len(got) != 2 || got[1].cmd != "/list" || got[1].out != "会话列表" {
		t.Fatalf("cmds = %+v, want 两条", got)
	}
}

// TestCommandRunBrokenByChat 中间的发言/回答把命令条隔断：再来的命令另起一条——
// 「连续」以转写流里的相邻为准，跨过内容的合并会把不同时刻的操作揉在一起。
func TestCommandRunBrokenByChat(t *testing.T) {
	m, _ := newTestModel(t)

	m.submit("/regen x")
	m.say("已重新生成")
	m.submit("你好")
	m.submit("/list")

	if len(m.blocks) != 3 {
		t.Fatalf("blocks = %+v, want 命令条 + 用户气泡 + 命令条", m.blocks)
	}
	if m.blocks[1].kind != blockUser || m.blocks[2].kind != blockCommand {
		t.Fatalf("块序 = [%v %v %v]", m.blocks[0].kind, m.blocks[1].kind, m.blocks[2].kind)
	}
}

// TestCommandOutAfterClear 清屏类命令（/switch、/regen）的结果晚于清屏到达：末尾已经
// 没有它的命令条了，按提交时记下的命令行另起一条——否则这句话会以纯文本行的样子
// 落在一轮回放/回答后面（正是要治的那种噪声）。
func TestCommandOutAfterClear(t *testing.T) {
	m, _ := newTestModel(t)

	m.submit("/switch 01K")
	m.clear() // 内核先清屏回放，结果随后才回
	m.add(blockAssistant, "回放出来的回答")
	m.say("已切换到会话 01K")

	last := m.blocks[len(m.blocks)-1]
	if last.kind != blockCommand {
		t.Fatalf("末块 = %v, want 命令条（迟到的结果归进折叠条）", last.kind)
	}
	if len(last.cmds) != 1 || last.cmds[0].cmd != "/switch 01K" || last.cmds[0].out != "已切换到会话 01K" {
		t.Fatalf("cmds = %+v", last.cmds)
	}

	// 启动提示这类没有命令可归的输出，仍走纯文本行（不能塞进凭空造的命令条）。
	m.clear()
	m.say("Aquarius — 输入 /help 查看命令")
	if last := m.blocks[len(m.blocks)-1]; last.kind != blockPlain {
		t.Fatalf("无归属的输出应落成纯文本行，实际 %v", last.kind)
	}
}

// TestCommandStripCollapse 折叠条默认只渲染一行（一条窄的深色薄条），点开才铺命令行与
// 输出；再点收起。开合是视图态，不改变块。
func TestCommandStripCollapse(t *testing.T) {
	u := newFrameUI()
	u.m.addCommand("/help")
	u.m.attachCommandOut("命令列表正文")
	u.m.addCommand("/jobs")
	u.m.attachCommandOut("3 条任务")

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)

	if u.cmdIsOpen(0) {
		t.Fatal("命令条应默认折叠")
	}
	if len(u.cmdClicks) != 1 {
		t.Fatalf("折叠行点击件 = %d, want 1（整条可点）", len(u.cmdClicks))
	}
	if got := u.blockText(0, 4); got != "" {
		t.Fatalf("折叠时正文不应铺开（行选键无文本）: %q", got)
	}
	collapsedH := u.contentH
	if len(u.shapes) != 1 {
		t.Fatalf("shapes = %d, want 1（单条淡灰底）", len(u.shapes))
	}

	// 展开：命令行与输出都铺开、可选中复制，内容变高。
	u.cmdClicks[0].Click()
	u.updateClicks(gtx)
	if !u.cmdIsOpen(0) {
		t.Fatal("点击折叠条应展开")
	}
	u.transcript(gtx, 600, 400)
	if u.contentH <= collapsedH {
		t.Fatalf("展开后内容高 = %d, 折叠时 = %d（应更高）", u.contentH, collapsedH)
	}
	got := u.blockText(0, 4)
	for _, want := range []string{"/help", "命令列表正文", "/jobs", "3 条任务"} {
		if !strings.Contains(got, want) {
			t.Fatalf("展开后缺 %q:\n%s", want, got)
		}
	}

	// 再点一次收起。
	u.cmdClicks[0].Click()
	u.updateClicks(gtx)
	if u.cmdIsOpen(0) {
		t.Fatal("再点一次应收起")
	}
}

// TestCommandStripWidthFollowsState 折叠条宽度随开合变（D266）：收起时是贴合文案的
// 窄条、水平居中；展开时铺满转写区（输出正文要能读整行）。
func TestCommandStripWidthFollowsState(t *testing.T) {
	u := newFrameUI()
	u.m.addCommand("/help")

	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 600, 400)

	if len(u.shapes) != 1 {
		t.Fatalf("shapes = %d, want 1", len(u.shapes))
	}
	full := 600 - 2*gtx.Dp(sideMarginDp)
	pill := u.shapes[0].outline
	if pill.Dx() >= full/2 {
		t.Fatalf("折叠条宽 = %d, want < %d（贴合文案，不横贯转写区）", pill.Dx(), full/2)
	}
	if c := pill.Min.X + pill.Dx()/2; c < 300-1 || c > 300+1 {
		t.Fatalf("折叠条中心 = %d, want ≈300（水平居中）", c)
	}

	// 容器变宽：窄条不跟着变宽（跟文案走）。
	u.shapes = u.shapes[:0] // 同帧形状缓冲，测试里手工复位（正常由整帧 layout 复位）
	u.transcript(gtx, 900, 400)
	if got := u.shapes[0].outline.Dx(); got != pill.Dx() {
		t.Fatalf("容器变宽后条宽 = %d, want %d（宽度跟文案，不跟容器）", got, pill.Dx())
	}

	// 展开：铺满转写区。
	u.cmdClicks[0].Click()
	u.updateClicks(gtx)
	u.shapes = u.shapes[:0]
	u.transcript(gtx, 600, 400)
	if got := u.shapes[0].outline.Dx(); got != full {
		t.Fatalf("展开后条宽 = %d, want %d（铺满转写区）", got, full)
	}
	if u.shapes[0].fill != tok().cardCmd {
		t.Fatalf("折叠条底板 = %v, want cardCmd（半透明深色薄层）", u.shapes[0].fill)
	}
}
