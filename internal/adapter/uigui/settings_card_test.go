package uigui

// settings_card_test.go 分组卡边框的连续性回归（D148/D149）。
//
// 复现过的故障：组内相邻两行之间留了 rowInsetDp/2 的上下外边距，卡与卡之间插进一段
// 白，**左右竖边逐行断开**——放大后看是每行各缺一块，整张卡像没画完。同期还存在
// 「抹横边时按整宽抹、把竖边一起凿掉」的写法，症状相同。两者都只在像素上可见，
// 数据层与几何断言全绿。

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

// TestCardPadOnlyAtGroupBoundary 只有组首/组尾留外边距；**组内上下必须为 0**。
//
// 组内留边距会让相邻行的卡之间插进一段白，竖边逐行断开；卡内行距由内容的内边距给。
func TestCardPadOnlyAtGroupBoundary(t *testing.T) {
	cases := []struct {
		first, last      bool
		wantTop, wantBot int
	}{
		{true, true, rowInsetDp, rowInsetDp}, // 单行成组：两头都留
		{true, false, rowInsetDp, 0},         // 组首：只留上
		{false, true, 0, rowInsetDp},         // 组尾：只留下
		{false, false, 0, 0},                 // 组中：不留——留了就断边
	}
	for _, c := range cases {
		top, bot := cardPad(c.first, c.last)
		if top != c.wantTop || bot != c.wantBot {
			t.Errorf("cardPad(first=%v,last=%v) = (%d,%d)，want (%d,%d)",
				c.first, c.last, top, bot, c.wantTop, c.wantBot)
		}
	}
}

// TestSettingsCardEdgeContinuous 真实渲染下，卡竖边必须按**整组**连续而不是逐行成段。
//
// 判据：沿卡左竖边扫出的长段（≥40px，即真正的边框而非圆角/文字噪点）数量必须接近
// 可见的分组数。逐行断边时每个卡行各成一段，长段数会随行数线性增长。
func TestSettingsCardEdgeContinuous(t *testing.T) {
	const (
		viewW, viewH = 900, 740
		pxPerDp      = 1.25
	)
	sz := image.Pt(viewW, viewH)

	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return promptSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	th := testTheme()
	f := newSettingsForm(u)
	f.page = pageGeneral
	f.focused = true
	// 滚到「外观与呼出」：连续多个单行卡（快捷键/主题/缩放/字号/窗口尺寸），
	// 正是竖边最容易逐行断开的位置。
	for i := 0; i < 5; i++ {
		f.pages[pageGeneral].ScrollBy(60)
	}

	var ops op.Ops
	src := new(input.Router).Source()
	mk := func() layout.Context {
		return layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
			Constraints: layout.Exact(sz),
			Source:      src,
		}
	}
	settingsFrame(mk(), th, u, f) // 第一帧建立行几何
	ops.Reset()
	settingsFrame(mk(), th, u, f)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("帧提交: %v", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: sz})
	if err := w.Screenshot(img); err != nil {
		t.Fatalf("取像: %v", err)
	}

	bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA)
	navPx := int(140 * pxPerDp)
	// 卡左竖边落在内容起点附近；取「非底色行最多」的那一列，不写死像素坐标。
	bestX, bestN := -1, 0
	for x := navPx; x < navPx+int(40*pxPerDp); x++ {
		n := 0
		for y := 0; y < viewH-100; y++ {
			if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) != bg {
				n++
			}
		}
		if n > bestN {
			bestX, bestN = x, n
		}
	}
	if bestX < 0 || bestN < 100 {
		t.Fatalf("找不到卡竖边（最密列 x=%d 仅 %d 行非底色）——卡片可能整体没画", bestX, bestN)
	}

	segments := []int{}
	cur := 0
	for y := 0; y < viewH-100; y++ {
		if color.RGBAModel.Convert(img.At(bestX, y)).(color.RGBA) != bg {
			cur++
			continue
		}
		if cur > 0 {
			segments = append(segments, cur)
			cur = 0
		}
	}
	if cur > 0 {
		segments = append(segments, cur)
	}

	long := 0
	total := 0
	for _, s := range segments {
		if s >= 40 { // 40px ≈ 32dp，短于此的是圆角/文字噪点
			long++
		}
		total += s
	}
	// 视口内最多看到三张卡（外观组、输出组、下一组的上沿）。
	if long > 3 {
		t.Errorf("卡竖边被切成 %d 段（≥40px 的段 %v）——组内相邻行之间留了外边距，"+
			"或抹横边时把竖边一起抹掉了", long, segments)
	}
	if total < 300 {
		t.Errorf("卡竖边总长仅 %dpx，want ≥300——竖边整体缺失", total)
	}
}
