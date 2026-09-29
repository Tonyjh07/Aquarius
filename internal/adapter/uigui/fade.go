package uigui

// fade.go —— 全帧合成（D62 单通道）：headless 离屏渲染同一布局 → 整窗位图
// （av = vis(形状) × g(y) × 内容alpha：元素边缘羽化 + 顶部/揭示带渐变）→ 预乘转换 →
// 主窗 UpdateLayeredWindow。D44–D59 期的「形裁 + LWA_ALPHA + 独立 overlay 窗」
// 三通道随之退役。
//
// 承重验证（spike/headless，§15.6）：headless 清屏 = 透明（alpha 通道 = 内容覆盖，
// 免布局掩码）；零值 Source 纯渲染不 panic；PxPerDp 可控（对齐主窗 DPI）；
// 像素语义 = 预乘线性 + sRGB 存储（此处转为 ULW 所需的字节空间预乘）。

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// fadeState headless 离屏渲染状态（全帧像素源，事件循环 goroutine 独占）。
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
// 在形状内完成（真实轮廓处 alpha = featherEdgeMin = 0）。
const featherEdgeMin = 0.0

// featherDisabled 羽化对比开关（D62 后评估）：设 AQUARIUS_NO_FEATHER=1 启动即禁用
// 元素边缘羽化（形状边缘 = SDF 硬切，无渐隐带），用于同一二进制下羽化与否的 A/B
// 观测；不设 = 羽化照常（生产默认）。启动读一次，运行期不变。
var featherDisabled = os.Getenv("AQUARIUS_NO_FEATHER") != ""

func init() {
	if featherDisabled {
		fmt.Println("[fade] 羽化已禁用（AQUARIUS_NO_FEATHER）——ULW 换壳后羽化收益对比观测")
	}
}

