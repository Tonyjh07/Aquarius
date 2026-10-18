package uigui

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// fieldH 文本档高度下限（dp，经 gtx.Dp 消费）：单行编辑器 + 上下留白（material 无
// 内建输入框边框）。实际高由 editorBox 自适应（≥ 编辑器行高 + 留白，D113 验收补充）。
const fieldH = 40

// fieldLabelW 标签列宽（dp，经 gtx.Dp 消费）：对齐各行控件起点。
const fieldLabelW = 92

// rowKind 表单行类型（动态行计划：provider 编辑区行数随 providers 数量变化，D110②；
// S4d/D142 起按页分组——页序与页内容见 pageRows）。
type rowKind int

// rowInsetDp 每行上下左右的行腔留白（六页共用，D148）。
// 原值 14dp 让表单文字几乎贴住卡片描边，抬到 18dp 后行间与卡内都有了呼吸；
// 这是「设置页密度」的唯一旋钮，调它 = 同时调六页，不要逐页微调。
const rowInsetDp = 18

const (
	rowTitle   rowKind = iota // 页标题（页名）
	rowSection                // 分组小标题（text = 分组名）
	rowProfCurrent
	rowProfName
	rowProfBtns
	rowWelcome // D141：欢迎 / 首次运行引导入口（两个菜单都不带，唯此一处 + 首跑自动）
	rowModelName
	rowPerm
	rowThink
	rowEffort
	rowHotkey
	rowTheme
	rowScale
	rowFontSize
	rowWinSize
	rowNotify
	rowTTS
	rowMaxTurns
	rowMaxCtx
	rowCompact
	rowToolChars
	rowToolTo
	rowSysPrompt
	rowRaw
	rowProvField
	rowProvBtns
	rowProvAdd
	rowProvApply
	// S6/D143：工具与 MCP 页。
	rowBuiltin     // 内置工具行（name = 工具名；field = 快照下标）
	rowPlugin      // 插件行（name = 插件名；field = 快照下标）
	rowToolsEmpty  // 占位/空态（text = 文案）
	rowToolsStatus // 异步动作反馈状态行
	// S7/D144：人格模块树（「系统提示词」页，S7b/D145 独立成页）。
	rowPromptNote    // 说明（text = 文案）
	rowPromptGrid    // 模块库三列网格（D148；整块占一行，模块数不影响行数）
	rowPromptRef     // 人格引用行（field = promptRefs 下标）
	rowPromptRefAdd  // 「＋ 添加引用」
	rowPromptBuiltin // 内置只读模块行（field = builtins 下标）
	rowPromptPreview // 组合预览（只读全文）
)

// provider 字段槽位（rowDesc.field）。
const (
	provNameFld = iota
	provURLFld
	provKeyFld
	provModelsFld
	provTokFld
	provUnsupFld
	provFldN
)

// provFieldMeta provider 字段行的标签与占位提示（下标对齐字段槽位）。
var provFieldMeta = [provFldN]struct{ label, hint string }{
	{"名称", "provider 唯一标识（primary/fallback 按名引用）"},
	{"接口地址", "如 https://api.openai.com/v1"},
	{"API 密钥", "只写不回显；留空保持不变"},
	{"模型列表", "逗号分隔；首个 = 缺省请求模型"},
	{"Tokenizer", "本地 tokenizer.json 路径；空 = 估算"},
	{"不认参数", "逗号分隔；这些参数会被剔除，不发往服务端"},
}

// rowDesc 一行的类型与定位。
type rowDesc struct {
	kind  rowKind
	text  string // rowSection / rowTitle 的文案
	prov  int    // rowProvField / rowProvBtns：provider 下标
	field int    // rowProvField：字段槽位
}

// sec 分组小标题行。
func sec(text string) rowDesc { return rowDesc{kind: rowSection, text: text} }

