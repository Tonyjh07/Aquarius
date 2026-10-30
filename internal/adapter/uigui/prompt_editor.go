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

// 本文件只留编辑区的状态与行为（表单类型、行计划、事件、引用与模块的增删改、视图导出）；
// 磁贴手势见 prompt_tiles.go，磁贴绘制见 prompt_tiles_view.go，编辑弹窗见 prompt_module_editor.go。

import (
	"strings"

	"gioui.org/layout"
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
