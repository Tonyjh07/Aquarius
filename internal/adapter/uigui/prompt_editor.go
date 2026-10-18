package uigui

// prompt_editor.go 人格模块树编辑区（D154）：**一份实现、两个宿主**——设置窗的
// 「系统提示词」页，与会话历史窗遮罩里人格节点的「默认模板」页签。
//
// 为什么抽成一个类型而不是两处各画一套：模块库磁贴的手势（单击填入 / 右键编辑 /
// 按住拖动排序）、人格引用的有序编辑、内置模块引用、组合预览——任何一处改了规则，
// 另一处会静默漂移，而漂移只会在「设置里改了却和遮罩里看到的不一样」时才被发现。
// 宿主只负责：外面套什么容器、页脚放什么钮、保存写哪条路径。
//
// 行计划与行内容也在这里（rows / rowBody），两个宿主用同一份行表渲染；卡片/留白的
// 版面由 rowLayout 统一（settings_rows.go）。

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// promptModuleForm 单个模块节点的编辑控件（S7/D144；本窗 goroutine 独占）。orig = 快照
// 原名（保存提示与「改名同步引用」的口径）。
type promptModuleForm struct {
	orig     string
	name     widget.Editor
	text     widget.Editor
	children widget.Editor
}

// promptRefForm 单个人格引用的编辑控件（S7/D144；有序：上移/下移/删除）。
type promptRefForm struct {
	name widget.Editor
	up   widget.Clickable
	down widget.Clickable
	del  widget.Clickable
}

// promptEditor 人格编辑区状态（设置页与遮罩模板页签共用）。
//
// 字段名即宿主侧的使用名（settingsForm 内嵌本类型 → f.promptMods / f.modEdit 等写法
// 在设置页里保持不变），避免两套命名让同一个东西看起来像两件事。
type promptEditor struct {
	sysPrompt widget.Editor // 多行：默认人格（= 内置 @default 正文；清空 = 内置默认）

	promptMods   []*promptModuleForm
	promptRefs   []*promptRefForm
	promptRefAdd widget.Clickable
	promptBtns   map[string]*widget.Clickable // 内置模块「引用」按钮（get-or-create 按 key）
	promptPrev   string                       // 组合预览（每帧现算；PreviewPrompts 提供）
	builtins     []BuiltinModuleView          // 内置只读模块展示项（装配根给的名集）

	// 模块库网格（D148）：三列磁贴 + 右键编辑弹窗 + 左键拖动排序。
	// modTiles 每格一个手势控件（长度随模块数增长，下标与 promptMods 对齐，
	// 末尾多一格 = 「＋ 新增模块」）；均为宿主 goroutine 独占。
	modTiles []*modTileCtl
	modDrag  modDragState
	modEdit  modEditState

	// overlayList 遮罩里的滚动容器（裸 layout.List：卡片里不画常驻滚动条）。
	overlayList widget.List
}

// init 控件骨架（map 初值；快照无关）。零值不可用——两个宿主都必须经它构造。
func (f *promptEditor) init() {
	f.promptBtns = map[string]*widget.Clickable{}
	f.overlayList.List.Axis = layout.Vertical
}

// load 以快照填表（开窗 / 打开遮罩模板页签时现取 config）。
func (f *promptEditor) load(s SettingsSnapshot) {
	f.sysPrompt.SetText(s.SystemPrompt)
	f.builtins = s.Builtins
	f.loadPrompts(s.PromptModules, s.PromptPersona)
}

// newPromptEditor 独立实例（遮罩模板页签用；不挂在设置表单上）。
func newPromptEditor(u *UI) *promptEditor {
	f := &promptEditor{}
	f.init()
	if u.opts.Settings == nil {
		return f
	}
	f.load(u.opts.Settings())
	return f
}