// pageRows 本页行计划（每帧现算：providers 数量在新增/删除后即变）。
func (f *settingsForm) pageRows(page int) []rowDesc {
	head := rowDesc{kind: rowTitle, text: settingsPageNames[page]} // 页标题 = 页名
	switch page {
	case pageGeneral:
		return []rowDesc{
			head, sec("Profile"),
			{kind: rowProfCurrent}, {kind: rowProfName}, {kind: rowProfBtns},
			{kind: rowWelcome},
			sec("权限"), {kind: rowPerm},
			sec("外观与呼出"),
			{kind: rowHotkey}, {kind: rowTheme}, {kind: rowScale},
			{kind: rowFontSize}, {kind: rowWinSize},
			sec("输出"), {kind: rowNotify}, {kind: rowTTS},
		}
	case pageModel:
		rows := []rowDesc{
			head,
			sec("当前模型"), {kind: rowModelName},
			sec("思考与推理"), {kind: rowThink}, {kind: rowEffort},
			sec("模型服务（provider 列表）"),
		}
		for i := range f.provs {
			for fld := 0; fld < provFldN; fld++ {
				rows = append(rows, rowDesc{kind: rowProvField, prov: i, field: fld})
			}
			rows = append(rows, rowDesc{kind: rowProvBtns, prov: i})
		}
		return append(rows, rowDesc{kind: rowProvAdd}, rowDesc{kind: rowProvApply})
	case pageChat:
		return []rowDesc{
			head,
			sec("限额"),
			{kind: rowMaxTurns}, {kind: rowMaxCtx}, {kind: rowCompact},
			{kind: rowToolChars}, {kind: rowToolTo},
		}
	case pagePrompt: // S7/D144 + S7b/D145：人格模块树独立成页（原先挂在「对话」页）
		rows := []rowDesc{
			head,
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
	case pageTools: // S6/D143：内置工具启停 + 插件管理（可交互；快照每帧同步）
		rows := []rowDesc{head}
		if f.tools == nil {
			return append(rows, rowDesc{kind: rowToolsEmpty, text: "未接线（只读占位）"})
		}
		rows = append(rows, sec("内置工具（开关即时生效，无需重启）"))
		for i := range f.toolsSnap.Builtins {
			rows = append(rows, rowDesc{kind: rowBuiltin, field: i})
		}
		rows = append(rows, sec("MCP 与插件（来源 config / 插件目录）"))
		for i := range f.toolsSnap.Plugins {
			rows = append(rows, rowDesc{kind: rowPlugin, field: i})
		}
		if len(f.toolsSnap.Plugins) == 0 {
			rows = append(rows, rowDesc{kind: rowToolsEmpty, text: "没有已声明的插件（config mcpServers 或 插件目录）"})
		}
		if f.toolsStatus != "" {
			rows = append(rows, rowDesc{kind: rowToolsStatus})
		}
		return rows
	default: // pageAdv
		return []rowDesc{head, sec("config.json 原文"), {kind: rowRaw}}
	}
}

// navColumnDp 左侧导航栏宽度（dp，经 gtx.Dp 消费）；navItemDpH 单个导航项高度。
const (
	navColumnDp unit.Dp = 140
	navItemDpH  unit.Dp = 36
)

// settingsFrame 设置窗单帧（S4d/D142 分页）：左侧导航栏 + 当前页滚动表单 + 全局底栏。
// 事件先于渲染取尽（Clicked 自带 Update；导航点击本帧即按新页渲染）。常规窗不做形状
// 位图/羽化（§15.7 形态）：windowBg 铺底，与主窗内容面同底。
func settingsFrame(gtx layout.Context, th *material.Theme, u *UI, f *settingsForm) layout.Dimensions {
	for p := 0; p < settingsPageCount; p++ {
		for f.navBtns[p].Clicked(gtx) {
			f.page = p
		}
	}
	// S6/D143：工具页动作分发（按钮只在工具页渲染；快照取上帧同步值）。
	for key, btn := range f.toolBtns {
		if btn.Clicked(gtx) {
			u.dispatchToolAction(f, key)
		}
	}
	f.drainToolResults() // 异步动作结果回灌状态行
	// 工具页原子快照（Revision 变了才重建行/按钮；同历史窗口径）。
	if f.tools != nil {
		cur := f.tools.Snapshot()
		if cur.Revision != f.toolsRev {
			f.toolsRev = cur.Revision
			f.toolsSnap = cur
		}
	}
	for f.save.Clicked(gtx) {
		u.saveSettings(f)
	}
	for f.profCreate.Clicked(gtx) {
		u.profileAction(f, "create")
	}
	for f.profCopy.Clicked(gtx) {
		u.profileAction(f, "copy")
	}
	for f.profSwitch.Clicked(gtx) {
		u.profileAction(f, "switch")
	}
	for f.provAdd.Clicked(gtx) {
		f.addProviderForm()
	}
	for f.provApply.Clicked(gtx) {
		u.applyProviderChanges(f)
	}
	for f.welcomeOpen.Clicked(gtx) {
		u.OpenWelcome() // D141：入口在设置窗（两个菜单都不带此项）
	}
	// S7/D144：人格模块树与引用编辑（新增/删除/上移/下移、内置模块「引用」）。
	// D148：模块库改成三列网格——新增走网格末尾那格，模块本身的增删改走右键弹窗。
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
	if f.page == pagePrompt && !f.modEdit.open {
		f.moduleGridEvents(gtx)
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
	if f.page == pagePrompt && u.opts.PreviewPrompts != nil {
		f.promptPrev = u.opts.PreviewPrompts(f.promptViews(), f.promptRefList(), f.sysPrompt.Text())
	}
	for i, pf := range f.provs {
		for pf.test.Clicked(gtx) {
			u.runProviderTest(f, pf)
		}
		for pf.setPrimary.Clicked(gtx) {
			f.primaryIdx = i
		}
		for pf.del.Clicked(gtx) {
			if len(f.provs) > 1 { // 至少保留一条（providers 不可为空，启动校验同口径）
				f.provs = append(f.provs[:i], f.provs[i+1:]...)
				switch {
				case f.primaryIdx == i:
					f.primaryIdx = 0
				case f.primaryIdx > i:
					f.primaryIdx--
				}
			}
		}
	}
	f.drainTestResults() // 连通性测试异步结果（最新一条覆盖展示）
	f.perm.advance(gtx)
	f.effort.advance(gtx)
	f.theme.advance(gtx)
	f.scale.advance(gtx)
	if !f.focused {
		f.focused = true
		if ed := f.firstEditor(); ed != nil { // 开窗焦点入当前页首个编辑档；点击切换（editor.go 自聚焦）
			gtx.Execute(key.FocusCmd{Tag: ed})
		}
	}
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			pp := paperOf()
			paint.FillShape(gtx.Ops, pp.win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Dimensions{}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							w := gtx.Dp(navColumnDp)
							gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
							return f.navColumn(gtx, th)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							rows := f.pageRows(f.page)
							return material.List(th, &f.pages[f.page]).Layout(gtx, len(rows), f.row(th, rows))
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return f.bottomBar(gtx, th)
				}),
			)
		}),
		// 编辑弹窗画在最后一层（模态）：它必须盖住内容区与底栏，否则遮罩盖不到的
		// 「保存并应用」在弹窗背后仍是亮的，读起来像是可以直接点。D148。
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			dims, _ := f.moduleEditorFrame(gtx, th)
			return dims
		}),
	)
}

