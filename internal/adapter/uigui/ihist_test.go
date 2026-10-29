package uigui

import (
	"testing"

	"gioui.org/io/key"
)

// TestIhistPush 历史记录口径：相邻重复不记、空行不记、超出上限丢最旧的。
func TestIhistPush(t *testing.T) {
	var h ihist
	h.push("a")
	h.push("a")  // 相邻重复
	h.push("  ") // 空白
	h.push("b")
	if len(h.lines) != 2 || h.lines[0] != "a" || h.lines[1] != "b" {
		t.Fatalf("history = %v", h.lines)
	}
	for i := 0; i < ihistMax+5; i++ {
		h.push(string(rune('c' + i%20)))
	}
	if len(h.lines) != ihistMax {
		t.Fatalf("上限未生效: %d", len(h.lines))
	}
}

// TestIhistPrevNext 翻历史：↑ 从最新往回、到头停住；↓ 越过最新回到草稿（用户翻出去
// 时框里原有的内容不丢）。
func TestIhistPrevNext(t *testing.T) {
	var h ihist
	h.push("one")
	h.push("two")

	if line, ok := h.prev("草稿"); !ok || line != "two" {
		t.Fatalf("首次 ↑ = (%q, %v)", line, ok)
	}
	if line, ok := h.prev("草稿"); !ok || line != "one" {
		t.Fatalf("再 ↑ = (%q, %v)", line, ok)
	}
	if line, ok := h.prev("草稿"); !ok || line != "one" {
		t.Fatalf("到头应停住: (%q, %v)", line, ok)
	}
	if line, ok := h.next(); !ok || line != "two" {
		t.Fatalf("↓ = (%q, %v)", line, ok)
	}
	if line, ok := h.next(); !ok || line != "草稿" {
		t.Fatalf("↓ 越过最新应回草稿: (%q, %v)", line, ok)
	}
	if _, ok := h.next(); ok {
		t.Fatal("已在草稿上时 ↓ 不该再动")
	}
	if _, ok := h.prev(""); !ok {
		t.Fatal("草稿态 ↑ 应回到最新一条")
	}
}

// TestIhistEmpty 没有历史时 ↑↓ 什么都不做（不能把编辑框清空）。
func TestIhistEmpty(t *testing.T) {
	var h ihist
	if _, ok := h.prev("x"); ok {
		t.Fatal("空历史不该有上一条")
	}
	if _, ok := h.next(); ok {
		t.Fatal("空历史不该有下一条")
	}
}

// TestHistoryKeysClosedInput 收起态单行输入的 ↑↓ 真的翻到历史（D250 顺带补上的键面）：
// 这些键本来归编辑器，历史命中后编辑框内容换成历史条目。
//
// 过滤器要一帧才生效（Gio 的事件派发按上一帧登记的过滤器走，与浮层同口径）——队列前
// 先空跑一帧，否则第一下按键会落到编辑器而不是这里。
func TestHistoryKeysClosedInput(t *testing.T) {
	u, q := complFixtureUI()
	u.editor.SetText("第一条")
	u.ihist.push("第一条")
	u.editor.SetText("草稿")
	complFrame(q, u) // 注册过滤器

	q.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "第一条" {
		t.Fatalf("↑ 后文本 = %q, want 第一条", got)
	}
	q.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "草稿" {
		t.Fatalf("↓ 后文本 = %q, want 草稿", got)
	}
}

// TestHistoryKeysSkipExpanded 展开态（多行）方向键仍归光标：不翻历史。
func TestHistoryKeysSkipExpanded(t *testing.T) {
	u, q := complFixtureUI()
	u.ihist.push("旧话")
	u.setExpanded(true)
	u.editor.SetText("多行")
	complFrame(q, u) // 注册过滤器（即使注册了，展开态也不该翻历史）

	q.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "多行" {
		t.Fatalf("展开态 ↑ 改了文本: %q", got)
	}
}

// TestHistorySkipsEditTarget 编辑既有消息时不翻历史（那不是在写一句话，是在改旧正文）。
func TestHistorySkipsEditTarget(t *testing.T) {
	u, q := complFixtureUI()
	u.ihist.push("旧话")
	u.apply(editMsg{id: "u1", text: "旧问题"})
	complFrame(q, u)

	q.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	complFrame(q, u)
	if got := u.editor.Text(); got != "旧问题" {
		t.Fatalf("编辑态 ↑ 改了文本: %q", got)
	}
}
