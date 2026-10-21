package uigui

// ask_test.go D184 提问面板：状态机（选项/自由输入/多题对齐/提交与忽略）、
// 键盘路径（数字选、Enter 进题、Alt+← 回退、Esc 中断）、以及 Ask 的取消收场。

import (
	"context"
	"errors"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// askFrame 布局两帧：首帧登记焦点（FocusCmd 在帧末生效），次帧起面板才收键盘事件。
func askFrame(q *input.Router, u *UI) {
	expandFrame(q, u)
	expandFrame(q, u)
}

// askReq 两题样本：单选 + 多选（带说明）。
func askReq() port.AskRequest {
	return port.AskRequest{Questions: []port.AskQuestion{
		{Header: "目标", Question: "选一个目标", Options: []port.AskOption{
			{Label: "A"}, {Label: "B", Description: "备选"}, {Label: "C"},
		}},
		{Header: "范围", Question: "多选范围", Multiple: true, Options: []port.AskOption{
			{Label: "前端"}, {Label: "后端"}, {Label: "文档"},
		}},
	}}
}

// TestAskToggleSingleAndMultiple 单选互斥、多选取反（再次点选 = 取消）。
func TestAskToggleSingleAndMultiple(t *testing.T) {
	m, _ := newTestModel(t)
	m.startAsk(askReq(), make(chan port.AskAnswer, 1))

	m.askToggle(0)
	m.askToggle(2)
	if got := m.ask.sel[0]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("单选应互斥, sel = %v", got)
	}

	m.askGoto(1)
	m.askToggle(0)
	m.askToggle(2)
	m.askToggle(0)
	if got := m.ask.sel[1]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("多选取反失败, sel = %v", got)
	}
}

// TestAskFreeTextJoinsSelection 「自己输入」与已选项一起回填，且切题时保留各自输入。
func TestAskFreeTextJoinsSelection(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan port.AskAnswer, 1)
	m.startAsk(askReq(), reply)

	m.askToggle(1)
	m.u.askEd.SetText("顺手加一句")
	m.askGoto(1) // 存自由输入 + 换题
	m.askToggle(1)
	if m.u.askEd.Text() != "" {
		t.Fatalf("换题应清空编辑器（本题无自由输入）: %q", m.u.askEd.Text())
	}
	m.askSubmit()

	ans := <-reply
	if len(ans.Answers) != 2 {
		t.Fatalf("answers = %v, want 2 题", ans.Answers)
	}
	if got := ans.Answers[0]; len(got) != 2 || got[0] != "B" || got[1] != "顺手加一句" {
		t.Fatalf("第 1 题答案 = %v, want [B 顺手加一句]", got)
	}
	if got := ans.Answers[1]; len(got) != 1 || got[0] != "后端" {
		t.Fatalf("第 2 题答案 = %v, want [后端]", got)
	}
	if m.ask != nil {
		t.Fatal("提交后面板应收场")
	}
}

// TestAskUnansweredQuestionKeepsEmpty 未答题在结果里留空切片（不是补默认值）。
func TestAskUnansweredQuestionKeepsEmpty(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan port.AskAnswer, 1)
	m.startAsk(askReq(), reply)
	m.askToggle(0)
	m.askSubmit()

	ans := <-reply
	if len(ans.Answers[1]) != 0 {
		t.Fatalf("未答题应为空, got %v", ans.Answers[1])
	}
}

// TestAskDismissIsNotSkip 「忽略」= 放弃全部：Dismissed 置位、不带半份答案。
func TestAskDismissIsNotSkip(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan port.AskAnswer, 1)
	m.startAsk(askReq(), reply)
	m.askToggle(0)
	m.askDismiss()

	ans := <-reply
	if !ans.Dismissed || len(ans.Answers) != 0 {
		t.Fatalf("忽略应答 = %+v", ans)
	}
	if m.ask != nil {
		t.Fatal("忽略后面板应收场")
	}
}

// TestAskNextAdvancesThenSubmits 下一步逐题推进，最后一题才提交。
func TestAskNextAdvancesThenSubmits(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan port.AskAnswer, 1)
	m.startAsk(askReq(), reply)

	m.askNext()
	if m.ask.cur != 1 {
		t.Fatalf("cur = %d, want 1", m.ask.cur)
	}
	m.askNext()
	if m.ask != nil {
		t.Fatal("最后一题「下一步」应提交")
	}
	select {
	case ans := <-reply:
		if len(ans.Answers) != 2 {
			t.Fatalf("answers = %v", ans.Answers)
		}
	default:
		t.Fatal("提交未送达调用方")
	}
}

