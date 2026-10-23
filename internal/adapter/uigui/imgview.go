package uigui

// imgview.go 图片预览窗（D207，§15.7）：主窗点转写区的缩略图 → 独立次窗看原图。
//
// 形态照「查看原文」同构（D99）：内容经原子槽注入（Store → 帧内 Load），窗单实例、
// 重开 = 聚焦 + 原地换图；常规装饰窗，不接主窗任何机制。
//
// 交互：起手适配窗口（小图不放大，1:1 更清楚），滚轮以光标为锚点缩放（1×–8×），
// 按住拖动平移，Esc 关闭。
//
// 解码纪律同缩略图（D187 口径四）：先 DecodeConfig 看尺寸、超预算就不解；解码在
// 本窗起的 goroutine 里做（卡帧只可能卡这个窗），结果经 channel 回帧循环。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// 预览窗口径。
const (
	imgViewZoomMax = 8.0 // 最大放大倍率（相对适配尺寸）
	// imgViewZoomK 滚轮增量的缩放系数：一格滚轮约 ±100 像素增量 → 约 ×1.16 每格。
	imgViewZoomK = 0.0015
)

// imageContent 预览窗的一次内容快照（D207）：title = 窗内标题行，ref = 图片取数口
// （已入库按哈希、路径项按路径；字节由本窗自读）。
type imageContent struct {
	title string
	ref   imgRef
}

// imgDecodeResult 后台解码结果（goroutine → 本窗帧循环）。
type imgDecodeResult struct {
	key string // 对应 ref.key()：帧内据此丢弃过期结果
	img image.Image
	pix image.Point // 原始像素尺寸
	err string      // 非空 = 失败原因（用户可见中文）
}

// imageViewState winImage 次窗的自持帧状态（runSecondary 创建、仅该窗 goroutine 读写）。
type imageViewState struct {
	applied *imageContent // 已呈现的内容快照（指针比较，变化才重解码）

	res     chan imgDecodeResult
	img     image.Image
	imgOp   paint.ImageOp
	hasOp   bool
	pix     image.Point
	err     string
	settled bool // 本张图已解码收尾（成功或失败），否则画「解码中…」

	zoom float64 // 1 = 适配窗口
	pan  f32.Point

	tag     struct{}
	pressed bool
	pid     pointer.ID
	press   f32.Point
	panFrom f32.Point

	viewport image.Rectangle // 上帧视口（缩放锚点换算用）
	close    func()          // 关闭动作（Esc）
	inval    func()          // 重绘请求（解码 goroutine 完成后调用）
	focused  bool            // 是否已把键盘焦点投到本窗输入区（Esc 需要它）
}

// newImageViewState 构造 winImage 帧状态（close/inval 由次窗装配处给）。
func newImageViewState(closeFn, inval func()) *imageViewState {
	return &imageViewState{
		res:   make(chan imgDecodeResult, 1),
		zoom:  1,
		close: closeFn,
		inval: inval,
	}
}

// sync 收后台结果 + 同步原子槽内容（内容变化才换图）。
func (st *imageViewState) sync(u *UI) {
	select {
	case r := <-st.res:
		// 过期结果（用户已经换了下一张）直接丢。
		if st.applied != nil && r.key == st.applied.ref.key() {
			st.img, st.pix, st.err, st.settled = r.img, r.pix, r.err, true
			st.hasOp = false
			if r.img != nil {
				st.imgOp = paint.NewImageOp(r.img)
				st.hasOp = true
			}
		}
	default:
	}
	c := u.imageView.Load()
	if c == nil || c == st.applied {
		return
	}
	st.applied = c
	st.img, st.pix, st.err, st.settled, st.hasOp = nil, image.Point{}, "", false, false
	st.zoom, st.pan, st.pressed = 1, f32.Point{}, false
	key := c.ref.key()
	ref := c.ref
	go func() {
		r := decodePreviewImage(u, ref)
		r.key = key
		select {
		case st.res <- r:
		default: // 缓冲已满：结果更晚的会来，本份丢弃（帧内按 key 比对，过期不采用）
		}
		if st.inval != nil {
			st.inval() // Window.Invalidate 并发安全
		}
	}()
}

