package uigui

// 设置窗（§15.7/D60 核心档 + §15.4/D61 主题档）：表单状态、保存流与帧渲染。
// 并发口径（§15.7）：表单状态仅次窗事件循环 goroutine 读写、不跨窗共享；持久化经
// ApplySettings 单一写回调（装配根实现），内核命令经 inCh 排队到主循环与键入同
// 路径串行执行（壳内不旁路），主题/快捷键热生效经消息与原子槽（同跨窗模型）。

import (
	"fmt"
	"strconv"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/widget"
)

// ProfilesSnapshot profile 区快照（D110③）：当前生效 profile + 可用清单。
type ProfilesSnapshot struct {
	Current string   // 本次启动生效的 profile 名（--profile 或指针决定）
	Items   []string // profiles/ 下可用清单（含 config.json 的目录）
}

// ProfilesManager profile 管理面（D110③/修订④，装配根实现）：设置窗 profile 区
// 数据面与三动作。切换/新建/复制均重启生效（Q1：profile 进程级，运行态不热切）；
// 复制语义 = 仅复制配置（新 profile 以当前 profile 的 config.json 为底，不带会话
// /记忆/附件数据）。
type ProfilesManager interface {
	Snapshot() ProfilesSnapshot
	Create(name string) error // 模板 config 新建
	Copy(name string) error   // 以当前 profile 配置为底新建（仅配置）
	Switch(name string) error // 原子写根指针（重启生效）
}

// ProviderSnapshot provider 编辑区单条快照（D110②/③）：models/unsupported 以逗号
// 分隔呈现在单行编辑档（表单口径）；APIKey 只写不回显（D35），不进快照。
type ProviderSnapshot struct {
	Name        string
	BaseURL     string
	Models      string // 逗号分隔（首个 = 该 provider 缺省请求模型）
	Tokenizer   string
	Unsupported string // 逗号分隔
	Primary     bool   // 是否当前 primary
}

// ProviderPatch provider 编辑区单条提交（Apply 全量替换口径）。Orig = 编辑前的
// 原名（cmd 侧按它保留原条目的 api_key 与无关键；空 = 新增条目）；APIKey 空 = 保持原值。
type ProviderPatch struct {
	Orig        string
	Name        string
	BaseURL     string
	APIKey      string // "" = 不改（D35 只写不回显）
	Models      string // 逗号分隔
	Tokenizer   string
	Unsupported string // 逗号分隔
}

// ProviderManager provider 列表管理面（D110②/③，装配根实现）：设置窗 provider
// 编辑区数据面 + 全量应用 + 连通性测试。provider 变更重启生效（base_url/api_key/
// tokenizer 构造期固化，port.LLM 不可热换）；Test = 真实最小请求（/models，短超时）。
type ProviderManager interface {
	Snapshot() []ProviderSnapshot
	// Apply 全量替换 model.providers（primary 须命中列表；删除 = 缺席；cmd 侧
	// 校验与 fallback 悬挂引用清理，泛键写回保留各条目无关键）。
	Apply(primary string, providers []ProviderPatch) error
	// Test 连通性测试（真实最小请求；调用方 goroutine 负责，不进帧循环）。
	Test(p ProviderPatch) error
}

// SettingsSnapshot 设置窗核心档快照（开窗现取；同 Status 口径线程安全回调）。
// Permission/Model/Think/Effort 由装配根取运行态（等级活槽与 Agent 访问器），
// 其余取 config 文件；密钥永不进快照（D35 只写不回显）。provider 编辑数据面走
// ProviderManager（D110②），不在本快照。MCPServers/Plugins 只读展示（编辑走
// 高级页 raw JSON，Q10）；RawJSON = config.json 原文（高级页，解析失败不阻断开窗）。
type SettingsSnapshot struct {
	Permission   string // 规范值 read-only|strict|permissive|full-access（perm.Level）
	Model        string
	Think        bool
	Effort       string  // "" = 未设（D34 清除态）
	Hotkey       string  // "" = 默认 Alt+A
	Theme        string  // "" = system
	Scale        float64 // D90：元素缩放倍率；0 = 缺省 1.0（表单归一）
	FontSize     float64 // D90：正文字号 sp；0 = 缺省 15（表单归一）
	WindowWidth  int     // D90：主窗像素宽；0 = 缺省 608×460dp
	WindowHeight int

	// 全量分组页（S4/D110③）：输出 / 限制 / 提示词 / MCP 与插件 / 高级。
	Notify           bool
	TTS              bool
	MaxTurns         int
	MaxContextTokens int
	CompactThreshold float64
	ToolOutputChars  int
	ToolTimeoutSec   int
	SystemPrompt     string   // 人格（空 = 内置默认）
	MCPServers       []string // 只读：声明名清单（sorted）
	Plugins          []string // 只读：声明名清单（sorted）
	RawJSON          string   // config.json 原文（高级页编辑起点）
}