// TestAskGotoClampsAndSaves 跳题越界无效；跳走前把当前自由输入存回原题。
func TestAskGotoClampsAndSaves(t *testing.T) {
	m, _ := newTestModel(t)
	m.startAsk(askReq(), make(chan port.AskAnswer, 1))

	m.askGoto(-1)
	m.askGoto(9)
	if m.ask.cur != 0 {
		t.Fatalf("越界跳题不该移动, cur = %d", m.ask.cur)
	}
	m.u.askEd.SetText("留在第一题")
	m.askGoto(1)
	m.askGoto(0)
	if m.u.askEd.Text() != "留在第一题" {
		t.Fatalf("回跳应还原自由输入, got %q", m.u.askEd.Text())
	}
}

// TestSubmitAskSemantics 外部输入泵一行文本 → 面板语义（编号/文字/忽略/空行）。
func TestSubmitAskSemantics(t *testing.T) {
	m, _ := newTestModel(t)
	m.startAsk(askReq(), make(chan port.AskAnswer, 1))

	m.submitAsk("  ")
	if len(m.ask.sel[0]) != 0 {
		t.Fatal("空行不该改答案")
	}
	m.submitAsk("2")
	if got := m.ask.sel[0]; len(got) != 1 || got[0] != 1 {
		t.Fatalf("编号 2 应选中第 2 项, sel = %v", got)
	}
	m.submitAsk("2 也行")
	if got := m.ask.free[0]; got != "2 也行" {
		t.Fatalf("含非编号内容应作自由答案, free = %q", got)
	}
	if !m.ask.open[0] {
		t.Fatal("自由答案应展开输入行")
	}
	m.askGoto(1)
	m.submitAsk("1,3")
	if got := m.ask.sel[1]; len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("多选编号, sel = %v", got)
	}
	m.submitAsk("/dismiss")
	if m.ask != nil {
		t.Fatal("/dismiss 应放弃全部")
	}
}

// TestParseAskPicks 编号解析边界：分隔符多样、去重保序、越界与混排一律不认。
func TestParseAskPicks(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want []int
		ok   bool
	}{
		{"1", 3, []int{0}, true},
		{"3,1", 3, []int{2, 0}, true},
		{"2 3", 3, []int{1, 2}, true},
		{"1、2", 3, []int{0, 1}, true},
		{"1，1", 3, []int{0}, true},
		{"2)", 3, []int{1}, true},
		{"0", 3, nil, false},
		{"4", 3, nil, false},
		{"1,a", 3, nil, false},
		{"", 3, nil, false},
		{"1", 0, nil, false}, // 无选项 = 纯自由输入题
	}
	for _, c := range cases {
		got, ok := parseAskPicks(c.in, c.n)
		if ok != c.ok {
			t.Fatalf("parseAskPicks(%q,%d) ok = %v, want %v", c.in, c.n, ok, c.ok)
		}
		if !ok {
			continue
		}
		if len(got) != len(c.want) {
			t.Fatalf("parseAskPicks(%q,%d) = %v, want %v", c.in, c.n, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("parseAskPicks(%q,%d) = %v, want %v", c.in, c.n, got, c.want)
			}
		}
	}
}

// TestAskPanelPxClamp 面板高按内容增长，夹在窗高 70% 以内（内容多也不吃满整窗）。
func TestAskPanelPxClamp(t *testing.T) {
	u := newFrameUI()
	u.frameSize.X, u.frameSize.Y = winWidthDp, winHeightDp
	u.m.startAsk(port.AskRequest{Questions: []port.AskQuestion{{
		Header: "多", Question: "选项很多",
		Options: []port.AskOption{
			{Label: "一"}, {Label: "二"}, {Label: "三"}, {Label: "四"},
			{Label: "五"}, {Label: "六"}, {Label: "七"}, {Label: "八"}, {Label: "九"},
		},
	}}}, make(chan port.AskAnswer, 1))

	gtx, _ := frameGtxSize(input.Source{}, winWidthDp, winHeightDp)
	px := u.askPanelPx(gtx)
	if px <= gtx.Dp(inputRowDp) {
		t.Fatalf("面板高 %d 应大于单行", px)
	}
	limit := gtx.Dp(unit.Dp(float32(winHeightDp) * askPanelFrac))
	if px > limit {
		t.Fatalf("面板高 %d 超过窗高 70%% = %d", px, limit)
	}
	if px < gtx.Dp(askPanelMinDp) {
		t.Fatalf("面板高 %d 低于最小值", px)
	}

	u.m.ask.mini = true
	if got := u.askPanelPx(gtx); got != gtx.Dp(inputRowDp) {
		t.Fatalf("最小化高 = %d, want 单行 %d", got, gtx.Dp(inputRowDp))
	}
}

