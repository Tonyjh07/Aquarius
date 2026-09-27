package uigui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/gesture"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// 窗口与布局常量（物理 px 经 gtx.Dp 换算——layout 坐标即物理 px，Dp 只换算尺寸）。
const (
	winWidthDp   = 608 // 默认窗宽：16 边距 + 576 三段行 + 16（行宽 = 设计稿，D49）
	winHeightDp  = 460
	winMinWidth  = 420
	winMinHeight = 240

	fadeBandDp   = 56 // 淡出带高（§15.3）
	inputRowDp   = 48 // 输入行元素高（D49 canvas 1:1）＝胶囊高＝logo/send 圆钮直径
	inputGapDp   = 12 // 三段间距与胶囊内元素间距（canvas spacing 12）
	inputPadDp   = 16 // 胶囊左右内边距（canvas padding 16）
	inputIconDp  = 20 // 胶囊内图标槽（canvas 20×20，灰占位不可点）
	pillTopDp    = 8  // 输入行上边距（下方 16）
	sideMarginDp = 16 // 左右边距
	rowGapDp     = 6  // 转写行间距
	bubblePadXDp = 12 // 气泡内边距
	bubblePadYDp = 7
	cardPadXDp   = 10 // 文本行卡内边距
	cardPadYDp   = 5
	radiusDp     = 12 // 气泡圆角
	cardRadiusDp = 8  // 文本行卡圆角
	statusChipDp = 20 // 状态行 chip 高

	// 边缘羽化（D45–D48/§15.1）：把淡出带那套「region 让位 + overlay 单绘」推广到所有
	// 边缘——region 沿元素真实边内缩 featherWidth（主窗不画边带），overlay 在让位出的边带
	// 内沿真轮廓画「内容自身由内向外渐隐」（轮廓处最低、向内升到 1），与顶部淡出带同模型；
	// **不向外堆光晕**（旧 D45–D47 的向外环在轮廓线 d=0 有折点 →「饱和核心 + 外圈亮带」，
	// 与带边观感割裂）。**响应式（D47）**：渐隐带宽按元素短边成比例再夹上下限，随元素尺寸/
	// 窗口缩放/DPI 自适应，不引入固定 px 羽化宽。
	featherRatio = 0.05 // 渐隐带（region 内缩）宽 = min(宽,高) × 该比例
	featherMinDp = 0    // 渐隐宽下限
	featherMaxDp = 5    // 渐隐宽上限

	// semiAlpha 统一半透明（LWA_ALPHA 整窗常量；淡出 overlay 同值衔接，D44）。
	semiAlpha byte = 235

	// dragClickSlackPx 拖窗/单击判定阈值（物理 px）：收起态单击球 = 展开，
	// 位移超阈值 = 拖窗。
	dragClickSlackPx = 4
)

// 主题令牌（§15.4 MVP：品牌色 + 输入栏浅白/浅灰；深浅两版与多预设后补）。
var (
	brandColor = color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF}
	pillBg     = color.NRGBA{R: 0xFA, G: 0xFA, B: 0xFC, A: 0xFF} // 输入栏浅白
	windowBg   = color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF3, A: 0xFF} // 兜底背景（形裁前/未适配平台）
	textDim    = color.NRGBA{R: 0x8A, G: 0x8F, B: 0x98, A: 0xFF}
	textMuted  = color.NRGBA{R: 0x6B, G: 0x70, B: 0x78, A: 0xFF}
	textError  = color.NRGBA{R: 0xD9, G: 0x3A, B: 0x3A, A: 0xFF}
	textNotice = color.NRGBA{R: 0xC0, G: 0x77, B: 0x00, A: 0xFF}
	textSystem = color.NRGBA{R: 0x8E, G: 0x6B, B: 0xC4, A: 0xFF}

	// 行卡底色（全部实色——形裁下元素外无底板，半透明只能整窗 LWA_ALPHA 叠加）。
	cardThinking = color.NRGBA{R: 0xE9, G: 0xEB, B: 0xF0, A: 0xFF}
	cardTool     = color.NRGBA{R: 0xE7, G: 0xEA, B: 0xEF, A: 0xFF}
	cardNotice   = color.NRGBA{R: 0xFD, G: 0xF2, B: 0xDC, A: 0xFF}
	cardError    = color.NRGBA{R: 0xFB, G: 0xE4, B: 0xE4, A: 0xFF}
	cardSystem   = color.NRGBA{R: 0xF1, G: 0xEB, B: 0xFA, A: 0xFF}
	whiteText    = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// 图标槽占位灰（canvas 稿 #94A3B8；配色为占位，D49）。
	iconDim = color.NRGBA{R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF}
	// 确认态置灰右圆（D49：几何不随状态变，只换色）。
	disabledCircle = color.NRGBA{R: 0xD8, G: 0xDC, B: 0xE3, A: 0xFF}
)

// point/rect Win32 坐标对（中性定义：非 Windows 构建仅作占位类型）。
type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }

// drawShape 布局期收集的可见元素：outline = 元素**真实轮廓**（未按视口/淡出带裁剪，
// 边缘渐隐沿此取边 → 不沿裁切线描边，消除横缝）；clip = 可见裁剪区（转写区视口/整窗，
// 渐隐只在此区内落笔）；radius = 圆角半径；fill = 元素自身底色（headless 不可用时的兜底
// 取色——见 writePremulFill）。形裁矩形（applyRegion）与边缘渐隐（fadeFeatherShapes）都由
// outline/clip/radius/fill 推导（§15.3/D45–D48）。
type drawShape struct {
	outline image.Rectangle
	clip    image.Rectangle
	radius  int // 圆角半径（px）
	fill    color.NRGBA
}

// shapePhys 形裁元素（传 win32 并集）。
type shapePhys struct {
	x, y, w, h int32
	ellipse    int32 // CreateRoundRectRgn 椭圆宽高 = 2×半径
	sqTop      bool  // 同 drawShape.sqTop：并集构建时上两角填方
}

// 主窗口句柄与窗口线程入口（win32 补位的两个跨 goroutine 交接点）。
var (
	mainHWND uintptr // atomic 存取：事件循环写、窗口线程读
	win32Run atomic.Pointer[func(func())]
)

