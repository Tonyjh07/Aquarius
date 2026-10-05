package uigui

// rawview_test.go D99 查看原文窗：帧内容同步（原子槽 → 只读编辑器）headless 测试；
// 真实次窗路径由手工验收覆盖（GUI 不进 CI）。

import (
	"testing"

	"gioui.org/io/input"
)

// TestRawViewFrame 帧内 sync：空槽显示未选择态；内容快照变化才 SetText，ReadOnly 置位。
func TestRawViewFrame(t *testing.T) {
	u := newFrameUI()
	st := newRawViewState()
	q := new(input.Router)
	th := newTheme()

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
