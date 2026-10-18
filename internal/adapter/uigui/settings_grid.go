package uigui

// settings_grid.go 「系统提示词」页的模块库网格（D148）：三列平铺的模块磁贴 +
// 两种手势（右键编辑、左键单击填入 / 按住拖动排序）。
//
// **为什么磁贴不是一行一行的表单行**：原先一个模块 = 一张纵排表单（名称/正文/子模块/
// 删除），一屏只能看到一两个模块；首要诉求是「有哪些模块」要一眼可见，故改成三列磁贴，
// 整个网格占**一个**列表行。
//
// **单击与拖动怎么分**：按下后位移超过 modDragSlopDp 才算拖动。没有这条阈值，
// 手抖就会把「想填入」变成「悄悄换了顺序」——顺序会写进配置，静默改动代价很高。

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	// modGridCols 模块磁贴列数（三列平铺）。
	modGridCols = 3
	// modTileHdp 磁贴高度（名称一行 + 元信息一行）。
	modTileHdp unit.Dp = 58
	// modGridGapDp 磁贴间距。
	modGridGapDp unit.Dp = 10
	// modDragSlopDp 单击 / 拖动判定阈值：按下后位移超过它才判为拖拽排序。
	modDragSlopDp unit.Dp = 4
	// modDlgWdp / modDlgHdp 编辑弹窗尺寸。
	modDlgWdp unit.Dp = 380
	modDlgHdp unit.Dp = 348
	// modScrimAlpha 编辑弹窗背后的遮罩浓度。比历史窗的查看遮罩浅——设置页仍需
	// 透出「改的是哪一页」，不是纯阅读态。
	modScrimAlpha uint8 = 0x46
)

// modTileCtl 单个磁贴的指针手势。每个磁贴自带 tag：Gio 按 tag 做命中与坐标变换，
// 因此不需要手工把窗口坐标换算回网格局部坐标（换算错了就会「拖这个格子、落位到
// 那个格子」，而且从画面上很难看出来）。
type modTileCtl struct {
	tag struct{}

	pressed bool
	pid     pointer.ID
	start   f32.Point
	moved   bool // 已越过阈值 = 本次是拖拽，抬起时不算单击
	right   bool // 按下时用的是右键 → 抬起弹编辑窗
}

// modDragState 拖动排序状态（settingsForm 持有；仅本窗 goroutine 读写）。
type modDragState struct {
	active     bool
	from, drop int
	dx, dy     int // 相对按下点的位移（物理像素）
}

// modEditState 编辑弹窗状态（同一时刻至多一个；-1 = 新增）。
type modEditState struct {
	open bool
	idx  int

	name, text, children                 widget.Editor
	btnSave, btnCancel, btnDel, btnClose widget.Clickable
	tag                                  struct{}
	card                                 image.Rectangle // 本帧卡片盒（窗口系）
}

// tileCtl 取第 i 个磁贴的手势控件（按需扩容；i == len(promptMods) = 「新增」格）。
func (f *settingsForm) tileCtl(i int) *modTileCtl {
	for len(f.modTiles) <= i {
		f.modTiles = append(f.modTiles, &modTileCtl{})
	}
	return f.modTiles[i]
}

// openModuleEditor 打开编辑窗（idx < 0 = 新增模块）：控件初值取自表单现状。
func (f *settingsForm) openModuleEditor(idx int) {
	st := &f.modEdit
	st.open = true
	st.idx = idx
	st.name.SingleLine = true
	st.children.SingleLine = true
	st.text.SingleLine = false
	st.name.SetText("")
	st.text.SetText("")
	st.children.SetText("")
	if idx >= 0 && idx < len(f.promptMods) {
		pf := f.promptMods[idx]
		st.name.SetText(pf.name.Text())
		st.text.SetText(pf.text.Text())
		st.children.SetText(pf.children.Text())
	}
}

// closeModuleEditor 关窗（不写回）。
func (f *settingsForm) closeModuleEditor() { f.modEdit.open = false }

// commitModuleEditor 把编辑窗内容写回表单并关窗。
//
// 名称就是模块身份：改名后引用列表里指向旧名的项必须同步，否则组合会凭空少一段
// ——而表单上看不出任何异常（引用名还在，只是再也对不上模块）。
func (f *settingsForm) commitModuleEditor() {
	st := &f.modEdit
	name := strings.TrimSpace(st.name.Text())
	if name == "" { // 空名 = 不要这个模块（与保存时跳过空名行同口径）
		f.closeModuleEditor()
		return
	}
	if st.idx < 0 || st.idx >= len(f.promptMods) {
		f.promptMods = append(f.promptMods, &promptModuleForm{})
		st.idx = len(f.promptMods) - 1
	}
	pf := f.promptMods[st.idx]
	old := strings.TrimSpace(pf.name.Text())
	pf.name.SetText(name)
	pf.text.SetText(st.text.Text())
	pf.children.SetText(st.children.Text())
	if old != "" && old != name {
		for _, rf := range f.promptRefs {
			if strings.TrimSpace(rf.name.Text()) == old {
				rf.name.SetText(name)
			}
		}
	}
	f.closeModuleEditor()
}

// movePromptModule 把第 i 个模块挪到第 j 位（拖拽落位；越界或原地 no-op）。
func (f *settingsForm) movePromptModule(i, j int) {
	n := len(f.promptMods)
	if i < 0 || i >= n || j < 0 || j >= n || i == j {
		return
	}
	pf := f.promptMods[i]
	if i < j {
		copy(f.promptMods[i:j], f.promptMods[i+1:j+1])
	} else {
		copy(f.promptMods[j+1:i+1], f.promptMods[j:i])
	}
	f.promptMods[j] = pf
}