// onWindowThread 把修改性 Win32 调用送到 Gio 窗口线程执行（Window.Run）并等待完成。
// 【§15.6 铁律 1，堆栈实证】Gio runLoop 在 deliverEvent 的 select 中服务 driverFuncs，
// 但从客户端协程跨线程 SendMessage（SetWindowPos/SetWindowRgn 等内部回投窗口过程）
// 会永久阻塞——runLoop 停在 select、不泵消息。查询类调用不受此限。
func onWindowThread(f func()) {
	if r := win32Run.Load(); r != nil {
		(*r)(f)
		return
	}
	f() // 窗口未就绪（理论上不发生）：直接执行兜底
}

// 位置记忆（§15.1：拖拽 + 位置记忆，含多显示器工作区夹取；置顶态随存——
// §15.1 置顶开关）。posMu：拖动保存（帧循环）与菜单切换保存（托盘线程）互斥。
type posRec struct {
	X, Y int32
	// TopMost 置顶态；nil = 旧文件/未设置 → 缺省置顶。
	TopMost *bool `json:"top_most,omitempty"`
}

var posMu sync.Mutex

func loadPos(path string) (posRec, bool) {
	posMu.Lock()
	defer posMu.Unlock()
	src, err := os.ReadFile(path)
	if err != nil {
		return posRec{}, false
	}
	var p posRec
	if json.Unmarshal(src, &p) != nil {
		return posRec{}, false
	}
	return p, true
}

func savePos(path string, p posRec) {
	posMu.Lock()
	defer posMu.Unlock()
	src, _ := json.Marshal(p)
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, src, 0o644)
}

// newTheme 主题组装（§15.4 骨架：系统中文字体优先——gofont 无 CJK，spike 实证路径；
// 失败回落 gofont）。
func newTheme() *material.Theme {
	th := material.NewTheme()
	if faces := loadCJKFaces(); len(faces) > 0 {
		th.Shaper = text.NewShaper(text.WithCollection(faces))
	} else {
		th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	}
	return th
}

// loadCJKFaces 加载 Windows 系统中文字体（§15.6 spike 实证：msyh.ttc → opentype）。
func loadCJKFaces() []text.FontFace {
	for _, p := range []string{
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
		`C:\Windows\Fonts\simsun.ttc`,
	} {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(src)
		if err != nil || len(faces) == 0 {
			continue
		}
		return faces
	}
	return nil
}

// runWindow Gio 窗口事件循环（本 goroutine 独占状态机与窗口侧控件）。
// 不调 app.Main：Windows 的 osMain 仅 select{}（Gio 自建带锁线程跑窗口消息），
// 库内调用会把装配根卡死。
func (u *UI) runWindow(w *app.Window) {
	defer close(u.done)
	runFn := w.Run
	win32Run.Store(&runFn) // 修改性 Win32 调用统一走窗口线程（§15.6 铁律 1）
	w.Option(
		app.Title("Aquarius"),
		app.Size(unit.Dp(winWidthDp), unit.Dp(winHeightDp)),
		app.MinSize(unit.Dp(winMinWidth), unit.Dp(winMinHeight)),
		app.Decorated(false), // 无边框 = 悬浮球形态前提（§15.1）
		app.TopMost(true),    // 悬浮球常驻顶层
	)
	u.th = newTheme()
	var ops op.Ops
	for {
		ev := w.Event()
		// 先应用排队消息再渲染（§15.5：每事件全量排空 inbox）。
		if !u.drainInbox() {
			return
		}
		switch e := ev.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			u.layout(gtx)
			e.Frame(&ops)
			u.fadeFrame() // 淡出带：headless 同布局重渲 → 渐变预乘 → ULW（D44）
		case app.DestroyEvent:
			// 用户关窗 = 输入流结束（Next → EOF → 装配根退出，退出码 0；
			// 创建失败 Err 非空 → Next 上抛，退出码 1）。
			// 正常 Alt+F4 已被 WM_CLOSE 拦截为隐藏（§15.1），走到这里 = 真销毁
			//（装配根收尾/系统关闭）——清托盘图标防悬浮区残留。
			trayDelete()
			u.signalEOF(e.Err)
			return
		default:
			if h, ok := u.viewEvent(ev); ok {
				u.onHWND(h)
			}
		}
	}
}

// onHWND Win32ViewEvent 投递的窗口句柄：统一半透明 + 置顶断言 + 位置记忆恢复
// （§15.1/D44）。
func (u *UI) onHWND(h uintptr) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	atomic.StoreUintptr(&mainHWND, h)
	applyAlpha(semiAlpha)  // LWA_ALPHA 整窗常量（与形裁正交，spike 已验证）
	subclassCloseToHide(h) // 关窗（Alt+F4）= 隐藏（§15.1；非 Windows 为 no-op 桩）
	// 置顶断言 + 记忆恢复（§15.1 置顶开关）：缺省置顶、菜单切换态随记忆回来——
	// 本端 SetWindowPos 断言，不依赖 Gio 的 TopMost 应用（实测会意外丢失、原因未明）。
	on := true
	if u.opts.PosFile != "" {
		if p, found := loadPos(u.opts.PosFile); found && p.TopMost != nil {
			on = *p.TopMost
		}
	}
	platformSetTopMost(on)
	rc, ok := windowRectPx()
	if !ok {
		return
	}
	w, ht := rc.right-rc.left, rc.bottom-rc.top
	u.x, u.y = rc.left, rc.top
	if u.opts.PosFile != "" {
		if p, found := loadPos(u.opts.PosFile); found {
			nx, ny := clampToWorkArea(p.X, p.Y, w, ht)
			moveWindowTo(nx, ny)
			u.x, u.y = nx, ny
		}
	}
	// 形裁在首帧布局后按元素矩形重建（applyRegion）。
}

