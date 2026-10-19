package uigui

import (
	"image"

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
	{"API 密钥", "明文存入本机加密库、配置只留引用；留空保持不变"},
	{"模型列表", "逗号分隔；首个 = 缺省请求模型"},
	{"Tokenizer", "本地 tokenizer.json 路径；空 = 估算"},
	{"不认参数", "逗号分隔；这些参数会被剔除，不发往服务端"},
}

// provModelCandMax 候选 chip 的上限（D153）：服务端可能列出几百个模型，全部铺开会把
// 设置页推成一面墙——取前 N 个，状态行同时给出「共 N 个」的事实。
const (
	provModelCandMax = 12 // chip 行最多显示
	relModelAutoFill = 8  // 自动填入模型名行的上限
)

// rowDesc 一行的类型与定位。
type rowDesc struct {
	kind  rowKind
	text  string // rowSection / rowTitle 的文案
	prov  int    // rowProvField / rowProvBtns：provider 下标
	field int    // rowProvField：字段槽位
}

// sec 分组小标题行。
func sec(text string) rowDesc { return rowDesc{kind: rowSection, text: text} }

// pageRows 本页行计划（每帧现算：providers / 密钥条目数在增删后即变）。首行恒为页标题
// （页名 = 注册表 Key），其余由该页模块给（D162）。
func (f *settingsForm) pageRows(page int) []rowDesc {
	head := rowDesc{kind: rowTitle, text: settingsPageNames[page]}
	return append([]rowDesc{head}, settingsPages[page].Rows(f)...)
}

// navColumnDp 左侧导航栏宽度（dp，经 gtx.Dp 消费）；navItemDpH 单个导航项高度。
const (
	navColumnDp unit.Dp = 140
	navItemDpH  unit.Dp = 36
)

// settingsFrame 设置窗单帧（S4d/D142 分页；D162 页面注册表）：左侧导航栏 + 当前页滚动
// 表单 + 全局底栏。事件先于渲染取尽（Clicked 自带 Update；导航点击本帧即按新页渲染）。
//
// 事件分两层：**帧级**（保存键、开窗焦点、异步结果排空、工具页快照同步——与页无关，恒执行）
// 与**页级**（settingsPages[i].Events，逐页各调一次：未渲染的控件 Clicked 恒假，故等价于
// 只处理当前页，却省掉了「按页分支」）。常规窗不做形状位图/羽化（§15.7 形态）：windowBg 铺底，
// 与主窗内容面同底。
func settingsFrame(gtx layout.Context, th *material.Theme, u *UI, f *settingsForm) layout.Dimensions {
	for p := 0; p < settingsPageCount; p++ {
		for f.navBtns[p].Clicked(gtx) {
			f.page = p
		}
	}
	f.drainTestResults() // 连通性 / 获取模型异步结果（最新一条覆盖展示）
	f.drainToolResults() // 工具页异步动作结果回灌状态行
	// 工具页原子快照（Revision 变了才重建行/按钮；同历史窗口径）。
	f.syncToolsSnapshot()
	for f.save.Clicked(gtx) {
		u.saveSettings(f)
	}
	// 当前页事件（D162：页模块自带事件，谁持有控件谁处理）。**所有页都调一次**：改前
	// provider / 工具页 / profile 等动作本就是帧级无条件消费（未渲染的控件 Clicked 恒假），
	// 这样口径与改前一致；页内需要按当前页收窄的（如模块网格手势）由该页自行判 f.page。
	for _, p := range settingsPages {
		p.Events(gtx, u, f)
	}
	if !f.focused {
		f.focused = true
		if ed := settingsPages[f.page].FirstEditor(f); ed != nil { // 开窗焦点入当前页首个编辑档
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
		return rowLayout(gtx, rows, i, f.rowBodyFn(th, rows, i))
	}
}

// rowLayout 单行版面（卡片分组 + 留白），行内容由 body 给（D154 起抽成自由函数：
// 设置页与遮罩「默认模板」页签的行表不同，版面必须同一份，否则两处卡片粗细不一样）。
func rowLayout(gtx layout.Context, rows []rowDesc, i int, body layout.Widget) layout.Dimensions {
	r := rows[i]
	if !rowInCard(r.kind) {
		return layout.UniformInset(unit.Dp(rowInsetDp)).Layout(gtx, body)
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
				}.Layout(gtx, body)
			}),
		)
	})
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

// rowBody 行内容本体（版面归 row，内容按注册表分派，D162）：先问框架通用行型
// （页标题 / 分区标题），未命中再交给**当前页**的模块认领。未认领的行不再有 `default`
// 静默兜底——交由行型覆盖测试断言恒被认领。
func (f *settingsForm) rowBody(gtx layout.Context, th *material.Theme, rows []rowDesc, i int) layout.Dimensions {
	r := rows[i]
	if dims, ok := frameworkRow(gtx, th, r); ok {
		return dims
	}
	dims, ok := settingsPages[f.page].Body(gtx, th, f, rows, i)
	if !ok {
		// 认不出的行型曾静默落进 default 兜底（D162 取消）；这里退化成空 Dimensions，
		// 让行型覆盖测试与离屏像素回归去发现，而不是悄悄画成别的控件。
		return layout.Dimensions{}
	}
	return dims
}

// trimRunes 按字符数截断（超长模型名在 chip 里以省略号收尾）。
func trimRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
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
