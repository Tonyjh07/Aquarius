package uigui

// 模块磁贴的控件状态与手势：三列网格常量、按下与拖拽判定、模块弹窗的开/关/提交、顺序移动。

import (
	"image"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
)

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