// layout 悬浮窗布局：背景（兜底 + 整窗拖动）| 转写区（手工布局 + 滚动）/ 状态行 /
// 输入栏——自底向上定高，全部绝对坐标登记形裁（§15.1/D44）。
// 本函数也被 fadeFrame 以零值 Source 二次调用（纯渲染，无事件消费）。
func (u *UI) layout(gtx layout.Context) layout.Dimensions {
	if u.focusPending && !u.collapsed {
		u.focusPending = false
		gtx.Execute(key.FocusCmd{Tag: &u.editor}) // 唤出（托盘/快捷键）后焦点进输入栏
	}
	if !u.collapsed {
		u.updateEditor(gtx) // 收起态不消费按键（编辑器不可见，防隐形收字）
	}
	u.updateClicks(gtx)
	u.updateDrag(gtx)
	u.updateLogo(gtx)

	size := gtx.Constraints.Max
	u.frameMetric = gtx.Metric
	u.frameSize = size
	u.shapes = u.shapes[:0]

	if u.collapsed {
		u.layoutCollapsed(gtx, size)
		u.applyRegion()
		return layout.Dimensions{Size: size}
	}

	inputH := gtx.Dp(inputRowDp + pillTopDp + 16) // 输入行 + 上 8 下 16 边距
	statusH := 0
	if u.statusText() != "" {
		statusH = gtx.Dp(statusChipDp + 8)
	}
	transH := size.Y - inputH - statusH
	if transH < 0 {
		transH = 0
	}

	dims := layout.Stack{Alignment: layout.N}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 兜底背景（形裁生效前的首帧、非 Windows 降级形态）+ 整窗拖动区：
			// 形裁后元素间隙点击穿透，实际能落到这里的把手 = 输入栏空白/状态行。
			// 淡出源渲染时跳过底色（inFadePass）：headless 清屏透明 = 带内无消息全透明。
			if !u.inFadePass {
				paint.Fill(gtx.Ops, windowBg)
			}
			st := clip.Rect{Max: size}.Push(gtx.Ops)
			u.drag.Add(gtx.Ops)
			st.Pop()
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if transH > 0 {
				u.transcript(gtx, size.X, transH)
			}
			if statusH > 0 {
				off := op.Offset(image.Pt(0, transH)).Push(gtx.Ops)
				u.statusChip(gtx, size.X, statusH, transH)
				off.Pop()
			}
			off := op.Offset(image.Pt(0, transH+statusH)).Push(gtx.Ops)
			u.inputBar(gtx, size.X, transH+statusH)
			off.Pop()
			return layout.Dimensions{Size: size}
		}),
	)
	u.applyRegion()
	return dims
}

// layoutCollapsed 收起态（§15.1 单组件"左键 logo 收起回球"）：只渲染 logo 悬浮球——
// 球位 = 展开态（无状态行）的 logo 位置（换形不跳动），窗口尺寸不变，球外区域经形裁
// 透明且点击穿透；拖动把手 = 整窗背景层（事件被形裁收到球上），单击球（位移小于
// dragClickSlackPx）再展开、移动则拖窗。
func (u *UI) layoutCollapsed(gtx layout.Context, size image.Point) {
	if !u.inFadePass {
		paint.Fill(gtx.Ops, windowBg)
	}
	dst := clip.Rect{Max: size}.Push(gtx.Ops)
	u.drag.Add(gtx.Ops)
	dst.Pop()

	// 球 = 展开态 logo 圆钮本身（D49 三段式：⌀ = 行高、x = 侧边距、y = 行内 logo 位）
	// → 换形不跳动；窗口尺寸不变，球外区域形裁透明。
	ballD := gtx.Dp(inputRowDp)
	ballX := gtx.Dp(sideMarginDp)
	ballY := size.Y - gtx.Dp(inputRowDp+pillTopDp+16) + gtx.Dp(pillTopDp)
	r := image.Rectangle{
		Min: image.Pt(ballX, ballY),
		Max: image.Pt(ballX+ballD, ballY+ballD),
	}
	drawLogo(gtx, r) // §15.2 logo 实装（品牌色圆钮 + 内嵌图标）
	u.record(r, ballD/2, brandColor, image.Rectangle{Max: u.frameSize})
}

// updateScroll 滚动手势 + 当帧边界钳制 + 尾随（§15.3 流式内容贴底）。
// d>0 = 向下滚（往新内容）；d<0 = 上滚离开底部 → 停止尾随；滚回底部 → 恢复。
// ScrollRange 内部按边界钳制手势距离（含 fling 溢出）。
func (u *UI) updateScroll(gtx layout.Context, viewH, total int) {
	overflow := total - viewH
	if overflow < 0 {
		overflow = 0
	}
	d := u.transcriptScroll.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Vertical,
		pointer.ScrollRange{Min: -u.scrollPx, Max: overflow - u.scrollPx},
		pointer.ScrollRange{})
	u.scrollPx += d
	if u.scrollPx < 0 {
		u.scrollPx = 0
	}
	if u.scrollPx > overflow {
		u.scrollPx = overflow
	}
	if d < 0 {
		u.followTail = false
	} else if d > 0 && u.scrollPx >= overflow {
		u.followTail = true
	}
	if u.followTail {
		u.scrollPx = overflow // 尾随：新内容永远贴底
	}
}

// transcript 转写区：两遍布局（量高 → 滚动定界 → 绘制），**底部锚定**——新消息贴着
// 输入栏出现（§15.1"提交后在胶囊上方出现"），旧消息上滚进顶部淡出带渐隐（§15.3）；
// 尾随贴底，用户上滚即停跟随。手工纵排（需要每行绝对矩形做逐元素形裁，D44）。
func (u *UI) transcript(gtx layout.Context, w, h int) {
	items := u.frameItems()
	if len(items) == 0 {
		// 空态不渲染任何元素（悬浮球只剩输入栏，区域全透；转写浮层"提交后出现"，§15.1）。
		u.contentH = 0
		u.scrollPx = 0
		return
	}
	viewport := image.Rectangle{Max: image.Pt(w, h)}
	st := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	defer st.Pop()
	u.transcriptScroll.Add(gtx.Ops) // 视口滚动手势

	gap := gtx.Dp(rowGapDp)
	// ① 量高（文本只排一次，录宏供绘制复用）。
	rows := make([]measuredRow, len(items))
	total := 0
	for i := range items {
		rows[i] = u.measureRow(gtx, items[i], w)
		total += rows[i].height()
	}
	total += gap * (len(rows) - 1)

	// ② 滚动定界：手势 + 当帧真实内容高（无一帧滞后），尾随贴底。
	u.updateScroll(gtx, h, total)

	// ③ 绘制：底部锚定基线——内容矮时贴底（信息悬在输入栏上方），超出视口后
	// 顶出上沿、上滚进淡出带（base 归零后退化为标准滚动）。
	base := h - total
	if base < 0 {
		base = 0
	}
	y := base - u.scrollPx
	for i := range rows {
		y += u.paintRow(gtx, rows[i], w, y, viewport) + gap
	}
	u.contentH = total
}

