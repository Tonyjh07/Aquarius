package uigui

// scroll_test.go 尾随滚动的平滑追及（D333）与回底按钮（D334）：追及不在单帧贴底、
// 整屏级跳变直接落定、手滚仍当帧生效、键盘翻屏、按钮的显隐与点击。

import (
	"fmt"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// scrollFixture 内容高过视口、真走时间线（motionInstant = false）的 UI；首帧量高后
// 已贴底（首次量高按整屏级跳变落定）。
func scrollFixture(t *testing.T) *UI {
	t.Helper()
	u := newFrameUI()
	u.motionInstant = false
	for i := 0; i < 30; i++ {
		u.m.add(blockAssistant, fmt.Sprintf("line %02d with enough text to make a row", i))
	}
	animDraw(u)
	if u.contentH <= u.transH {
		t.Fatalf("测试前提：内容应高过视口 contentH=%d transH=%d", u.contentH, u.transH)
	}
	if u.scrollPx != u.contentH-u.transH {
		t.Fatalf("首次量高应落定贴底: scrollPx=%d overflow=%d", u.scrollPx, u.contentH-u.transH)
	}
	return u
}

// TestScrollFollowEases 流式增长时逐帧逼近底：单帧不贴底、位移单调递增，停长后落定到底。
func TestScrollFollowEases(t *testing.T) {
	t.Parallel()
	u := scrollFixture(t)
	now := time.Now()
	u.scrollLast = now
	before := u.scrollPx
	for i := 0; i < 6; i++ {
		u.m.add(blockAssistant, "streamed line")
	}
	// 一帧声明新目标、第二帧才开始走（目标在 layout 里声明，进度在帧前推进——
	// 与 D50/D54 同一条次序，落后一帧是既定规则）。
	now = now.Add(16 * time.Millisecond)
	u.stepScroll(now)
	animDraw(u)
	now = now.Add(16 * time.Millisecond)
	u.stepScroll(now)
	animDraw(u)
	overflow := u.contentH - u.transH
	if u.scrollPx >= overflow {
		t.Fatalf("一帧就贴底（等于没做平滑）: scrollPx=%d overflow=%d", u.scrollPx, overflow)
	}
	if u.scrollPx <= before {
		t.Fatalf("应开始追: scrollPx=%d before=%d", u.scrollPx, before)
	}
	prev := u.scrollPx
	for i := 0; i < 120 && u.scrollAn.run; i++ {
		now = now.Add(16 * time.Millisecond)
		u.stepScroll(now)
		animDraw(u)
		if u.scrollPx < prev {
			t.Fatalf("追及过程不应回退: %d → %d", prev, u.scrollPx)
		}
		prev = u.scrollPx
	}
	if u.scrollAn.run {
		t.Fatal("内容停长后应停表")
	}
	if want := u.contentH - u.transH; u.scrollPx != want {
		t.Fatalf("静止后应精确贴底: scrollPx=%d want %d", u.scrollPx, want)
	}
}

// TestScrollSnapsOnScreenJump 一整块内容落地（超过一屏）不是流式增长：当帧落定。
func TestScrollSnapsOnScreenJump(t *testing.T) {
	t.Parallel()
	u := scrollFixture(t)
	now := time.Now()
	u.scrollLast = now
	for i := 0; i < 40; i++ {
		u.m.add(blockAssistant, "big block line")
	}
	u.stepScroll(now.Add(16 * time.Millisecond)) // 目标还是上一帧的：不动
	animDraw(u)                                  // 本帧量高新目标 → 应落定
	if want := u.contentH - u.transH; u.scrollPx != want {
		t.Fatalf("整屏级跳变应落定: scrollPx=%d want %d", u.scrollPx, want)
	}
	if u.scrollAn.run {
		t.Fatal("落定后不应还起表")
	}
}

// TestScrollWheelStaysImmediate 手滚 1:1：当帧就到位、停止尾随，且追及位置同步
// （否则下次恢复跟随会跳一下）。
func TestScrollWheelStaysImmediate(t *testing.T) {
	t.Parallel()
	u := scrollFixture(t)
	var q input.Router
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		u.updateClicks(gtx)
		u.transcript(gtx, 600, 400)
		q.Frame(ops)
	}
	frame() // 首帧：登记手势区（事件投递要用上一帧的命中树）
	bottom := u.scrollPx
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, -60)})
	frame()
	if u.scrollPx != bottom-60 {
		t.Fatalf("上滚应 1:1: scrollPx=%d want %d", u.scrollPx, bottom-60)
	}
	if u.followTail {
		t.Fatal("上滚离底应停止尾随")
	}
	if u.scrollAn.pos != float64(u.scrollPx) || u.scrollAn.run {
		t.Fatalf("手滚后追及位置应同步且停表: pos=%v run=%v scrollPx=%d",
			u.scrollAn.pos, u.scrollAn.run, u.scrollPx)
	}
	q.Queue(pointer.Event{Kind: pointer.Scroll, Position: f32.Pt(300, 200), Scroll: f32.Pt(0, 60)})
	frame()
	if !u.followTail || u.scrollPx != u.contentH-u.transH {
		t.Fatalf("滚回底部应恢复贴底: follow=%v scrollPx=%d", u.followTail, u.scrollPx)
	}
}

