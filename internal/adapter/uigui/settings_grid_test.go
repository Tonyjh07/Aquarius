package uigui

// settings_grid_test.go 模块库网格的两条回归线：
//
//  1. 网格必须画在**自己那一行的坐标系里**。列表给的约束是 Min.X == Max.X == 行宽
//     （行原点已由 List 平移好），再按 Min 推一次偏移就等于把整块网格推出视野——
//     画面表现是「模块库标题在、下面一片空白」，而所有数据层测试都还是绿的。
//  2. Cancel 只能清**自己那一路**按压。Cancel 会分发给同 tag 的所有控件，无脑清全局
//     拖动状态会让别的磁贴的拖动被一次空 Cancel 抹掉（残影消失、松手不落位）。

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestModuleGridDrawsInsideRow 网格内容必须落在行宽之内（左三分之一不能是空白）。
func TestModuleGridDrawsInsideRow(t *testing.T) {
	const (
		viewW, viewH = 600, 200
		pxPerDp      = 1.25
	)
	f := &settingsForm{}
	for _, n := range []string{"house", "identity"} {
		pf := &promptModuleForm{}
		pf.name.SetText(n)
		f.promptMods = append(f.promptMods, pf)
	}
	th := testTheme()

	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用: %v", err)
	}
	defer w.Release()

	var ops op.Ops
	// 与 material.List 给行的约束同形：宽度已定（Min == Max），高度不设限。
	gtx := layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
		Constraints: layout.Constraints{Min: image.Pt(viewW, 0), Max: image.Pt(viewW, viewH)},
		Source:      new(input.Router).Source(),
	}
	f.moduleGridRow(gtx, th)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("帧提交: %v", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: image.Pt(viewW, viewH)})
	if err := w.Screenshot(img); err != nil {
		t.Fatalf("取像: %v", err)
	}
	// 背景纯白；磁贴有描边与浅底。左三分之一有像素 = 没被整体推走。
	bg := color.RGBAModel.Convert(img.At(viewW-1, viewH-1)).(color.RGBA)
	left := 0
	for y := 0; y < viewH; y++ {
		for x := 0; x < viewW/3; x++ {
			if c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA); c != bg {
				left++
			}
		}
	}
	if left == 0 {
		t.Fatal("网格左三分之一全为背景色：模块库被整体推出了行坐标系（疑似二次偏移）")
	}
}

// TestModuleGridCancelScopedToPress 一次不属于本磁贴的 Cancel 不得清掉拖动状态。
func TestModuleGridCancelScopedToPress(t *testing.T) {
	f := &settingsForm{}
	pf := &promptModuleForm{}
	pf.name.SetText("house")
	f.promptMods = append(f.promptMods, pf)

	pressed := f.tileCtl(0)
	pressed.pressed, pressed.pid = true, pointer.ID(7)
	other := f.tileCtl(1) // 「＋ 新增」格：没按下
	f.modDrag = modDragState{active: true, from: 0, drop: 0}

	if f.cancelPress(other, 7) {
		t.Fatal("未按下的磁贴不应报告「清掉了按压」")
	}
	if !f.modDrag.active {
		t.Fatal("别人的 Cancel 把拖动状态清了：残影会突然消失、松手不落位")
	}
	if f.cancelPress(pressed, 9) {
		t.Fatal("指针 ID 不匹配时不应清按压")
	}
	if !f.modDrag.active {
		t.Fatal("异源指针的 Cancel 清了拖动状态")
	}
	if !f.cancelPress(pressed, 7) || pressed.pressed {
		t.Fatal("本路 Cancel 应结束按压")
	}
	if f.modDrag.active {
		t.Fatal("本路 Cancel 后拖动应结束")
	}
}