// navColumn 左侧导航栏（S4d/D142）：页名竖排，当前页高亮；点击切页在帧首消费。
func (f *settingsForm) navColumn(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, settingsPageCount)
		for p := 0; p < settingsPageCount; p++ {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.navItem(gtx, th, p)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// navItem 单个导航项：整宽高亮底衬（当前页）+ 页名；点击件即整项。
//
// Paper 版的选中态 = 软底 + 左侧 2dp 品牌色短条。短条不与文字重叠，故可以用品牌色
// #00AEEF 本身（承载白字的那档才需要深一档）；选中项文字同时升到 text 最亮一档。
func (f *settingsForm) navItem(gtx layout.Context, th *material.Theme, p int) layout.Dimensions {
	return f.navBtns[p].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(navItemDpH))
		return layout.Stack{Alignment: layout.W}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				if f.page == p {
					pp := paperOf()
					r := image.Rectangle{Max: size}
					paint.FillShape(gtx.Ops, pp.sel,
						clip.UniformRRect(r, gtx.Dp(unit.Dp(pp.rChip))).Op(gtx.Ops))
					// 左条：贴着圆角内侧，上下各让 6dp——贴满整高会与圆角打架。
					bar := image.Rect(r.Min.X+gtx.Dp(2), r.Min.Y+gtx.Dp(6),
						r.Min.X+gtx.Dp(4), r.Max.Y-gtx.Dp(6))
					paint.FillShape(gtx.Ops, pp.brand,
						clip.UniformRRect(bar, gtx.Dp(1)).Op(gtx.Ops))
				}
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pp := paperOf()
					l := material.Body1(th, settingsPageNames[p])
					if f.page == p {
						l.Color = pp.text
					} else {
						l.Color = pp.text2
					}
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// bottomBar 全局底栏（S4d/D142）：「保存并应用」+ 反馈文案任何页常驻——保存仍是一次
// 全量 patch（跨页 diff 基准 = 开窗快照 f.snap），语义与改前一致。
func (f *settingsForm) bottomBar(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 底衬（Expanded 拿到 Stacked 子项的最大尺寸）+ 1dp hairline 分隔内容区。
			// 底栏与窗体同色、只靠这条线分层：Paper 里不出现第二块大底色。
			pp := paperOf()
			size := gtx.Constraints.Min
			paint.FillShape(gtx.Ops, pp.win, clip.Rect(image.Rectangle{Max: size}).Op())
			paint.FillShape(gtx.Ops, pp.hairline,
				clip.Rect(image.Rectangle{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}).Op())
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.actionsRow(gtx, th)
			})
		}),
	)
}

