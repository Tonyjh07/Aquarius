package uigui

// fade.go —— 淡出带 + 边缘羽化 overlay（D44/§15.3 + D45/§15.1）：headless 离屏渲染
// 同一布局 → 整窗效果层（带内垂直渐变 + 带外元素羽化环）→ 预乘转换 → UpdateLayeredWindow。
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

// writePremul 单像素转换（带内渐变与羽化环共用同一口径）：源 = headless 语义
// （A 直通 alpha、C = sRGB_encode(线性预乘值)），目标 alpha = av（0..1）→ 输出
// 字节空间预乘 BGRA（out[oi..oi+3]）。返回是否写入非透明像素。
func writePremul(src []byte, si int, av float64, out []byte, oi int) bool {
	a := src[si+3]
	if a == 0 || av <= 0 {
		out[oi], out[oi+1], out[oi+2], out[oi+3] = 0, 0, 0, 0
		return false
	}
	al := float64(a) / 255
	aOut := int(math.Round(av * 255))
	if aOut == 0 {
		out[oi], out[oi+1], out[oi+2], out[oi+3] = 0, 0, 0, 0
		return false
	}
	if aOut > 255 {
		aOut = 255
	}
	for ch := 0; ch < 3; ch++ {
		colorLin := decodeLUT[src[si+ch]] / al
		if colorLin > 1 {
			colorLin = 1
		}
		out[oi+(2-ch)] = byte(math.Round(encodeLUTAt(colorLin) * av * 255)) // R,G,B → BGRA
	}
	out[oi+3] = byte(aOut)
	return true
}

// fadePremultiplyBand 把源图淡出带 [0, bandH) 行转成 UpdateLayeredWindow 用的
// 预乘 BGRA（写入 out 顶部 bandH 行），返回带内是否有可见内容。
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
			al := float64(rowIn[i*4+3]) / 255
			if writePremul(rowIn, i*4, al*g, rowOut, i*4) {
				content = true
			}
		}
	}
	return content
}

// rrectSD 点到圆角矩形填充区的有符号距离（外正内负；iq 标准 SDF，px/py 为像素中心）。
func rrectSD(px, py, cx, cy, hw, hh, rad float32) float32 {
	ax, ay := px-cx, py-cy
	if ax < 0 {
		ax = -ax
	}
	if ay < 0 {
		ay = -ay
	}
	qx := ax - (hw - rad)
	qy := ay - (hh - rad)
	mx, my := qx, qy
	if mx < 0 {
		mx = 0
	}
	if my < 0 {
		my = 0
	}
	out := float32(math.Sqrt(float64(mx*mx + my*my)))
	in := qx
	if qy > in {
		in = qy
	}
	if in > 0 {
		in = 0
	}
	return out + in - rad
}

