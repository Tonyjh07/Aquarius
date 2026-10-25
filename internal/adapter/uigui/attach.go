package uigui

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// stagedAtt 一项已暂存附件（D186 多附件）：path 与 blob 二选一——blob 非空 = 字节
// 已入附件库（D187 粘贴图片），提交时不再从文件系统读第二遍。
type stagedAtt struct {
	path string
	blob *conversation.BlobRef
	// thumb 缩略图（D187 口径四）：图片类附件解码出的方图小样，chip 里替代回形针。
	// nil = 不是图片、或像素数超预算没解（回落回形针，绝不为了小图标卡帧）。
	thumb *thumbSrc
}

// displayName 附件显示名（路径取文件名、已入库项取库内名）。
func (a stagedAtt) displayName() string {
	if a.blob != nil {
		if a.blob.Name != "" {
			return a.blob.Name
		}
		return "粘贴的图片"
	}
	return filepath.Base(a.path)
}

// attachment 转成端口附件项（port.Attachment：路径与已入库引用二选一）。
func (a stagedAtt) attachment() port.Attachment {
	if a.blob != nil {
		return port.Attachment{Blob: a.blob}
	}
	return port.Attachment{Path: a.path}
}

// key 去重键：路径按清洗后的绝对路径、已入库项按内容哈希（同一次输入里同一个文件
// 附两遍没有意义，只会白占上下文）。
func (a stagedAtt) key() string {
	if a.blob != nil {
		return "blob:" + a.blob.Hash
	}
	return "path:" + strings.ToLower(filepath.Clean(a.path))
}

// chip 尺寸口径（D186 口径四，单位 dp）。
const (
	attachChipDp    = 32  // chip 高（单行胶囊 48 内垂直居中）
	attachNameMaxDp = 200 // 名称显示预算（超宽截断）
	attachIconDp    = 12  // 回形针小图边长（非图片附件、或无缩略图时）
	attachPadDp     = 10
	attachGapDp     = 6
	attachChipGapDp = 8 // 同一行 chip 之间的横向间距
	attachRowGapDp  = 6 // 换行后的纵向间距
)

// iconDp chip 左侧图标的边长（dp）：有缩略图用 20dp 小图，否则 12dp 回形针——
// 两者不同宽，chip 宽度量测必须用同一个判据（attachChipMetrics 与 attachChip 同源）。
func chipIconDp(thumb *thumbSrc) int {
	if thumb != nil {
		return attachThumbDp
	}
	return attachIconDp
}

// attachSlot 附件槽实装（D104⑤）：20dp 命中 + 回形针图标（canvas 1:1，D49 灰槽启用）。
func (u *UI) attachSlot(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	return u.attachBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		drawPaperclip(gtx, box)
		return layout.Dimensions{Size: box.Size()}
	})
}

// chipItem 一条附件 chip 的统一视图（D188）：非编辑态 = 新暂存项；编辑态 = 目标节点
// 的已有附件（序号 >= 0，可标记删除）+ 新暂存项。渲染与量测都只看这个切片。
type chipItem struct {
	label   string
	thumb   *thumbSrc
	click   *widget.Clickable
	dropped bool // 编辑态已有附件：已标记删除（提交时翻成 --drop N）
}

// chipItems 当前应显示的 chip 列表（D188）：编辑态先摆目标节点的已有附件（按序号），
// 再摆这次新增的暂存项——顺序与提交后的节点分片序一致。
func (u *UI) chipItems() []chipItem {
	out := make([]chipItem, 0, len(u.m.editAtts)+len(u.m.stagedFiles))
	if u.m.editTarget != "" {
		for _, att := range u.m.editAtts {
			out = append(out, chipItem{
				label:   att.Name,
				click:   u.editAttClick(att.Index),
				dropped: u.m.editDropped[att.Index],
			})
		}
	}
	for i, att := range u.m.stagedFiles {
		out = append(out, chipItem{label: att.displayName(), thumb: att.thumb, click: u.attachClick(i)})
	}
	return out
}

