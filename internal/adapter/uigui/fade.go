package uigui

// fade.go —— 淡出带 + 边缘内容渐隐 overlay（D44/§15.3 + D45–D48/§15.1）：headless 离屏
// 渲染同一布局 → 整窗效果层（带内垂直渐变 + 带外元素自身渐隐）→ 预乘转换 →
// UpdateLayeredWindow。
//
// 承重验证（spike/headless，§15.6）：headless 清屏 = 透明（alpha 通道 = 内容覆盖，
// 免布局掩码）；零值 Source 纯渲染不 panic；PxPerDp 可控（对齐主窗 DPI）；
// 像素语义 = 预乘线性 + sRGB 存储（此处转为 ULW 所需的字节空间预乘）。

import (
	"image"
	"image/color"
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

// writePremul 单像素转换（淡出带与边缘渐隐共用同一口径）：源 = headless 语义
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

// writePremulFill 用元素底色写一个预乘像素——headless 内容不可用（src 缺省、或尺寸小于
// 本帧）时的兜底取色：底色恒定，处处均匀、与形状无关（若在圆角/窄条处盲目外扩采样，
// 采样点会落到形状外的透明区 → writePremul 丢弃 → 直边有边、圆角没有的"十字/阶梯"伪影）。
// fill 为直通 sRGB（A 直通 alpha），av = 渐隐不透明度（0..1）→ 输出字节空间预乘 BGRA。
func writePremulFill(fill color.NRGBA, av float64, out []byte, oi int) bool {
	if av <= 0 || fill.A == 0 {
		out[oi], out[oi+1], out[oi+2], out[oi+3] = 0, 0, 0, 0
		return false
	}
	if fill.A != 0xFF {
		av *= float64(fill.A) / 255
	}
	aOut := int(math.Round(av * 255))
	if aOut == 0 {
		out[oi], out[oi+1], out[oi+2], out[oi+3] = 0, 0, 0, 0
		return false
	}
	if aOut > 255 {
		aOut = 255
	}
	for ch := 0; ch < 3; ch++ {
		lin := decodeLUT[[3]byte{fill.R, fill.G, fill.B}[ch]]
		out[oi+(2-ch)] = byte(math.Round(encodeLUTAt(lin*av) * 255)) // R,G,B → BGRA
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

// featherEdgeMin 轮廓处（d=0）的最低不透明度：0 = 内容在真实轮廓处淡到全透明——渐隐完全
// 收在形状内，**不向外外扩 1px、不堆光晕**（D48）。要让轮廓处留一点可见度可调大。
const featherEdgeMin = 0.0

// fadeFeatherShapes 元素边缘内容渐隐（D45–D48/§15.1）：与顶部淡出带同模型——region 沿真
// 轮廓内缩 featherWidth（主窗不画边带，见 regionShapes），overlay 在让位出的边带内沿真轮廓
// 画「内容自身由内向外渐隐」：从 region 边界的首像素（alpha=1，与主窗像素同不透明度、无缝
// 衔接）起，随 d 向轮廓 smoothstep 衰减到 featherEdgeMin，在 d=0 处收尾。**不向外堆光晕**：
// 旧 D45–D47 的向外环在轮廓线 d=0 有折点（内 alpha=1 平坦、外衰减）→ 观感成「饱和核心 +
// 外圈亮带」，与带边割裂；本式从 region 边界即起衰减、只在形状内落笔，与带底同类。**颜色
// （D47/D48）**：采样同帧 headless 内容（保文字/图标随渐隐自然淡出；只在形状内采样——避开
// 圆角外透明区，无 D47 的"十字/阶梯"），headless 不可用（src 缺省或尺寸不符）时用元素底色
// 兜底。**带内 (y < bandPx) 额外乘淡出带因子 g(y)**：跨带元素在带底的左右边与带下连续
// （带底 g→1），消除带底横缝。只落笔在元素可见裁剪区内。真轮廓保证不沿视口/带裁切线描边。
// 返回是否写入像素。
func fadeFeatherShapes(src *image.RGBA, shapes []drawShape, bandPx int, m unit.Metric, out []byte, size image.Point) bool {
	if len(shapes) == 0 {
		return false
	}
	w, h := size.X, size.Y
	if w <= 0 || h <= 0 || len(out) < w*h*4 {
		return false
	}
	if src != nil && (src.Bounds().Dx() < w || src.Bounds().Dy() < h) {
		src = nil
	}
	_ = lutForever
	content := false
	for _, s := range shapes {
		r := s.outline
		if r.Empty() {
			continue
		}
		fw := featherWidth(m, r.Dx(), r.Dy())
		if fw <= 0 {
			continue
		}
		// region 区块覆盖 d ≤ -(fw+0.5)（GDI 边界落在 SDF 零线内 0.5px，见 regionShapes），
		// 渐隐覆盖其余 d > -(fw+0.5) → 逐像素恰好互补。用 fw+0 会在圆角处留 1px 空洞，
		// 用 fw+1 会在每条真实边内叠 1px 亮线。
		inF := float32(fw) + 0.5
		d0 := -inF + 1 // 渐隐首像素（region 边界外 1px）：alpha=1，与主窗像素同不透明度
		span := -d0    // 渐隐跨度
		if span <= 0 || span >= 1e6 {
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
		// 落笔范围 = 真轮廓 ∩ 可见裁剪区 ∩ 缓冲区（渐隐全在形状内，无需向轮廓外扩；
		// 带内也落笔：跨带元素在带底的左右边要与带下连续，见函数头）。
		x0 := max(r.Min.X, max(clip.Min.X, 0))
		x1 := min(r.Max.X, min(clip.Max.X, w))
		y0 := max(r.Min.Y, max(clip.Min.Y, 0))
		y1 := min(r.Max.Y, min(clip.Max.Y, h))
		for y := y0; y < y1; y++ {
			g := 1.0
			if y < bandPx {
				t := (float64(y) + 0.5) / float64(bandPx)
				g = t * t * (3 - 2*t) // 与 fadePremultiplyBand 同式
			}
			for x := x0; x < x1; x++ {
				d := rrectSD(float32(x)+0.5, float32(y)+0.5, cx, cy, hw, hh, rad)
				if d <= -inF || d > 0 {
					continue // region 区块（主窗画）/ 轮廓外（不外扩）
				}
				t := (float64(d) - float64(d0)) / float64(span)
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
				av := (featherEdgeMin + (1-featherEdgeMin)*(1-t*t*(3-2*t))) * g
				if av <= 0 {
					continue
				}
				oi := (y*w + x) * 4
				if src != nil {
					if writePremul(src.Pix, src.PixOffset(x, y), av, out, oi) {
						content = true
					}
				} else if writePremulFill(s.fill, av, out, oi) {
					content = true
				}
			}
		}
	}
	return content
}

// fadeFrame 主帧之后执行（runWindow 调）：headless 同布局重渲 → 整窗效果层
// （顶部带渐变 + 元素边缘渐隐，D44/D48）→ 整窗预乘 BGRA → overlay 提交。
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
	clear(u.fadeBuf) // 整窗效果层：band 渐变写顶部带、边缘渐隐写元素边带，先清零
	band := fadePremultiplyBand(u.fade.img, u.frameSize.X, bandPx, u.fadeBuf)
	edges := fadeFeatherShapes(u.fade.img, u.shapes, bandPx, u.frameMetric, u.fadeBuf, u.frameSize)
	if !band && !edges {
		overlaySetVisible(false) // 带内无内容且无元素（空态）：隐藏（桌面/下层直接可见）
		return
	}
	x, y := u.x, u.y
	if rc, ok := windowRectPx(); ok { // 实际窗口矩形优先（防自跟踪位置失联）
		x, y = rc.left, rc.top
	}
	overlayPresent(x, y, int32(u.frameSize.X), int32(u.frameSize.Y), u.fadeBuf, u.alpha)
}