// row 第 i 行渲染（material.List 回调；行内容按 pageRows 的类型分派）。
//
// 版面口径（D148 Paper 版）：
//   - **表单行**（字段/开关/按钮组）落进「分组卡」：连续的字段行合成一张卡，卡内
//     行间一条 hairline，卡外沿 1dp 描边、组首/组尾给圆角。
//   - **非表单行**（页标题/分区标题/说明/网格/预览/整组按钮行）自带上下留白，不落卡。
//
// 为什么卡底按行画而不是整块画：设置页是 material.List（虚拟化），滚出视口的行为
// 不参与布局，拿不到「这一组的完整矩形」。按行画、组界只补外沿，视觉上仍是连续一张
// 卡——这也是 iOS 分组表的画法（被视口切断处是直边，本来就不该有圆角）。
func (f *settingsForm) row(th *material.Theme, rows []rowDesc) func(layout.Context, int) layout.Dimensions {
	return func(gtx layout.Context, i int) layout.Dimensions {
		r := rows[i]
		if !rowInCard(r.kind) {
			return layout.UniformInset(unit.Dp(rowInsetDp)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return f.rowBody(gtx, th, rows, i)
			})
		}
		first := i == 0 || !rowInCard(rows[i-1].kind)
		last := i == len(rows)-1 || !rowInCard(rows[i+1].kind)
		// 组首/组尾与相邻标题之间留整份 rowInsetDp；**组内上下不留**——卡内行距由
		// Stacked 的内边距给。留了的话相邻行的卡之间会插进一段白，竖边逐行断开，
		// 整张卡看上去被切成几截（实测：放大后左右各缺一块，像没画完）。
		padT, padB := cardPad(first, last)
		return layout.Inset{
			Left: unit.Dp(rowInsetDp), Right: unit.Dp(rowInsetDp),
			Top: unit.Dp(padT), Bottom: unit.Dp(padB),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pp := paperOf()
			rad := gtx.Dp(unit.Dp(pp.rCard))
			bd := gtx.Dp(1)
			return layout.Stack{}.Layout(gtx,
				// 卡底 + 外沿：Expanded 拿到 Stacked 子项的最大尺寸，即「本行高度」。
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					// 卡宽取**可用宽**而不是内容宽：layout.Stack 会把 Stacked 子项的
					// Min 归零，只报自身尺寸的内容（复选框、单个按钮）会让卡缩成一条
					// ——同一页出现两种卡宽，读起来像排版塌了。
					w, h := gtx.Constraints.Min.X, gtx.Constraints.Min.Y
					if m := gtx.Constraints.Max.X; m > w {
						w = m
					}
					// 上下圆角只在组界生效：中间行直角，与相邻行拼成一张连续的卡。
					topR, botR := 0, 0
					if first {
						topR = rad
					}
					if last {
						botR = rad
					}
					// **整圈画环**：先铺描边色的圆角矩形，再内缩 bd 铺面色。逐条边画直线
					// 会在四个角留缺口（实测：框线断成四截，读起来像渲染坏了）。
					outer := clip.RRect{Rect: image.Rect(0, 0, w, h), NE: topR, NW: topR, SE: botR, SW: botR}
					paint.FillShape(gtx.Ops, pp.hairline, outer.Op(gtx.Ops))
					ir := func(v int) int {
						if v <= bd {
							return 0
						}
						return v - bd
					}
					inner := clip.RRect{
						Rect: image.Rect(bd, bd, w-bd, h-bd),
						NE:   ir(topR), NW: ir(topR), SE: ir(botR), SW: ir(botR),
					}
					paint.FillShape(gtx.Ops, pp.card, inner.Op(gtx.Ops))
					// 组内：把「不该出现的那条横边」用面色抹掉，再补一条让开内边距的分隔线。
					// **抹边必须让开左右 bd**：抹整宽会把两条竖边一起凿掉，卡在每行交界
					// 处断成一段（实测：放大后左右各缺一块，整张卡看着像没画完）。
					// 抹边只碰本行自己的边界像素（相邻行各有各的边界行），不会伤到邻行。
					erase := func(rr image.Rectangle) {
						paint.FillShape(gtx.Ops, pp.card, clip.Rect(rr).Op())
					}
					if !first {
						erase(image.Rect(bd, 0, w-bd, bd))
						paint.FillShape(gtx.Ops, pp.hairline,
							clip.Rect(image.Rect(gtx.Dp(14), 0, w-gtx.Dp(14), bd)).Op())
					}
					if !last {
						erase(image.Rect(bd, h-bd, w-bd, h))
					}
					return layout.Dimensions{Size: image.Pt(w, h)}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					// 卡内边距：上下 14 与左右一致，字段才不会贴着卡的描边。
					return layout.Inset{
						Top: unit.Dp(14), Bottom: unit.Dp(14),
						Left: unit.Dp(14), Right: unit.Dp(14),
					}.Layout(gtx, f.rowBodyFn(th, rows, i))
				}),
			)
		})
	}
}