// TestAskSummary 最小化行摘要：题号/总数/页签/已答数。
func TestAskSummary(t *testing.T) {
	m, _ := newTestModel(t)
	m.startAsk(askReq(), make(chan port.AskAnswer, 1))
	m.askToggle(0)
	if got := m.u.askSummary(m.ask); got != "提问 1/2 · 目标 · 已答 1" {
		t.Fatalf("summary = %q", got)
	}
}

// TestAskPanelKeyboard 键盘路径：数字选、Enter 进下一题、Ctrl+Enter 提交、
// Alt+← 回退、Tab 前进；面板期主编辑器不吃按键。
func TestAskPanelKeyboard(t *testing.T) {
	u, q := newExpandUI(t)
	reply := make(chan port.AskAnswer, 1)
	u.m.startAsk(askReq(), reply)
	askFrame(q, u) // 注册面板焦点与过滤器

	if !u.expanded {
		t.Fatal("面板应展开输入栏")
	}
	u.editor.SetText("")
	q.Queue(key.Event{Name: "x", State: key.Press})
	expandFrame(q, u)
	if u.editor.Text() != "" {
		t.Fatalf("面板期主编辑器不该收字: %q", u.editor.Text())
	}

	q.Queue(key.Event{Name: "2", State: key.Press})
	expandFrame(q, u)
	if got := u.m.ask.sel[0]; len(got) != 1 || got[0] != 1 {
		t.Fatalf("数字键应选中第 2 项, sel = %v", got)
	}
	q.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	expandFrame(q, u)
	if u.m.ask.cursor != 2 {
		t.Fatalf("↓ 游标 = %d, want 2", u.m.ask.cursor)
	}
	q.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	expandFrame(q, u)
	if u.m.ask.cursor != 1 {
		t.Fatalf("↑ 游标 = %d, want 1", u.m.ask.cursor)
	}
	// 游标落到底 = 「自己输入」行：就地展开输入框，焦点让给它。
	q.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	expandFrame(q, u)
	q.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	expandFrame(q, u)
	if u.m.ask.cursor != 3 || !u.m.ask.open[0] {
		t.Fatalf("游标落到底应展开自由输入, cursor=%d open=%v", u.m.ask.cursor, u.m.ask.open)
	}
	// 收起自由输入行（等价于鼠标点回选项/换题）：焦点交还面板后再走键盘。
	u.m.ask.open[0] = false
	u.m.ask.cursor = -1
	u.askFreeFocus = false
	u.askFocus = true
	expandFrame(q, u)
	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	expandFrame(q, u)
	if u.m.ask.cur != 1 {
		t.Fatalf("Tab 应进下一题, cur = %d", u.m.ask.cur)
	}
	q.Queue(key.Event{Name: key.NameLeftArrow, State: key.Press, Modifiers: key.ModAlt})
	expandFrame(q, u)
	if u.m.ask.cur != 0 {
		t.Fatalf("Alt+← 应回上一题, cur = %d", u.m.ask.cur)
	}

	q.Queue(key.Event{Name: key.NameTab, State: key.Press})
	expandFrame(q, u)
	q.Queue(key.Event{Name: "1", State: key.Press})
	expandFrame(q, u)
	q.Queue(key.Event{Name: "3", State: key.Press})
	expandFrame(q, u)
	q.Queue(key.Event{Name: key.NameEnter, State: key.Press, Modifiers: key.ModCtrl})
	expandFrame(q, u)

	select {
	case ans := <-reply:
		if got := ans.Answers[0]; len(got) != 1 || got[0] != "B" {
			t.Fatalf("第 1 题答案 = %v", got)
		}
		if got := ans.Answers[1]; len(got) != 2 || got[0] != "前端" || got[1] != "文档" {
			t.Fatalf("第 2 题答案 = %v", got)
		}
	default:
		t.Fatal("Ctrl+Enter 未提交")
	}
	if u.m.ask != nil || u.expanded {
		t.Fatalf("提交后面板应收起: ask=%v expanded=%v", u.m.ask != nil, u.expanded)
	}
}