// TestScrollKeysPageStep PageUp / PageDown 翻一屏：上翻即脱离跟随，翻回底部恢复。
func TestScrollKeysPageStep(t *testing.T) {
	t.Parallel()
	u := scrollFixture(t)
	var q input.Router
	frame := func() {
		gtx, ops := frameGtx(q.Source())
		u.updateClicks(gtx)
		u.transcript(gtx, 600, 400)
		q.Frame(ops)
	}
	frame() // 首帧：登记键盘过滤器
	bottom := u.scrollPx
	step := 400 - 400/10
	q.Queue(key.Event{Name: key.NamePageUp, State: key.Press})
	frame()
	if u.scrollPx != bottom-step {
		t.Fatalf("PageUp 应上翻一屏: scrollPx=%d want %d", u.scrollPx, bottom-step)
	}
	if u.followTail {
		t.Fatal("PageUp 后应停止跟随")
	}
	// 翻回去（多翻几次到底）：恢复跟随。
	for i := 0; i < 3; i++ {
		q.Queue(key.Event{Name: key.NamePageDown, State: key.Press})
		frame()
	}
	if !u.followTail {
		t.Fatalf("PageDown 到底应恢复跟随: scrollPx=%d overflow=%d", u.scrollPx, u.contentH-u.transH)
	}
}

// TestScrollPillShowsAndJumps 脱离跟随且有溢出 → 按钮淡入；点它回底并恢复跟随。
func TestScrollPillShowsAndJumps(t *testing.T) {
	t.Parallel()
	u := scrollFixture(t)
	u.followTail = false
	u.scrollPx = 0
	u.snapScroll()
	animDraw(u) // 声明 show=true
	if u.pillAn.to != 1 || !u.pillAn.run {
		t.Fatalf("应起淡入: to=%v run=%v", u.pillAn.to, u.pillAn.run)
	}
	u.stepPill(time.Now().Add(200 * time.Millisecond))
	if u.pillAn.v != 1 {
		t.Fatalf("淡入应到位: v=%v", u.pillAn.v)
	}
	u.pillBtn.Click()
	animDraw(u) // 消费点击
	if !u.followTail || !u.scrollAn.jump {
		t.Fatalf("点按钮应回底并起定长缓动: follow=%v jump=%v", u.followTail, u.scrollAn.jump)
	}
	u.stepScroll(u.scrollAn.start.Add(300 * time.Millisecond))
	animDraw(u)
	if want := u.contentH - u.transH; u.scrollPx != want {
		t.Fatalf("回底缓动结束应贴底: scrollPx=%d want %d", u.scrollPx, want)
	}
	if u.pillAn.to != 0 {
		t.Fatalf("回底后按钮应收起: to=%v", u.pillAn.to)
	}
}
