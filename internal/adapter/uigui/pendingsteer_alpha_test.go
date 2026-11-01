package uigui

// pendingsteer_alpha_test.go 待响应引导气泡的淡出（D272）：它还不是会话节点，画得比
// 真气泡淡一档，好让「已经进树的话」与「还在等下一次请求的话」一眼可分。
//
// 判据只能走像素：不透明度是观感，状态位证不了它。同一句话、同一个位置渲染两次——
// 一次是真 user 节点，一次是待响应引导——比较气泡核的底色。

import (
	"image"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// renderShot 整窗渲染一帧并截图（无 GPU/软件后端则跳过）。
func renderShot(t *testing.T, u *UI, w, h int) *image.RGBA {
	t.Helper()
	win, err := headless.NewWindow(w, h)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer win.Release()
	var q input.Router
	gtx := layout.Context{
		Ops:         new(op.Ops),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(w, h)),
		Source:      q.Source(),
	}
	u.layout(gtx)
	q.Frame(gtx.Ops)
	if err := win.Frame(gtx.Ops); err != nil {
		t.Fatalf("离屏渲染失败: %v", err)
	}
	shot := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(shot); err != nil {
		t.Fatalf("截图失败: %v", err)
	}
	return shot
}

// TestPendingSteerBubbleDimmed 待响应引导的气泡底色介于窗口底色与真气泡之间，且更靠
// 近底色；真气泡则是不透明的品牌色。
func TestPendingSteerBubbleDimmed(t *testing.T) {
	const w, h = 800, 462

	realUI := newPendingUI(&fakePending{}, true) // 没有引导 = 只有真节点
	realUI.th = testTheme()
	realUI.m.addMsg(blockUser, "不要截图了", "u1")
	realUI.expanded = true
	realShot := renderShot(t, realUI, w, h)
	if len(realUI.bubbleRects) != 1 {
		t.Fatalf("真气泡矩形 = %d 条, want 1", len(realUI.bubbleRects))
	}
	rect := realUI.bubbleRects[0].rect

	p := &fakePending{}
	_, _ = p.Add("不要截图了", port.DeliverySteer)
	pendUI := newPendingUI(p, true)
	pendUI.th = testTheme()
	pendUI.expanded = true
	pendShot := renderShot(t, pendUI, w, h)
	if len(pendUI.steerHot) != 1 {
		t.Fatalf("引导气泡控件 = %d 组, want 1（控件本身不该跟着淡）", len(pendUI.steerHot))
	}

	// 气泡右内衬区：正文够不到，纯底色。
	x, y := rect.Max.X-3, rect.Min.Y+rect.Dy()/2
	full := realShot.RGBAAt(x, y)
	dim := pendShot.RGBAAt(x, y)
	bg := realShot.RGBAAt(rect.Min.X-40, y) // 气泡左侧空白 = 窗口底色

	t.Logf("底色 %v, 真气泡 %v, 引导气泡 %v", bg, full, dim)
	checked := 0
	for _, ch := range [][3]float64{
		{float64(bg.R), float64(full.R), float64(dim.R)},
		{float64(bg.G), float64(full.G), float64(dim.G)},
		{float64(bg.B), float64(full.B), float64(dim.B)},
	} {
		bgV, fullV, dimV := ch[0], ch[1], ch[2]
		if d := fullV - bgV; d > -8 && d < 8 {
			continue // 这一通道两版本来就接近，比不出东西
		}
		checked++
		lo, hi := min(bgV, fullV), max(bgV, fullV)
		if dimV < lo+1 || dimV > hi-1 {
			t.Fatalf("引导气泡底色 %.0f 不在底色 %.0f 与真气泡 %.0f 之间", dimV, bgV, fullV)
		}
		if abs(dimV-bgV) >= abs(fullV-bgV) {
			t.Fatalf("引导气泡（%.0f）没有比真气泡（%.0f）更靠近底色（%.0f）", dimV, fullV, bgV)
		}
	}
	if checked == 0 {
		t.Fatal("没找到可比的通道，判据失效")
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
