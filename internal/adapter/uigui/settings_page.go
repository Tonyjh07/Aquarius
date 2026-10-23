package uigui

// settings_page.go 设置窗页面注册表（D162）。
//
// 背景：设置窗页数会持续增长（「模型」之后又加了「系统提示词」「工具与 MCP」，D161 还要加
// 「密钥库」）。改前加一页要同时改 settings.go（页常量 / 页名阵列 / 控件）与
// settings_rows.go（行型 / 行计划 / 行渲染 / 帧首事件）四五处，且遗漏多半是**静默**的
// （行型忘了分派就落进 `default` 兜底、被画成别的控件）。这里把「一页」收成一个**页面
// 模块**：页序 = 注册序，导航栏 / 页标题 / 滚动列表 / 页名契约测试全部由注册表推导——
// 加一页 = 一个新文件 + 在 settingsPages 里加一行。
//
// 分派口径：行型（rowKind）仍是**单一共用词表**（设置页与遮罩「默认模板」页签共用同一份
// 行表，D154），但**渲染按页拆开**——框架只认页标题 / 分区标题（frameworkRow），其余由该页
// 自己的 Body 认领。Body 返回 claimed 表示「这行我认识」：未认领的行不会再被静默兜底，
// 由行型覆盖测试（每个 Rows() 产出的行型都必须被对应页 Body 认领）响亮地兜住。

import (
	"gioui.org/layout"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// settingsPage 一页的完整契约。页面模块是无状态值（状态一律挂在 *settingsForm 上，与
// promptEditor / settings_tools.go 既有写法同款）；页序即 settingsPages 的次序。
type settingsPage interface {
	// Key 稳定标识（导航项与页标题共用文案；也是页序契约测试的锚点）。
	Key() string
	// Rows 本页行计划（每帧现算：provider / 密钥条目数会变）。
	Rows(f *settingsForm) []rowDesc
	// Body 渲染第 i 行；claimed=false = 本行不认识（测试断言恒 true，防静默兜底）。
	Body(gtx layout.Context, th *material.Theme, f *settingsForm, rows []rowDesc, i int) (layout.Dimensions, bool)
	// Events 帧首消费本页控件的事件（谁持有控件谁处理）。settingsFrame 对**所有**页各调
	// 一次：未渲染的控件 Clicked 恒假，故等价于「只处理当前页」，却省掉了「按页分支」；
	// 确需按当前页收窄的（如磁贴手势会吃整帧指针）在本方法内自行判 f.page。
	Events(gtx layout.Context, u *UI, f *settingsForm)
	// FirstEditor 开窗焦点落点（nil = 本页无编辑档，不抢焦点）。
	FirstEditor(f *settingsForm) *widget.Editor
}

// enterHook 可选钩子：切到本页时被调用（每帧由 settingsFrame 在渲染前触发）。页面用
// 表单上的标记自行去重（D181：「模型服务」页借此在首次进入时自动拉一轮服务端清单）。
type enterHook interface{ OnEnter(u *UI, f *settingsForm) }

// settingsPages 页注册表：**页序 = 注册序**。加一页在这里追加一行即可（各页面模块见
// settings_page_*.go）。页索引常量（pageGeneral…）仍保留供代码与测试引用可读，其值与
// 本表次序由 TestSettingsPageNamesContract 锁定。
var settingsPages = []settingsPage{
	generalPage{},   // 常规：Profile 区 / 欢迎引导 / 权限等级 / 外观与呼出 / 输出
	modelPage{},     // 模型：当前模型名 / 原生思考与推理档位 / 有序降级链（D176 口径一）
	providersPage{}, // 模型服务：provider 管理（协议/端点/密钥池/模型能力表；D176 口径二）
	secretsPage{},   // 密钥库：已存名字 + 尾 4 位掩码 / 新增覆盖 / 行内二次确认删除（S9-6b/D161）
	chatPage{},      // 对话：限额
	promptPage{},    // 系统提示词：人格模块树 / 有序引用 / 内置只读模块 / 组合预览
	toolsPage{},     // 工具与 MCP：内置工具启停 + 插件管理
	skillsPage{},    // 技能：已装清单 + 导入 + 更新/卸载 + 额外技能目录（D202/D203）
	webPage{},       // 联网：搜索与抓取提供商、本地抓取参数（D200）
	audioPage{},     // 语音：识别 / 合成服务、音色与风格、试听（D179）
	advPage{},       // 高级：config.json 原文
	aboutPage{},     // 关于：图标 / 版本 / 运行环境 / 项目主页 / 版权与许可（D208）
}

// settingsPageCount 页数 = 注册表长度（不是独立常量：加页只改 settingsPages）。
var settingsPageCount = len(settingsPages)

// settingsPageNames 页名由注册表推导（导航项与页标题共用）。
var settingsPageNames = pageKeys()

// pageKeys 逐页取导航文案（settingsPageNames 的唯一来源）。
func pageKeys() []string {
	names := make([]string, settingsPageCount)
	for i, p := range settingsPages {
		names[i] = p.Key()
	}
	return names
}

// frameworkRow 框架通用行型（页标题 / 分区标题）：所有页面共用同一份渲染；页面模块的
// Body 先问它、未命中再认领自己的行型。
func frameworkRow(gtx layout.Context, th *material.Theme, r rowDesc) (layout.Dimensions, bool) {
	switch r.kind {
	case rowTitle:
		return paperTitle(th, r.text)(gtx), true
	case rowSection:
		return paperSection(th, r.text)(gtx), true
	}
	return layout.Dimensions{}, false
}
