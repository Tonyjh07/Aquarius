package uigui

// rawview_test.go D99 查看原文窗：帧内容同步（原子槽 → 只读编辑器）headless 测试；
// 真实次窗路径由手工验收覆盖（GUI 不进 CI）。

import (
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestTextViewEvent 内核投来的上下文全文只进只读次窗，不进转写（§7.1）。
// headless 下 openWin no-op，因此这里验的是「原子槽收到、转写不变」这两件。
func TestTextViewEvent(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	u.wins = newWinHost(nil)

	before := len(u.m.blocks)
	title := i18n.Tf("session.ctx.view_title", "model", "m", "tok", 10)
	body := "full context body\nsecond line"
	u.apply(eventMsg{ev: port.TextViewEvent{Title: title, Text: body}})
	if got := len(u.m.blocks); got != before {
		t.Fatalf("转写块数 %d → %d，上下文全文不该进转写", before, got)
	}
	c := u.rawView.Load()
	if c == nil || c.title != title || c.text != body {
		t.Fatalf("次窗内容 = %+v", c)
	}
}

// TestRawViewFrame 帧内 sync：空槽显示未选择态；内容快照变化才 SetText，ReadOnly 置位。
func TestRawViewFrame(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	st := newRawViewState()
	q := new(input.Router)
	th := testTheme()

	frame := func() {
		gtx, ops := frameGtx(q.Source())
		rawViewFrame(gtx, th, u, st)
		q.Frame(ops)
	}

	frame()
	if st.applied != nil || st.ed.Text() != "" {
		t.Fatalf("空槽应无内容: applied=%v text=%q", st.applied, st.ed.Text())
	}

	c := &rawContent{title: "原文 · abc12345", text: "**原始**\nmarkdown"}
	u.rawView.Store(c)
	frame()
	if st.applied != c || !st.ed.ReadOnly || st.ed.Text() != "**原始**\nmarkdown" {
		t.Fatalf("sync 未生效: applied=%v readOnly=%v text=%q", st.applied, st.ed.ReadOnly, st.ed.Text())
	}

	// 同一快照重帧不重置（指针比较防抖；重置会丢用户在只读器里的选中态）。
	st.ed.SetCaret(0, 2)
	frame()
	if st.ed.Text() != "**原始**\nmarkdown" {
		t.Fatalf("重帧后文本 = %q, want 不变", st.ed.Text())
	}

	// 新快照替换内容。
	c2 := &rawContent{title: "原文 · ffff0000", text: "另一条"}
	u.rawView.Store(c2)
	frame()
	if st.applied != c2 || st.ed.Text() != "另一条" {
		t.Fatalf("替换未生效: applied=%v text=%q", st.applied, st.ed.Text())
	}
}

// TestRawViewCopyGivesMarkdownSource 左上角按钮复制的是**原文源码**（D353）：
// 与主窗气泡右键的「复制」（渲染后的可选文本）是两条语义，不能合成一条。
func TestRawViewCopyGivesMarkdownSource(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	st := newRawViewState()
	var got string
	st.writeClip = func(_ layout.Context, text string) { got = text }
	q := new(input.Router)
	gtx, _ := frameGtx(q.Source())

	// 空槽：没内容可复制，点了也不该写（按钮同时是禁用的）。
	st.copyMarkdown(gtx)
	if got != "" {
		t.Fatalf("空槽也写了剪贴板：%q", got)
	}

	src := "**粗体**\n\n```go\nfmt.Println(1)\n```"
	u.rawView.Store(&rawContent{title: "原文 · abc", text: src})
	st.sync(u)
	st.copyMarkdown(gtx)
	if got != src {
		t.Fatalf("剪贴板 = %q，want 原始 markdown %q", got, src)
	}
	if st.copiedAt.IsZero() {
		t.Fatal("复制后没记时间：按钮不会给出「已复制」")
	}
	if label := st.copyLabel(gtx); label != i18n.T("ui.rawview.copied") {
		t.Errorf("复制后按钮文案 = %q，want 已复制", label)
	}

	// 过了闪光期回落成「复制 Markdown」（定时重绘会再走一次这个分支）。
	st.copiedAt = gtx.Now.Add(-copiedFlash)
	if label := st.copyLabel(gtx); label != i18n.T("ui.rawview.copy_md") {
		t.Errorf("闪光期后按钮文案 = %q，want 复制 Markdown", label)
	}
}

// TestRawViewCopySchedulesRedraw 复制后要请一次重绘：次窗事件驱动，没有帧就没有
// 「2 秒后回落」这回事，按钮会一直挂着「已复制」。
func TestRawViewCopySchedulesRedraw(t *testing.T) {
	t.Parallel()
	u := newFrameUI()
	st := newRawViewState()
	st.writeClip = func(layout.Context, string) {}
	redraw := make(chan struct{}, 1)
	st.inval = func() { redraw <- struct{}{} }
	q := new(input.Router)
	gtx, _ := frameGtx(q.Source())

	u.rawView.Store(&rawContent{title: "t", text: "x"})
	st.sync(u)
	st.copyMarkdown(gtx)

	select {
	case <-redraw:
	case <-time.After(copiedFlash + time.Second):
		t.Fatal("复制后没有请重绘：「已复制」会一直挂着")
	}
}
