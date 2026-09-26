package uigui

// fade.go —— 淡出带 overlay（D44/§15.3）：headless 离屏渲染同一布局 → 裁淡出带 →
// 垂直渐变 × 预乘转换 → UpdateLayeredWindow。
//
// 承重验证（spike/headless，§15.6）：headless 清屏 = 透明（alpha 通道 = 内容覆盖，
// 免布局掩码）；零值 Source 纯渲染不 panic；PxPerDp 可控（对齐主窗 DPI）；
// 像素语义 = 预乘线性 + sRGB 存储（此处转为 ULW 所需的字节空间预乘）。

import (
	"image"
	"math"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// fadeState 淡出带离屏渲染状态（事件循环 goroutine 独占）。
type fadeState struct {
	hw       *headless.Window
	wPx, hPx int
	metric   unit.Metric
	ops      op.Ops
	img      *image.RGBA
	ready    bool
}

// ensure 分配/重建离屏上下文（窗口尺寸或 DPI 变化时重建）。
func (f *fadeState) ensure(wPx, hPx int, metric unit.Metric) error {
	if f.ready && f.wPx == wPx && f.hPx == hPx && f.metric.PxPerDp == metric.PxPerDp {
		return nil
	}
	if f.hw != nil {
		f.hw.Release()
		f.hw = nil
		f.ready = false
	}
	hw, err := headless.NewWindow(wPx, hPx)
	if err != nil {
		return err
	}
	f.hw = hw
	f.wPx, f.hPx, f.metric = wPx, hPx, metric
	f.img = image.NewRGBA(image.Rect(0, 0, wPx, hPx))
	f.ready = true
	return nil
}

// render 把布局离屏渲染并截图（layoutFn 必须与主窗同一布局代码——同源同帧）。
// 零值 Source = disabled：纯渲染无事件消费（spike 实证安全）。
func (f *fadeState) render(layoutFn func(layout.Context) layout.Dimensions) error {
	f.ops.Reset()
	gtx := layout.Context{
		Ops:         &f.ops,
		Now:         time.Now(),
		Metric:      f.metric,
		Constraints: layout.Exact(image.Pt(f.wPx, f.hPx)),
		Source:      input.Source{},
	}
	layoutFn(gtx)
	if err := f.hw.Frame(&f.ops); err != nil {
		return err
	}
	return f.hw.Screenshot(f.img)
}

// srgbDecode/encode sRGB ↔ 线性（IEC 61966-2-1 分段式）。
func srgbDecode(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func srgbEncode(l float64) float64 {
	if l <= 0.0031308 {
		return l * 12.92
	}
	return 1.055*math.Pow(l, 1/2.4) - 0.055
}

// decodeLUT/encodeLUT 一次性查表（decode 按 256 字节；encode 按 4096 档浮点）。
var (
	decodeLUT  [256]float64
	encodeLUT  [4097]float64
	lutForever = func() struct{} {
		for i := 0; i < 256; i++ {
			decodeLUT[i] = srgbDecode(float64(i) / 255)
		}
		for i := 0; i <= 4096; i++ {
			encodeLUT[i] = srgbEncode(float64(i) / 4096)
		}
		return struct{}{}
	}()
)

func encodeLUTAt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return encodeLUT[int(v*4096)]
}

// fadePremultiplyBand 把源图淡出带 [0, bandH) 行转成 UpdateLayeredWindow 用的
// 预乘 BGRA（顶向紧密排布，len = w*bandH*4），返回带内是否有可见内容
// （无内容时调用方隐藏 overlay）。
//
// 源语义（spike/headless 实证）：A = 直通 alpha；C = sRGB_encode(线性预乘值)。
// 输出：A' = a·g(y)，C' = sRGB(color)·a·g(y)——恒满足 C' ≤ A'（合法预乘）；
// g(y) = smoothstep（带顶 0 → 带底 1），带底与主窗像素在 alpha 上无缝衔接。
func fadePremultiplyBand(src *image.RGBA, w, bandH int, out []byte) bool {
	if w <= 0 || bandH <= 0 || src == nil || src.Bounds().Dx() < w || src.Bounds().Dy() < bandH {
		return false
	}
	if len(out) < w*bandH*4 {
		return false
	}
	_ = lutForever // 确保 LUT 已初始化
	content := false
	for y := 0; y < bandH; y++ {
		t := (float64(y) + 0.5) / float64(bandH)
		g := t * t * (3 - 2*t) // smoothstep
		inOff := src.PixOffset(0, y)
		rowIn := src.Pix[inOff : inOff+w*4]
		rowOut := out[y*w*4 : (y+1)*w*4]
		for i := 0; i < w; i++ {
			o := i * 4
			a := rowIn[o+3] // image.RGBA = RGBA 字节序
			if a == 0 {
				rowOut[o], rowOut[o+1], rowOut[o+2], rowOut[o+3] = 0, 0, 0, 0
				continue
			}
			al := float64(a) / 255
			av := al * g
			aOut := byte(math.Round(av * 255))
			if aOut == 0 {
				rowOut[o], rowOut[o+1], rowOut[o+2], rowOut[o+3] = 0, 0, 0, 0
				continue
			}
			// 反预乘还原线性色，再按目标空间重新预乘（per channel）。
			for ch := 0; ch < 3; ch++ {
				c := rowIn[o+ch]
				colorLin := decodeLUT[c] / al
				if colorLin > 1 {
					colorLin = 1
				}
				cout := encodeLUTAt(colorLin) * av
				rowOut[o+(2-ch)] = byte(math.Round(cout * 255)) // R,G,B → BGRA
			}
			rowOut[o+3] = aOut
			content = true
		}
	}
	return content
}

// fadeFrame 主帧之后执行（runWindow 调）：headless 同布局重渲 → 裁带 → 渐变预乘 →
// 提交 overlay。非 Windows（hwnd=0）或离屏上下文创建失败时静默降级。
func (u *UI) fadeFrame() {
	if u.hwnd == 0 || u.frameSize.X <= 0 || u.frameSize.Y <= 0 {
		return
	}
	bandPx := u.frameMetric.Dp(fadeBandDp)
	if bandPx <= 0 || bandPx > u.frameSize.Y {
		bandPx = u.frameSize.Y
	}
	if err := u.fade.ensure(u.frameSize.X, u.frameSize.Y, u.frameMetric); err != nil {
		return // 无 GPU 后端等：淡出降级为硬切（形裁仍生效）
	}
	if err := u.fade.render(u.layout); err != nil {
		return
	}
	if u.fadeBuf == nil || len(u.fadeBuf) < u.frameSize.X*bandPx*4 {
		u.fadeBuf = make([]byte, u.frameSize.X*bandPx*4)
	}
	if !fadePremultiplyBand(u.fade.img, u.frameSize.X, bandPx, u.fadeBuf) {
		overlaySetVisible(false) // 带内无内容：隐藏（桌面/下层直接可见）
		return
	}
	overlayPresent(u.x, u.y, int32(u.frameSize.X), int32(bandPx), u.fadeBuf, semiAlpha)
}
