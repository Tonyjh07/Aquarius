package uigui

import (
	"fmt"
	"hash"
	"hash/fnv"
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// transcript 转写区：两遍布局（量高 → 滚动定界 → 绘制），**底部锚定**——新消息贴着
// 输入栏出现（§15.1"提交后在胶囊上方出现"），旧消息上滚进顶部淡出带渐隐（§15.3）；
// 尾随贴底，用户上滚即停跟随。手工纵排（需要每行绝对矩形登记形状，D44）。
func (u *UI) transcript(gtx layout.Context, w, h int) {
	items := u.frameItems()
	if len(items) == 0 {
		// 空态不渲染任何元素（悬浮球只剩输入栏，区域全透；转写浮层"提交后出现"，§15.1）。
		u.contentH = 0
		u.scrollPx = 0
		u.transH = 0
		u.keyRects = u.keyRects[:0]       // 键清零 → 指针值（0）与任何选区指纹都不同，选区下帧自愈
		u.bubbleRects = u.bubbleRects[:0] // D92：气泡矩形随帧复位
		u.keyFp = 0
		return
	}
	u.transH = h // D102：贴边自动滚动判缘（消费者阶段读上一帧值）
	viewport := image.Rectangle{Max: image.Pt(w, h)}
	st := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	defer st.Pop()
	u.transcriptScroll.Add(gtx.Ops) // 视口滚动手势
	// D91 ① 跨块拖选观察者热区：转写视口整片（clip 无平移 = 窗口系坐标，与行 keyRects
	// 同一坐标系；注册于同组内与滚动手势同一命中链——根级注册会被行组命中跳过）。
	event.Op(gtx.Ops, &u.sel)
	// D92 气泡右键手势：同组命中链（同 D91 ①——根级注册不可达）；坐标 = 窗口系。
	u.bubbleRight.add(gtx.Ops)

	gap := gtx.Dp(rowGapDp)
	// ① 量高（文本只排一次，录宏供绘制复用）；行选几何按（行,块）双键挂接（D63/D66/
	// D91 ②）：线性序号 base 随条目 selCount 累计，rowSel 消费于 measureRow 内
	//（note 记行盒、layout 画高亮）。跨度缓冲由 updateSel 在消费者阶段从上帧
	// keyRects 预算——此处量期只读。
	rows := make([]measuredRow, len(items))
	total, totalKeys := 0, 0
	for i := range items {
		items[i].keyBase = totalKeys // D92：整条复制按键区间取渲染文本
		rs := &rowSel{u: u, base: totalKeys, n: items[i].selCount()}
		rows[i] = u.measureRow(gtx, items[i], w, rs)
		total += rows[i].height()
		totalKeys += rs.n
	}
	total += gap * (len(rows) - 1)

	// D74 顶部 headroom：滚动内容头部垫一个**淡出带高**的空白（计入 total → 参与
	// overflow/钳制与 base）。滚到最上（scrollPx=0）时首行落在 y ≥ pad、完整在带下
	// 可读——否则首行困在带内而 scrollPx 不可为负，永远半透明（= 淡出遮挡内容）。
	// 空白随内容滚（非视口固定留白，否则渐隐作用于空白而失效）；短内容时 pad 一并
	// 制造 overflow，拥挤内容同样能往上滚出让出带外。
	pad, lowPad := u.bandHeightsClamped(gtx, h) // D90：与 layout 带写入同源（极矮窗夹取一致）
	total += pad

	// D79 尾部留白：滚动内容尾部垫一个**底部矮带高**的空白（等高、单源、随内容滚，
	// D74 同款）——贴底/尾随时末行底 = h − lowPad，正好停在底带**之外**（带里只剩
	// 空白，渐隐作用于内容而非留白）；上滚离底（scrollPx=0）时内容延伸进带内 → 底缘
	// 渐隐而非硬切。计入 total → 同样参与 overflow/钳制与 base。
	total += lowPad

	// ② 滚动定界：手势 + 当帧真实内容高（无一帧滞后），尾随贴底。
	u.updateScroll(gtx, h, total)

	// ③ 绘制：底部锚定基线——内容矮时贴底（信息悬在输入栏上方），超出视口后
	// 顶出上沿、上滚进淡出带（base 归零后退化为标准滚动）。起点 +pad 抵消头部
	// 空白，底钉只整体上移一个尾部留白（base + pad + 行高总和 + lowPad == h →
	// 尾随时末行底 = h − lowPad，恰在底带之上，D79）。
	base := h - total
	if base < 0 {
		base = 0
	}
	// 键几何落盘（D91 ②）：本帧量期记录 → 行原点定后译窗口系；复位[:0]保容量
	//（行盒缓冲逐帧复用），指纹流式喂哈希（行序 = 键序）。消费者阶段（下一帧
	// updateSel）读到的即上一帧整帧几何——一帧陈旧是既定口径。
	u.keyRects = u.keyRects[:0]
	u.bubbleRects = u.bubbleRects[:0] // D92：气泡矩形随帧复位（paint 期重登记）
	fph := fnv.New64a()
	y := base - u.scrollPx + pad
	for i := range rows {
		rh := u.paintRow(gtx, rows[i], items[i], w, y, viewport, fph)
		y += rh + gap
	}
	u.keyRects = u.keyRects[:totalKeys] // 结构缩小时清尾（writeKeyRects 按 base 定位不越界）
	u.keyFp = fph.Sum64()
	u.contentH = total
}

// selFor 行选状态 get-or-create（D63）：按行序缓存——跨帧持久（选中态/焦点不丢），
// 行文本变化由 LabelStyle.Layout 的 SetText 幂等更新并自动清选区（流式行、会话切换
// 同路径）。仅事件循环 goroutine 调用。
func (u *UI) selFor(i int) *widget.Selectable {
	for len(u.selRows) <= i {
		u.selRows = append(u.selRows, new(widget.Selectable))
	}
	return u.selRows[i]
}

// measuredRow 量高后的转写行（文本录宏 + 样式令牌；sel = 行选几何挂接件，
// D91 ②——paint 期译 keyRects）。
type measuredRow struct {
	txt                op.CallOp
	dims               image.Point
	bg                 color.NRGBA
	radius, padX, padY int
	right              bool
	sel                *rowSel
}

// height 行总高（含上下内边距，px）。
func (m measuredRow) height() int { return m.dims.Y + 2*m.padY }

// measureRow 量高 + 取样式（文本录入宏，不在本步落 ops；rs = 行选几何挂接件，
// D63/D66 双键：k 为复合行内块序，nil = 测试直通）。
func (u *UI) measureRow(gtx layout.Context, it blockView, w int, rs *rowSel) measuredRow {
	label, bg, radius, rightAlign, bubble := u.rowStyle(gtx, it, rs)
	padX, padY := gtx.Dp(cardPadXDp), gtx.Dp(cardPadYDp)
	if bubble {
		padX, padY = gtx.Dp(bubblePadXDp), gtx.Dp(bubblePadYDp)
	}
	maxW := w - 2*gtx.Dp(sideMarginDp)
	if maxW < gtx.Dp(bubbleMinWDp) {
		maxW = gtx.Dp(bubbleMinWDp)
	}
	cs := gtx
	cs.Constraints = layout.Constraints{Min: image.Point{}, Max: image.Pt(maxW-2*padX, 1<<30)}
	m := op.Record(gtx.Ops)
	dims := label(cs)
	return measuredRow{
		txt: m.Stop(), dims: dims.Size, bg: bg, radius: radius,
		padX: padX, padY: padY, right: rightAlign, sel: rs,
	}
}

// paintRow 绘制底板 + 文本并登记形状；y 为视口内绝对坐标（可为负）。
// 顺带把本行量期记录译成窗口系键几何（D91 ②）并喂结构指纹；menuable 条目登记
// 气泡底板矩形（D92 右键命中）。返回行总高（px）。
func (u *UI) paintRow(gtx layout.Context, mr measuredRow, it blockView, w, y int, viewport image.Rectangle, fph hash.Hash64) int {
	x := gtx.Dp(sideMarginDp)
	if mr.right {
		x = w - gtx.Dp(sideMarginDp) - mr.dims.X - 2*mr.padX
	}
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+mr.dims.X+2*mr.padX, y+mr.height()),
	}
	if mr.sel != nil {
		u.writeKeyRects(mr.sel, image.Pt(bgRect.Min.X+mr.padX, bgRect.Min.Y+mr.padY), fph)
	}
	if it.menuable() {
		u.bubbleRects = append(u.bubbleRects, bubbleHit{
			rect: bgRect, bi: it.bi, id: it.id, kind: it.kind,
			keyBase: it.keyBase, keyN: it.selCount(),
		})
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
// rs 行选几何挂接件（D63；D66 双键 k = 复合行内块序，D91 ② 记行盒/画高亮）——
// 除思考头部（元信息）外全部可选；nil 挂接件 = 测试直通。
func (u *UI) rowStyle(gtx layout.Context, it blockView, rs *rowSel) (layout.Widget, color.NRGBA, int, bool, bool) {
	radiusDp, cardR := gtx.Dp(radiusDp), gtx.Dp(cardRadiusDp)
	switch it.kind {
	case blockUser: // 用户气泡：品牌色底白字、右对齐（§15.3 双色气泡）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, it.text)
				s.Color = whiteText
				s.State = rs.sel(0)
				return s.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, brandColor, radiusDp, true, true
	case blockAssistant: // 助手气泡：浅白底、左对齐；markdown 复合行 = 垂直多块共底板（D66）
		blocks := it.md
		if len(blocks) == 0 {
			blocks = []mdBlock{{kind: mdPara, text: it.text}} // live 草稿/兜底：纯文本单块
		}
		// D91 ② 手排纵列：量期即知块偏移（Flex 的 Offset 回放期才施加，量期不可知
		// ——行盒/高亮定位须块内偏移）；尺寸口径与 Flex Rigid 逐像素一致。
		return func(gtx layout.Context) layout.Dimensions {
			vs := beginVStack(gtx)
			for k, b := range blocks {
				if k > 0 {
					vs.gap(mdBlockGapDp)
				}
				off := image.Pt(0, vs.y) // add 的 offY 即当前 y（先 gap 后取）
				vs.add(u.mdBlockWidget(b, rs, k, off))
			}
			return vs.dims()
		}, pillBg, radiusDp, false, true
	case blockThinking: // 思考行：头部（元信息，不选）+ 正文
		header := "已思考"
		if it.live {
			header = "思考中…"
		} else if it.secs >= 0 {
			header = fmt.Sprintf("已思考 · %ds", it.secs)
		}
		return func(gtx layout.Context) layout.Dimensions {
			vs := beginVStack(gtx)
			vs.add(func(gtx layout.Context) layout.Dimensions {
				s := material.Caption(u.th, header)
				s.Color = textDim
				return s.Layout(gtx)
			})
			off := image.Pt(0, vs.y)
			vs.add(func(gtx layout.Context) layout.Dimensions {
				return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, it.text)
					s.Color = textDim
					s.State = rs.sel(0)
					return s.Layout(gtx)
				}, 0, off, image.Point{})
			})
			return vs.dims()
		}, cardThinking, cardR, false, false
	case blockTool:
		if it.chip == nil { // 兜底：无 chip 的工具行（防御，正常路径都有 chip）
			return func(gtx layout.Context) layout.Dimensions {
				return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, it.text)
					l.Color = textMuted
					l.State = rs.sel(0)
					return l.Layout(gtx)
				}, 0, image.Point{}, image.Point{})
			}, cardTool, cardR, false, false
		}
		return u.toolChipRow(it, rs, cardR)
	case blockBranch: // 分叉条（D81）：气泡下方 `◀ i/n ▶`，与气泡同向对齐
		if it.branch == nil { // 防御：无数据的空行不渲染也不登记形状
			return func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{}
			}, cardTool, 0, false, false
		}
		return u.branchRow(it.branch, cardR)
	case blockNotice:
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, it.text)
				l.Color = textNotice
				l.State = rs.sel(0)
				return l.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, cardNotice, cardR, false, false
	case blockError:
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, it.text)
				l.Color = textError
				l.State = rs.sel(0)
				return l.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, cardError, cardR, false, false
	case blockSystem:
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, it.text)
				l.Color = textSystem
				l.State = rs.sel(0)
				return l.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, cardSystem, cardR, false, false
	default: // blockPlain（Say/命令输出/提示）：底板浅白、正文默认色
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, it.text)
				s.State = rs.sel(0)
				return s.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, pillBg, cardR, false, false
	}
}