// cardPad 卡在组首/组尾的上下外边距（dp）：只在组界留，组内为 0。
//
// 组内留边距 = 相邻卡的竖边之间插进一段白，整张卡被切成几截；卡内行距由内容的
// 内边距承担，不靠行间空隙。
func cardPad(first, last bool) (top, bottom int) {
	if first {
		top = rowInsetDp
	}
	if last {
		bottom = rowInsetDp
	}
	return top, bottom
}

// rowInCard 该行是否落进分组卡（表单行）。
//
// 口径：**可编辑/可操作的表单行**打成一张卡（iOS 分组表读法）——分隔线在卡内、
// 圆角只在组首组尾；标题、分区标题、说明段、整块内容（代码块/网格/预览）不落卡。
// 「人格引用」整组落卡：它们是同一份有序列表的成员，逐行浮着会被误读成互不相关的项。
func rowInCard(k rowKind) bool {
	switch k {
	case rowTitle, rowSection, rowPromptNote, rowPromptGrid, rowToolsEmpty, rowRaw,
		rowPromptPreview, rowProfBtns, rowProvBtns, rowProvAdd, rowProvApply,
		rowPromptRefAdd:
		return false
	}
	return true
}

// rowBodyFn 绑定「第 i 行内容」的 widget（Stacked 子项需要 widget 而不是立即调用）。
func (f *settingsForm) rowBodyFn(th *material.Theme, rows []rowDesc, i int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return f.rowBody(gtx, th, rows, i)
	}
}

