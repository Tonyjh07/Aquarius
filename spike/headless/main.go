// spike/headless —— 验证 ULW 淡出 overlay 的像素源（DESIGN §15.3）：
// gioui.org/gpu/headless 离屏渲染能否作为"淡出带内容"的来源。
//
// 验证项（PrintWindow 已证不可用：连可见区域都返回全零，见 spike/printwin）：
//  1. 清屏 = 透明（color.NRGBA{}）→ Screenshot 的 alpha 通道 = 内容覆盖；
//  2. 像素语义（预乘 or 直通）：半透明红填充的字节形态；
//  3. DPI 可控：Metric.PxPerDp=1.25（对齐用户 125% 缩放）下真实 widget
//     （material.Button/Editor + gtx.Execute）在零值 Source 上纯渲染不 panic；
//  4. 背景无绘制处 alpha=0 → 直接当"挖空"用，无需布局掩码同步。
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"time"

	"gioui.org/font/gofont"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	pxPerDp = 1.25 // 用户环境 125% 缩放
	wPx     = 400  // = 320dp × 1.25
	hPx     = 200  // = 160dp × 1.25
)

func main() {
	hw, err := headless.NewWindow(wPx, hPx)
	if err != nil {
		fmt.Printf("[FAIL] headless.NewWindow: %v\n", err)
		os.Exit(1)
	}
	defer hw.Release()
	fmt.Println("[ok] headless.NewWindow")

	ops := new(op.Ops)
	gtx := layout.Context{
		Ops:         ops,
		Now:         time.Now(),
		Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
		Constraints: layout.Exact(image.Pt(wPx, hPx)),
		Source:      input.Source{}, // 零值 = disabled（纯渲染无事件）
	}

	// ① 半透明红矩形（像素语义探针，直接用像素坐标）。
	st := clip.Rect{Min: image.Pt(20, 20), Max: image.Pt(120, 80)}.Push(ops)
	paint.Fill(ops, color.NRGBA{R: 0xFF, A: 0x80})
	st.Pop()

	// ② 不透明绿矩形。
	st = clip.Rect{Min: image.Pt(140, 20), Max: image.Pt(240, 80)}.Push(ops)
	paint.Fill(ops, color.NRGBA{G: 0xFF, A: 0xFF})
	st.Pop()

	// ③ 真实 widget 在零值 Source 上渲染（含 gtx.Execute 焦点命令路径）。
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[FAIL] widget render panic: %v\n", r)
				os.Exit(3)
			}
		}()
		gtx.Execute(key.FocusCmd{Tag: new(widget.Editor)})
		ed := widget.Editor{Submit: true, SingleLine: true}
		ed.SetText("中文 abc")
		layout.Inset{Left: unit.Dp(20), Top: unit.Dp(80)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Editor(th, &ed, "hint").Layout(gtx)
		})
		layout.Inset{Left: unit.Dp(200), Top: unit.Dp(80)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Button(th, new(widget.Clickable), "发送").Layout(gtx)
		})
		fmt.Println("[ok] widgets render on zero Source (no panic)")
	}()

	if err := hw.Frame(ops); err != nil {
		fmt.Printf("[FAIL] Frame: %v\n", err)
		os.Exit(1)
	}
	img := image.NewRGBA(image.Rect(0, 0, wPx, hPx))
	if err := hw.Screenshot(img); err != nil {
		fmt.Printf("[FAIL] Screenshot: %v\n", err)
		os.Exit(1)
	}

	px := func(x, y int) [4]uint8 {
		i := img.PixOffset(x, y)
		return [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
	}
	bg := px(10, 150)    // 真·背景（编辑器/按钮/矩形都不覆盖的左下角）
	red := px(70, 50)    // 半透明红（语义探针）
	green := px(190, 50) // 不透明绿
	btn := px(260, 110)  // 按钮区域（渲染是否落像素）

	fmt.Printf("[px] bg=%v red=%v green=%v btn=%v\n", bg, red, green, btn)

	fails := 0
	if bg[3] != 0 {
		fmt.Println("[FAIL] 清屏非透明（bg.A != 0）")
		fails++
	} else {
		fmt.Println("[ok] clear = transparent (bg.A == 0) → 挖空免掩码")
	}
	if green[3] != 0xFF || green[1] != 0xFF {
		fmt.Println("[FAIL] 不透明绿未按预期落像素")
		fails++
	}
	// 半透明红语义分类（alpha 不受伽马影响，应恰为 0x80；R 通道形态定语义）：
	//   0xFF      → straight（ULW 前自行预乘，字节空间乘 alpha）
	//   ~0x80     → 预乘（字节空间直出，AC_SRC_ALPHA 直接可用）
	//   0xA0..0xD0 → 预乘于线性空间 + sRGB 存储（需解码/再编码转换）
	if red[3] != 0x80 {
		fmt.Printf("[FAIL] 半透明红 alpha 异常: %v\n", red)
		fails++
	} else {
		switch r := red[0]; {
		case r == 0xFF:
			fmt.Println("[ok] semantics=straight → ULW 前按字节预乘")
		case r >= 0x7F && r <= 0x81:
			fmt.Println("[ok] semantics=premultiplied-byte → AC_SRC_ALPHA 直接可用")
		case r >= 0xA0 && r <= 0xD0:
			fmt.Println("[ok] semantics=premultiplied-linear-sRGB → 需 decode/encode 转换")
		default:
			fmt.Printf("[FAIL] 半透明红 R 通道形态未识别: %v\n", red)
			fails++
		}
	}
	if btn[3] == 0 && btn[1] == 0 {
		fmt.Println("[FAIL] 按钮区域无像素（widget 未渲染？）")
		fails++
	}

	if fails > 0 {
		fmt.Printf("[RESULT] headless_source_viable=false (%d fails)\n", fails)
		os.Exit(2)
	}
	fmt.Println("[RESULT] headless_source_viable=true")
}