// mdBlockGapDp 气泡内 markdown 块间距（D66 复合行）。
const mdBlockGapDp unit.Dp = 4

// mdCodeInsetDp 代码小卡内边距（D66：卡嵌气泡内）。
const mdCodeInsetDp unit.Dp = 6

// mdBlockWidget 单个 markdown 块的行内部件（D66 复合行分派；rs/k = 该块行选挂接
// 件与块序，off = 块原点相对行内容原点（vstack 量期已知，供 note；高亮坐标走
// ops 局部变换——vstack 的 Offset 已在变换内，D91 ②③）。
func (u *UI) mdBlockWidget(b mdBlock, rs *rowSel, k int, off image.Point) layout.Widget {
	switch b.kind {
	case mdHeading: // 大字求粗（D65 阶梯；CJK 粗体面缺省回落常规）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body1(u.th, b.text)
				s.TextSize = mdHeadingSp(u.th, b.level)
				s.Font.Weight = font.SemiBold
				s.State = rs.sel(k)
				return s.Layout(gtx)
			}, k, off, image.Point{})
		}
	case mdCode: // 等宽小卡：录宏量高 → 画底 →（高亮，卡内衬偏移）→ 重放（同 paintRow 次序，卡嵌气泡内）
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, b.text)
			s.Font = monoFace
			s.TextSize = u.th.TextSize * 13.0 / 16.0
			s.State = rs.sel(k)
			m := op.Record(gtx.Ops)
			dims := layout.UniformInset(mdCodeInsetDp).Layout(gtx, s.Layout)
			txt := m.Stop()
			st := clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(radiusDp)).Push(gtx.Ops)
			paint.Fill(gtx.Ops, cardTool)
			st.Pop()
			inset := gtx.Dp(mdCodeInsetDp)
			if rs != nil {
				rs.note(k, off.Add(image.Pt(inset, inset))) // 文本在内衬原点（内容系）
				rs.paint(gtx, k, image.Pt(inset, inset))    // 高亮局部 = 盒 + 内衬（文本回放前落 ops）
			}
			txt.Add(gtx.Ops)
			return dims
		}
	case mdQuote: // 引用：暗色正文（前缀已在文本）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, b.text)
				s.Color = textDim
				s.State = rs.sel(k)
				return s.Layout(gtx)
			}, k, off, image.Point{})
		}
	case mdRule: // 分隔线：暗点行
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, "· · · · · ·")
				l.Color = textDim
				l.State = rs.sel(k)
				return l.Layout(gtx)
			}, k, off, image.Point{})
		}
	default: // mdPara / mdListItem：正文（列表前缀已在文本）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, b.text)
				s.State = rs.sel(k)
				return s.Layout(gtx)
			}, k, off, image.Point{})
		}
	}
}