// rows 人格编辑区行计划（设置页「系统提示词」页与遮罩「默认模板」页签共用）：
// 模块树说明 → 默认人格多行档 → 有序引用 → 模块库网格 → 内置模块 → 组合预览。
func (f *promptEditor) rows() []rowDesc {
	rows := []rowDesc{
		sec("人格（模块树组合）"),
		{kind: rowPromptNote},
		{kind: rowSysPrompt},
		sec("人格引用（有序拼接）"),
	}
	for i := range f.promptRefs {
		rows = append(rows, rowDesc{kind: rowPromptRef, field: i})
	}
	rows = append(rows, rowDesc{kind: rowPromptRefAdd})
	rows = append(rows, sec("模块库"))
	// 模块库整体占**一个**行（D148）：磁贴在模块库网格里自行排布，模块增删
	// 只改网格内容、不改行数。逐模块一行是改版前的形态（一屏只能看到一个模块）。
	rows = append(rows, rowDesc{kind: rowPromptGrid})
	if len(f.builtins) > 0 {
		rows = append(rows, sec("内置模块（只读，可引用）"))
		for i := range f.builtins {
			rows = append(rows, rowDesc{kind: rowPromptBuiltin, field: i})
		}
	}
	return append(rows, sec("组合预览"), rowDesc{kind: rowPromptPreview})
}

// rowBody 人格编辑区的行内容；ok=false = 不是本区的行（交宿主分派）。
func (f *promptEditor) rowBody(gtx layout.Context, th *material.Theme, rows []rowDesc, i int) (layout.Dimensions, bool) {
	r := rows[i]
	switch r.kind {
	// 分区/页标题也归本区（设置页里由 settingsForm.rowBody 分派；遮罩宿主只有本区行表，
	// 不接管这两类会静默画成空白——实测：模板页签里「模块库」等标题整排消失）。
	case rowTitle:
		return paperTitle(th, r.text)(gtx), true
	case rowSection:
		return paperSection(th, r.text)(gtx), true
	case rowSysPrompt:
		return editorBoxH(th, &f.sysPrompt, "默认人格（= 内置 @default 正文；清空 = 内置默认）", 80)(gtx), true
	case rowPromptNote:
		l := material.Body2(th, "人格 = 有序引用的展开结果；模块可分组（children 逗号分隔，可多级）。"+
			"改动只对新会话生效，且重启后应用。")
		l.Color = paperOf().text3
		return l.Layout(gtx), true
	case rowPromptGrid:
		return f.moduleGridRow(gtx, th), true
	case rowPromptRef:
		return f.promptRefRow(gtx, th, r.field), true
	case rowPromptRefAdd:
		return paperButton(th, &f.promptRefAdd, "＋ 添加引用", paperBtnSecondary, true)(gtx), true
	case rowPromptBuiltin:
		return f.promptBuiltinRow(gtx, th, f.builtins[r.field]), true
	case rowPromptPreview:
		text := f.promptPrev
		l := material.Body2(th, text)
		if strings.TrimSpace(text) == "" {
			text = "（保存后按模块树组合；预览需装配根接线）"
			l = material.Body2(th, text)
			l.Color = paperOf().text3
		} else {
			l.Color = paperOf().text2
		}
		return l.Layout(gtx), true
	}
	return layout.Dimensions{}, false
}

// refEvents 引用/内置按钮事件（帧首；两种宿主都调）。磁贴手势另有开关（模块编辑
// 弹窗开着时不许拖动），由宿主按页签可见性决定是否调 moduleGridEvents。
func (f *promptEditor) refEvents(gtx layout.Context) {
	for f.promptRefAdd.Clicked(gtx) {
		f.addPromptRef("")
	}
	for key, btn := range f.promptBtns {
		if !btn.Clicked(gtx) {
			continue
		}
		if name, ok := strings.CutPrefix(key, "ref:"); ok {
			f.addPromptRef(name)
		}
	}
	for i, rf := range f.promptRefs {
		for rf.up.Clicked(gtx) {
			f.movePromptRef(i, -1)
		}
		for rf.down.Clicked(gtx) {
			f.movePromptRef(i, 1)
		}
		for rf.del.Clicked(gtx) {
			f.delPromptRef(i)
		}
	}
}

// preview 组合预览每帧现算（装配根纯计算；未接线 = 保持空）。
func (f *promptEditor) preview(u *UI) {
	if u.opts.PreviewPrompts == nil {
		return
	}
	f.promptPrev = u.opts.PreviewPrompts(f.promptViews(), f.promptRefList(), f.sysPrompt.Text())
}