// measuredRow 量高后的转写行（文本录宏 + 样式令牌）。
type measuredRow struct {
	txt                op.CallOp
	dims               image.Point
	bg                 color.NRGBA
	radius, padX, padY int
	right              bool
}

// height 行总高（含上下内边距，px）。
func (m measuredRow) height() int { return m.dims.Y + 2*m.padY }

// measureRow 量高 + 取样式（文本录入宏，不在本步落 ops）。
func (u *UI) measureRow(gtx layout.Context, it blockView, w int) measuredRow {
	label, bg, radius, rightAlign, bubble := u.rowStyle(gtx, it)
	padX, padY := gtx.Dp(cardPadXDp), gtx.Dp(cardPadYDp)
	if bubble {
		padX, padY = gtx.Dp(bubblePadXDp), gtx.Dp(bubblePadYDp)
	}
	maxW := w - 2*gtx.Dp(sideMarginDp)
	if maxW < gtx.Dp(80) {
		maxW = gtx.Dp(80)
	}
	cs := gtx
	cs.Constraints = layout.Constraints{Min: image.Point{}, Max: image.Pt(maxW-2*padX, 1<<30)}
	m := op.Record(gtx.Ops)
	dims := label(cs)
	return measuredRow{
		txt: m.Stop(), dims: dims.Size, bg: bg, radius: radius,
		padX: padX, padY: padY, right: rightAlign,
	}
}

// paintRow 绘制底板 + 文本并登记形裁；y 为视口内绝对坐标（可为负）。
// 返回行总高（px）。
func (u *UI) paintRow(gtx layout.Context, mr measuredRow, w, y int, viewport image.Rectangle) int {
	x := gtx.Dp(sideMarginDp)
	if mr.right {
		x = w - gtx.Dp(sideMarginDp) - mr.dims.X - 2*mr.padX
	}
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+mr.dims.X+2*mr.padX, y+mr.height()),
	}
	st := clip.UniformRRect(bgRect, mr.radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, mr.bg)
	inner := op.Offset(image.Pt(bgRect.Min.X+mr.padX, bgRect.Min.Y+mr.padY)).Push(gtx.Ops)
	mr.txt.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect, mr.radius, mr.bg, viewport)
	return bgRect.Dy()
}

// rowStyle 每种块的渲染样式：文本控件、底板色、圆角、是否右对齐、是否气泡。
func (u *UI) rowStyle(gtx layout.Context, it blockView) (layout.Widget, color.NRGBA, int, bool, bool) {
	radiusDp, cardR := gtx.Dp(radiusDp), gtx.Dp(cardRadiusDp)
	switch it.kind {
	case blockUser: // 用户气泡：品牌色底白字、右对齐（§15.3 双色气泡）
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.Color = whiteText
			return s.Layout(gtx)
		}, brandColor, radiusDp, true, true
	case blockAssistant: // 助手气泡：浅白底、左对齐
		return material.Body2(u.th, it.text).Layout, pillBg, radiusDp, false, true
	case blockThinking: // 思考行：头部 + 正文（流式"思考中…" / 定稿"已思考 · Ns"）
		header := "已思考"
		if it.live {
			header = "思考中…"
		} else if it.secs >= 0 {
			header = fmt.Sprintf("已思考 · %ds", it.secs)
		}
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					s := material.Caption(u.th, header)
					s.Color = textDim
					return s.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, it.text)
					s.Color = textDim
					return s.Layout(gtx)
				}),
			)
		}, cardThinking, cardR, false, false
	case blockTool:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(u.th, it.text)
			l.Color = textMuted
			return l.Layout(gtx)
		}, cardTool, cardR, false, false
	case blockNotice:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(u.th, it.text)
			l.Color = textNotice
			return l.Layout(gtx)
		}, cardNotice, cardR, false, false
	case blockError:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(u.th, it.text)
			l.Color = textError
			return l.Layout(gtx)
		}, cardError, cardR, false, false
	case blockSystem:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(u.th, it.text)
			l.Color = textSystem
			return l.Layout(gtx)
		}, cardSystem, cardR, false, false
	default: // blockPlain（Say/命令输出/提示）：底板浅白、正文默认色
		return material.Body2(u.th, it.text).Layout, pillBg, cardR, false, false
	}
}

// statusChip 状态行 chip（§15.1：仅生成时显示"思考中/生成中 · 模型 · 权限档"），
// 右对齐小胶囊（自带底板 → 形裁可见 + 可作拖拽把手）。
func (u *UI) statusChip(gtx layout.Context, w, h, absY int) {
	txt := u.statusText()
	if txt == "" {
		return
	}
	label := func(gtx layout.Context) layout.Dimensions {
		s := material.Caption(u.th, txt)
		s.Color = textMuted
		return s.Layout(gtx)
	}
	m := op.Record(gtx.Ops)
	dims := label(gtx)
	txtOp := m.Stop()
	padX := gtx.Dp(10)
	chipH := gtx.Dp(statusChipDp)
	x := w - gtx.Dp(sideMarginDp) - dims.Size.X - 2*padX
	y := (h - chipH) / 2
	bgRect := image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x+dims.Size.X+2*padX, y+chipH)}
	st := clip.UniformRRect(bgRect, chipH/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, pillBg)
	inner := op.Offset(image.Pt(bgRect.Min.X+padX, bgRect.Min.Y+(chipH-dims.Size.Y)/2)).Push(gtx.Ops)
	txtOp.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), chipH/2, pillBg, image.Rectangle{Max: u.frameSize})
}