// attachToolSlot 收起态（单行胶囊）的附件位（D186 口径四）：无附件 = 图标槽；
// 一个 = 该附件的 chip；多个 = 汇总 chip（点一下清空全部——逐项删除在展开态做）。
func (u *UI) attachToolSlot(gtx layout.Context) layout.Dimensions {
	items := u.chipItems()
	switch len(items) {
	case 0:
		return u.attachSlot(gtx)
	case 1:
		it := items[0]
		return u.attachChipState(gtx, it.label, it.thumb, it.click, it.dropped)
	default:
		return u.attachChip(gtx, fmt.Sprintf("%d 个文件", len(items)), nil, &u.attachClear)
	}
}

// attachChipFixedPx chip 除名称外的固定宽（左右内边距 + 图标 + 两处间距 + × 位）。
func attachChipFixedPx(gtx layout.Context, iconDp int) int {
	return 2*gtx.Dp(attachPadDp) + gtx.Dp(unit.Dp(iconDp)) + 2*gtx.Dp(attachGapDp) + gtx.Dp(14)
}

// attachChipMetrics 量出 chip 宽与被截断后的显示名（文本量测不落笔）。
func (u *UI) attachChipMetrics(gtx layout.Context, label string, iconDp, cap int) (int, string) {
	fixed := attachChipFixedPx(gtx, iconDp)
	budget := cap - fixed
	if maxB := gtx.Dp(attachNameMaxDp); budget > maxB {
		budget = maxB
	}
	if budget < 0 {
		budget = 0
	}
	shown := u.complFitText(gtx, label, budget)
	_, dims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
		s := material.Body2(u.th, shown)
		s.TextSize = unit.Sp(13)
		return s.Layout(gtx)
	})
	return fixed + dims.Size.X, shown
}

// attachChip 暂存附件 chip（D186）：图标（图片 = 缩略图、其余 = 回形针）+ 名称（按剩余
// 宽截断）+ ×，深底圆角条；整 chip 点击 = 从暂存列表移除这一项（chip 无第二动作，
// 命中面最大化）。
func (u *UI) attachChip(gtx layout.Context, label string, thumb *thumbSrc, cl *widget.Clickable) layout.Dimensions {
	return u.attachChipState(gtx, label, thumb, cl, false)
}

// attachChipState 同上，另带「已标记删除」态（D188 编辑态已有附件）：文字压暗 + 删除线、
// 右侧 × 变 +（点击即恢复）——不能让它看起来像普通 chip。
func (u *UI) attachChipState(gtx layout.Context, label string, thumb *thumbSrc, cl *widget.Clickable, dropped bool) layout.Dimensions {
	// 压暗色 = tips 白字降不透明度（两主题同口径，不新增主题令牌）。
	dimFg := tok().whiteText
	dimFg.A = 0x70
	return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(attachChipDp)
		iconDp := chipIconDp(thumb)
		w, shown := u.attachChipMetrics(gtx, label, iconDp, gtx.Constraints.Max.X)
		padX, iconD, gap := gtx.Dp(attachPadDp), gtx.Dp(unit.Dp(iconDp)), gtx.Dp(attachGapDp)
		nameOp, nameDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, shown)
			s.TextSize = unit.Sp(13)
			if dropped {
				s.Color = dimFg
			}
			return s.Layout(gtx)
		})
		box := image.Rectangle{Max: image.Pt(w, h)}
		paint.FillShape(gtx.Ops, tok().tipBg, clip.UniformRRect(box, h/2).Op(gtx.Ops))
		// 图标：图片附件画缩略图（圆角小图），其余画回形针（品牌色小图）。
		off := op.Offset(image.Pt(padX, (h-iconD)/2)).Push(gtx.Ops)
		if img, ok := thumb.imageOp(); ok {
			drawThumb(gtx, image.Rectangle{Max: image.Pt(iconD, iconD)}, img)
		} else {
			drawPaperclip(gtx, image.Rectangle{Max: image.Pt(iconD, iconD)})
		}
		off.Pop()
		// 名称。
		tr := op.Offset(image.Pt(padX+iconD+gap, (h-nameDims.Size.Y)/2)).Push(gtx.Ops)
		nameOp.Add(gtx.Ops)
		tr.Pop()
		if dropped {
			// 删除线：横穿名称中间，与压暗的文字一起表达「这一项会从修订里去掉」。
			y := float32(h) / 2
			x0, x1 := float32(padX+iconD+gap), float32(padX+iconD+gap+nameDims.Size.X)
			var lp clip.Path
			lp.Begin(gtx.Ops)
			lp.MoveTo(f32.Pt(x0, y))
			lp.LineTo(f32.Pt(x1, y))
			paint.FillShape(gtx.Ops, dimFg, clip.Stroke{Path: lp.End(), Width: float32(gtx.Dp(1))}.Op())
		}
		// ×（两杆）；已删除态画「+」= 点一下恢复。
		cx, cy := float32(w-padX-gtx.Dp(7)), float32(h)/2
		a := float32(gtx.Dp(5))
		var p clip.Path
		p.Begin(gtx.Ops)
		if dropped {
			p.MoveTo(f32.Pt(cx, cy-a))
			p.LineTo(f32.Pt(cx, cy+a))
			p.MoveTo(f32.Pt(cx-a, cy))
			p.LineTo(f32.Pt(cx+a, cy))
		} else {
			p.MoveTo(f32.Pt(cx-a, cy-a))
			p.LineTo(f32.Pt(cx+a, cy+a))
			p.MoveTo(f32.Pt(cx+a, cy-a))
			p.LineTo(f32.Pt(cx-a, cy+a))
		}
		stroke := tok().whiteText
		if dropped {
			stroke = dimFg
		}
		paint.FillShape(gtx.Ops, stroke, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
		return layout.Dimensions{Size: box.Size()}
	})
}