// rowBody 行内容本体（版面归 row，内容只按类型分派）。
func (f *settingsForm) rowBody(gtx layout.Context, th *material.Theme, rows []rowDesc, i int) layout.Dimensions {
	r := rows[i]
	switch r.kind {
	case rowTitle:
		return paperTitle(th, r.text)(gtx)
	case rowSection:
		return paperSection(th, r.text)(gtx)
	case rowProfCurrent:
		return fieldRow(gtx, th, "当前", func(gtx layout.Context) layout.Dimensions {
			txt := f.profiles.Current
			if len(f.profiles.Items) > 0 {
				txt += "（可用：" + strings.Join(f.profiles.Items, ", ") + "）"
			}
			l := material.Body1(th, txt)
			l.Color = paperOf().text3
			return l.Layout(gtx)
		})
	case rowProfName:
		return fieldRow(gtx, th, "Profile 名", editorBox(th, &f.profName, "新建 / 复制 / 切换的目标名"))
	case rowProfBtns:
		return f.profileButtonsRow(gtx, th)
	case rowWelcome: // D141：欢迎 / 首次运行引导入口（开 winWelcome 次窗）
		return f.welcomeRow(gtx, th)
	case rowModelName: // S4d：当前模型名（D110② 起在 UI 上失位，本次补回）
		return fieldRow(gtx, th, "当前模型名", editorBox(th, &f.model, "model.name；改动保存后经 /model 热切换"))
	case rowProvField:
		pf := f.provs[r.prov]
		eds := []*widget.Editor{&pf.name, &pf.baseURL, &pf.apiKey, &pf.models, &pf.tokenizer, &pf.unsupported}
		meta := provFieldMeta[r.field]
		return fieldRow(gtx, th, meta.label, editorBox(th, eds[r.field], meta.hint))
	case rowProvBtns:
		return f.providerButtonsRow(gtx, th, r.prov)
	case rowProvAdd:
		return f.providerAddRow(gtx, th)
	case rowProvApply:
		return f.providerApplyRow(gtx, th)
	case rowPerm:
		return fieldRow(gtx, th, "权限等级", cycleBtn(th, &f.perm))
	case rowThink:
		return paperCheckBox(th, &f.think, "原生思考（/think 总开关，关时忽略推理档位）")(gtx)
	case rowEffort:
		return fieldRow(gtx, th, "推理档位", cycleBtn(th, &f.effort))
	case rowHotkey:
		return fieldRow(gtx, th, "全局快捷键", editorBox(th, &f.hotkey, "如 Alt+A（空 = 默认）"))
	case rowTheme:
		return fieldRow(gtx, th, "主题", cycleBtn(th, &f.theme))
	case rowScale:
		return fieldRow(gtx, th, "元素缩放", cycleBtn(th, &f.scale))
	case rowFontSize:
		return fieldRow(gtx, th, "字号", editorBox(th, &f.fontSize, "正文字号 10–28（最终 = 字号 × 缩放）"))
	case rowWinSize:
		return fieldRow(gtx, th, "窗口尺寸", sizeRow(gtx, th, &f.winW, &f.winH))
	case rowNotify:
		return paperCheckBox(th, &f.notify, "桌面通知（回答完成时）")(gtx)
	case rowTTS:
		return paperCheckBox(th, &f.tts, "语音播报（已解析配置，尚未启用）")(gtx)
	case rowMaxTurns:
		return fieldRow(gtx, th, "最大轮数", editorBox(th, &f.maxTurns, "单次提问的工具调用轮数上限（空 = 保持）"))
	case rowMaxCtx:
		return fieldRow(gtx, th, "上下文上限", editorBox(th, &f.maxCtx, "tokens；自动压缩与截断的预算分母（空 = 保持）"))
	case rowCompact:
		return fieldRow(gtx, th, "压缩阈值", editorBox(th, &f.compact, "0–1；占用超阈值触发自动压缩（空 = 保持）"))
	case rowToolChars:
		return fieldRow(gtx, th, "工具输出上限", editorBox(th, &f.toolChars, "字符；超出截断（空 = 保持）"))
	case rowToolTo:
		return fieldRow(gtx, th, "工具超时", editorBox(th, &f.toolTo, "秒；缺省单次调用超时（空 = 保持）"))
	case rowSysPrompt:
		return editorBoxH(th, &f.sysPrompt, "默认人格（= 内置 @default 正文；清空 = 内置默认）", 80)(gtx)
	// S7/D144：人格模块树（「系统提示词」页，S7b/D145 独立成页）。
	case rowPromptNote:
		l := material.Body2(th, "人格 = 有序引用的展开结果；模块可分组（children 逗号分隔，可多级）。"+
			"改动只对新会话生效，且重启后应用。")
		l.Color = paperOf().text3
		return l.Layout(gtx)
	case rowPromptGrid:
		return f.moduleGridRow(gtx, th)
	case rowPromptRef:
		return f.promptRefRow(gtx, th, r.field)
	case rowPromptRefAdd:
		return paperButton(th, &f.promptRefAdd, "＋ 添加引用", paperBtnSecondary, true)(gtx)
	case rowPromptBuiltin:
		return f.promptBuiltinRow(gtx, th, f.builtins[r.field])
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
		return l.Layout(gtx)
	// S6/D143：工具与 MCP 页。
	case rowBuiltin:
		return f.builtinToolRow(gtx, th, f.toolsSnap.Builtins[r.field])
	case rowPlugin:
		return f.pluginToolRow(gtx, th, f.toolsSnap.Plugins[r.field])
	case rowToolsEmpty:
		l := material.Body2(th, r.text)
		l.Color = paperOf().text3
		return l.Layout(gtx)
	case rowToolsStatus:
		l := material.Body2(th, f.toolsStatus)
		if f.toolsErr {
			l.Color = paperOf().danger
		} else {
			l.Color = paperOf().text3
		}
		return l.Layout(gtx)
	default: // rowRaw
		return editorBoxH(th, &f.rawCfg, "整文件编辑；保存前 JSON 校验（解析失败原文件不动，Q10）", 220)(gtx)
	}
}

