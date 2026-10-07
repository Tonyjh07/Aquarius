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
	"strings"
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

// featherDisabled 羽化对比开关（D68）：设 AQUARIUS_NO_FEATHER=1（或 true/yes/on，
// 大小写不敏感）启动即禁用元素边缘羽化（形状边缘 = SDF 硬切，无渐隐带），用于同一
// 二进制下羽化与否的 A/B 观测；不设或设 0/false/off = 羽化照常（生产默认）——按值
// 解析，非空即禁用会把显式的「0」误判为禁用。启动读一次，运行期不变。
var featherDisabled = envBool("AQUARIUS_NO_FEATHER")

// envBool 环境布尔：真值 = 1/true/yes/on（大小写不敏感）；0/false/off/空/未设为假。
func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func init() {
	if featherDisabled {
		fmt.Println("[fade] 羽化已禁用（AQUARIUS_NO_FEATHER）——ULW 换壳后羽化收益对比观测")
	}
}

// fadeBands 淡出带几何（窗口坐标、物理 px；零值 = 两带皆无）：
//   - [top, bottom) 顶带（D54 揭示带 / §15.3 静息顶带）：y < top 不可见，带内 0 → 1；
//   - [lowTop, lowEnd) 底部矮带（D79）：带内 1 → 0 渐隐到带底（= 转写区底），带下
//     恢复 1（状态行/输入栏在带之下、不受淡化）；lowEnd <= lowTop 即无底带
//     （收起态与零值调用皆走此口径）。**作用域按 clip 界定**：只乘 `clip 底 ≤ 带底`
//     的形状（= 转写视口登记的滚动内容）——chrome 形状（悬浮 tips/状态行/输入栏，
//     整窗 clip）几何上跨进带内也保持 g=1。
type fadeBands struct {
	top, bottom    int
	lowTop, lowEnd int
}