// SettingsPatch 保存提交（表单全量；模型服务走 ProviderManager.Apply，D110②）。
// 数值键 0 = 不改（表单留空 = 保持快照原值的既有口径）；Notify/TTS 恒写（布尔无
// 留空语义）；SystemPrompt 恒写（清空 = 合法语义「内置默认」）；RawJSON 非空 =
// 高级页整文件替换（保存前 cmd 侧 JSON 校验）。
type SettingsPatch struct {
	Permission   string
	Model        string
	Think        bool
	Effort       string
	Hotkey       string
	Theme        string
	Scale        float64 // D90：元素缩放倍率（cycle 档，恒非零）
	FontSize     float64 // D90：正文字号 sp；0 = 不改
	WindowWidth  int     // D90：主窗像素宽；0 = 不改（缺省）
	WindowHeight int

	Notify           bool
	TTS              bool
	MaxTurns         int
	MaxContextTokens int
	CompactThreshold float64
	ToolOutputChars  int
	ToolTimeoutSec   int
	SystemPrompt     string
	RawJSON          string // "" = 不改
}

// 枚举档（展示值 + 键值；内核 /permission /effort 的校验为单一事实源，此处仅渲染循环）。
var (
	// permLevels 权限档（§5/D22，与 domain/perm.Level 对齐）。
	permLevels = []string{"read-only", "strict", "permissive", "full-access"}
	// effortLevels 推理档位（D34）：首项 = 未设（键值 "" = 清除态）。
	effortLevels = []string{"未设", "minimal", "low", "medium", "high"}
	effortValues = []string{"", "minimal", "low", "medium", "high"}
	// themeLevels 主题档展示与键值（§15.4/D61：system|light|dark）。
	themeLevels = []string{"跟随系统", "浅色", "深色"}
	themeValues = []string{"system", "light", "dark"}
	// zoomLevels 元素缩放档（D90/§15.8）：展示百分比，键值 = 倍率字符串（cycleField
	// 键值为 string 的既有口径，表单侧 ParseFloat 换算；%g 往返一致）。
	zoomLevels = []string{"100%", "125%", "150%", "175%", "200%"}
	zoomValues = []string{"1", "1.25", "1.5", "1.75", "2"}
)

// cycleField 点击循环枚举档（material v0.10 无下拉框）：按钮显示当前项，点击进一档。
type cycleField struct {
	click    widget.Clickable
	displays []string
	values   []string
	idx      int
}

// setValue 按键值定位；未命中保持当前（快照异常值不致越界）。
func (c *cycleField) setValue(v string) {
	for i, x := range c.values {
		if x == v {
			c.idx = i
			return
		}
	}
}

// value 当前键值（空表返回 ""）。
func (c *cycleField) value() string {
	if len(c.values) == 0 {
		return ""
	}
	return c.values[c.idx]
}

// advance 消费点击进一档（帧渲染前调用——Clickable.Clicked 自带 Update）。
func (c *cycleField) advance(gtx layout.Context) {
	if len(c.values) == 0 {
		return
	}
	for c.click.Clicked(gtx) {
		c.idx = (c.idx + 1) % len(c.values)
	}
}