// decodePreviewImage 读字节 + 预算门 + 全图解码（预览 goroutine；只读不可变输入）。
func decodePreviewImage(u *UI, r imgRef) imgDecodeResult {
	rc, err := u.openImg(r)
	if err != nil || rc == nil {
		return imgDecodeResult{err: "读不到这张图"}
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, thumbReadBytes))
	if err != nil || len(raw) == 0 {
		return imgDecodeResult{err: "读取图片失败"}
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return imgDecodeResult{err: "这个文件不是可识别的图片"}
	}
	pix := image.Pt(cfg.Width, cfg.Height)
	if cfg.Width*cfg.Height > thumbMaxPixels {
		return imgDecodeResult{
			pix: pix,
			err: fmt.Sprintf("图片过大（%d×%d），暂不支持预览", cfg.Width, cfg.Height),
		}
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return imgDecodeResult{pix: pix, err: "图片解码失败"}
	}
	return imgDecodeResult{img: img, pix: pix}
}

// openImg 打开取数口：已入库走附件库、路径项读文件（与缩略图同一条来源）。
func (u *UI) openImg(r imgRef) (io.ReadCloser, error) {
	if r.blob != nil {
		if u.opts.Blobs == nil {
			return nil, errors.New("未接入附件库")
		}
		return u.opts.Blobs.Get(context.Background(), *r.blob)
	}
	return os.Open(r.path)
}

// zoomAt 以光标为锚点缩放（pos = 视口局部坐标）：光标下的图点保持不动。
func (st *imageViewState) zoomAt(pos f32.Point, dy float64) {
	if st.img == nil || dy == 0 {
		return
	}
	old := st.zoom
	z := old * math.Exp(-dy*imgViewZoomK)
	if z < 1 {
		z = 1
	}
	if z > imgViewZoomMax {
		z = imgViewZoomMax
	}
	if z == old {
		return
	}
	// 视口中心到光标的向量 c 在缩放后应按 z/old 拉长：pan' = c - c*(z/old) + pan*(z/old)
	k := float32(z / old)
	c := pos.Sub(f32.Pt(float32(st.viewport.Dx())/2, float32(st.viewport.Dy())/2))
	st.pan = c.Sub(c.Mul(k)).Add(st.pan.Mul(k))
	st.zoom = z
	st.clampPan()
}

// clampPan 平移夹取：适配尺寸下居中不动；放大后允许到图像边缘（不留大片空白）。
func (st *imageViewState) clampPan() {
	if st.img == nil || st.zoom <= 1 {
		st.pan = f32.Point{}
		return
	}
	s := st.fitScale()
	maxX := float32(math.Max(0, (float64(st.pix.X)*s-float64(st.viewport.Dx()))/2))
	maxY := float32(math.Max(0, (float64(st.pix.Y)*s-float64(st.viewport.Dy()))/2))
	st.pan.X = clampF32(st.pan.X, -maxX, maxX)
	st.pan.Y = clampF32(st.pan.Y, -maxY, maxY)
}

// fitScale 当前绘制比例（适配 × 用户缩放；小图不放大超过 1:1）。
func (st *imageViewState) fitScale() float64 {
	if st.pix.X <= 0 || st.pix.Y <= 0 || st.viewport.Dx() <= 0 || st.viewport.Dy() <= 0 {
		return 1
	}
	s := math.Min(
		float64(st.viewport.Dx())/float64(st.pix.X),
		float64(st.viewport.Dy())/float64(st.pix.Y))
	if s > 1 {
		s = 1
	}
	return s * st.zoom
}

// events 手势/键：滚轮缩放、拖动平移、Esc 关闭（输入区已在调用方 clip 内登记）。
func (st *imageViewState) events(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &st.tag,
			Kinds:  pointer.Scroll | pointer.Press | pointer.Release | pointer.Drag | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, isPtr := ev.(pointer.Event)
		if !isPtr {
			continue
		}
		switch e.Kind {
		case pointer.Scroll:
			st.zoomAt(e.Position, float64(e.Scroll.Y))
		case pointer.Press:
			st.pressed, st.pid = true, e.PointerID
			st.press, st.panFrom = e.Position, st.pan
		case pointer.Drag:
			if st.pressed && e.PointerID == st.pid {
				st.pan = st.panFrom.Add(e.Position.Sub(st.press))
				st.clampPan()
			}
		case pointer.Release, pointer.Cancel:
			st.pressed = false
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &st.tag, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			if st.close != nil {
				st.close()
			}
			return
		}
	}
}