// attachRows 把附件 chip 按可用宽分行（D186 口径四：横排、超宽换行）：返回每行下标。
func (u *UI) attachRows(gtx layout.Context, maxW int) [][]int {
	items := u.chipItems()
	if len(items) == 0 || maxW <= 0 {
		return nil
	}
	rowGap := gtx.Dp(attachChipGapDp)
	var rows [][]int
	var cur []int
	curW := 0
	for i, it := range items {
		w, _ := u.attachChipMetrics(gtx, it.label, chipIconDp(it.thumb), maxW)
		if len(cur) > 0 && curW+rowGap+w > maxW {
			rows = append(rows, cur)
			cur, curW = nil, 0
		}
		if len(cur) > 0 {
			curW += rowGap
		}
		cur = append(cur, i)
		curW += w
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	return rows
}

// attachFlowHeightPx chip 排总高（行数×chip 高 + 行间距）；无附件 = 0。
func (u *UI) attachFlowHeightPx(gtx layout.Context, maxW int) int {
	rows := u.attachRows(gtx, maxW)
	if len(rows) == 0 {
		return 0
	}
	return len(rows)*gtx.Dp(attachChipDp) + (len(rows)-1)*gtx.Dp(attachRowGapDp)
}

// attachFlowMaxW 展开态 chip 排可用宽（与 attachChipsFlow 同口径；高度预估略收一点，
// 让预估行数不少于实排行数——预估偏小只会多留白，偏大会压扁编辑区）。
func (u *UI) attachFlowMaxW(gtx layout.Context) int {
	pillW := u.frameSize.X - 2*gtx.Dp(sideMarginDp) - 2*gtx.Dp(inputRowDp) - 2*gtx.Dp(inputGapDp)
	avail := pillW - 2*gtx.Dp(inputPadDp) - 2*gtx.Dp(inputIconDp) - 2*gtx.Dp(inputGapDp) - gtx.Dp(attachChipGapDp)
	if avail < gtx.Dp(attachChipDp) {
		return gtx.Dp(attachChipDp)
	}
	return avail
}

// attachOverflowPx 展开态胶囊因 chip 换行需要的增高（单行 chip 的高度已由定高容纳）。
func (u *UI) attachOverflowPx(gtx layout.Context) int {
	if len(u.chipItems()) == 0 {
		return 0
	}
	flow := u.attachFlowHeightPx(gtx, u.attachFlowMaxW(gtx))
	one := gtx.Dp(attachChipDp)
	if flow <= one {
		return 0
	}
	return flow - one
}

// attachChipsFlow 展开态 chip 排（D186 口径四）：横排、超宽换行，每个 chip 可点删。
// maxW = 本排可用宽（调用方按工具行剩余宽算出，右侧要给展开槽留位）。
func (u *UI) attachChipsFlow(gtx layout.Context, maxW int) layout.Dimensions {
	if maxW <= 0 {
		return layout.Dimensions{}
	}
	gtx.Constraints.Max.X = maxW
	if gtx.Constraints.Min.X > maxW {
		gtx.Constraints.Min.X = maxW
	}
	items := u.chipItems()
	rows := u.attachRows(gtx, maxW)
	if len(rows) == 0 {
		return layout.Dimensions{}
	}
	kids := make([]layout.FlexChild, 0, 2*len(rows))
	for ri, row := range rows {
		row := row
		if ri > 0 {
			kids = append(kids, layout.Rigid(layout.Spacer{Height: unit.Dp(attachRowGapDp)}.Layout))
		}
		kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			chips := make([]layout.FlexChild, 0, len(row))
			for ci, idx := range row {
				idx, ci := idx, ci
				chips = append(chips, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					chip := func(gtx layout.Context) layout.Dimensions {
						it := items[idx]
						return u.attachChipState(gtx, it.label, it.thumb, it.click, it.dropped)
					}
					if ci == 0 {
						return chip(gtx)
					}
					return layout.Inset{Left: unit.Dp(attachChipGapDp)}.Layout(gtx, chip)
				}))
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, chips...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
}

// attachSlotRect 附件槽窗口系矩形（D85 tooltip 直采；布局拓扑 [pad16][附件槽20]…，
// 内容按终位整盒排版 → 锚定 pillEnd 左缘）。
func attachSlotRect(pillEnd image.Rectangle, padX, rowH, iconPx int) image.Rectangle {
	y := pillEnd.Min.Y + (rowH-iconPx)/2
	x := pillEnd.Min.X + padX
	return image.Rect(x, y, x+iconPx, y+iconPx)
}

// attachClick 单个暂存 chip 的点击控件 get-or-create（D186：按暂存序号缓存——Gio 要求
// 同帧内每个可点元素标签唯一，故不能共用一个 Clickable）。
func (u *UI) attachClick(i int) *widget.Clickable {
	for len(u.attachClicks) <= i {
		u.attachClicks = append(u.attachClicks, new(widget.Clickable))
	}
	return u.attachClicks[i]
}

// editAttClick 编辑态已有附件的点击控件（D188：按节点内附件序号缓存，与暂存的序号缓存
// 分开——两套序号含义不同，同一帧里可能同时出现在 chip 排中）。
func (u *UI) editAttClick(i int) *widget.Clickable {
	if u.editAttClicks == nil {
		u.editAttClicks = map[int]*widget.Clickable{}
	}
	cl, ok := u.editAttClicks[i]
	if !ok {
		cl = new(widget.Clickable)
		u.editAttClicks[i] = cl
	}
	return cl
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
	paint.FillShape(gtx.Ops, tok().iconDim, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// record 登记可见元素（D44–D48、D62）：保存真实轮廓 + 可见裁剪区 + 底色，全帧合成的
// vis 覆盖度（fadeFrame）由此推导。零半径、裁剪后为空的元素不登记；带内元素**必须登记**
// （D62：带渐变只作用于登记形状的像素——取代旧「带整行单绘」机制）。
func (u *UI) record(abs image.Rectangle, radius int, fill color.NRGBA, clipRect image.Rectangle) {
	if radius <= 0 || abs.Empty() {
		return
	}
	vis := abs.Intersect(clipRect)
	if vis.Empty() {
		return
	}
	u.shapes = append(u.shapes, drawShape{outline: abs, clip: clipRect, radius: radius, fill: fill})
}

// featherWidth 元素边缘渐隐带的宽（px，D47/D48 响应式 + D70 全元素统一）：带宽恒取
// **输入胶囊的带宽** round(Dp(inputRowDp)×featherRatio)——同屏各元素边缘剖面一致（按
// 元素自身短边算会让气泡/chip/胶囊各得不同带宽）；仍夹到 [Dp(featherMinDp),
// Dp(featherMaxDp)] 且不超过该元素短边 1/3（极小元素防被整带吃掉）。Dp 换算保证
// DPI/缩放跟随、不写死 px。纯逻辑，可测。
func featherWidth(m unit.Metric, w, h int) int {
	short := w
	if h < short {
		short = h
	}
	f := int(float64(m.Dp(inputRowDp))*featherRatio + 0.5)
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