// settingsForm 设置窗表单状态（次窗事件循环 goroutine 独占；控件即状态，不跨窗
// 共享）。行模型为动态行计划（rowPlan）：行数随 provider 数量变化（D110② 编辑区）。
type settingsForm struct {
	list widget.List
	// 核心档控件（模型名/思考/档位/外观；provider 编辑走 provs）。
	model    widget.Editor
	hotkey   widget.Editor
	fontSize widget.Editor // D90：正文字号 sp（数值，空 = 不改）
	winW     widget.Editor // D90：主窗像素宽（数值，空 = 不改）
	winH     widget.Editor // D90：主窗像素高
	profName widget.Editor // D110③：profile 名（新建/复制/切换共用输入档）
	think    widget.Bool
	perm     cycleField
	effort   cycleField
	theme    cycleField
	scale    cycleField // D90：元素缩放（cycle 百分比档）
	save     widget.Clickable

	// 全量分组页控件（S4/D110③）：输出 / 限制 / 提示词 / 高级 raw JSON。
	notify    widget.Bool
	tts       widget.Bool
	maxTurns  widget.Editor
	maxCtx    widget.Editor
	compact   widget.Editor
	toolChars widget.Editor
	toolTo    widget.Editor
	sysPrompt widget.Editor // 多行：人格（清空 = 内置默认）
	rawCfg    widget.Editor // 多行：config.json 原文（Q10）

	// provider 编辑区（D110②）：每条一组控件 + 测试/设主/删除；provApply 全量应用。
	provs       []*providerForm
	primaryIdx  int // 当前 primary（provs 下标；应用时取该条名字）
	provAdd     widget.Clickable
	provApply   widget.Clickable
	provStatus  string // provider 区反馈（"" = 默认提示）
	provErr     bool
	testResults chan testOutcome // 连通性测试异步结果（测试 goroutine 写、帧循环排空）
	win         *app.Window      // 结果回灌的 Invalidate（headless = nil）

	// profile 区（D110③）。
	profCreate widget.Clickable
	profCopy   widget.Clickable
	profSwitch widget.Clickable

	snap     SettingsSnapshot // 开窗快照（保存 diff 基准 + 变更提示）
	profiles ProfilesSnapshot // profile 区数据面（动作后刷新，D110③）
	status   string           // 保存反馈（本 goroutine 独占；"" = 默认提示）
	err      bool             // 反馈为错误态（红字）
	readOnly bool             // 无写回调：保存仅报只读占位
	focused  bool             // 首帧焦点入首档（其余点击自聚焦，editor.go FocusCmd）
}

// providerForm 单个 provider 的编辑控件组（D110②；本窗 goroutine 独占）。
type providerForm struct {
	orig        string // 快照原名（Apply 保留原 api_key 与无关键；"" = 新增）
	name        widget.Editor
	baseURL     widget.Editor
	apiKey      widget.Editor // 只写不回显（D35）：留空 = 保持原值
	models      widget.Editor // 逗号分隔
	tokenizer   widget.Editor
	unsupported widget.Editor // 逗号分隔
	test        widget.Clickable
	del         widget.Clickable
	setPrimary  widget.Clickable
}

// testOutcome 连通性测试异步结果（testResults 单消费者 = 帧循环）。
type testOutcome struct {
	name string
	err  error
}

// newSettingsFormState 表单控件与固定档的构造骨架（快照无关：列表轴向/枚举档/
// 单行档/结果通道）。与快照填表分开 = 开窗路径与测试共用同一构造口径（S4c：
// 列表轴向属构造不变量——轴向错配会让 material.List 按视口**宽**迭代，只画首行）。
func newSettingsFormState() *settingsForm {
	f := &settingsForm{
		perm:        cycleField{displays: permLevels, values: permLevels},
		effort:      cycleField{displays: effortLevels, values: effortValues},
		theme:       cycleField{displays: themeLevels, values: themeValues},
		scale:       cycleField{displays: zoomLevels, values: zoomValues},
		testResults: make(chan testOutcome, 8),
	}
	f.list.Axis = layout.Vertical // 表单行列表恒纵向（material.List 未设轴向 = Horizontal）
	for _, ed := range []*widget.Editor{&f.model, &f.hotkey, &f.fontSize, &f.winW, &f.winH, &f.profName} {
		ed.SingleLine = true // 表单单行档（Enter 无提交语义，保存走按钮）
	}
	return f
}