// loadPrompts 以快照重建人格模块树与引用编辑区（开窗填表；S7/D144）。
func (f *promptEditor) loadPrompts(mods []PromptModuleView, persona []string) {
	f.promptMods = make([]*promptModuleForm, 0, len(mods))
	for _, m := range mods {
		pf := &promptModuleForm{orig: m.Name}
		pf.name.SingleLine = true
		pf.children.SingleLine = true
		pf.name.SetText(m.Name)
		pf.text.SetText(m.Text)
		pf.children.SetText(strings.Join(m.Children, ", "))
		f.promptMods = append(f.promptMods, pf)
	}
	f.promptRefs = make([]*promptRefForm, 0, len(persona))
	for _, name := range persona {
		rf := &promptRefForm{}
		rf.name.SingleLine = true
		rf.name.SetText(name)
		f.promptRefs = append(f.promptRefs, rf)
	}
}

// addPromptModule 追加一个空白模块编辑行（帧循环与测试共用；空名行保存时跳过）。
func (f *promptEditor) addPromptModule() {
	pf := &promptModuleForm{}
	pf.name.SingleLine = true
	pf.children.SingleLine = true
	f.promptMods = append(f.promptMods, pf)
}

// addPromptRef 追加一个人格引用行（name 空 = 待填；内置模块「引用」按钮带名调用）。
func (f *promptEditor) addPromptRef(name string) {
	rf := &promptRefForm{}
	rf.name.SingleLine = true
	rf.name.SetText(name)
	f.promptRefs = append(f.promptRefs, rf)
}

// movePromptRef 人格引用上/下移（delta = -1 上移 / +1 下移；越界不动）。顺序即拼接顺序。
func (f *promptEditor) movePromptRef(i, delta int) {
	j := i + delta
	if i < 0 || i >= len(f.promptRefs) || j < 0 || j >= len(f.promptRefs) {
		return
	}
	f.promptRefs[i], f.promptRefs[j] = f.promptRefs[j], f.promptRefs[i]
}

// delPromptRef 删除第 i 个人格引用（越界 no-op）。
func (f *promptEditor) delPromptRef(i int) {
	if i < 0 || i >= len(f.promptRefs) {
		return
	}
	f.promptRefs = append(f.promptRefs[:i], f.promptRefs[i+1:]...)
}

// delPromptModule 删除第 i 个模块（越界 no-op）。
func (f *promptEditor) delPromptModule(i int) {
	if i < 0 || i >= len(f.promptMods) {
		return
	}
	f.promptMods = append(f.promptMods[:i], f.promptMods[i+1:]...)
}

// promptViews 表单现状 → 模块视图（空名行跳过；子模块按逗号/空白切分去重）。
func (f *promptEditor) promptViews() []PromptModuleView {
	out := make([]PromptModuleView, 0, len(f.promptMods))
	for _, pf := range f.promptMods {
		name := strings.TrimSpace(pf.name.Text())
		if name == "" {
			continue
		}
		out = append(out, PromptModuleView{
			Name:     name,
			Text:     pf.text.Text(),
			Children: splitRefList(pf.children.Text()),
		})
	}
	return out
}

