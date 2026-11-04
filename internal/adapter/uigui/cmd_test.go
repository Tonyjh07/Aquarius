package uigui

// cmd_test.go D265 命令折叠条：命令回显与命令返回都是运行输出——不进会话树，也不
// 冒充用户气泡；连续的命令合成一条淡灰圆角条，默认只留一行「折叠 N 条命令 · 点击展开」。

import (
	"strings"
	"testing"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCmdHeaderText 折叠行文案：条数 + 动作提示，展开态换动作词；全是回执时说「提示」
// 不说「命令」（D267）。
func TestCmdHeaderText(t *testing.T) {
	cases := []struct {
		cmd, note int
		open      bool
		want      string
	}{
		{cmd: 1, open: false, want: i18n.Tf("ui.cmd.header_commands", "n", 1) + " · " + i18n.T("ui.cmd.expand")},
		{cmd: 3, open: false, want: i18n.Tf("ui.cmd.header_commands", "n", 3) + " · " + i18n.T("ui.cmd.expand")},
		{cmd: 3, open: true, want: i18n.Tf("ui.cmd.header_commands", "n", 3) + " · " + i18n.T("ui.cmd.collapse")},
		// 一条命令带一项回执：仍报 1 条（按命令行数，不按条目数）。
		{cmd: 1, note: 2, open: false, want: i18n.Tf("ui.cmd.header_commands", "n", 1) + " · " + i18n.T("ui.cmd.expand")},
		{note: 1, open: false, want: i18n.Tf("ui.cmd.header_notes", "n", 1) + " · " + i18n.T("ui.cmd.expand")},
		{note: 2, open: true, want: i18n.Tf("ui.cmd.header_notes", "n", 2) + " · " + i18n.T("ui.cmd.collapse")},
	}
	for _, c := range cases {
		if got := cmdHeaderText(c.cmd, c.note, c.open); got != c.want {
			t.Fatalf("cmdHeaderText(%d, %d, %v) = %q, want %q", c.cmd, c.note, c.open, got, c.want)
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

// TestNoticeFoldsIntoStrip 操作回执收进折叠条（D267）：没有命令可归的回执自成一条
// 「提示条」，紧跟在命令回显后的回执挂到那条命令上——都不再以纯文本行占转写。
func TestNoticeFoldsIntoStrip(t *testing.T) {
	m, _ := newTestModel(t)

	m.handleEvent(port.NoticeEvent{Text: "已恢复会话 甲 (01K)，回放 3 条历史"})
	if len(m.blocks) != 1 || m.blocks[0].kind != blockCommand {
		t.Fatalf("blocks = %+v, want 一条提示条", m.blocks)
	}
	if got := m.blocks[0].cmds; len(got) != 1 || got[0].note == "" || got[0].cmd != "" {
		t.Fatalf("cmds = %+v, want 一条只有 note 的条目", got)
	}
	if nCmd, nNote := cmdCounts(m.blocks[0].cmds); nCmd != 0 || nNote != 1 {
		t.Fatalf("cmdCounts = (%d, %d), want (0, 1)", nCmd, nNote)
	}

	// 再来一条回执照旧并入同一条（连续提示共用一枚窄条）。
	m.handleEvent(port.NoticeEvent{Text: "正在录音，最长 30 秒…"})
	if len(m.blocks) != 1 || len(m.blocks[0].cmds) != 2 {
		t.Fatalf("blocks = %+v, want 同一条里两条提示", m.blocks)
	}

	// 命令回显之后的回执 = 这次操作的附注，挂在同一条目上。
	m.submit("/model x")
	m.handleEvent(port.NoticeEvent{Text: "已切换模型 → x"})
	last := m.blocks[len(m.blocks)-1]
	tail := last.cmds[len(last.cmds)-1]
	if tail.cmd != "/model x" || tail.note == "" {
		t.Fatalf("cmds = %+v（命令回显后的回执应挂在该命令上）", last.cmds)
	}
}

// TestNoticeAttentionStaysInline 改变了本轮上下文的提示（截断/压缩失败）留在转写里当
// 一行提示（D267）：它必须立刻看得见，也值得事后回看。
func TestNoticeAttentionStaysInline(t *testing.T) {
	m, _ := newTestModel(t)

	m.handleEvent(port.NoticeEvent{Text: "硬保底截断：已省略 12 条最旧消息", Attention: true})
	if len(m.blocks) != 1 || m.blocks[0].kind != blockNotice {
		t.Fatalf("blocks = %+v, want 一行提示", m.blocks)
	}
	if strings.Contains(m.blocks[0].text, "[notice]") {
		t.Fatalf("提示文本不该带调试前缀: %q", m.blocks[0].text)
	}
}

// TestNoteAbsorbsDirectCommandOut 左栏点会话这类「直投命令」没有输入行可回显，也就没有
// pendingCmd：结果紧跟在回执后面时并进同一条，而不是以纯文本行（助手气泡底板）落下来
// （D267）。启动提示没有回执在前，仍落纯文本行。
func TestNoteAbsorbsDirectCommandOut(t *testing.T) {
	m, _ := newTestModel(t)

	m.handleEvent(port.NoticeEvent{Text: "已切换到会话 甲 (01K)，回放 3 条历史"})
	m.say("已切换到会话 01K")
	st := m.blocks[len(m.blocks)-1]
	if st.kind != blockCommand || len(st.cmds) != 2 {
		t.Fatalf("blocks = %+v, want 一条里 note + 结果", m.blocks)
	}
	if st.cmds[0].note == "" || st.cmds[1].out != "已切换到会话 01K" {
		t.Fatalf("cmds = %+v", st.cmds)
	}

	// 启动提示：前面没有回执条 → 仍是纯文本行。
	m.clear()
	m.say("Aquarius — 输入 /help 查看命令")
	if last := m.blocks[len(m.blocks)-1]; last.kind != blockPlain {
		t.Fatalf("启动提示应落纯文本行，实际 %v", last.kind)
	}
}