// moduleGridEvents 消费网格内所有磁贴的指针事件（帧首调用，先于渲染）。
//
// 三种结果：右键抬起 → 开编辑窗；左键拖动 → 实时更新落位；左键原地抬起 → 填入组合。
// 「新增」格（i == len(promptMods)）也走同一套：单击开新模块编辑窗，不可拖动。
func (f *settingsForm) moduleGridEvents(gtx layout.Context) {
	n := len(f.promptMods)
	slop := gtx.Dp(modDragSlopDp)
	for i := 0; i <= n; i++ {
		c := f.tileCtl(i)
		for {
			ev, ok := gtx.Event(pointer.Filter{
				Target: &c.tag,
				Kinds:  pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel,
			})
			if !ok {
				break
			}
			e, isPtr := ev.(pointer.Event)
			if !isPtr {
				continue
			}
			switch e.Kind {
			case pointer.Press:
				c.pressed = true
				c.pid = e.PointerID
				c.start = e.Position
				c.moved = false
				c.right = e.Buttons.Contain(pointer.ButtonSecondary)
			case pointer.Drag:
				if !c.pressed || e.PointerID != c.pid || c.right || i >= n {
					continue
				}
				d := e.Position.Sub(c.start)
				if !c.moved && d.X*d.X+d.Y*d.Y < float32(slop*slop) {
					continue // 还没越过阈值：仍可能是单击
				}
				c.moved = true
				f.updateModuleDrag(gtx, i, d)
			case pointer.Release:
				if !c.pressed || e.PointerID != c.pid {
					continue
				}
				c.pressed = false
				switch {
				case c.right:
					f.openModuleEditor(i)
				case c.moved:
					f.dropModuleDrag(gtx)
				case i >= n:
					f.openModuleEditor(-1) // 「＋ 新增模块」
				default:
					f.addPromptRef(strings.TrimSpace(f.promptMods[i].name.Text()))
				}
			case pointer.Cancel:
				f.cancelPress(c, e.PointerID)
			}
		}
	}
}

// cancelPress 结束本次按压（Cancel / 指针被系统收回）。
//
// **只清自己那一路**：Cancel 会分发给所有监听同一 tag 的控件，无脑清全局拖动状态
// 会让**别的**磁贴的拖动被一次空 Cancel 抹掉——表现是拖动残影突然消失、松手不落位，
// 而且只在有指针取消时复现（鼠标点一下别处、切窗、触控被系统打断）。
func (f *settingsForm) cancelPress(c *modTileCtl, pid pointer.ID) bool {
	if !c.pressed || pid != c.pid {
		return false
	}
	c.pressed = false
	f.modDrag.active = false
	return true
}

// updateModuleDrag 拖动中：记录源、位移与当前落位。
func (f *settingsForm) updateModuleDrag(gtx layout.Context, from int, d f32.Point) {
	step := gtx.Dp(modTileHdp) + gtx.Dp(modGridGapDp)
	half := step / 2
	// 阈值取半格：越过半格说明用户已经「看向」下一格。等整格才让位，
	// 落位会慢半拍，拖起来像粘住了。
	drop := from + (int(d.X)+half)/step + ((int(d.Y)+half)/step)*modGridCols
	if last := len(f.promptMods) - 1; drop > last {
		drop = last
	}
	if drop < 0 {
		drop = 0
	}
	f.modDrag.active = true
	f.modDrag.from, f.modDrag.drop = from, drop
	f.modDrag.dx, f.modDrag.dy = int(d.X), int(d.Y)
	wakeFrame(gtx) // 让位与浮动卡需要立即重绘
}

// dropModuleDrag 结束拖动：落位写入数组。
func (f *settingsForm) dropModuleDrag(gtx layout.Context) {
	if f.modDrag.active {
		f.movePromptModule(f.modDrag.from, f.modDrag.drop)
	}
	f.modDrag.active = false
	wakeFrame(gtx)
}

// moduleGridRow 「模块库」整块网格作为一个列表行渲染（D148）。
//
// 网格整体占一行：拖动落位要在同一坐标系里算。分成多行后每个磁贴分属不同行的
// 局部空间，落位判定要么错位、要么得自己累加偏移。
func (f *settingsForm) moduleGridRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
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
func (f *settingsForm) drawModuleTile(gtx layout.Context, th *material.Theme, r image.Rectangle, idx int) {
	f.drawTileFace(gtx, th, r, f.promptMods[idx], false)
	// 命中区：每个磁贴自带 tag，Gio 据此做坐标变换——不必手工把窗口坐标换算回
	// 网格局部坐标（换算错了会「拖这个格子、落位到那个格子」，画面上还看不出来）。
	hit := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &f.tileCtl(idx).tag)
	hit.Pop()
}

// drawTileFace 磁贴外观（静置磁贴与拖动浮动卡共用；floating 只改名字颜色与描边）。
func (f *settingsForm) drawTileFace(gtx layout.Context, th *material.Theme, r image.Rectangle, pf *promptModuleForm, floating bool) {
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
func (f *settingsForm) drawAddTile(gtx layout.Context, th *material.Theme, r image.Rectangle) {
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
func (f *settingsForm) drawDropSlot(gtx layout.Context, th *material.Theme, r image.Rectangle) {
	pp := paperOf()
	rad := gtx.Dp(unit.Dp(pp.rCard))
	paint.FillShape(gtx.Ops, pp.brand, clip.UniformRRect(r, rad).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, pp.win, clip.UniformRRect(r.Inset(gtx.Dp(1)), rad-gtx.Dp(1)).Op(gtx.Ops))
}