// newSettingsForm 开窗现取快照填表：读回调缺失 = 空表单，写回调缺失 = 只读占位。
func newSettingsForm(u *UI) *settingsForm {
	f := newSettingsFormState()
	f.readOnly = u.opts.ApplySettings == nil
	if u.opts.Profiles != nil {
		f.profiles = u.opts.Profiles.Snapshot() // D110③：profile 区数据面
	}
	if u.opts.ProviderMgr != nil {
		f.loadProviders(u.opts.ProviderMgr.Snapshot()) // D110②：provider 编辑区
	}
	if u.opts.Settings == nil {
		return f
	}
	s := u.opts.Settings()
	if s.Permission == "" {
		s.Permission = "strict" // 快照未就绪（Agent 晚于 UI 构造）对齐默认档 perm.DefaultLevel，防误发切换
	}
	if s.Scale == 0 {
		s.Scale = 1.0 // D90：config 未配 = 缺省（表单显示 100%）
	}
	if s.FontSize == 0 {
		s.FontSize = fontSpBase
	}
	for _, ed := range []*widget.Editor{&f.maxTurns, &f.maxCtx, &f.compact, &f.toolChars, &f.toolTo} {
		ed.SingleLine = true
	}
	// f.sysPrompt / f.rawCfg 保持多行（表单仅有的多行档）。
	f.snap = s
	f.model.SetText(s.Model)
	// api_key 只写不回显（D35）：字段恒空，留空 = 保持不变。
	f.hotkey.SetText(s.Hotkey)
	f.think.Value = s.Think
	f.perm.setValue(s.Permission)
	f.effort.setValue(s.Effort)
	f.theme.setValue(s.Theme) // "" 未命中 → 首项 = 跟随系统
	f.scale.setValue(fmt.Sprintf("%g", s.Scale))
	f.fontSize.SetText(fmt.Sprintf("%g", s.FontSize))
	if s.WindowWidth > 0 {
		f.winW.SetText(strconv.Itoa(s.WindowWidth))
	}
	if s.WindowHeight > 0 {
		f.winH.SetText(strconv.Itoa(s.WindowHeight))
	}
	// S4/D110③ 全量分组页填表。
	f.notify.Value = s.Notify
	f.tts.Value = s.TTS
	if s.MaxTurns > 0 {
		f.maxTurns.SetText(strconv.Itoa(s.MaxTurns))
	}
	if s.MaxContextTokens > 0 {
		f.maxCtx.SetText(strconv.Itoa(s.MaxContextTokens))
	}
	if s.CompactThreshold > 0 {
		f.compact.SetText(fmt.Sprintf("%g", s.CompactThreshold))
	}
	if s.ToolOutputChars > 0 {
		f.toolChars.SetText(strconv.Itoa(s.ToolOutputChars))
	}
	if s.ToolTimeoutSec > 0 {
		f.toolTo.SetText(strconv.Itoa(s.ToolTimeoutSec))
	}
	f.sysPrompt.SetText(s.SystemPrompt)
	f.rawCfg.SetText(s.RawJSON)
	return f
}

// addProviderForm 追加一个空白 provider 编辑组（帧循环与测试共用；Apply 校验必填项）。
func (f *settingsForm) addProviderForm() {
	pf := &providerForm{}
	for _, ed := range []*widget.Editor{&pf.name, &pf.baseURL, &pf.apiKey, &pf.models, &pf.tokenizer, &pf.unsupported} {
		ed.SingleLine = true
	}
	f.provs = append(f.provs, pf)
}

// loadProviders 以快照重建 provider 编辑区（开窗填表；快照空 = 无条目，留新增入口）。
func (f *settingsForm) loadProviders(snap []ProviderSnapshot) {
	f.provs = make([]*providerForm, 0, len(snap))
	f.primaryIdx = 0
	for i, p := range snap {
		pf := &providerForm{orig: p.Name}
		for _, ed := range []*widget.Editor{&pf.name, &pf.baseURL, &pf.apiKey, &pf.models, &pf.tokenizer, &pf.unsupported} {
			ed.SingleLine = true
		}
		pf.name.SetText(p.Name)
		pf.baseURL.SetText(p.BaseURL)
		pf.models.SetText(p.Models)
		pf.tokenizer.SetText(p.Tokenizer)
		pf.unsupported.SetText(p.Unsupported)
		if p.Primary {
			f.primaryIdx = i
		}
		f.provs = append(f.provs, pf)
	}
}
