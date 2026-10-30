package uigui

// 模块库网格与磁贴的绘制：网格行、磁贴面（含浮动态）、新增磁贴、落位槽与元信息行。

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// moduleGridRow 「模块库」整块网格作为一个列表行渲染（D148）。
//
// 网格整体占一行：拖动落位要在同一坐标系里算。分成多行后每个磁贴分属不同行的
// 局部空间，落位判定要么错位、要么得自己累加偏移。
func (f *promptEditor) moduleGridRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	gap := gtx.Dp(modGridGapDp)
	tileH := gtx.Dp(modTileHdp)
	width := gtx.Constraints.Max.X
	tileW := (width - (modGridCols-1)*gap) / modGridCols
	n := len(f.promptMods)
	// 末尾一个「＋ 新增模块」格：与模块格同尺寸，参与同一套排布。
	rows := (n + modGridCols) / modGridCols

	slotRect := func(pos int) image.Rectangle {
		col, row := pos%modGridCols, pos/modGridCols
		x, y := col*(tileW+gap), row*(tileH+gap)
		return image.Rect(x, y, x+tileW, y+tileH)
	}

	// 拖动中：源磁贴从序列里摘掉、其余格子**实时让位**，落点留空槽。只画一条
	// 插入线不够——用户看不出「松开之后这一格会挪到哪」，让位才能让结果可预判。
	dragging := f.modDrag.active && f.modDrag.from >= 0 && f.modDrag.from < n
	order := make([]int, 0, n+1)
	for i := 0; i < n; i++ {
		if dragging && i == f.modDrag.from {
			continue
		}
		order = append(order, i)
	}
	if dragging {
		drop := f.modDrag.drop
		if drop > len(order) {
			drop = len(order)
		}
		if drop < 0 {
			drop = 0
		}
		order = append(order, -1)
		copy(order[drop+1:], order[drop:])
		order[drop] = -1
	} else {
		order = append(order, -1) // 末尾「新增」格
	}

	gtx.Constraints = layout.Exact(image.Pt(width, rows*tileH+(rows-1)*gap))
	var dropRect image.Rectangle
	for pos, idx := range order {
		r := slotRect(pos)
		switch {
		case idx < 0 && dragging:
			dropRect = r
			f.drawDropSlot(gtx, th, r)
		case idx < 0:
			f.drawAddTile(gtx, th, r)
		default:
			f.drawModuleTile(gtx, th, r, idx)
		}
	}
	if dragging && !dropRect.Empty() {
		// 浮动卡贴着空槽偏一点点：偏移过大就会切掉邻格文字（读起来像渲染坏了），
		// 过小又与空槽分不清。
		offset := image.Pt(f.modDrag.dx+gtx.Dp(6), f.modDrag.dy+gtx.Dp(10))
		f.drawTileFace(gtx, th, dropRect.Add(offset), f.promptMods[f.modDrag.from], true)
	}
	return layout.Dimensions{Size: image.Pt(width, rows*tileH+(rows-1)*gap)}
}

// drawModuleTile 单个模块磁贴（指针路由锚点与外观一起画，保证命中区与视觉一致）。
func (f *promptEditor) drawModuleTile(gtx layout.Context, th *material.Theme, r image.Rectangle, idx int) {
	f.drawTileFace(gtx, th, r, f.promptMods[idx], false)
	// 命中区：每个磁贴自带 tag，Gio 据此做坐标变换——不必手工把窗口坐标换算回
	// 网格局部坐标（换算错了会「拖这个格子、落位到那个格子」，画面上还看不出来）。
	hit := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &f.tileCtl(idx).tag)
	hit.Pop()
}