// promptRefList 表单现状 → 人格引用列表（空行跳过，保持表单顺序 = 拼接顺序）。
func (f *promptEditor) promptRefList() []string {
	out := make([]string, 0, len(f.promptRefs))
	for _, rf := range f.promptRefs {
		if name := strings.TrimSpace(rf.name.Text()); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// ── 模块库网格（D148）────────────────────────────────────────────────────────
//
// **为什么磁贴不是一行一行的表单行**：原先一个模块 = 一张纵排表单（名称/正文/子模块/
// 删除），一屏只能看到一两个模块；首要诉求是「有哪些模块」要一眼可见，故改成三列磁贴，
// 整个网格占**一个**列表行（逐模块一行会让磁贴分属不同行坐标系，落位判定要么错位、
// 要么得自己累加偏移）。
//
// **单击与拖动怎么分**：按下后位移超过 modDragSlopDp 才算拖动。没有这条阈值，手抖就会
// 把「想填入」变成「悄悄换了顺序」——顺序会写进配置，静默改动代价很高。

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
	// modScrimAlpha 编辑弹窗背后的遮罩浓度。比历史窗的查看遮罩浅——编辑时仍需
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

// modDragState 拖动排序状态（promptEditor 持有；仅宿主 goroutine 读写）。
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
func (f *promptEditor) tileCtl(i int) *modTileCtl {
	for len(f.modTiles) <= i {
		f.modTiles = append(f.modTiles, &modTileCtl{})
	}
	return f.modTiles[i]
}

// openModuleEditor 打开编辑窗（idx < 0 = 新增模块）：控件初值取自表单现状。
func (f *promptEditor) openModuleEditor(idx int) {
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
func (f *promptEditor) closeModuleEditor() { f.modEdit.open = false }

// commitModuleEditor 把编辑窗内容写回表单并关窗。
//
// 名称就是模块身份：改名后引用列表里指向旧名的项必须同步，否则组合会凭空少一段
// ——而表单上看不出任何异常（引用名还在，只是再也对不上模块）。
func (f *promptEditor) commitModuleEditor() {
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
func (f *promptEditor) movePromptModule(i, j int) {
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
func (f *promptEditor) moduleGridEvents(gtx layout.Context) {
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
func (f *promptEditor) cancelPress(c *modTileCtl, pid pointer.ID) bool {
	if !c.pressed || pid != c.pid {
		return false
	}
	c.pressed = false
	f.modDrag.active = false
	return true
}

// updateModuleDrag 拖动中：记录源、位移与当前落位。
func (f *promptEditor) updateModuleDrag(gtx layout.Context, from int, d f32.Point) {
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
func (f *promptEditor) dropModuleDrag(gtx layout.Context) {
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

// ── 模块编辑弹窗（D148）─────────────────────────────────────────────────────
//
// 右键磁贴直接弹出，不再多一层菜单：菜单只是多一次点击，而这些字段本来就该在同一个
// 表单里改（改名与改正文是同一件事的两面）。删除放在弹窗左下、与「保存」隔开。

// moduleEditorFrame 右键编辑窗（模态）：遮罩铺满宿主盒 + 居中卡片。
//
// **模态怎么成立**（与历史窗遮罩同一机制）：Gio 把一次点击投给所有命中的 handler，
// 所以「画一层挡板」挡不住背景。真正起作用的是整盒输入区排在命中链顶——背景控件
// 收不到指针事件；背景侧的显式不消费是纵深防御。宿主须把它画在最后一层（盖住
// 自己的页脚钮）。
func (f *promptEditor) moduleEditorFrame(gtx layout.Context, th *material.Theme) (layout.Dimensions, bool) {
	st := &f.modEdit
	if !st.open {
		return layout.Dimensions{}, false
	}
	area := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	gtx.Event(key.FocusFilter{Target: &st.tag})
	event.Op(gtx.Ops, &st.tag)
	area.Pop()

	// Esc 关闭：弹窗是编辑区里唯一能「取消」的地方，没有它改错了只能关窗重开。
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &st.tag, Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
			f.closeModuleEditor()
			return layout.Dimensions{}, false
		}
	}
	// 点卡片外关闭（抬起点不在卡片内）。card 在第一帧还是零值，此时不能判——
	// 否则弹窗刚开就被自己这一帧的抬起关掉。
	if !st.card.Empty() {
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: &st.tag, Kinds: pointer.Release})
			if !ok {
				break
			}
			e, isPtr := ev.(pointer.Event)
			if !isPtr {
				continue
			}
			p := image.Pt(int(e.Position.X), int(e.Position.Y))
			if !p.In(st.card) {
				f.closeModuleEditor()
				return layout.Dimensions{}, false
			}
		}
	}

	// 动作先消费：本帧点了保存/删除，就不该再按旧内容画一帧。
	if st.btnClose.Clicked(gtx) || st.btnCancel.Clicked(gtx) {
		f.closeModuleEditor()
		return layout.Dimensions{}, false
	}
	if st.btnSave.Clicked(gtx) {
		f.commitModuleEditor()
		return layout.Dimensions{}, false
	}
	if st.btnDel.Clicked(gtx) && st.idx >= 0 {
		f.delPromptModule(st.idx)
		f.closeModuleEditor()
		return layout.Dimensions{}, false
	}

	w, h := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	dw, dh := gtx.Dp(modDlgWdp), gtx.Dp(modDlgHdp)
	if lim := w - 2*gtx.Dp(24); dw > lim {
		dw = lim
	}
	if lim := h - 2*gtx.Dp(24); dh > lim {
		dh = lim
	}
	card := image.Rect((w-dw)/2, (h-dh)/2, (w+dw)/2, (h+dh)/2)
	st.card = card

	// 遮罩铺满宿主盒（含页脚）：只盖内容区会让「保存」在弹窗背后仍是亮的，
	// 读起来像是可以直接点。
	pp := paperOf()
	paint.FillShape(gtx.Ops, scrimColor(),
		clip.Rect{Max: image.Pt(w, h)}.Op())
	// 弹窗是浮层：阴影必须比卡片更远（spread 10 vs 3），否则「盖在上面」不成立。
	rad := gtx.Dp(unit.Dp(pp.rCard))
	paperShadow(gtx, card, rad, 14, 8, 0x38)
	paperPanel(gtx, card, rad, gtx.Dp(1), pp.card, pp.hairline)

	stack := op.Offset(card.Min).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Constraints = layout.Exact(card.Size())
	layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.moduleEditorTitle(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "名称", editorBox(th, &st.name, "唯一名；不以 @ 开头（保留给内置）"))
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "正文 / 组头", editorBoxH(th, &st.text, "叶：正文；组：组头（可空）", 84))
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return fieldRow(gtx, th, "子模块", editorBox(th, &st.children, "逗号分隔；填了即成为组，清空即转为叶"))
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperMicroSp(th), "改结构不动已有会话：人格在新建会话时才重新组合。")
				l.Color = pp.text3
				return l.Layout(gtx)
			}),
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.moduleEditorButtons(gtx, th)
			}),
		)
	})
	return layout.Dimensions{Size: image.Pt(w, h)}, true
}

