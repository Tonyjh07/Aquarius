package uigui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"gioui.org/app"
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
	winWidthDp   = 560
	winHeightDp  = 460
	winMinWidth  = 420
	winMinHeight = 240

	fadeBandDp   = 56 // 淡出带高（§15.3）
	pillHeightDp = 72 // 输入栏胶囊高
	pillTopDp    = 8  // 胶囊上边距（下方 16）
	sideMarginDp = 16 // 左右边距
	rowGapDp     = 6  // 转写行间距
	bubblePadXDp = 12 // 气泡内边距
	bubblePadYDp = 7
	cardPadXDp   = 10 // 文本行卡内边距
	cardPadYDp   = 5
	radiusDp     = 12 // 气泡圆角
	cardRadiusDp = 8  // 文本行卡圆角
	statusChipDp = 20 // 状态行 chip 高
	maxTextColDp = 400

	// semiAlpha 统一半透明（LWA_ALPHA 整窗常量；淡出 overlay 同值衔接，D44）。
	semiAlpha byte = 235
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
)

// point/rect Win32 坐标对（中性定义：非 Windows 构建仅作占位类型）。
type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }

// drawShape 布局期收集的可见元素矩形（窗口系物理 px；layout 坐标即物理）。
type drawShape struct {
	r      image.Rectangle
	radius int // 圆角半径（px）
}

// shapePhys 形裁元素（传 win32 并集）。
type shapePhys struct {
	x, y, w, h int32
	ellipse    int32 // CreateRoundRectRgn 椭圆宽高 = 2×半径
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

// 位置记忆（§15.1：拖拽 + 位置记忆，含多显示器工作区夹取）。
type posRec struct{ X, Y int32 }

func loadPos(path string) (int32, int32, bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	var p posRec
	if json.Unmarshal(src, &p) != nil {
		return 0, 0, false
	}
	return p.X, p.Y, true
}

func savePos(path string, x, y int32) {
	src, _ := json.Marshal(posRec{X: x, Y: y})
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
			// "关窗 = 隐藏、退出经菜单"的托盘常驻语义留托盘步接入（§15.1）。
			u.signalEOF(e.Err)
			return
		default:
			if h, ok := u.viewEvent(ev); ok {
				u.onHWND(h)
			}
		}
	}
}