// providerButtonsRow 单个 provider 的动作按钮：连通测试 / 设为主 / 删除（D110②）。
func (f *settingsForm) providerButtonsRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return paperButton(th, c, label, paperBtnSecondary, true)
	}
	primary := "设为主"
	if f.primaryIdx == i {
		primary = "★ 主"
	}
	// 主 provider 的「★ 主」用强调色文字标识当前主，不铺整块实心底——一行里两块
	// 实心按钮会互相抢视线；不可删的那条（providers 不可为空）呈禁用态。
	mark := paperBtnSecondary
	if f.primaryIdx == i {
		mark = paperBtnPrimary
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&pf.test, "测试连通")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, &pf.setPrimary, primary, mark, true))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, paperButton(th, &pf.del, "删除", paperBtnDanger, len(f.provs) > 1))
		}),
	)
}

// providerAddRow 新增 provider 入口（编辑区尾部；Apply 校验名字等必填项）。
func (f *settingsForm) providerAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return paperButton(th, &f.provAdd, "＋ 新增 provider", paperBtnSecondary, true)(gtx)
}

// providerApplyRow provider 区应用按钮 + 反馈文案（成功绿/错误红 = 转写区语义色）。
func (f *settingsForm) providerApplyRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.provApply, "应用 provider 变更", paperBtnPrimary, true)(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.provStatus
				col := paperOf().text2
				switch {
				case text == "":
					text, col = "变更全量写回 config，重启生效；「测试连通」发真实最小请求。", paperOf().text3
				case f.provErr:
					col = paperOf().danger
				}
				l := material.Label(th, paperNoteSp(th), text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}

// profileButtonsRow profile 区三动作按钮（D110③）：新建 / 复制当前 / 切换（重启生效）。
func (f *settingsForm) profileButtonsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return paperButton(th, c, label, paperBtnSecondary, true)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&f.profCreate, "新建")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profCopy, "复制当前"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, btn(&f.profSwitch, "切换（重启生效）"))
		}),
	)
}

// welcomeRow 欢迎 / 首次运行引导入口（D141）：设置窗是**唯一入口**——托盘与球右键菜单都
// 不带此项；另有「首跑 / `ui.welcomed` 缺失」时自动打开一次（装配根接线）。按钮开次窗，
// 单实例防重开由注册表保证（§15.7）。
func (f *settingsForm) welcomeRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return fieldRow(gtx, th, "欢迎引导", func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return paperButton(th, &f.welcomeOpen, "打开", paperBtnSecondary, true)(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(th, "模型接入向导 + 快捷键 / 右键菜单 / 常用命令")
					l.Color = paperOf().text3
					return l.Layout(gtx)
				})
			}),
		)
	})
}

// promptRefRow 人格引用行（S7/D144）：序号 + 引用名 + 上移/下移/删除（顺序 = 拼接顺序）。
func (f *settingsForm) promptRefRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	rf := f.promptRefs[i]
	small := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, paperButton(th, c, label, paperBtnSecondary, true))
		}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, fmt.Sprintf("%d.", i+1))
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
func (f *settingsForm) promptBuiltinRow(gtx layout.Context, th *material.Theme, b BuiltinModuleView) layout.Dimensions {
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
			btn := f.promptBtns["ref:"+b.Name]
			if btn == nil {
				btn = new(widget.Clickable)
				f.promptBtns["ref:"+b.Name] = btn
			}
			return paperButton(th, btn, "引用", paperBtnSecondary, true)(gtx)
		}),
	)
}

// fieldRow 一行：定宽标签列 + 控件（基线对齐，各行控件起点一致）。标签列宽随
// 度量缩放（Dp）——裸 px 常量在高 DPI 下实际变窄，「接口地址」等四字标签折行。
func fieldRow(gtx layout.Context, th *material.Theme, label string, control layout.Widget) layout.Dimensions {
	// **不能按基线对齐**：基线只对单行文字成立，多行控件（正文区）的基线与它的
	// 顶边差很远，标签会掉到输入框下方去（离屏渲染实测：标签浮在框底之下）。
	// 改为顶端对齐 + 给标签加与输入框首行等高的内衬，视觉上仍与首行文字齐平。
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			labelW := gtx.Dp(fieldLabelW)
			gtx.Constraints.Min.X = labelW
			gtx.Constraints.Max.X = labelW
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(th, label)
					l.Color = paperOf().text2
					return l.Layout(gtx)
				})
			})
		}),
		layout.Flexed(1, control),
	)
}