// fadeFeatherShapes 元素边缘羽化环（D45/§15.1）：overlay 沿元素**真实轮廓**画 alpha
// 斜坡——轮廓内 (in+1)px 内 alpha=1（盖住 region 内缩留下的缝），向外 outPx smoothstep
// 衰减到 0；颜色取同帧内侧像素（headless 无 region 裁剪、内容完整，夹进形状内 1px 作
// 最近内侧点近似）。只落笔在 y ≥ 带底（带内由 fadePremultiplyBand 单绘——避免与带
// 重复叠加出横缝）且落在元素可见裁剪区内的像素。真轮廓保证环不沿视口/带裁切线描边
// （消除带底横缝）。返回是否写入环像素。
func fadeFeatherShapes(src *image.RGBA, shapes []drawShape, bandPx, inPx, outPx int, out []byte, size image.Point) bool {
	if src == nil || len(shapes) == 0 || outPx <= 0 {
		return false
	}
	w, h := size.X, size.Y
	if w <= 0 || h <= 0 || src.Bounds().Dx() < w || src.Bounds().Dy() < h {
		return false
	}
	if len(out) < w*h*4 {
		return false
	}
	_ = lutForever
	inF := float32(inPx) + 1 // 边界内覆盖宽（+1 与 region 内缩重叠 1px，防漏缝）
	outF := float32(outPx)
	content := false
	for _, s := range shapes {
		r := s.outline
		if r.Empty() {
			continue
		}
		hw, hh := float32(r.Dx())/2, float32(r.Dy())/2
		rad := float32(s.radius)
		if rad > hw {
			rad = hw
		}
		if rad > hh {
			rad = hh
		}
		cx := float32(r.Min.X) + hw
		cy := float32(r.Min.Y) + hh
		clip := s.clip
		if clip.Empty() {
			clip = image.Rectangle{Max: size}
		}
		// 落笔范围 = 真轮廓外扩 ∩ 可见裁剪区 ∩ 带底以下 ∩ 缓冲区。
		x0 := max(r.Min.X-int(outF)-1, max(clip.Min.X, 0))
		x1 := min(r.Max.X+int(outF)+1, min(clip.Max.X, w))
		y0 := max(r.Min.Y-int(outF)-1, max(max(clip.Min.Y, bandPx), 0))
		y1 := min(r.Max.Y+int(outF)+1, min(clip.Max.Y, h))
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				d := rrectSD(float32(x)+0.5, float32(y)+0.5, cx, cy, hw, hh, rad)
				if d < -inF || d > outF {
					continue
				}
				av := 1.0
				if d > 0 {
					td := float64(d) / float64(outF)
					g := td * td * (3 - 2*td) // smoothstep
					av = 1 - g
				}
				// 颜色采样：夹进形状内 1px（最近内侧点近似），再夹进缓冲区（轮廓可越窗）。
				sx, sy := x, y
				if sx < r.Min.X+1 {
					sx = r.Min.X + 1
				}
				if sx > r.Max.X-1 {
					sx = r.Max.X - 1
				}
				if sy < r.Min.Y+1 {
					sy = r.Min.Y + 1
				}
				if sy > r.Max.Y-1 {
					sy = r.Max.Y - 1
				}
				if sx < 0 {
					sx = 0
				}
				if sx >= w {
					sx = w - 1
				}
				if sy < 0 {
					sy = 0
				}
				if sy >= h {
					sy = h - 1
				}
				if writePremul(src.Pix, src.PixOffset(sx, sy), av, out, (y*w+x)*4) {
					content = true
				}
			}
		}
	}
	return content
}

// fadeFrame 主帧之后执行（runWindow 调）：headless 同布局重渲 → 整窗效果层
// （带内渐变 + 带外羽化环，D44+D45）→ 整窗预乘 BGRA → overlay 提交。
// 非 Windows（hwnd=0）或离屏上下文创建失败时静默降级。
func (u *UI) fadeFrame() {
	if u.hwnd == 0 || u.frameSize.X <= 0 || u.frameSize.Y <= 0 {
		return
	}
	if !mainVisible() { // 主窗隐藏：overlay 不得孤立上屏（hideMain 已藏，这里兜底）
		overlaySetVisible(false)
		return
	}
	bandPx := u.frameMetric.Dp(fadeBandDp)
	if bandPx <= 0 || bandPx > u.frameSize.Y {
		bandPx = u.frameSize.Y
	}
	if err := u.fade.ensure(u.frameSize.X, u.frameSize.Y, u.frameMetric); err != nil {
		return // 无 GPU 后端等：淡出降级为硬切（形裁仍生效）
	}
	u.inFadePass = true // 淡出源：跳过兜底底色（带内无消息 = 全透明，overlay 隐藏）
	err := u.fade.render(u.layout)
	u.inFadePass = false
	if err != nil {
		return
	}
	size := u.frameSize.X * u.frameSize.Y * 4
	if u.fadeBuf == nil || len(u.fadeBuf) < size {
		u.fadeBuf = make([]byte, size)
	}
	clear(u.fadeBuf) // 整窗效果层：band 渐变写顶部带、羽化环写其余区域，先清零
	band := fadePremultiplyBand(u.fade.img, u.frameSize.X, bandPx, u.fadeBuf)
	rings := fadeFeatherShapes(u.fade.img, u.shapes, bandPx,
		u.frameMetric.Dp(featherInDp), u.frameMetric.Dp(featherOutDp), u.fadeBuf, u.frameSize)
	if !band && !rings {
		overlaySetVisible(false) // 带内无内容且无元素（空态）：隐藏（桌面/下层直接可见）
		return
	}
	x, y := u.x, u.y
	if rc, ok := windowRectPx(); ok { // 实际窗口矩形优先（防自跟踪位置失联）
		x, y = rc.left, rc.top
	}
	overlayPresent(x, y, int32(u.frameSize.X), int32(u.frameSize.Y), u.fadeBuf, semiAlpha)
}