// inputBar 输入栏（§15.2 骨架）：logo（拖拽把手）｜编辑器（确认态 = 提示行）｜
// 发送/停止；确认态整体切换为 [允许/拒绝] 按钮组（非模态）。
// inputBar 输入行（D49 三段式，§15.2）：[logo ⌀48] 12 [输入胶囊] 12 [send ⌀48]——三段等高
// 独立成形（各自 record → 形裁/羽化，间隙透明且点击穿透），中间胶囊吃掉全部剩余宽度
// （响应式：窗口宽变化只伸缩它，字号/圆钮尺寸不随窗口变）。坐标原点 = 输入行段左上，
// w = 窗口宽（均经 gtx.Dp 换算为物理 px）。
func (u *UI) inputBar(gtx layout.Context, w, absY int) {
	rowH := gtx.Dp(inputRowDp)
	logo, pill, send := inputRowRects(w, gtx.Dp(pillTopDp), rowH,
		gtx.Dp(inputGapDp), gtx.Dp(sideMarginDp))
	clipRect := image.Rectangle{Max: u.frameSize}

	// logo 圆钮（§15.1 把手含 logo）：悬停启动 tips、拖动移窗、单击收起回球。
	drawLogo(gtx, logo)
	gst := clip.Rect(logo).Push(gtx.Ops)
	u.logoHover.Add(gtx.Ops)
	u.logoDrag.Add(gtx.Ops)
	gst.Pop()
	u.record(logo.Add(image.Pt(0, absY)), rowH/2, brandColor, clipRect)

	// 输入胶囊：底色 + 内容（内边距/图标槽/文字/动作区）。
	paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(pill, rowH/2).Op(gtx.Ops))
	pst := clip.UniformRRect(pill, rowH/2).Push(gtx.Ops)
	inner := op.Offset(pill.Min).Push(gtx.Ops)
	gtxC := gtx
	gtxC.Constraints = layout.Exact(pill.Size())
	u.pillContent(gtxC)
	inner.Pop()
	pst.Pop()
	u.record(pill.Add(image.Pt(0, absY)), rowH/2, pillBg, clipRect)

	// 右圆钮（恒在，几何不随状态变，D49）：idle = 发送、生成中 = 停止、确认态 = 置灰不可点。
	fill, stop, cl := brandColor, false, &u.sendBtn
	switch {
	case u.m.confirm != nil:
		fill, cl = disabledCircle, nil
	case u.generating.Load():
		fill, stop, cl = textError, true, &u.stopBtn
	}
	off := op.Offset(send.Min).Push(gtx.Ops)
	gtxS := gtx
	gtxS.Constraints = layout.Exact(send.Size())
	actionCircle(gtxS, cl, fill, stop)
	off.Pop()
	u.record(send.Add(image.Pt(0, absY)), rowH/2, fill, clipRect)

	// 悬浮 tips（§15.1 启动提示 / §15.2 发送·停止键）：独立底板元素随形裁。
	if u.logoHovered {
		u.hoverTip(gtx, absY, startupHint, false)
	}
	if u.m.confirm == nil && u.sendBtn.Hovered() && !u.generating.Load() {
		u.hoverTip(gtx, absY, "发送", true)
	}
	if u.m.confirm == nil && u.generating.Load() && u.stopBtn.Hovered() {
		u.hoverTip(gtx, absY, "停止", true)
	}
}

// inputRowRects 三段几何（纯逻辑，可测，D49/§15.2）：拓扑 = 边距 | logo | 间隙 | 胶囊 |
// 间隙 | send | 边距——两圆钮贴边距定宽（⌀ = rowH），胶囊吃掉全部剩余宽度（`grow`）。
// 参数均为已换算的 px。
func inputRowRects(w, top, rowH, gap, margin int) (logo, pill, send image.Rectangle) {
	logo = image.Rect(margin, top, margin+rowH, top+rowH)
	send = image.Rect(w-margin-rowH, top, w-margin, top+rowH)
	pill = image.Rect(margin+rowH+gap, top, w-margin-rowH-gap, top+rowH)
	return
}

// startupHint 启动提示（装配根对 GUI 不再发 Say 启动行，§15.1）。
const startupHint = "Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成"

// tipBg 悬浮 tips 底色（实色——形裁下元素必须自带底板）。
var tipBg = color.NRGBA{R: 0x26, G: 0x2A, B: 0x2E, A: 0xFF}

// hoverTip 悬浮提示卡片：画在胶囊上沿之上（输入栏段局部坐标，可为负 → 溢出到
// 转写区之上，无遮挡裁剪）；自带底板并登记形裁。rightAlign=右对齐到胶囊内边距
// （发送键），false=左对齐（logo）。
func (u *UI) hoverTip(gtx layout.Context, absY int, text string, rightAlign bool) {
	label := func(gtx layout.Context) layout.Dimensions {
		s := material.Caption(u.th, text)
		s.Color = whiteText
		return s.Layout(gtx)
	}
	m := op.Record(gtx.Ops)
	dims := label(gtx)
	txt := m.Stop()
	padX, padY := gtx.Dp(10), gtx.Dp(6)
	radius := gtx.Dp(8)
	x := gtx.Dp(sideMarginDp)
	if rightAlign {
		x = gtx.Constraints.Max.X - gtx.Dp(sideMarginDp) - dims.Size.X - 2*padX
		if x < 0 {
			x = 0
		}
	}
	y := gtx.Dp(pillTopDp) - dims.Size.Y - 2*padY - gtx.Dp(6)
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+dims.Size.X+2*padX, y+dims.Size.Y+2*padY),
	}
	st := clip.UniformRRect(bgRect, radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, tipBg)
	inner := op.Offset(image.Pt(bgRect.Min.X+padX, bgRect.Min.Y+padY)).Push(gtx.Ops)
	txt.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), radius, tipBg, image.Rectangle{Max: u.frameSize})
}

// pillContent 胶囊内横排（坐标原点 = 胶囊左上，约束 = 胶囊尺寸；D49/§15.2）：
// [pad16][附件槽20][12][文字 grow][12][展开槽20][12][动作区][pad16]。图标槽是 canvas 的
// 20dp 灰占位（附件/展开未实现、不可点，实现时启用）；动作区仅确认态有内容（[允许/拒绝]）——
// 生成中停止在右圆、胶囊内不占位，idle 时胶囊内只有占位文字。
func (u *UI) pillContent(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(inputPadDp), Right: unit.Dp(inputPadDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{
			Axis:      layout.Horizontal,
			Alignment: layout.Middle,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconSlot(gtx, drawPaperclip)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				inset := layout.Inset{Left: unit.Dp(inputGapDp), Right: unit.Dp(inputGapDp)}
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// 约束 Min.Y 被 Exact 拉满会把编辑器/提示行顶到盒顶（Flex Middle 对满高盒
					// 无效 → 文本视觉偏上）：放开 Min 让其返回自然行高，由 Flex 垂直居中（§15.2）。
					gtx.Constraints.Min.Y = 0
					if u.m.confirm != nil {
						return material.Body2(u.th, u.m.confirm.prompt).Layout(gtx)
					}
					ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
					ed.TextSize = unit.Sp(15)
					return ed.Layout(gtx)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconSlot(gtx, drawMaximize)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: unit.Dp(inputGapDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Rigid(u.actionBtn(&u.allowBtn, "允许", brandColor)),
						layout.Rigid(u.actionBtn(&u.denyBtn, "拒绝", textMuted)),
					)
				})
			}),
		)
	})
}