// drawTileFace 磁贴外观（静置磁贴与拖动浮动卡共用；floating 只改名字颜色与描边）。
func (f *promptEditor) drawTileFace(gtx layout.Context, th *material.Theme, r image.Rectangle, pf *promptModuleForm, floating bool) {
	pp := paperOf()
	rad := gtx.Dp(unit.Dp(pp.rCard))
	if floating { // 浮动卡：阴影抬起来 + 品牌色描一圈，让「它被拎起来了」成立
		paperShadow(gtx, r, rad, 6, 6, 0x28)
		paint.FillShape(gtx.Ops, pp.brand, clip.UniformRRect(r, rad).Op(gtx.Ops))
		r = r.Inset(gtx.Dp(2))
		rad -= gtx.Dp(2)
	}
	paperPanel(gtx, r, rad, gtx.Dp(1), pp.card, pp.hairline)

	name := strings.TrimSpace(pf.name.Text())
	if name == "" {
		name = "（未命名）"
	}
	pad := gtx.Dp(10)
	gtx.Constraints = layout.Exact(image.Pt(r.Dx()-2*pad, r.Dy()-2*pad))
	stack := op.Offset(r.Min.Add(image.Pt(pad, pad))).Push(gtx.Ops)
	defer stack.Pop()
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperBodySp(th), name)
			l.Color = pp.text
			l.MaxLines, l.Truncator = 1, "…"
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, paperMicroSp(th), moduleMeta(pf, f.promptRefs))
			l.Color = pp.text3
			l.MaxLines, l.Truncator = 1, "…"
			return l.Layout(gtx)
		}),
	)
}

// moduleMeta 磁贴第二行：组/叶 + 被引用次数。
//
// 被引用次数是这一页最需要一眼看到的信息——改了模块却不知道它有没有被用上，
// 就只能靠记忆去比对「人格引用」那一段。
func moduleMeta(pf *promptModuleForm, refs []*promptRefForm) string {
	name := strings.TrimSpace(pf.name.Text())
	kind := "正文"
	if len(splitRefList(pf.children.Text())) > 0 {
		kind = "组头"
	}
	used := 0
	for _, rf := range refs {
		if strings.TrimSpace(rf.name.Text()) == name {
			used++
		}
	}
	if used == 0 {
		return kind + " · 未引用"
	}
	return kind + " · 被引用 " + strconv.Itoa(used) + " 次"
}

// drawAddTile 「＋ 新增模块」格（浅底 + 居中加号）。
func (f *promptEditor) drawAddTile(gtx layout.Context, th *material.Theme, r image.Rectangle) {
	// 新增格：虚线感的浅面——用 hairline 描边 + 比卡片更淡的底，读起来是「空格位」，
	// 不是「又一个模块」。
	pp := paperOf()
	paperPanel(gtx, r, gtx.Dp(unit.Dp(pp.rCard)), gtx.Dp(1), pp.sunken, pp.hairline)
	gtx.Constraints = layout.Exact(r.Size())
	stack := op.Offset(r.Min).Push(gtx.Ops)
	defer stack.Pop()
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, paperNoteSp(th), "＋ 新增模块")
		l.Color = pp.text2
		return l.Layout(gtx)
	})
	// 单击开新模块编辑窗（右键同样；拖动不适用）。
	hit := clip.Rect(image.Rectangle{Max: r.Size()}).Push(gtx.Ops)
	event.Op(gtx.Ops, &f.tileCtl(len(f.promptMods)).tag)
	hit.Pop()
}

// drawDropSlot 落位空槽：品牌色描一圈 + 浅底。用「实心底 + 内缩一圈浅底」画环——
// Gio 没有描边，两次填充才画得出真正的空心（直接给透明底描边会把整块涂成描边色）。
func (f *promptEditor) drawDropSlot(gtx layout.Context, th *material.Theme, r image.Rectangle) {
	pp := paperOf()
	rad := gtx.Dp(unit.Dp(pp.rCard))
	paint.FillShape(gtx.Ops, pp.brand, clip.UniformRRect(r, rad).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, pp.win, clip.UniformRRect(r.Inset(gtx.Dp(1)), rad-gtx.Dp(1)).Op(gtx.Ops))
}
