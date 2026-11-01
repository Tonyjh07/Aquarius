package uigui

// pendingsteer_alpha_test.go 待响应引导气泡的两条观感契约（D272），都只能走**真合成**
// 判：直接看 Gio 那遍渲染是不够的——Windows 上屏的是 fadeFrame 的整窗位图，它只写已
// 登记形状覆盖的像素。漏登记的形状画得出来、上屏却看不见（这正是控件消失的成因）。
//
//   - 气泡本体淡一档：它还不是会话节点，与已入树的同形气泡要一眼可分；
//   - 两枚控件不跟着淡、更要紧的是**留得下**：合成位图里必须有覆盖度。

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// composed 一帧的两种产物：Gio 渲染（内容层，含文字与半透明图层）与合成位图
// （fadeFrame 的整窗预乘 BGRA，即真正上屏的那份）。
type composed struct {
	content *image.RGBA
	out     []byte
	size    image.Point
}

// alphaAt 合成位图某点的覆盖度（0 = 该点整颗没上屏）。
func (c composed) alphaAt(x, y int) int {
	if x < 0 || y < 0 || x >= c.size.X || y >= c.size.Y {
		return 0
	}
	return int(c.out[(y*c.size.X+x)*4+3])
}

// composeFrame 整窗渲染一帧再走一遍全帧合成（与 fadeCompose 同一条路，只是源是离屏
// 渲染而非主窗 GPU 后端）。
func composeFrame(t *testing.T, u *UI, w, h int) composed {
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
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		t.Fatalf("截图失败: %v", err)
	}
	out := make([]byte, w*h*4)
	fadeFrame(img, u.shapes,
		fadeBands{top: u.bandTop, bottom: u.bandBottom, lowTop: u.bandLowTop, lowEnd: u.bandLowEnd},
		u.frameMetric, out, image.Pt(w, h))
	return composed{content: img, out: out, size: image.Pt(w, h)}
}

// TestPendingSteerBubbleDimmed 同一句话、同一个位置渲两帧：真节点 vs 待响应引导。
//
// 判据是「被洗淡」的观感本身（三通道整体抬向浅底），不是某个精确的混合系数——离屏
// 后端的混色空间与 Windows 上屏那道不完全一致，锁死系数只会锁死后端实现。
func TestPendingSteerBubbleDimmed(t *testing.T) {
	const w, h = 800, 462

	realUI := newPendingUI(&fakePending{}, true) // 没有引导 = 只有真节点
	realUI.th = testTheme()
	realUI.m.addMsg(blockUser, "不要截图了", "u1")
	realUI.expanded = true
	real := composeFrame(t, realUI, w, h)
	if len(realUI.bubbleRects) != 1 {
		t.Fatalf("真气泡矩形 = %d 条, want 1", len(realUI.bubbleRects))
	}
	rect := realUI.bubbleRects[0].rect

	p := &fakePending{}
	_, _ = p.Add("不要截图了", port.DeliverySteer)
	pendUI := newPendingUI(p, true)
	pendUI.th = testTheme()
	pendUI.expanded = true
	dim := composeFrame(t, pendUI, w, h)

	// 气泡右内衬区：正文够不到，纯底色。
	x, y := rect.Max.X-3, rect.Min.Y+rect.Dy()/2
	if dim.alphaAt(x, y) == 0 {
		t.Fatal("引导气泡没上屏（合成后透明）")
	}
	full := real.content.RGBAAt(x, y)
	pale := dim.content.RGBAAt(x, y)
	t.Logf("真气泡 %v, 引导气泡 %v", full, pale)

	var delta int
	for _, ch := range [][2]uint8{{full.R, pale.R}, {full.G, pale.G}, {full.B, pale.B}} {
		d := int(ch[1]) - int(ch[0])
		if d < -2 {
			t.Fatalf("引导气泡比真气泡更暗（%d）：淡出应朝浅底洗，方向反了", d)
		}
		delta += d
	}
	if delta < 30 {
		t.Fatalf("引导气泡与原气泡几乎同色（三通道合计 +%d），70%% 不透明度没画出来", delta)
	}
}

// TestPendingSteerControlsSurviveCompose 两枚控件必须活到合成之后。
//
// 判据是**合成位图的覆盖度**而不是热区存在：控件在气泡左侧的空白区，没登记形状就
// 整颗不落笔——热区还在、点得着，屏幕上却什么都没有（用户实测就是这一条）。
func TestPendingSteerControlsSurviveCompose(t *testing.T) {
	const w, h = 800, 462
	p := &fakePending{}
	_, _ = p.Add("不要截图了", port.DeliverySteer)
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.expanded = true
	c := composeFrame(t, u, w, h)
	if len(u.steerHot) != 1 {
		t.Fatalf("引导气泡控件 = %d 组, want 1", len(u.steerHot))
	}
	for name, r := range map[string]image.Rectangle{
		"↓": u.steerHot[0].down,
		"×": u.steerHot[0].drop,
	} {
		cy := r.Min.Y + r.Dy()/2
		left, right := r.Min.X+2, r.Max.X-3
		if c.alphaAt(left, cy) == 0 || c.alphaAt(right, cy) == 0 {
			t.Fatalf("%s 圆钮没上屏（合成后透明）：左 %d / 右 %d，热区 %v",
				name, c.alphaAt(left, cy), c.alphaAt(right, cy), r)
		}
		// 控件跟着气泡淡到 70%。合成位图的 alpha 是**覆盖度**（形状有没有上屏），不
		// 透明度烘进内容层的 RGB——所以判据取「圆钮底色离窗口底色有多远」：满不
		// 透明是 1、70% 一档约 0.7、整颗丢失是 0（那时这里是纯窗口底色）。
		bg, ring := tok().windowBg, tok().pillBg
		full := chanDist(ring, bg)
		sample := c.content.RGBAAt(r.Min.X+3, cy)
		got := chanDist(color.NRGBA{R: sample.R, G: sample.G, B: sample.B, A: sample.A}, bg)
		if full == 0 {
			t.Fatalf("%s 圆钮底色与窗口底色同值，判据失效", name)
		}
		if ratio := float64(got) / float64(full); ratio < 0.55 || ratio > 0.85 {
			t.Fatalf("%s 圆钮离底色的距离比 = %.2f（%d/%d）, want ≈%.0f%% 档",
				name, ratio, got, full, steerBubbleAlpha*100)
		}
	}
}

// chanDist 三通道绝对差之和（判「离某个底色有多远」用）。
func chanDist(c, base color.NRGBA) int {
	return absInt(int(c.R)-int(base.R)) + absInt(int(c.G)-int(base.G)) + absInt(int(c.B)-int(base.B))
}

// TestPendingSteerShapesRecorded 控件必须进形状表（上一条的成因级判据）。
// 气泡 1 块 + 两枚圆钮 = 3 条；少登记就少一条，合成阶段被丢掉。
func TestPendingSteerShapesRecorded(t *testing.T) {
	p := &fakePending{}
	_, _ = p.Add("不要截图了", port.DeliverySteer)
	u := newPendingUI(p, true)
	u.th = testTheme()
	u.expanded = true
	gtx, _ := frameGtx(input.Source{})
	u.transcript(gtx, 800, 300)
	if len(u.steerHot) != 1 {
		t.Fatalf("引导气泡控件 = %d 组, want 1", len(u.steerHot))
	}
	hot := u.steerHot[0]
	var got int
	for _, s := range u.shapes {
		if s.outline == hot.down || s.outline == hot.drop {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("控件形状登记 = %d 条, want 2（少登记 = 上屏时看不见）", got)
	}
}