// iconSlot 胶囊内 20dp 图标槽（D49/§15.2：canvas 20×20 灰占位、不可点——附件/展开实现时启用）。
func iconSlot(gtx layout.Context, draw func(gtx layout.Context, box image.Rectangle)) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	box := image.Rectangle{Max: image.Pt(d, d)}
	draw(gtx, box)
	return layout.Dimensions{Size: box.Size()}
}

// drawMaximize 展开图标占位（灰、不可点）：四角括号——canvas 原几何（内缩 2、臂长 6、
// 臂厚 2，稿内 1:1 即 dp），只把配色换成占位灰。
func drawMaximize(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	in, arm, th := gtx.Dp(2), gtx.Dp(6), gtx.Dp(2)
	x0, y0 := box.Min.X, box.Min.Y
	x1, y1 := box.Max.X, box.Max.Y
	for _, r := range []image.Rectangle{
		image.Rect(x0+in, y0+in, x0+in+arm, y0+in+th), // 左上·横
		image.Rect(x0+in, y0+in, x0+in+th, y0+in+arm), // 左上·竖
		image.Rect(x1-in-arm, y0+in, x1-in, y0+in+th), // 右上·横
		image.Rect(x1-in-th, y0+in, x1-in, y0+in+arm), // 右上·竖
		image.Rect(x0+in, y1-in-th, x0+in+arm, y1-in), // 左下·横
		image.Rect(x0+in, y1-in-arm, x0+in+th, y1-in), // 左下·竖
		image.Rect(x1-in-arm, y1-in-th, x1-in, y1-in), // 右下·横
		image.Rect(x1-in-th, y1-in-arm, x1-in, y1-in), // 右下·竖
	} {
		paint.FillShape(gtx.Ops, iconDim, clip.Rect(r).Op())
	}
}

// cubicArc 以 c 为圆心、从 p 到 q 的 90° 圆弧（三次贝塞尔逼近，k = 0.5523）：
// 切向 = 半径向量 ±90°（方向由 p→q 相对 c 的旋转取号），控制点 = 出点沿切向前伸、
// 入点沿切向回退。占位图标折线用。
func cubicArc(path *clip.Path, c, p, q f32.Point) {
	const k = 0.5523
	r1 := f32.Point{X: p.X - c.X, Y: p.Y - c.Y}
	r2 := f32.Point{X: q.X - c.X, Y: q.Y - c.Y}
	t1 := f32.Point{X: -r1.Y, Y: r1.X}
	t2 := f32.Point{X: -r2.Y, Y: r2.X}
	if r1.X*r2.Y-r1.Y*r2.X < 0 {
		t1 = f32.Point{X: r1.Y, Y: -r1.X}
		t2 = f32.Point{X: r2.Y, Y: -r2.X}
	}
	path.CubeTo(
		f32.Point{X: p.X + k*t1.X, Y: p.Y + k*t1.Y},
		f32.Point{X: q.X - k*t2.X, Y: q.Y - k*t2.Y},
		q,
	)
}