// fadeFrame 全帧合成（D62 单通道）：headless 内容 + 元素形状 → 整窗预乘 BGRA，
// av = vis(形状) × g(y) × 内容alpha 一次写入。
//   - vis = 形状覆盖度：核心（d ≤ -(fw+0.5)）= 1；边带沿**真轮廓**（未按视口/带裁剪——
//     不沿裁切线描边，跨带形状不留横缝）smoothstep 渐隐到 featherEdgeMin（D45–D48
//     剖面；只在形状内落笔、不向外外扩——旧向外环在 d=0 有折点 →「饱和核心 + 外圈
//     亮带」，已否决）；轮廓外 = 0（等价旧形裁裁剪语义）。边带内缩界沿用 D47 标定
//     （fw+0.5：GDI 区块边界落在 SDF 零线内 0.5px）——渐隐首像素 alpha≈1，无 1px
//     空洞/亮线；fw=0 的退化元素按无羽化处理（核心即轮廓）。
//   - g(y) = 带渐变 smoothstep（带顶 0 → 带底 1；带顶以上 av=0 不可见——D54 揭示带
//     /§15.3 顶带同式，取代旧「带整带挖空 + 带内单绘」两段机制）。
//   - 形状按 record 顺序落笔（= 绘制顺序），重叠处后者覆盖 → z 序与单遍渲染一致。
//
// src 不可用（nil 或尺寸小于本帧）时用元素底色兜底（writePremulFill：形状可见
// 可点、无文字）。返回是否写入非零像素（false = 全透明帧）。
func fadeFrame(src *image.RGBA, shapes []drawShape, bandTop, bandBottom int,
	m unit.Metric, out []byte, size image.Point) bool {
	w, h := size.X, size.Y
	if w <= 0 || h <= 0 || len(shapes) == 0 || len(out) < w*h*4 {
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
		inF := float32(fw) + 0.5
		d0 := -inF + 1 // 渐隐首像素：alpha=1（与核心连续）
		span := -d0    // 渐隐跨度
		feather := fw > 0 && span > 0 && span < 1e6 && !featherDisabled
		// 落笔范围 = 真轮廓 ∩ 可见裁剪区 ∩ 缓冲区（渐隐全在形状内，不向轮廓外扩）。
		x0 := max(r.Min.X, max(clip.Min.X, 0))
		x1 := min(r.Max.X, min(clip.Max.X, w))
		y0 := max(r.Min.Y, max(clip.Min.Y, 0))
		y1 := min(r.Max.Y, min(clip.Max.Y, h))
		for y := y0; y < y1; y++ {
			g := 1.0
			if y < bandTop {
				continue // D54：带顶以上不可见（揭示带全隐段）
			} else if y < bandBottom {
				t := (float64(y-bandTop) + 0.5) / float64(bandBottom-bandTop)
				g = t * t * (3 - 2*t)
			}
			for x := x0; x < x1; x++ {
				d := rrectSD(float32(x)+0.5, float32(y)+0.5, cx, cy, hw, hh, rad)
				if d > 0 {
					continue // 轮廓外（不外扩）
				}
				vis := 1.0
				if feather && d > -inF {
					t := (float64(d) - float64(d0)) / float64(span)
					if t < 0 {
						t = 0
					} else if t > 1 {
						t = 1
					}
					vis = featherEdgeMin + (1-featherEdgeMin)*(1-t*t*(3-2*t))
				}
				av := vis * g
				if av <= 0 {
					continue
				}
				oi := (y*w + x) * 4
				if src != nil {
					si := src.PixOffset(x, y)
					a := src.Pix[si+3]
					if a == 0 {
						continue // 本形状在此无内容：不落笔（保留更早形状的同位像素）
					}
					if writePremul(src.Pix, si, av*float64(a)/255, out, oi) {
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

// clampedBand 当前帧淡出带 [top, bottom)（D54 纯读，可测）：把 layout 写入的带范围夹到
// 当前窗口内（带顶不越窗、带底不越过带顶、不出窗）。静息 = §15.3 顶带 [0, Dp(fadeBandDp))。
func (u *UI) clampedBand() (top, bottom int) {
	top, bottom = u.bandTop, u.bandBottom
	if top < 0 {
		top = 0
	}
	if h := u.frameSize.Y; top > h {
		top = h
	}
	if bottom < top {
		bottom = top
	}
	if h := u.frameSize.Y; bottom > h {
		bottom = h
	}
	return top, bottom
}

// fadeCompose 合成阶段（frame 调，**在 commit/submit/present 之前**）：headless 同布局
// 重渲 → 整窗位图（av = vis × g(y) × alpha，D62 全帧合成）。不改屏幕态——它是一帧里
// 唯一的慢段（离屏 GPU 重渲 + 预乘），先跑完，之后的移窗/提交/ULW 才各自落地。
// 返回位图是否可上屏（false = 降级：非 Windows / hwnd 未到 / 主窗隐藏 / 离屏渲染失败，
// fadePresent 保持上一帧位图）。主窗隐藏在此判定（隐藏期间不提交，防被唤回）。
func (u *UI) fadeCompose() bool {
	if u.hwnd == 0 || u.frameSize.X <= 0 || u.frameSize.Y <= 0 {
		return false
	}
	if !mainVisible() {
		return false
	}
	if err := u.fade.ensure(u.frameSize.X, u.frameSize.Y, u.frameMetric); err != nil {
		return false // 无 GPU 后端等：保持上一帧（主窗 GPU 路径同样不可用，见 D62 降级段）
	}
	u.inFadePass = true // 淡出源：跳过兜底底色（位图以 alpha=0 表达间隙，不画窗底）
	err := u.fade.render(u.layout)
	u.inFadePass = false
	if err != nil {
		return false
	}
	size := u.frameSize.X * u.frameSize.Y * 4
	if u.fadeBuf == nil || len(u.fadeBuf) < size {
		u.fadeBuf = make([]byte, size)
	}
	clear(u.fadeBuf)
	// D54 消息揭示带：重渲刚把 bandTop/bandBottom 按当帧 msgP 写好（与主窗那遍同帧同值），
	// 就地读取保证与 u.shapes 同源；夹到窗口内，静息即 §15.3 顶带 [0, Dp(fadeBandDp))。
	top, bottom := u.clampedBand()
	u.fadeEmpty = !fadeFrame(u.fade.img, u.shapes, top, bottom, u.frameMetric,
		u.fadeBuf, u.frameSize)
	return true
}

// fadePresent 上屏阶段（frame 调，殿后）：整窗 ULW 提交到主窗（D62 单通道——位图
// alpha 即形状/命中，SourceConstantAlpha = u.alpha 统一半透明；空帧也提交以清除
// 上一帧像素，位图全透明时窗口不可见且点击穿透）。取实测窗口矩形定位（防自跟踪
// 位置失联）；矩形与位图尺寸不符（调整尺寸竞态）跳过本帧防拉伸。经窗口线程（铁律 1）。
func (u *UI) fadePresent(composed bool) {
	if u.hwnd == 0 || u.frameSize.X <= 0 || u.frameSize.Y <= 0 {
		return
	}
	if !composed {
		return
	}
	x, y := u.x, u.y
	if rc, ok := windowRectPx(); ok { // 实际窗口矩形优先（防自跟踪位置失联）
		if int(rc.right-rc.left) != u.frameSize.X || int(rc.bottom-rc.top) != u.frameSize.Y {
			return // 尺寸竞态：跳过一帧，下帧对齐后提交（防 ULW 拉伸位图/改窗尺寸）
		}
		x, y = rc.left, rc.top
	}
	presentMain(x, y, int32(u.frameSize.X), int32(u.frameSize.Y), u.fadeBuf, u.alpha)
}

// presentMain 提交函数槽（测试注入点；生产恒 mainPresent——Windows 实作 / 非 Windows no-op）。
var presentMain = mainPresent
