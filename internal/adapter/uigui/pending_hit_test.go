package uigui

// pending_hit_test.go 待发条能不能真的被点：用 Gio 的 input.Router 走一遍
// press/release，断言命中后清单真的变了。
//
// 这层测的不是「逻辑对不对」（那在 pending_test 里），而是**热区到底铺在哪**——
// 待发条是手绘的，一行里叠了好几个矩形，画得出来不等于点得着。

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// pendingFrame 待发条的点击往返台：每帧重新布局待发条（热区随几何走）。
type pendingFrame struct {
	q   input.Router
	u   *UI
	gtx layout.Context
	ops *op.Ops
}

func newPendingFrame(u *UI) *pendingFrame {
	f := &pendingFrame{u: u}
	f.gtx = layout.Context{
		Ops:         new(op.Ops),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(600, 60)),
		Source:      f.q.Source(),
	}
	return f
}

func (f *pendingFrame) draw() {
	f.gtx.Reset()
	f.u.pendingBand(f.gtx, 600, 0)
	f.q.Frame(f.gtx.Ops)
}

// click 在指定位置按下并抬起（两次布局，与真实事件循环同构）。
func (f *pendingFrame) click(p image.Point) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	f.q.Queue(pointer.Event{Kind: pointer.Press, Position: pos, Buttons: pointer.ButtonPrimary, PointerID: 1, Source: pointer.Mouse})
	f.draw()
	f.q.Queue(pointer.Event{Kind: pointer.Release, Position: pos, Buttons: pointer.ButtonPrimary, PointerID: 1, Source: pointer.Mouse})
	f.draw()
}

func (f *pendingFrame) hot() pendHot {
	if len(f.u.pendHot) == 0 {
		panic("没有登记待发热区")
	}
	return f.u.pendHot[0]
}

func center(r image.Rectangle) image.Point { return r.Min.Add(r.Max).Div(2) }

// TestPendingToggleClickable 点「转引导」= 改档（排队 → 引导）。
func TestPendingToggleClickable(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("这句要早点到", port.DeliveryQueue)
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.frameSize = image.Pt(600, 60)

	f := newPendingFrame(u)
	f.draw()
	f.click(center(f.hot().toggle))

	if p.items[0].Delivery != port.DeliverySteer {
		t.Fatalf("点转引导后档位 = %v, want 引导", p.items[0].Delivery)
	}
}

// TestPendingRecallClickable 点「拉回」= 条目回输入框。
func TestPendingRecallClickable(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("拉回来改改", port.DeliveryQueue)
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.frameSize = image.Pt(600, 60)

	f := newPendingFrame(u)
	f.draw()
	f.click(center(f.hot().recall))

	if len(p.items) != 0 {
		t.Fatalf("清单还剩 %d 条, want 0（应被拉回输入框）", len(p.items))
	}
	if got := u.editor.Text(); got != "拉回来改改" {
		t.Fatalf("编辑器 = %q, want 被拉回的那句", got)
	}
}

// TestPendingDropClickable 点「×」= 撤销。
func TestPendingDropClickable(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("不要了", port.DeliveryQueue)
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.frameSize = image.Pt(600, 60)

	f := newPendingFrame(u)
	f.draw()
	f.click(center(f.hot().drop))

	if len(p.items) != 0 || u.editor.Text() != "" {
		t.Fatalf("清单 = %d 条、编辑器 = %q，want 都为空", len(p.items), u.editor.Text())
	}
}

// TestPendingFollowsReveal 待发条跟着转写区的揭示进度走：转写区收完（msgP 归零）就不画，
// 展开中随揭示淡入。待发条在转写区下沿，这就是「展开时显示、收起后隐藏」的自然时机。
func TestPendingFollowsReveal(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("一条待发", port.DeliveryQueue)
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.frameSize = image.Pt(600, 60)
	f := newPendingFrame(u)

	u.expandAn = expandAnim{active: true, expand: false, msgP: 0} // 收起收尾
	f.draw()
	if len(u.pendHot) != 0 {
		t.Fatalf("转写区已收完仍画了待发条（热区 %d 条）", len(u.pendHot))
	}

	u.expandAn = expandAnim{active: true, expand: true, msgP: 0.5} // 展开中
	f.draw()
	if len(u.pendHot) != 1 {
		t.Fatalf("展开中待发条没画出来（热区 %d 条）", len(u.pendHot))
	}

	u.expandAn = expandAnim{} // 静止展开态
	f.draw()
	if len(u.pendHot) != 1 {
		t.Fatalf("静止态待发条没画出来（热区 %d 条）", len(u.pendHot))
	}
}

// steerFrame 跑完整 layout 帧的往返台（引导气泡长在转写区里，必须走整帧）。
type steerFrame struct {
	q input.Router
	u *UI
}

func (f *steerFrame) frame() {
	gtx, ops := frameGtx(f.q.Source())
	f.u.layout(gtx)
	f.q.Frame(ops)
}

func (f *steerFrame) click(p image.Point) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	f.q.Queue(pointer.Event{Kind: pointer.Press, Position: pos, Buttons: pointer.ButtonPrimary, PointerID: 1, Source: pointer.Mouse})
	f.frame()
	f.q.Queue(pointer.Event{Kind: pointer.Release, Position: pos, Buttons: pointer.ButtonPrimary, PointerID: 1, Source: pointer.Mouse})
	f.frame()
}

// TestSteerBubbleRendred 引导消息铺在转写区里、待发条上只有排队。
func TestSteerBubbleRendered(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("引导一句", port.DeliverySteer)
	_, _ = p.Add("排队一句", port.DeliveryQueue)
	u := newPendingUI(p, true)
	u.th = testTheme()
	if got := len(u.pendingQueued()); got != 1 {
		t.Fatalf("待发条条目 = %d, want 1（只有排队那条）", got)
	}
	if got := len(u.pendingSteers()); got != 1 {
		t.Fatalf("引导条目 = %d, want 1", got)
	}
	f := &steerFrame{u: u}
	f.frame()
	if len(u.steerHot) != 1 {
		t.Fatalf("引导气泡控件热区 = %d 条, want 1", len(u.steerHot))
	}
}

// TestSteerDownReturnsToQueue 点 ↓ = 退回队列（气泡消失、条目回到待发条）。
func TestSteerDownReturnsToQueue(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("等等，先看这里", port.DeliverySteer)
	u := newPendingUI(p, true)
	u.th = testTheme()
	f := &steerFrame{u: u}
	f.frame()
	if len(u.steerHot) != 1 {
		t.Fatalf("没有画出引导气泡的控件")
	}
	f.click(center(u.steerHot[0].down))

	if len(p.items) != 1 || p.items[0].Delivery != port.DeliveryQueue {
		t.Fatalf("清单 = %+v, want 退回排队", p.items)
	}
}

// TestSteerDropDiscards 点 × = 丢弃这条引导。
func TestSteerDropDiscards(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("算了不说了", port.DeliverySteer)
	u := newPendingUI(p, true)
	u.th = testTheme()
	f := &steerFrame{u: u}
	f.frame()
	if len(u.steerHot) != 1 {
		t.Fatalf("没有画出引导气泡的控件")
	}
	f.click(center(u.steerHot[0].drop))

	if len(p.items) != 0 {
		t.Fatalf("清单还剩 %d 条, want 0", len(p.items))
	}
}