// drawPaperclip 附件图标占位（灰、不可点）：回形针折线 = 三段直线 + 三段半圆（24 视图的
// 中心线 (2.005,1.39)–(21.44,22) 等比 s=0.8 缩进 20dp 槽居中，描边 2dp 按稿）。
func drawPaperclip(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	const s = 0.8
	const bw, bh = 15.548, 16.488 // 缩后中心线尺寸
	ox := float32(box.Min.X) + (float32(box.Dx())-bw)/2 - 2.005*s
	oy := float32(box.Min.Y) + (float32(box.Dy())-bh)/2 - 1.39*s
	at := func(x, y float32) f32.Point { return f32.Point{X: ox + x*s, Y: oy + y*s} }

	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(at(21.44, 11.05)) // 外端（右侧中）
	p.LineTo(at(12.25, 20.24)) // 沿外线下行到底
	c1 := at(8.005, 16)        // 大半圆 r=6（底 → 左）
	m1 := at(3.762, 20.243)
	cubicArc(&p, c1, at(12.25, 20.24), m1)
	cubicArc(&p, c1, m1, at(3.76, 11.75))
	p.LineTo(at(12.95, 2.56)) // 上行到顶
	c2 := at(15.78, 5.39)     // 中半圆 r=4（顶 → 右）
	m2 := at(18.61, 2.56)
	cubicArc(&p, c2, at(12.95, 2.56), m2)
	cubicArc(&p, c2, m2, at(18.61, 8.22))
	p.LineTo(at(9.41, 17.41)) // 内线斜下
	c3 := at(7.995, 15.995)   // 小半圆 r=2（内底 → 内左）
	m3 := at(6.58, 17.41)
	cubicArc(&p, c3, at(9.41, 17.41), m3)
	cubicArc(&p, c3, m3, at(6.58, 14.58))
	p.LineTo(at(15.07, 6.1)) // 内端（收尾）
	paint.FillShape(gtx.Ops, iconDim, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// record 登记可见元素（D44–D48）：保存真实轮廓 + 可见裁剪区 + 底色，形裁（applyRegion）
// 与羽化渐隐（fadeFeatherShapes）都由此推导。零半径、裁剪后为空、或完全落在淡出带内
// （整条由 overlay 带渐变绘制）的元素不登记。
func (u *UI) record(abs image.Rectangle, radius int, fill color.NRGBA, clipRect image.Rectangle) {
	if radius <= 0 || abs.Empty() {
		return
	}
	vis := abs.Intersect(clipRect)
	if vis.Empty() {
		return
	}
	if vis.Max.Y <= u.frameMetric.Dp(fadeBandDp) {
		return
	}
	u.shapes = append(u.shapes, drawShape{outline: abs, clip: clipRect, radius: radius, fill: fill})
}

// featherWidth 元素边缘内容渐隐带的宽（px，D47/D48 响应式）：＝ region 内缩宽 ＝ overlay
// 渐隐带宽。按元素**短边**成比例，夹到 [Dp(featherMinDp), Dp(featherMaxDp)]，且不超过短边
// 的 1/3（再大 region 退化、元素整体被吃掉）——元素尺寸/窗口缩放/DPI 变化时自动跟随，
// 不再用固定 px。纯逻辑，可测。
func featherWidth(m unit.Metric, w, h int) int {
	short := w
	if h < short {
		short = h
	}
	f := int(float64(short)*featherRatio + 0.5)
	if lo := m.Dp(featherMinDp); f < lo {
		f = lo
	}
	if hi := m.Dp(featherMaxDp); f > hi {
		f = hi
	}
	if lim := short / 3; f > lim {
		f = lim
	}
	return f
}

// regionShapes 由可见元素推导形裁并集（纯逻辑，可测，D45–D48/§15.1）：视口裁剪 ∩ 淡出带
// 裁切 → 沿元素**真实边**内缩（裁切边不缩——内缩会露出羽化渐隐不覆盖的洞），圆角同步收窄
// （同心内缩圆角）；带顶裁切标 sqTop（并集构建时上两角填方续接带渐变，§15.3）。内缩量按
// 元素短边响应式（featherWidth，D47/D48）——取**真轮廓**尺寸（与 overlay 渐隐带同源，
// 跨带元素裁剪后短边会变、按裁剪尺寸算会与渐隐带错位），与渐隐带宽逐像素互补。
func regionShapes(dst []shapePhys, shapes []drawShape, band int, m unit.Metric) []shapePhys {
	for _, s := range shapes {
		r := s.outline.Intersect(s.clip)
		if r.Empty() {
			continue
		}
		ins := featherWidth(m, s.outline.Dx(), s.outline.Dy())
		cutTop := r.Min.Y > s.outline.Min.Y
		cutBottom := r.Max.Y < s.outline.Max.Y
		cutLeft := r.Min.X > s.outline.Min.X
		cutRight := r.Max.X < s.outline.Max.X
		if r.Min.Y < band {
			r.Min.Y = band
			cutTop = true
		}
		if !cutTop {
			r.Min.Y += ins
		}
		if !cutBottom {
			r.Max.Y -= ins
		}
		if !cutLeft {
			r.Min.X += ins
		}
		if !cutRight {
			r.Max.X -= ins
		}
		if r.Dx() <= 0 || r.Dy() <= 0 {
			continue
		}
		rad := s.radius - ins
		if rad < 0 {
			rad = 0
		}
		dst = append(dst, shapePhys{
			x: int32(r.Min.X), y: int32(r.Min.Y),
			w: int32(r.Dx()), h: int32(r.Dy()),
			ellipse: int32(rad * 2),
			sqTop:   cutTop,
		})
	}
	return dst
}

// applyRegion 形裁并集仅在变化时重建（布局结果稳定 → 多数帧零开销）。
// 失败（hwnd 未到/系统调用错误）不写缓存 → 下帧重试，防止首帧竞态把缓存污染成
// "已应用"导致形裁永久失效。
func (u *UI) applyRegion() {
	u.physShapes = regionShapes(u.physShapes[:0], u.shapes,
		u.frameMetric.Dp(fadeBandDp), u.frameMetric)
	if shapesEqual(u.physShapes, u.lastShapes) {
		return
	}
	if applyShapesRegion(u.physShapes) { // 内部经 onWindowThread（铁律 1）
		u.lastShapes = append(u.lastShapes[:0], u.physShapes...)
		if regionLogN < 3 {
			regionLogN++
			fmt.Printf("[region] 形裁应用成功 shapes=%d %v（第 %d 次）\n",
				len(u.physShapes), u.physShapes, regionLogN)
		}
		return
	}
	if regionFailLogN < 5 {
		regionFailLogN++
		fmt.Printf("[region] 形裁应用失败 shapes=%d hwnd=%#x（下帧重试，第 %d 次）\n",
			len(u.physShapes), atomic.LoadUintptr(&mainHWND), regionFailLogN)
	}
}

// 形裁应用日志限次（流式时形状高频变化，只留首几次成功与失败记录）。
var regionLogN, regionFailLogN int

// shapesEqual 形裁相等判定（纯逻辑，可测）。
func shapesEqual(a, b []shapePhys) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// blockView 渲染期块视图（live = 本帧实时追加的思考/草稿，非定稿块）。
type blockView struct {
	kind blockKind
	text string
	secs int
	live bool
}

// frameItems 定稿块 + 实时思考/草稿（流式可见；对齐 D33"流式原样、定稿渲染"口径）。
func (u *UI) frameItems() []blockView {
	items := make([]blockView, 0, len(u.m.blocks)+2)
	for _, b := range u.m.blocks {
		items = append(items, blockView{kind: b.kind, text: b.text, secs: b.secs})
	}
	if u.m.think.Len() > 0 {
		items = append(items, blockView{kind: blockThinking, text: u.m.think.String(), live: true})
	}
	if u.m.drafting || u.m.draft.Len() > 0 {
		items = append(items, blockView{
			kind: blockAssistant,
			text: strings.TrimRight(u.m.draft.String(), "\n"),
			live: true,
		})
	}
	return items
}

// statusText 状态行文本（数据源同 uitui Status 回调：Model/Level/Effort 现取）。
func (u *UI) statusText() string {
	if !u.generating.Load() {
		return ""
	}
	phase := "生成中"
	if u.m.think.Len() > 0 {
		phase = "思考中"
	}
	parts := []string{phase}
	if u.opts.Status != nil {
		st := u.opts.Status()
		if st.Model != "" {
			parts = append(parts, st.Model)
		}
		if st.Level != "" {
			parts = append(parts, st.Level)
		}
		if st.Effort != "" {
			parts = append(parts, st.Effort)
		}
	}
	return strings.Join(parts, " · ")
}

// updateEditor 消费编辑器事件（Enter → SubmitEvent → 提交）。material.Editor.Layout
// 内部也会 Update 但丢弃返回的 SubmitEvent——必须在渲染前自己循环取尽。
func (u *UI) updateEditor(gtx layout.Context) {
	if u.m.confirm != nil {
		return // 确认态：输入栏是按钮组，编辑器不消费按键（§15.2）
	}
	gtx.Execute(key.FocusCmd{Tag: &u.editor}) // 常驻焦点（窗口内唯一可聚焦控件）
	for {
		evt, ok := u.editor.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := evt.(widget.SubmitEvent); isSubmit {
			u.submitEditor()
		}
	}
}

// submitEditor 提交编辑器内容（发送键与 Enter 同路）；确认态由 model.submit 路由应答。
func (u *UI) submitEditor() {
	text := strings.TrimSpace(u.editor.Text())
	if text == "" {
		return
	}
	u.editor.SetText("")
	u.m.submit(text)
}

// updateClicks 控件行为：发送/停止/允许/拒绝（logo 手势与悬停在 updateLogo）。
func (u *UI) updateClicks(gtx layout.Context) {
	if u.sendBtn.Clicked(gtx) {
		u.submitEditor()
	}
	if u.stopBtn.Clicked(gtx) {
		u.interruptNow() // 生成中发送键变停止键（§15.2）
	}
	if u.allowBtn.Clicked(gtx) {
		u.m.replyConfirm(true)
	}
	if u.denyBtn.Clicked(gtx) {
		u.m.replyConfirm(false)
	}
}

// updateDrag 拖动定位（§15.6 铁律 2：按下记「窗口左上角 + 光标屏幕坐标」按差值
// 定位——窗口移动会改变指针本地坐标，本地增量法与移动互为反馈会回弹）。
// 背景把手 = 输入栏空白/状态行/收起态整窗（气泡区是滚动区，§15.1）；
// 收起态单击球（位移小于阈值）= 再展开。
func (u *UI) updateDrag(gtx layout.Context) {
	for {
		ev, ok := u.drag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			u.beginDrag()
		case pointer.Drag:
			u.moveDrag()
		case pointer.Release, pointer.Cancel:
			was := u.dragging
			u.endDrag()
			// 收起态：单击球 = 再展开；移动 = 拖窗（§15.1）。
			if ev.Kind == pointer.Release && was && u.collapsed && u.clickHeld() {
				u.collapsed = false
				u.focusPending = true // 展开即入焦点
				u.w.Invalidate()
			}
		}
	}
}

// updateLogo logo 圆钮手势（§15.1 把手含 logo）：悬停驱动启动 tips；拖动移窗；
// 单击（位移小于 dragClickSlackPx）= 收起回球。收起态圆钮区不存在（事件归背景
// 把手），本循环空转并清悬停态。
func (u *UI) updateLogo(gtx layout.Context) {
	u.logoHovered = u.logoHover.Update(gtx.Source)
	if u.collapsed {
		u.logoHovered = false
	}
	for {
		ev, ok := u.logoDrag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			u.beginDrag()
		case pointer.Drag:
			u.moveDrag()
		case pointer.Release, pointer.Cancel:
			was := u.dragging
			u.endDrag()
			if ev.Kind == pointer.Release && was && u.clickHeld() {
				u.collapsed = true // 左键 logo = 收起回球（§15.1）
				u.w.Invalidate()
			}
		}
	}
}