// TestAskEnterOnLastSubmits Enter 在最后一题直接提交（不必找「提交」钮）。
func TestAskEnterOnLastSubmits(t *testing.T) {
	u, q := newExpandUI(t)
	reply := make(chan port.AskAnswer, 1)
	u.m.startAsk(port.AskRequest{Questions: []port.AskQuestion{{
		Header: "单", Question: "只有一题", Options: []port.AskOption{{Label: "好"}},
	}}}, reply)
	askFrame(q, u)

	q.Queue(key.Event{Name: "1", State: key.Press})
	expandFrame(q, u)
	q.Queue(key.Event{Name: key.NameEnter, State: key.Press})
	expandFrame(q, u)
	select {
	case ans := <-reply:
		if len(ans.Answers[0]) != 1 || ans.Answers[0][0] != "好" {
			t.Fatalf("answers = %v", ans.Answers)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter 未提交")
	}
}

// TestAskEscInterruptsNotDismiss Esc = 中断本轮（不是放弃全部）：走停止键同路径，
// 面板等 Ask 侧据 ctx 取消收场。
func TestAskEscInterruptsNotDismiss(t *testing.T) {
	u, q := newExpandUI(t)
	var n int
	u.SetInterrupt(func() { n++ })
	u.m.startAsk(askReq(), make(chan port.AskAnswer, 1))
	askFrame(q, u)

	q.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	expandFrame(q, u)
	if n != 1 {
		t.Fatalf("Esc 应触发中断一次, n = %d", n)
	}
	if u.m.ask == nil {
		t.Fatal("Esc 不等于放弃：面板应留给 Ask 侧收场")
	}
}

// TestAskPanelShrinksTranscript 面板增高从输入带扣，转写区相应压缩（D184 原地展开）。
func TestAskPanelShrinksTranscript(t *testing.T) {
	u, q := newExpandUI(t)
	u.m.add(blockUser, "问")
	expandFrame(q, u)
	base := u.transH

	u.m.startAsk(askReq(), make(chan port.AskAnswer, 1))
	expandFrame(q, u)
	if u.transH >= base {
		t.Fatalf("面板期转写区应压缩, base=%d now=%d", base, u.transH)
	}
}

// TestAskExpandsCollapsedWindow 窗口收成球时来提问：先展开窗口（否则问题永远没人看得见）。
func TestAskExpandsCollapsedWindow(t *testing.T) {
	u, q := newExpandUI(t)
	u.collapsed = true
	expandFrame(q, u)

	u.m.startAsk(askReq(), make(chan port.AskAnswer, 1))
	expandFrame(q, u)
	if u.collapsed {
		t.Fatal("提问应把收起的窗口展开")
	}
	if u.m.ask == nil {
		t.Fatal("提问面板应打开")
	}
}

// TestClearDismissesAsk 清屏（/switch、/new）时面板收场：应答为「放弃全部」，不留悬空面板。
func TestClearDismissesAsk(t *testing.T) {
	m, _ := newTestModel(t)
	reply := make(chan port.AskAnswer, 1)
	m.startAsk(askReq(), reply)
	m.clear()
	if m.ask != nil {
		t.Fatal("清屏后面板应收场")
	}
	select {
	case ans := <-reply:
		if !ans.Dismissed {
			t.Fatalf("清屏收场应为放弃全部, got %+v", ans)
		}
	default:
		t.Fatal("清屏未回填应答（调用方会永久等待）")
	}
}

// TestAskCanceled 取消上下文 → Ask 返回 ctx.Err，面板收场（不留没人等的提问）。
func TestAskCanceled(t *testing.T) {
	u := newHeadless(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		ans port.AskAnswer
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		ans, err := u.Ask(ctx, askReq())
		resCh <- result{ans, err}
	}()
	waitAskOpen(t, u)
	cancel()

	select {
	case r := <-resCh:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 Ask 未返回")
	}
	drainSync(t, u)
	if u.askOpen.Load() {
		t.Fatal("取消后面板应收场")
	}
	if u.expanded {
		t.Fatal("取消后输入栏应回到常态")
	}
}

// TestAskDismissedOnEOF 输入流结束 → 放弃全部（不替用户编答案），与 repl/uitui 同口径。
func TestAskDismissedOnEOF(t *testing.T) {
	u := newHeadless(t, Options{})

	type result struct {
		ans port.AskAnswer
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ans, err := u.Ask(ctx, askReq())
		resCh <- result{ans, err}
	}()
	waitAskOpen(t, u)
	u.post(eofMsg{})

	select {
	case r := <-resCh:
		if r.err != nil || !r.ans.Dismissed {
			t.Fatalf("EOF 应答 = %+v, %v, want Dismissed", r.ans, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EOF 后 Ask 未返回")
	}
}

// TestAskClosedInterface 界面已关闭 → 立即报错，不挂等。
func TestAskClosedInterface(t *testing.T) {
	u := newHeadless(t, Options{})
	if err := u.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := u.Ask(context.Background(), askReq()); err == nil {
		t.Fatal("关闭后 Ask 应报错")
	}
}

// TestAskEmptyRequest 空请求直接回空答案（不发面板）。
func TestAskEmptyRequest(t *testing.T) {
	u := newHeadless(t, Options{})
	ans, err := u.Ask(context.Background(), port.AskRequest{})
	if err != nil || len(ans.Answers) != 0 {
		t.Fatalf("空请求 = %+v, %v", ans, err)
	}
}

// waitAskOpen 等面板在事件循环里打开（Ask 先投递 askMsg，故用屏障对齐）。
func waitAskOpen(t *testing.T, u *UI) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		drainSync(t, u)
		if u.askOpen.Load() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("提问面板未打开")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
