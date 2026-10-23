package uigui

// settings_page_adv_test.go 高级页（config.json 整文件编辑）的高度口径回归。
//
// 复现过的问题：编辑区写死 220dp，窗口拉高后框底下留一大片空白，窗口高度在这个页面上
// 等于白给。修法 = 该页按 Flex 排、编辑区吸走剩余高度（fillPage），于是「窗口给多少就
// 用多少」。判据取用户看到的像素事实：框随窗口一起变高，且不再是那个固定值。

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestAdvPageFillsRow （纯几何口径）高级页声明了撑满行，且它必须排在行表末尾——
// 后面还有行的话，Flex 会把剩余高度分给编辑区、把后续行挤成 0 高。
func TestAdvPageFillsRow(t *testing.T) {
	fp, ok := settingsPages[pageAdv].(fillPage)
	if !ok {
		t.Fatal("「高级」页应实现 fillPage（整文件编辑区要随窗口高度撑满）")
	}
	f := &settingsForm{page: pageAdv}
	rows := f.pageRows(pageAdv)
	i := fp.FillRow(rows)
	if i < 0 {
		t.Fatal("「高级」页未声明撑满行")
	}
	if rows[i].kind != rowRaw {
		t.Errorf("撑满行 kind = %v, want rowRaw（整文件编辑区）", rows[i].kind)
	}
	if i != len(rows)-1 {
		t.Errorf("撑满行下标 = %d（共 %d 行），want 末行——后面的行会被挤成 0 高", i, len(rows))
	}

	// 其余页仍走滚动列表：撑满行不是全局口径，只属于「整块内容没有自然高度」的页。
	for p, page := range settingsPages {
		if p == pageAdv {
			continue
		}
		if _, ok := page.(fillPage); ok {
			t.Errorf("页 %s 也实现了 fillPage——撑满行只在无自然高度的整块页成立", settingsPageNames[p])
		}
	}
}

// TestAdvEditorTracksWindowHeight 离屏渲染：同一个 config 文本在两个窗口高度下，编辑框
// 高度之差 ≈ 窗口高度之差（框跟着窗口长，而不是停在某个固定值）。
func TestAdvEditorTracksWindowHeight(t *testing.T) {
	const (
		viewW, pxPerDp = 900, 1.25
		navWpx         = 140 * pxPerDp
	)
	w, err := headless.NewWindow(viewW, 1200)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	th := testTheme()

	// boxH 把某一窗高下的「高级」页光栅化，返回编辑框的上下沿（沿框左侧一列量非底色像素）。
	boxH := func(viewH int) (top, bottom int) {
		f := newSettingsForm(u)
		f.page = pageAdv
		f.focused = true
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
			Constraints: layout.Exact(image.Pt(viewW, viewH)),
		}, th, u, f)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("离屏帧提交失败: %v", err)
		}
		img := image.NewRGBA(image.Rectangle{Max: image.Pt(viewW, viewH)})
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("离屏取像失败: %v", err)
		}
		bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA) // 导航栏顶角 = 窗体底色
		// 框内一列：贴着框左沿往里几个像素，避开文字（编辑器内容有内边距）。
		x := int(navWpx) + int(rowInsetDp*pxPerDp+0.5) + 3
		top, bottom = -1, -1
		for y := 0; y < viewH; y++ {
			if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) == bg {
				continue
			}
			if top < 0 {
				top = y
			}
			bottom = y
		}
		return top, bottom
	}

	top1, bot1 := boxH(700)
	top2, bot2 := boxH(1000)
	if top1 < 0 || top2 < 0 {
		t.Fatal("高级页没画出编辑框（整列为底色）")
	}
	h1, h2 := bot1-top1+1, bot2-top2+1
	// 旧的固定 220dp @1.25 = 275px；撑满后应明显更高。
	if h1 < 400 {
		t.Errorf("窗高 700 时编辑框高 %dpx < 400——仍是被写死的高度，没随窗口撑满", h1)
	}
	// 窗口长 300px，框也应长 300px（上下留白与底栏高度是常量）。
	if grew := h2 - h1; grew < 290 || grew > 310 {
		t.Errorf("窗口长 300px 而编辑框只长 %dpx（高 %d→%d）——框未跟随窗口高度", grew, h1, h2)
	}
	if top1 != top2 {
		t.Errorf("两窗高下框顶 = %d / %d，want 相同（框顶由上面的标题决定）", top1, top2)
	}
}