// moduleEditorTitle 标题行：标题带模块名（一眼看出改的是哪个）+ 右上关闭。
func (f *promptEditor) moduleEditorTitle(gtx layout.Context, th *material.Theme) layout.Dimensions {
	st := &f.modEdit
	title := "编辑模块 · " + st.name.Text()
	if st.idx < 0 {
		title = "新增模块"
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, paperTitleSp(th), title)
				l.Font.Weight = font.Bold
				l.Color = paperOf().text
				return l.Layout(gtx)
			}(gtx)
		}),
		layout.Rigid(overlayBtn(&st.btnClose, th, "✕", false, true)),
	)
}

// moduleEditorButtons 底行：删除（左）+ 取消 / 保存（右）。
func (f *promptEditor) moduleEditorButtons(gtx layout.Context, th *material.Theme) layout.Dimensions {
	st := &f.modEdit
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if st.idx < 0 { // 新增时没有可删的东西
				return layout.Dimensions{}
			}
			return paperButton(th, &st.btnDel, "删除模块", paperBtnDanger, true)(gtx)
		}),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(paperButton(th, &st.btnCancel, "取消", paperBtnSecondary, true)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(paperButton(th, &st.btnSave, "保存", paperBtnPrimary, true)),
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// scrimColor 遮罩色（半透黑；深浅主题同值——遮罩的作用是压暗，不是配色）。
func scrimColor() color.NRGBA {
	return color.NRGBA{R: 0x10, G: 0x14, B: 0x18, A: modScrimAlpha}
}

// ── 行内容：引用行与内置模块行（D144）──────────────────────────────────────

// promptRefRow 人格引用行（S7/D144）：序号 + 引用名 + 上移/下移/删除（顺序 = 拼接顺序）。
func (f *promptEditor) promptRefRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	rf := f.promptRefs[i]
	small := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, c, label, paperBtnSecondary, true))
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, strconv.Itoa(i+1)+".")
			l.Color = paperOf().text3
			return layout.Inset{Right: unit.Dp(6)}.Layout(gtx, l.Layout)
		}),
		layout.Flexed(1, editorBox(th, &rf.name, "引用名（模块名或内置 @名）")),
		layout.Rigid(small(&rf.up, "↑")),
		layout.Rigid(small(&rf.down, "↓")),
		layout.Rigid(small(&rf.del, "✕")),
	)
}

// promptBuiltinRow 内置只读模块行（S7/D144）：引用名 + 说明 + 「引用」按钮（加入人格引用）。
func (f *promptEditor) promptBuiltinRow(gtx layout.Context, th *material.Theme, b BuiltinModuleView) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Body2(th, b.Name).Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, b.Label)
					l.Color = paperOf().text2
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if f.promptBtns == nil { // 零值构造（测试/直建）也要能用
				f.promptBtns = map[string]*widget.Clickable{}
			}
			btn := f.promptBtns["ref:"+b.Name]
			if btn == nil {
				btn = new(widget.Clickable)
				f.promptBtns["ref:"+b.Name] = btn
			}
			return paperButton(th, btn, "引用", paperBtnSecondary, true)(gtx)
		}),
	)
}