// fadeFrame 全帧合成（D62 单通道）：headless 内容 + 元素形状 → 整窗预乘 BGRA，
// av = vis(形状) × g(y) × 内容alpha 一次写入。
//   - vis = 形状覆盖度：核心（d ≤ -(fw+0.5)）= 1；边带沿**真轮廓**（未按视口/带裁剪——
//     不沿裁切线描边，跨带形状不留横缝）smoothstep 渐隐到 featherEdgeMin（D45–D48
//     剖面；只在形状内落笔、不向外外扩——旧向外环在 d=0 有折点 →「饱和核心 + 外圈
//     亮带」，已否决）；轮廓外 = 0（等价旧形裁裁剪语义）。边带内缩界沿用 D47 标定
//     （fw+0.5：GDI 区块边界落在 SDF 零线内 0.5px）——渐隐首像素 alpha≈1，无 1px
//     空洞/亮线；fw=0 的退化元素按无羽化处理（核心即轮廓）。
//   - g(y) = 双带渐变 smoothstep：顶带**带顶 0 → 带底 1**（带顶以上 av=0 不可见——
//     D54 揭示带/§15.3 顶带同式，取代旧「带整带挖空 + 带内单绘」两段机制）；底带
//     **反向 1 → 0** 收零于带底（= 转写区底，D79），带下恢复 1——断点两侧无同形状
//     跨越（转写形状裁剪上沿即带底、状态行/输入栏形状自带底起），故不留横缝；且底带
//     **只作用于转写视口登记的形状**（clip 底 ≤ 带底），chrome 跨带也保持 g=1。
//   - 形状按 record 顺序落笔（= 绘制顺序），重叠处后者覆盖 → z 序与单遍渲染一致。
//
// src 不可用（nil 或尺寸小于本帧）时用元素底色兜底（writePremulFill：形状可见
// 可点、无文字）。返回是否写入非零像素（false = 全透明帧）。
func fadeFrame(src *image.RGBA, shapes []drawShape, b fadeBands,
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
		// D83：核心上界 = -(fw+0.5)（D47 内缩标定），渐隐跨度 = fw+0.5 —— 与本节文档
		// 同式。旧式 `-inF + 1` 把跨度压到 fw−0.5：1× 缩放（PxPerDp=1）下 fw =
		// round(Dp(48)×0.03) = 1 → 跨度 0.5px，而直边带内唯一的像素中心恰在 d=-0.5
		// = d0 → t=0 → vis≡1，整条边全不透明（用户实测：100% 缩放下羽化彻底消失）。
		d0 := -inF
		span := -d0 // 渐隐跨度
		feather := fw > 0 && span > 0 && span < 1e6 && !featherDisabled
		// 落笔范围 = 真轮廓 ∩ 可见裁剪区 ∩ 缓冲区（渐隐全在形状内，不向轮廓外扩）。
		x0 := max(r.Min.X, max(clip.Min.X, 0))
		x1 := min(r.Max.X, min(clip.Max.X, w))
		y0 := max(r.Min.Y, max(clip.Min.Y, 0))
		y1 := min(r.Max.Y, min(clip.Max.Y, h))
		// D79 底带作用域：只乘**转写视口登记**的形状（clip 底 ≤ 带底 = 转写区底）——
		// chrome 形状（悬浮 tips/状态行/输入栏，均整窗 clip）几何上可跨进带内，一律 g=1。
		lowHere := clip.Max.Y <= b.lowEnd
		for y := y0; y < y1; y++ {
			g := 1.0
			if y < b.top {
				continue // D54：带顶以上不可见（揭示带全隐段）
			} else if y < b.bottom {
				t := (float64(y-b.top) + 0.5) / float64(b.bottom-b.top)
				g = t * t * (3 - 2*t)
			}
			if lowHere && b.lowTop <= y && y < b.lowEnd {
				// D79 底带：1 → 0（首像素≈1、末像素≈0，与带外两侧连续）。
				// 进入此分支必有 span = lowEnd−lowTop ≥ 1，无除零。
				t := (float64(b.lowEnd-y) - 0.5) / float64(b.lowEnd-b.lowTop)
				if t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
				g *= t * t * (3 - 2*t)
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

// clampedLowBand 底部矮带（D79 纯读，可测）[top, end)：夹到当前窗口内，end ≤ top 即关。
// 展开路径写 [transH−带高, transH)——transH 低于带高（极矮窗）时带高已在布局侧夹
// ≤ transH/4（D90 bandHeightsClamped，本处夹取保留为下界防御），不再整片渐隐；
// 收起态写 size.Y,size.Y → 夹后仍空。
func (u *UI) clampedLowBand() (top, end int) {
	top, end = u.bandLowTop, u.bandLowEnd
	h := u.frameSize.Y
	if top < 0 {
		top = 0
	}
	if top > h {
		top = h
	}
	if end < 0 {
		end = 0
	}
	if end > h {
		end = h
	}
	if end < top {
		end = top
	}
	return top, end
}

// fadeCompose 合成阶段（frame 调，**在 commit/submit/present 之前**）：headless 同布局
// 重渲 → 整窗位图（av = vis × g(y) × alpha，D62 全帧合成）。不改屏幕态——它是一帧里
// 唯一的慢段（离屏 GPU 重渲 + 预乘），先跑完，之后的移窗/提交/ULW 才各自落地。
// 返回位图是否可上屏（false = 降级：非 Windows / hwnd 未到 / 主窗隐藏 / 离屏渲染失败，
// fadePresent 保持上一帧位图）。主窗隐藏在此判定（隐藏期间不提交，防被唤回）——唯启动
// 揭示期例外（revealPending，D78：隐藏是我方所为，须照常合成提交否则永不首帧）。
func (u *UI) fadeCompose() bool {
	if u.hwnd == 0 || u.frameSize.X <= 0 || u.frameSize.Y <= 0 {
		return false
	}
	if !u.presentable() {
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
	// D54 消息揭示带 + D79 底部矮带：重渲刚把四端按当帧 msgP/转写区底写好（与主窗那遍
	// 同帧同值），就地读取保证与 u.shapes 同源；夹到窗口内，静息即 §15.3 顶带
	// [0, Dp(fadeBandDp)) + 底带 [transH−Dp(fadeBandBottomDp), transH)。
	top, bottom := u.clampedBand()
	lowTop, lowEnd := u.clampedLowBand()
	u.fadeEmpty = !fadeFrame(u.fade.img, u.shapes,
		fadeBands{top: top, bottom: bottom, lowTop: lowTop, lowEnd: lowEnd},
		u.frameMetric, u.fadeBuf, u.frameSize)
	return true
}

// pinWheelCursor 滚轮手势钉点（D71）：手势期间把光标所在帧像素 alpha 顶到 ≥1——
// 分层窗逐像素命中以 alpha==0 为穿透（spike/wheelpcap 实证：alpha=1 即送达、
// SetCapture 不改滚轮路由），光标停在透明间隙时滚轮仍路由到本窗 → 跨间隙连续可滚。
// 只钉 A==0 像素（既有可见内容不改写）；RGB=0 合法预乘，观感 ≤1/255 不可见。
// 光标在帧外（窗移位让出光标下方）不落笔：命中自然失效，手势随 layout 下帧复评解除。
func pinWheelCursor(data []byte, size image.Point, winX, winY int32, cur point) {
	if size.X <= 0 || size.Y <= 0 || len(data) < size.X*size.Y*4 {
		return
	}
	fx, fy := int(cur.x-winX), int(cur.y-winY)
	if fx < 0 || fy < 0 || fx >= size.X || fy >= size.Y {
		return
	}
	oi := (fy*size.X + fx) * 4
	if data[oi+3] != 0 {
		return
	}
	data[oi+0], data[oi+1], data[oi+2], data[oi+3] = 0, 0, 0, 1
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
		// D88 取证：揭示期合成失败节流留证（冷启动一次性竞态无法现场复现——
		// hwnd/尺寸/GPU 任一未就绪都会走到这里，下次出现直接有据可查）。
		if u.revealPending.Load() {
			fadeStallN++
			if fadeStallN <= 8 {
				msg := fmt.Sprintf("[fade] 揭示期合成未就绪 x%d: hwnd=%x size=%dx%d presentable=%v",
					fadeStallN, u.hwnd, u.frameSize.X, u.frameSize.Y, u.presentable())
				fmt.Println(msg)
			}
		}
		return
	}
	x, y := u.x, u.y
	if rc, ok := u.windowRect(); ok { // 实际窗口矩形优先（防自跟踪位置失联）
		if int(rc.right-rc.left) != u.frameSize.X || int(rc.bottom-rc.top) != u.frameSize.Y {
			return // 尺寸竞态：跳过一帧，下帧对齐后提交（防 ULW 拉伸位图/改窗尺寸）
		}
		x, y = rc.left, rc.top
	}
	if u.wheelCap { // D71 滚轮手势钉点（提交前落笔：位图 alpha 即形状/命中/穿透）
		pinWheelCursor(u.fadeBuf, u.frameSize, x, y, u.cursorPos())
	}
	if u.plat.Present(x, y, int32(u.frameSize.X), int32(u.frameSize.Y), u.fadeBuf, u.alpha) && u.revealPending.Swap(false) {
		u.plat.RevealWindow(u.hwnd) // D78：首帧 ULW 已提交 → 揭示（显示 + 激活前台），首个可见帧带内容
		u.representAfterShow()      // D88：揭示后可见态立即补提交（防 DWM 露 Gio 不透明表面）
	}
}

// representAfterShow 揭示/呼出后的可见态补提交（D88，D115 加固）：隐藏期间
// presentable=false 不提交 ULW，ShowWindow 重显瞬间 DWM 可能呈现 Gio 的重定向表面
// （事件 pass 恒涂兜底底色 → 整幅不透明），直到下一次 ULW 才恢复——D88 的单次补提交
// 在展示过渡期可能被吞，而静默零帧（D50）下「下一次」可能久到以分钟计（用户实机复现：
// 启动后整幅不透明，直至停靠动画催生帧才自愈）。加固为梯次：① 立即补提交一次（D88
// 原机制，同步、窗口线程）；② Invalidate 催一帧走完整 compose→ULW（实测自愈路径）；
// ③ representLadder 各拍再补提交，任一拍发现主窗已再隐藏（visMain）即止。位图/矩形
// 口径同 fadePresent（尺寸与实测矩形不符跳帧防拉伸）；跨 goroutine 读 u.frameSize/
// fadeBuf/x/y/alpha 沿 showMain 既有口径（整型/字节/指针读，无撕裂）。
func (u *UI) representAfterShow() {
	u.representOnce(u.plat.Present)
	if u.w != nil {
		u.w.Invalidate() // 并发安全（Gio）；催一帧让常规提交路径接管
	}
	// 梯次三件套在 spawn 前捕获（go 语句给 happens-before）：goroutine 只碰捕获值与
	// u 字段，不迟到读平台对象（-race 口径）；测试把 representLadder 置空即不启 goroutine。
	delays, vis, present := representLadder, u.plat.Visible, u.plat.Present
	if len(delays) == 0 {
		return
	}
	go func() {
		for _, d := range delays {
			time.Sleep(d)
			if !vis() {
				return
			}
			u.representOnce(present)
		}
	}()
}

// representOnce 以最后一次合成位图按 fadePresent 同口径提交一次（present 经窗口线程，
// 铁律 1）。提交函数作参数传：梯次 goroutine 用 spawn 前捕获的槽值，不迟到读全局。
func (u *UI) representOnce(present func(x, y, w, h int32, bits []byte, alpha byte) bool) {
	if u.fadeBuf == nil || u.frameSize.X <= 0 || u.frameSize.Y <= 0 {
		return
	}
	x, y := u.x, u.y
	if rc, ok := u.windowRect(); ok {
		if int(rc.right-rc.left) != u.frameSize.X || int(rc.bottom-rc.top) != u.frameSize.Y {
			return
		}
		x, y = rc.left, rc.top
	}
	present(x, y, int32(u.frameSize.X), int32(u.frameSize.Y), u.fadeBuf, u.alpha)
}

// representLadder 揭示后补提交梯次（D115）：每次延迟一拍；测试置空防泄漏 goroutine
// 在测试结束后继续提交（-race）。
var representLadder = []time.Duration{
	40 * time.Millisecond, 120 * time.Millisecond, 250 * time.Millisecond,
}

// fadeStallN 揭示期合成失败计数（D88 取证日志节流；帧循环 goroutine 独占）。
var fadeStallN int

// 【S4b】原 presentMain / visMain / revealMain 三个包级注入槽已退役：平台面迁入
// `platform` 包后，测试改为注入假平台实现（`u.plat`），生产经 `*platform.Plat`
// （编译期断言见 platform_api.go）。

// presentable 合成/提交可否进行（D78）：主窗隐藏时不提交（HideWindow 语义——防隐藏后仍有
// 帧把位图唤回）；唯启动揭示期例外（revealPending：隐藏是我方所为，须照常合成提交，
// 否则永不首帧）。
func (u *UI) presentable() bool {
	return u.revealPending.Load() || u.plat.Visible()
}
