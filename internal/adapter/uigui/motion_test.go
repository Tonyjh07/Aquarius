package uigui

// motion_test.go 折叠块开合动画（D332）：曲线端点与单调、揭示因子对高度的映射、
// 中途反向不跳变、半隐正文不落行选键、清屏复位。

import (
	"testing"
	"time"

	"gioui.org/io/input"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCubicBezier 曲线端点、单调与两个方向的形状（展开快起、收起慢起）。
func TestCubicBezier(t *testing.T) {
	t.Parallel()
	for name, ease := range map[string]func(float64) float64{"open": easeOpen, "close": easeClose} {
		if v := ease(0); v != 0 {
			t.Fatalf("%s(0) = %v, want 0", name, v)
		}
		if v := ease(1); v != 1 {
			t.Fatalf("%s(1) = %v, want 1", name, v)
		}
		prev := 0.0
		for i := 1; i <= 20; i++ {
			v := ease(float64(i) / 20)
			if v < prev-1e-9 {
				t.Fatalf("%s 不单调：t=%v 时 %v < %v", name, float64(i)/20, v, prev)
			}
			prev = v
		}
	}
	if easeOpen(0.25) <= 0.25 { // 展开 = 起手快
		t.Fatalf("easeOpen(0.25) = %v, want > 0.25", easeOpen(0.25))
	}
	if easeClose(0.25) >= 0.25 { // 收起 = 慢起
		t.Fatalf("easeClose(0.25) = %v, want < 0.25", easeClose(0.25))
	}
}

// TestRevealAlpha 透明度：v ≥ 0.7 全实、往下线性淡出、两端夹紧。
func TestRevealAlpha(t *testing.T) {
	t.Parallel()
	cases := []struct{ v, want float64 }{
		{0, 0}, {0.1, 0}, {0.4, float64(float32(0.5))}, {0.7, 1}, {1, 1},
	}
	for _, c := range cases {
		if got := float64(revealAlpha(c.v)); got != c.want {
			t.Fatalf("revealAlpha(%v) = %v, want %v", c.v, got, c.want)
		}
	}
}

// chipAnimFixture 一张带结果的工具 chip；motionInstant = false（真的走时间线）。
func chipAnimFixture(t *testing.T) *UI {
	t.Helper()
	u := newFrameUI()
	u.motionInstant = false
	u.m.handleEvent(port.ToolCallEvent{Call: tool.Call{ID: "c1", Name: "file_read"}})
	u.m.handleEvent(port.ToolResultEvent{Result: tool.Result{CallID: "c1", OK: true, Output: "done"}})
	return u
}

// draw 一帧：消费点击 → 布局转写区（揭示值在这条路径上读取）。
func animDraw(u *UI) {
	gtx, _ := frameGtx(input.Source{})
	u.updateClicks(gtx)
	u.transcript(gtx, 600, 400)
}

// TestRevealBodyHeightAndKeys 展开动画：高度严格介于折叠与全开之间，动画期正文不落
// 行选键（半隐的字不该能被选中），跑完回到全量。
func TestRevealBodyHeightAndKeys(t *testing.T) {
	t.Parallel()
	u := chipAnimFixture(t)
	animDraw(u)
	closedH := u.contentH

	u.chipClick(0).Click()
	animDraw(u) // 这一帧布防：目标是 1，值还在 0
	a := u.reveals[blockKey{blockTool, 0}]
	if a == nil || !a.run {
		t.Fatalf("未起时间线: %+v", a)
	}
	if u.contentH != closedH {
		t.Fatalf("布防帧高度 = %d，want %d（值还在起点）", u.contentH, closedH)
	}

	u.stepReveals(a.start.Add(a.dur / 2))
	animDraw(u)
	midH := u.contentH
	if got := u.blockText(1, 1); got != "" {
		t.Fatalf("动画中正文不该落行选键，实际 %q", got)
	}

	u.stepReveals(a.start.Add(a.dur))
	animDraw(u)
	fullH := u.contentH
	if got := u.blockText(1, 1); got != "done" {
		t.Fatalf("展开后正文可选 = %q, want done", got)
	}
	if !(closedH < midH && midH < fullH) {
		t.Fatalf("高度 折叠=%d 动画中=%d 全开=%d，want 严格递增", closedH, midH, fullH)
	}
	if a.run {
		t.Fatal("跑完应停表")
	}
}

// TestRevealReverseKeepsValue 动画中途反向：起点 = 现值（不跳变），跑完落回折叠态。
func TestRevealReverseKeepsValue(t *testing.T) {
	t.Parallel()
	u := chipAnimFixture(t)
	animDraw(u)
	u.chipClick(0).Click()
	animDraw(u)
	a := u.reveals[blockKey{blockTool, 0}]
	u.stepReveals(a.start.Add(a.dur / 2))
	mid := a.v
	if mid <= 0 || mid >= 1 {
		t.Fatalf("半程 v = %v, want (0,1)", mid)
	}

	u.chipClick(0).Click() // 收起
	animDraw(u)
	if a.to != 0 || a.from > mid+1e-9 || a.v > mid+1e-9 {
		t.Fatalf("反向 from=%v v=%v to=%v，want 从现值 %v 续跑", a.from, a.v, a.to, mid)
	}
	u.stepReveals(a.start.Add(time.Second))
	if a.v != 0 || a.run {
		t.Fatalf("收起跑完 v = %v run = %v, want 0/false", a.v, a.run)
	}
}

// TestTurnGroupRevealRows Turn 组：整行揭示——动画中高介于两端，跑完等于全开。
func TestTurnGroupRevealRows(t *testing.T) {
	t.Parallel()
	u := frameTurnFixture(t)
	u.motionInstant = false
	animDraw(u)
	closedH := u.contentH

	u.turnClick(3).Click()
	animDraw(u)
	g := u.reveals[blockKey{blockTurn, u.turnAnchors[3].slot}]
	if g == nil || !g.run {
		t.Fatalf("组未起时间线: %+v", g)
	}
	u.stepReveals(g.start.Add(motionStaggerMs * time.Millisecond))
	animDraw(u)
	midH := u.contentH
	if !(closedH < midH) {
		t.Fatalf("展开中高 = %d，折叠 = %d（应已开始长高）", midH, closedH)
	}
	u.stepReveals(g.start.Add(time.Second))
	animDraw(u)
	fullH := u.contentH
	if !(midH < fullH) {
		t.Fatalf("全开高 = %d，动画中 = %d（应更高）", fullH, midH)
	}
	for bi := range u.m.blocks { // 跑完全部落定
		if v := u.revealValue(blockKey{blockTurn, bi}); v != 0 && v != 1 {
			t.Fatalf("块 %d 揭示值 = %v，跑完应落定", bi, v)
		}
	}
}

// TestRevealClearsOnClear 清屏复位揭示时间线（块序重排后不能继承半开状态）。
func TestRevealClearsOnClear(t *testing.T) {
	t.Parallel()
	u := chipAnimFixture(t)
	animDraw(u)
	u.chipClick(0).Click()
	animDraw(u)
	if len(u.reveals) == 0 {
		t.Fatal("应已登记时间线")
	}
	u.m.clear()
	if u.reveals != nil || u.motionStop != nil {
		t.Fatalf("清屏后未复位: reveals=%v stop=%v", u.reveals, u.motionStop)
	}
}