// imgViewFrame winImage 单帧：标题行 + 图片视口 + 底部提示行。
func imgViewFrame(gtx layout.Context, th *material.Theme, u *UI, st *imageViewState) layout.Dimensions {
	st.sync(u)
	pp := paperOf()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, pp.win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						title := "（未选择）"
						if c := st.applied; c != nil {
							title = c.title
						}
						l := material.Label(th, paperBodySp(th), title)
						l.Color = pp.text2
						l.MaxLines, l.Truncator = 1, "…"
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return st.viewportFrame(gtx, th)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, paperNoteSp(th), "滚轮缩放 · 拖动平移 · Esc 关闭")
						l.Color = pp.text3
						l.MaxLines, l.Truncator = 1, "…"
						return l.Layout(gtx)
					}),
				)
			})
		}),
	)
}

// viewportFrame 图片视口：下沉面 + 图（或加载/错误提示）+ 视口内输入区。
func (st *imageViewState) viewportFrame(gtx layout.Context, th *material.Theme) layout.Dimensions {
	pp := paperOf()
	sz := gtx.Constraints.Max
	if sz.X <= 0 || sz.Y <= 0 {
		return layout.Dimensions{Size: sz}
	}
	st.viewport = image.Rectangle{Max: sz}
	paperSurface(gtx, pp, image.Rectangle{Max: sz}, gtx.Dp(unit.Dp(pp.rCard)))
	// 输入区收在视口 clip 内：光标坐标即视口坐标（缩放锚点换算用）。
	area := clip.Rect(image.Rectangle{Max: sz}).Push(gtx.Ops)
	gtx.Event(key.FocusFilter{Target: &st.tag})
	event.Op(gtx.Ops, &st.tag)
	if !st.focused {
		// 首帧把键盘焦点落到本窗输入区：Esc 的 Focus 过滤才有接收方（否则整窗没有
		// 任何 Focus 目标，键事件无处可去）。
		gtx.Execute(key.FocusCmd{Tag: &st.tag})
		st.focused = true
	}
	st.events(gtx)

	switch {
	case st.err != "":
		area.Pop()
		return centeredNote(gtx, th, st.err, pp.text3)
	case st.img == nil || !st.hasOp:
		area.Pop()
		note := "解码中…"
		if st.settled {
			note = "没有可显示的图"
		}
		return centeredNote(gtx, th, note, pp.text3)
	}
	// 窗被拉大/缩小后平移范围随之变（不重算就会留下越界偏移）。
	st.clampPan()
	s := st.fitScale()
	cx := float32(sz.X)/2 + st.pan.X
	cy := float32(sz.Y)/2 + st.pan.Y
	// 变换：图像中心移到原点 → 按 s 缩放 → 移到视口中心 + 平移。整数偏移会在拖动时
	// 逐像素跳（亚像素被截掉），一律用浮点仿射；图仍在 area 的 clip 内，放大后不会
	// 溢出到标题行/提示行。
	//
	// 必须合成成一个矩阵再压栈：Gio 的变换栈是右乘级联（先压的在外层、后压的先生效），
	// 分三次 Push 会得到 T(-c)·S·T(+c) 这种反序组合——图被推到视口左上角之外（视口
	// 装不下时几乎整张不见）。Affine2D 的链式方法在左侧叠加（a.Scale = S·a），所以
	// 从左到右就按「先作用」的顺序写。
	mat := f32.Affine2D{}.
		Offset(f32.Pt(-float32(st.pix.X)/2, -float32(st.pix.Y)/2)).
		Scale(f32.Point{}, f32.Pt(float32(s), float32(s))).
		Offset(f32.Pt(cx, cy))
	tr := op.Affine(mat).Push(gtx.Ops)
	st.imgOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	tr.Pop()
	area.Pop()
	return layout.Dimensions{Size: sz}
}

// centeredNote 视口中央的一行提示。
func centeredNote(gtx layout.Context, th *material.Theme, text string, col color.NRGBA) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperNoteSp(th), text)
		l.Color = col
		return l.Layout(gtx)
	})
}

// clampF32 夹取浮点值。
func clampF32(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