// beginDrag 记录拖动基准（按下；窗口未就绪则忽略本次触发）。
func (u *UI) beginDrag() {
	if rc, ok := windowRectPx(); ok {
		u.dragWin0 = point{x: rc.left, y: rc.top}
		u.dragCur0 = cursorPos()
		u.dragging = true
	}
}

// moveDrag 主窗跟随光标（拖动中，铁律 2 绝对跟踪）。
func (u *UI) moveDrag() {
	if !u.dragging {
		return
	}
	cur := cursorPos()
	u.x = u.dragWin0.x + (cur.x - u.dragCur0.x)
	u.y = u.dragWin0.y + (cur.y - u.dragCur0.y)
	moveWindowTo(u.x, u.y)
}

// endDrag 抬起/取消收尾：持久化位置。
func (u *UI) endDrag() {
	if u.dragging && u.opts.PosFile != "" {
		tm := topMostQuery()
		savePos(u.opts.PosFile, posRec{X: u.x, Y: u.y, TopMost: &tm})
	}
	u.dragging = false
}

// clickHeld 抬起时位移小于阈值 = 单击（与拖窗判定互斥）。
func (u *UI) clickHeld() bool {
	cur := cursorPos()
	dx, dy := cur.x-u.dragCur0.x, cur.y-u.dragCur0.y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= dragClickSlackPx && dy <= dragClickSlackPx
}

// actionBtn 动作键（停止/允许/拒绝；主题色底白字）。
func (u *UI) actionBtn(cl *widget.Clickable, label string, bg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.Button(u.th, cl, label)
		b.Background = bg
		b.Color = whiteText
		b.TextSize = unit.Sp(14)
		b.Inset = layout.Inset{
			Left: unit.Dp(14), Right: unit.Dp(14), Top: unit.Dp(8), Bottom: unit.Dp(8),
		}
		return b.Layout(gtx)
	}
}

// actionCircle 右侧主操作圆钮（D49/§15.2：⌀ = 行高，三态恒在同一位置、几何不随状态变）：
// idle = 发送（向上箭头）、生成中 = 停止（方块图标）、确认态 = 置灰（cl=nil 不可点）。
// 坐标 = 当前原点（调用方偏移到 send 位）；Gio Clickable 在原点登记面积（无居中），
// 走同一 updateClicks 路径。
func actionCircle(gtx layout.Context, cl *widget.Clickable, fill color.NRGBA, stop bool) layout.Dimensions {
	d := gtx.Dp(inputRowDp)
	draw := func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, d/2).Op(gtx.Ops))
		if stop {
			// 停止 = 圆角方块。
			s := d / 3
			r := image.Rectangle{
				Min: image.Pt((d-s)/2, (d-s)/2),
				Max: image.Pt((d+s)/2, (d+s)/2),
			}
			paint.FillShape(gtx.Ops, whiteText, clip.UniformRRect(r, gtx.Dp(2)).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(d, d)}
		}
		// 向上箭头 = 竖杆 + 人字头（白色描边）。
		cx, cy := float32(d)/2, float32(d)/2
		a := float32(d) * 0.18
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(cx, cy+a)) // 竖杆
		p.LineTo(f32.Pt(cx, cy-a))
		p.MoveTo(f32.Pt(cx-a, cy)) // 人字头
		p.LineTo(f32.Pt(cx, cy-a))
		p.LineTo(f32.Pt(cx+a, cy))
		paint.FillShape(gtx.Ops, whiteText,
			clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
		return layout.Dimensions{Size: image.Pt(d, d)}
	}
	if cl == nil {
		return draw(gtx)
	}
	return cl.Layout(gtx, draw)
}