// onHWND Win32ViewEvent 投递的窗口句柄：统一半透明 + 位置记忆恢复（§15.1/D44）。
func (u *UI) onHWND(h uintptr) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	atomic.StoreUintptr(&mainHWND, h)
	applyAlpha(semiAlpha) // LWA_ALPHA 整窗常量（与形裁正交，spike 已验证）
	rc, ok := windowRectPx()
	if !ok {
		return
	}
	w, ht := rc.right-rc.left, rc.bottom-rc.top
	u.x, u.y = rc.left, rc.top
	if u.opts.PosFile != "" {
		if sx, sy, found := loadPos(u.opts.PosFile); found {
			nx, ny := clampToWorkArea(sx, sy, w, ht)
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
	u.updateEditor(gtx)
	u.updateClicks(gtx)
	u.updateDrag(gtx)

	size := gtx.Constraints.Max
	u.frameMetric = gtx.Metric
	u.frameSize = size
	u.shapes = u.shapes[:0]

	inputH := gtx.Dp(pillHeightDp + pillTopDp + 16) // 胶囊 + 上 8 下 16 边距
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
			paint.Fill(gtx.Ops, windowBg)
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

// updateScroll 滚轮滚动（边界经 ScrollRange 钳制；d>0 = 向下滚）。
func (u *UI) updateScroll(gtx layout.Context, viewH int) {
	overflow := u.contentH - viewH
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
}

// transcript 转写区：手工纵向布局（不用 widget.List——需要每行绝对矩形做逐元素
// 形裁）；视口裁剪 + 滚动手势注册；空态给提示卡片。
func (u *UI) transcript(gtx layout.Context, w, h int) {
	u.updateScroll(gtx, h)
	st := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	defer st.Pop()
	u.transcriptScroll.Add(gtx.Ops)

	items := u.frameItems()
	if len(items) == 0 {
		u.contentH = 0
		u.scrollPx = 0
		// 空态提示（形裁下每个可见元素都得有底板——否则底板被挖空连文本一起不可见）。
		hint := blockView{kind: blockPlain, text: "输入 /help 查看命令"}
		y := (h - gtx.Dp(statusChipDp+10)) / 2
		if y < 0 {
			y = 0
		}
		u.row(gtx, hint, w, y, image.Rectangle{Max: image.Pt(w, h)})
		return
	}
	gap := gtx.Dp(rowGapDp)
	y := 0
	viewport := image.Rectangle{Max: image.Pt(w, h)}
	for i := range items {
		rh := u.row(gtx, items[i], w, y-u.scrollPx, viewport)
		y += rh + gap
	}
	u.contentH = y - gap
	if overflow := u.contentH - h; u.scrollPx > overflow {
		u.scrollPx = overflow
		if u.scrollPx < 0 {
			u.scrollPx = 0
		}
	}
}

// row 转写行：底板（气泡/卡片）+ 文本，y 为视口内绝对坐标（可为负，视口裁剪）。
// 返回行总高（含底板，px）。
func (u *UI) row(gtx layout.Context, it blockView, w, y int, viewport image.Rectangle) int {
	label, bg, radius, rightAlign, bubble := u.rowStyle(gtx, it)
	padX, padY := gtx.Dp(cardPadXDp), gtx.Dp(cardPadYDp)
	if bubble {
		padX, padY = gtx.Dp(bubblePadXDp), gtx.Dp(bubblePadYDp)
	}
	// 量文本（录宏，先不落 ops）。
	maxW := w - 2*gtx.Dp(sideMarginDp)
	if maxW < gtx.Dp(80) {
		maxW = gtx.Dp(80)
	}
	cs := gtx
	cs.Constraints = layout.Constraints{Min: image.Point{}, Max: image.Pt(maxW-2*padX, 1<<30)}
	m := op.Record(gtx.Ops)
	dims := label(cs)
	txt := m.Stop()

	x := gtx.Dp(sideMarginDp)
	if rightAlign {
		x = w - gtx.Dp(sideMarginDp) - dims.Size.X - 2*padX
	}
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+dims.Size.X+2*padX, y+dims.Size.Y+2*padY),
	}
	st := clip.UniformRRect(bgRect, radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, bg)
	inner := op.Offset(image.Pt(bgRect.Min.X+padX, bgRect.Min.Y+padY)).Push(gtx.Ops)
	txt.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect, radius, viewport)
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
	u.record(bgRect.Add(image.Pt(0, absY)), chipH/2, image.Rectangle{Max: u.frameSize})
}

// inputBar 输入栏（§15.2 骨架）：logo（拖拽把手）｜编辑器（确认态 = 提示行）｜
// 发送/停止；确认态整体切换为 [允许/拒绝] 按钮组（非模态）。
func (u *UI) inputBar(gtx layout.Context, w, absY int) {
	pillH := gtx.Dp(pillHeightDp)
	top := gtx.Dp(pillTopDp)
	pill := image.Rectangle{
		Min: image.Pt(gtx.Dp(sideMarginDp), top),
		Max: image.Pt(w-gtx.Dp(sideMarginDp), top+pillH),
	}
	paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(pill, pillH/2).Op(gtx.Ops))
	st := clip.UniformRRect(pill, pillH/2).Push(gtx.Ops)
	inner := op.Offset(pill.Min).Push(gtx.Ops)
	gtxC := gtx
	gtxC.Constraints = layout.Exact(pill.Size())
	u.pillContent(gtxC)
	inner.Pop()
	st.Pop()
	u.record(pill.Add(image.Pt(0, absY)), pillH/2, image.Rectangle{Max: u.frameSize})
}