// cycleBtn 循环档按钮（当前展示值；点击进一档——material v0.10 无下拉框）。
func cycleBtn(th *material.Theme, c *cycleField) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		label := ""
		if len(c.displays) > 0 {
			label = c.displays[c.idx%len(c.displays)]
		}
		return paperButton(th, &c.click, label, paperBtnSecondary, true)(gtx)
	}
}

// fillFieldBg 输入框底衬：Paper 的「下沉面 + 1dp hairline」。
//
// Gio 没有描边，靠「先铺满描边色、再内缩 1dp 铺底色」画出真正的空心框——直接给透明
// 底描边会把整块涂成描边色（这条坑在次窗与主窗同样成立）。
//
// 为什么不用窗底色当输入框底：设置页的窗体底就是白，输入框同白等于没有边界，用户
// 看不出哪里能打字。故下沉一档（sunken），边界用 hairline。
func fillFieldBg(gtx layout.Context, th *material.Theme, r image.Rectangle) {
	if r.Empty() {
		return
	}
	paperSurface(gtx, paperOf(), r, gtx.Dp(unit.Dp(paperOf().rCtl)))
}

// editorBox 文本档输入区（layout.Widget，供 fieldRow 装配）：满宽底衬，高度自适应
// ——max(Dp(fieldH) 下限, 编辑器实测行高 + 2×留白)。固定裸 px 定高会在任何 DPI 裁掉
// 文字底部（CJK 字面行高 > 内容区，D113 验收补充；实测行高 38px @1.0 / 48px @1.25 vs
// 旧内容区 24px/20px），自适应后框随文字度量走。
func editorBox(th *material.Theme, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{Alignment: layout.NW}.Layout(gtx,
			// 底衬（Expanded 拿 Stacked 最大尺寸；垫底项保证下限，编辑器项决定实际高）。
			// 浅底 + 内缩一圈的窗底 = 1dp 描边环：设置页底与窗底同色，只铺 th.Bg
			// 等于没铺，用户看不出哪里可以打字。
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				fillFieldBg(gtx, th, image.Rectangle{Max: gtx.Constraints.Min})
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Point{Y: gtx.Dp(fieldH)}}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X // 铺满行宽：点击聚焦区 = 整框
				return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Editor(th, ed, hint).Layout(gtx)
				})
			}),
		)
	}
}

// editorBoxH 定高多行编辑区（编辑框的加高变体）：提示词与高级 raw JSON 用。高度
// 随度量缩放（Dp）——多行内容超高走编辑器内部滚动，但首行行高必须容得下。
func editorBoxH(th *material.Theme, ed *widget.Editor, hint string, h int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := image.Point{X: gtx.Constraints.Max.X, Y: gtx.Dp(unit.Dp(h))}
		fillFieldBg(gtx, th, image.Rectangle{Max: size})
		gtx.Constraints = layout.Exact(size)
		layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.Editor(th, ed, hint).Layout(gtx)
		})
		return layout.Dimensions{Size: size}
	}
}

// sizeRow 窗口尺寸双数值档（D90）：宽 × 高（px），并排等分；空 = 保持缺省。
func sizeRow(gtx layout.Context, th *material.Theme, w, h *widget.Editor) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, editorBox(th, w, "宽 px")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, "×").Layout(gtx)
				})
			}),
			layout.Flexed(1, editorBox(th, h, "高 px")),
		)
	}
}

// actionsRow 保存键 + 反馈文案（成功绿/错误红 = 转写区语义色；空 = 默认提示）。
func (f *settingsForm) actionsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return paperButton(th, &f.save, "保存并应用", paperBtnPrimary, true)(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.status
				col := paperOf().text2
				switch {
				case text == "":
					text, col = "保存后立即生效；provider 列表在「模型」页应用（重启生效）。", paperOf().text3
				case f.err:
					col = paperOf().danger
				}
				l := material.Label(th, paperNoteSp(th), text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}