// pillContent 胶囊内横排（坐标原点 = 胶囊左上，约束 = 胶囊尺寸）。
func (u *UI) pillContent(gtx layout.Context) layout.Dimensions {
	return layout.Flex{
		Axis:      layout.Horizontal,
		Alignment: layout.Middle,
		Spacing:   layout.SpaceBetween,
	}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(8)}.Layout(gtx, u.logo)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if u.m.confirm != nil {
				return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(8)}.Layout(gtx,
					material.Body2(u.th, u.m.confirm.prompt).Layout)
			}
			ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
			ed.TextSize = unit.Sp(15)
			return ed.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm != nil {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Rigid(u.actionBtn(&u.allowBtn, "允许", brandColor)),
						layout.Rigid(u.actionBtn(&u.denyBtn, "拒绝", textMuted)),
					)
				}
				if u.generating.Load() {
					return u.actionBtn(&u.stopBtn, "停止", textError)(gtx)
				}
				return u.actionBtn(&u.sendBtn, "发送", brandColor)(gtx)
			})
		}),
	)
}

// record 登记可见元素的形裁矩形：视口裁剪 + 淡出带剔除（带内由 overlay 接管，D44）。
func (u *UI) record(abs image.Rectangle, radius int, clipRect image.Rectangle) {
	r := abs.Intersect(clipRect)
	band := u.frameMetric.Dp(fadeBandDp)
	if r.Min.Y < band {
		r.Min.Y = band
	}
	if r.Empty() || radius <= 0 {
		return
	}
	u.shapes = append(u.shapes, drawShape{r: r, radius: radius})
}

// applyRegion 形裁并集仅在变化时重建（布局结果稳定 → 多数帧零开销）。
func (u *UI) applyRegion() {
	u.physShapes = u.physShapes[:0]
	for _, s := range u.shapes {
		r := s.r
		if r.Dx() <= 0 || r.Dy() <= 0 {
			continue
		}
		u.physShapes = append(u.physShapes, shapePhys{
			x: int32(r.Min.X), y: int32(r.Min.Y),
			w: int32(r.Dx()), h: int32(r.Dy()),
			ellipse: int32(s.radius * 2),
		})
	}
	if shapesEqual(u.physShapes, u.lastShapes) {
		return
	}
	u.lastShapes = append(u.lastShapes[:0], u.physShapes...)
	applyShapesRegion(u.lastShapes) // 内部经 onWindowThread（铁律 1）
}

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

// updateClicks 控件行为：发送/停止/允许/拒绝。
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
// 形裁后把手 = 输入栏空白/状态行/logo（气泡区是滚动区，DESIGN §15.1）。
func (u *UI) updateDrag(gtx layout.Context) {
	for {
		ev, ok := u.drag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			if rc, ok := windowRectPx(); ok {
				u.dragWin0 = point{x: rc.left, y: rc.top}
				u.dragCur0 = cursorPos()
				u.dragging = true
			}
		case pointer.Drag:
			if !u.dragging {
				break
			}
			cur := cursorPos()
			u.x = u.dragWin0.x + (cur.x - u.dragCur0.x)
			u.y = u.dragWin0.y + (cur.y - u.dragCur0.y)
			moveWindowTo(u.x, u.y)
		case pointer.Release, pointer.Cancel:
			if u.dragging && u.opts.PosFile != "" {
				savePos(u.opts.PosFile, u.x, u.y)
			}
			u.dragging = false
		}
	}
}

// logo 品牌色圆钮（§15.1 单组件语义的骨架等价物；贴 assets/icon 与展开/收起
// 交互留形态步）。
func (u *UI) logo(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(44)
	paint.FillShape(gtx.Ops, brandColor,
		clip.UniformRRect(image.Rectangle{Max: image.Pt(d, d)}, d/2).Op(gtx.Ops))
	return layout.Dimensions{Size: image.Pt(d, d)}
}

// actionBtn 动作键（发送/停止/允许/拒绝；主题色底白字）。
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
