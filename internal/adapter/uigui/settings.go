package uigui

// 设置窗（§15.7/D60 核心档 + §15.4/D61 主题档）：表单状态、保存流与帧渲染。
// 并发口径（§15.7）：表单状态仅次窗事件循环 goroutine 读写、不跨窗共享；持久化经
// ApplySettings 单一写回调（装配根实现），内核命令经 inCh 排队到主循环与键入同
// 路径串行执行（壳内不旁路），主题/快捷键热生效经消息与原子槽（同跨窗模型）。

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
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
// ProviderManager（D110②），不在本快照。
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
}

// SettingsPatch 保存提交（表单全量；模型服务走 ProviderManager.Apply，D110②）。
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

// fieldH 文本档统一高度（dp）：单行编辑器 + 上下留白（material 无内建输入框边框）。
const fieldH = 40

// fieldLabelW 标签列宽（dp）：对齐各行控件起点。
const fieldLabelW = 92

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

// newSettingsForm 开窗现取快照填表：读回调缺失 = 空表单，写回调缺失 = 只读占位。
func newSettingsForm(u *UI) *settingsForm {
	f := &settingsForm{
		perm:        cycleField{displays: permLevels, values: permLevels},
		effort:      cycleField{displays: effortLevels, values: effortValues},
		theme:       cycleField{displays: themeLevels, values: themeValues},
		scale:       cycleField{displays: zoomLevels, values: zoomValues},
		testResults: make(chan testOutcome, 8),
	}
	for _, ed := range []*widget.Editor{&f.model, &f.hotkey, &f.fontSize, &f.winW, &f.winH, &f.profName} {
		ed.SingleLine = true // 表单单行档（Enter 无提交语义，保存走按钮）
	}
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

// saveSettings 保存流（渲染外的可 headless 测段）：组装 patch → 写回调（装配根持久化
// 文本键并返回内核命令计划）→ 命令经 inCh 排队（与键入同路径串行执行、回执进转写区；
// 队列 256 深、主循环持续排空，阻塞投递不致死锁）→ 主题/快捷键 GUI 侧热生效 →
// 反馈文案写回表单。
func (u *UI) saveSettings(f *settingsForm) {
	if f.readOnly || u.opts.ApplySettings == nil {
		f.err = true
		f.status = "设置窗未接线（只读占位）"
		return
	}
	p := SettingsPatch{
		Permission: f.perm.value(),
		Model:      strings.TrimSpace(f.model.Text()),
		Think:      f.think.Value,
		Effort:     f.effort.value(),
		Hotkey:     strings.TrimSpace(f.hotkey.Text()),
		Theme:      f.theme.value(),
	}
	if p.Model == "" {
		p.Model = f.snap.Model // 空模型名 = 保持当前
	}
	// D90 三旋钮（写回调前校验，非法不落盘）：缩放 = cycle 档（恒合法）；字号/窗口
	// 尺寸空 = 保持快照原值，数值非法/越界拦截。
	p.Scale = f.snap.Scale
	if v, err := strconv.ParseFloat(f.scale.value(), 64); err == nil {
		p.Scale = v
	}
	if v := strings.TrimSpace(f.fontSize.Text()); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < fontSpMin || n > fontSpMax {
			f.err = true
			f.status = fmt.Sprintf("字号须为 %g–%g 的数值", fontSpMin, fontSpMax)
			return
		}
		p.FontSize = n
	}
	if v := strings.TrimSpace(f.winW.Text()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			f.err = true
			f.status = "窗口宽须为非负像素数值"
			return
		}
		p.WindowWidth = n
	}
	if v := strings.TrimSpace(f.winH.Text()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			f.err = true
			f.status = "窗口高须为非负像素数值"
			return
		}
		p.WindowHeight = n
	}
	if p.Hotkey != "" {
		if _, err := parseHotkey(p.Hotkey); err != nil {
			f.err = true
			f.status = "快捷键非法：" + err.Error()
			return
		}
	}
	cmds, err := u.opts.ApplySettings(p)
	if err != nil {
		f.err = true
		f.status = "保存失败：" + err.Error()
		return
	}
	for i := range cmds {
		c := cmds[i]
		u.inCh <- port.UserInput{Command: &c}
	}
	if p.Theme != f.snap.Theme {
		_ = u.post(themeMsg{mode: p.Theme}) // 主窗主题热生效（事件循环串行，§15.4）
	}
	if p.Hotkey != f.snap.Hotkey {
		u.setHotkey(p.Hotkey) // 原子槽 + 托盘线程重注册（失败回退 Ctrl+Alt+A，§15.1）
	}
	if p.Scale != f.snap.Scale || p.FontSize != f.snap.FontSize {
		u.SetZoom(p.Scale, p.FontSize) // D90：下帧 Metric 咽喉点生效（原子换存）
	}
	if p.WindowWidth != f.snap.WindowWidth || p.WindowHeight != f.snap.WindowHeight {
		u.SetWindowSize(p.WindowWidth, p.WindowHeight) // D90：窗口线程 SetWindowPos（headless no-op）
	}
	f.err = false
	f.status = "已保存"
	if p.Model != f.snap.Model {
		f.status = "已保存（模型名经 /model 热切换）"
	}
	f.snap = SettingsSnapshot{
		Permission: p.Permission, Model: p.Model, Think: p.Think, Effort: p.Effort,
		Hotkey: p.Hotkey, Theme: p.Theme,
		Scale: p.Scale, FontSize: p.FontSize,
		WindowWidth: p.WindowWidth, WindowHeight: p.WindowHeight,
	}
}

// profileActionLabel 三动作的反馈前缀（渲染与反馈共用）。
func profileActionLabel(act string) string {
	switch act {
	case "create":
		return "新建"
	case "copy":
		return "复制"
	case "switch":
		return "切换"
	}
	return act
}

// profileAction profile 区三动作（D110③/修订④）：名字非空校验 → ProfilesManager
// 执行 → 反馈写表单状态（成功绿/错误红，同保存流口径）并刷新快照。headless 可测
// （不进帧渲染）。
func (u *UI) profileAction(f *settingsForm, act string) {
	if u.opts.Profiles == nil {
		f.err = true
		f.status = "profile 管理未接线"
		return
	}
	name := strings.TrimSpace(f.profName.Text())
	if name == "" {
		f.err = true
		f.status = "请先填写 profile 名"
		return
	}
	var err error
	switch act {
	case "create":
		err = u.opts.Profiles.Create(name)
	case "copy":
		err = u.opts.Profiles.Copy(name)
	case "switch":
		err = u.opts.Profiles.Switch(name)
	default:
		err = fmt.Errorf("未知动作 %q", act)
	}
	if err != nil {
		f.err = true
		f.status = "profile " + profileActionLabel(act) + "失败：" + err.Error()
		return
	}
	f.err = false
	f.profiles = u.opts.Profiles.Snapshot() // 新建/复制后清单刷新，立即可切换
	switch act {
	case "create":
		f.status = "已新建 profile " + name + "（模板已生成；填「Profile 名」后点「切换」启用，重启生效）"
	case "copy":
		f.status = "已复制当前 profile 配置到 " + name + "（仅配置不含会话数据；重启生效）"
	case "switch":
		f.status = "已切换 profile → " + name + "（重启生效）"
	}
}

// providerPatchOf 组装单条 provider 提交（编辑区现值 → patch；apiKey 只写不回显）。
func providerPatchOf(pf *providerForm) ProviderPatch {
	return ProviderPatch{
		Orig:        pf.orig,
		Name:        strings.TrimSpace(pf.name.Text()),
		BaseURL:     strings.TrimSpace(pf.baseURL.Text()),
		APIKey:      strings.TrimSpace(pf.apiKey.Text()),
		Models:      strings.TrimSpace(pf.models.Text()),
		Tokenizer:   strings.TrimSpace(pf.tokenizer.Text()),
		Unsupported: strings.TrimSpace(pf.unsupported.Text()),
	}
}

// applyProviderChanges provider 区「应用变更」（D110②）：全量 patch + primary →
// ProviderManager.Apply（cmd 侧校验与泛键写回，重启生效）。与核心档保存流独立——
// 两类写路径互不混叠。
func (u *UI) applyProviderChanges(f *settingsForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = "provider 编辑未接线"
		return
	}
	if len(f.provs) == 0 {
		f.provErr = true
		f.provStatus = "至少保留一个 provider"
		return
	}
	patches := make([]ProviderPatch, 0, len(f.provs))
	for _, pf := range f.provs {
		patches = append(patches, providerPatchOf(pf))
	}
	primary := ""
	if f.primaryIdx >= 0 && f.primaryIdx < len(f.provs) {
		primary = strings.TrimSpace(f.provs[f.primaryIdx].name.Text())
	}
	if err := u.opts.ProviderMgr.Apply(primary, patches); err != nil {
		f.provErr = true
		f.provStatus = "应用失败：" + err.Error()
		return
	}
	f.provErr = false
	f.provStatus = "已应用 provider 变更（重启生效）"
	f.loadProviders(u.opts.ProviderMgr.Snapshot()) // 以落盘事实源重建编辑区（orig 对齐、primary 回正）
}

// runProviderTest 连通性测试（D110②，真实最小请求）：goroutine 跑 Test（网络 +
// 短超时，不得卡帧循环），结果经 testResults 通道回灌帧循环（win.Invalidate 唤醒）。
func (u *UI) runProviderTest(f *settingsForm, pf *providerForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = "provider 编辑未接线"
		return
	}
	patch := providerPatchOf(pf)
	go func() {
		err := u.opts.ProviderMgr.Test(patch)
		select {
		case f.testResults <- testOutcome{name: patch.Name, err: err}:
		default: // 槽满丢弃最旧（测试结果是最简反馈，不积压）
			select {
			case <-f.testResults:
			default:
			}
			f.testResults <- testOutcome{name: patch.Name, err: err}
		}
		if f.win != nil {
			f.win.Invalidate() // 帧循环重绘排空结果（并发安全）
		}
	}()
}

// drainTestResults 帧循环排空连通性测试结果（最新一条覆盖展示）。
func (f *settingsForm) drainTestResults() {
	for {
		select {
		case r := <-f.testResults:
			if r.err != nil {
				f.provErr = true
				f.provStatus = "连通测试 " + r.name + " 失败：" + r.err.Error()
			} else {
				f.provErr = false
				f.provStatus = "连通测试 " + r.name + " 通过"
			}
		default:
			return
		}
	}
}

// setHotkey 更新快捷键配置原子槽并请求托盘线程重注册（设置窗保存；注册归属托盘
// 线程——RegisterHotKey 归调用线程，跨线程只能投消息，§15.1）。headless/非窗口
// 平台 = 槽更新即止。
func (u *UI) setHotkey(hk string) {
	u.hotkeyCfg.Store(hk)
	reRegisterHotkey()
}

// rowKind 表单行类型（动态行计划：provider 编辑区行数随 providers 数量变化，D110②）。
type rowKind int

const (
	rowTitle rowKind = iota
	rowProfHead
	rowProfCurrent
	rowProfName
	rowProfBtns
	rowSecModel
	rowProvField
	rowProvBtns
	rowProvAdd
	rowProvApply
	rowSecCore
	rowPerm
	rowThink
	rowEffort
	rowSecLook
	rowHotkey
	rowTheme
	rowScale
	rowFontSize
	rowWinSize
	rowActions
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
	{"API 密钥", "只写不回显；留空保持不变（D35）"},
	{"模型列表", "逗号分隔；首个 = 缺省请求模型"},
	{"Tokenizer", "本地 tokenizer.json 路径；空 = 估算"},
	{"不认参数", "逗号分隔；服务端不认的请求参数（D34）"},
}

// rowDesc 一行的类型与定位。
type rowDesc struct {
	kind  rowKind
	prov  int // rowProvField / rowProvBtns：provider 下标
	field int // rowProvField：字段槽位
}

// rowPlan 本帧行计划（每帧现算：providers 数量在新增/删除后即变）。
func (f *settingsForm) rowPlan() []rowDesc {
	rows := []rowDesc{
		{kind: rowTitle}, {kind: rowProfHead}, {kind: rowProfCurrent},
		{kind: rowProfName}, {kind: rowProfBtns}, {kind: rowSecModel},
	}
	for i := range f.provs {
		for fld := 0; fld < provFldN; fld++ {
			rows = append(rows, rowDesc{kind: rowProvField, prov: i, field: fld})
		}
		rows = append(rows, rowDesc{kind: rowProvBtns, prov: i})
	}
	return append(rows,
		rowDesc{kind: rowProvAdd}, rowDesc{kind: rowProvApply},
		rowDesc{kind: rowSecCore}, rowDesc{kind: rowPerm}, rowDesc{kind: rowThink}, rowDesc{kind: rowEffort},
		rowDesc{kind: rowSecLook}, rowDesc{kind: rowHotkey}, rowDesc{kind: rowTheme}, rowDesc{kind: rowScale},
		rowDesc{kind: rowFontSize}, rowDesc{kind: rowWinSize}, rowDesc{kind: rowActions})
}

// settingsFrame 设置窗单帧：滚动表单（§15.7/D60 核心档数据面 + D110②③ 动态区）。
// 事件先于渲染取尽（Clicked 自带 Update；点击触发重绘，新值随后续帧呈现）。常规窗
// 不做形状位图/羽化（§15.7 形态）：windowBg 铺底，与主窗内容面同底。
func settingsFrame(gtx layout.Context, th *material.Theme, u *UI, f *settingsForm) layout.Dimensions {
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
		gtx.Execute(key.FocusCmd{Tag: &f.model}) // 开窗焦点入模型名档；点击切换（editor.go 自聚焦）
	}
	rows := f.rowPlan()
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, windowBg,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return material.List(th, &f.list).Layout(gtx, len(rows), f.row(th, rows))
		}),
	)
}

// row 第 i 行渲染（material.List 回调；行内容按 rowPlan 的类型分派）。
func (f *settingsForm) row(th *material.Theme, rows []rowDesc) func(layout.Context, int) layout.Dimensions {
	return func(gtx layout.Context, i int) layout.Dimensions {
		return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			r := rows[i]
			switch r.kind {
			case rowTitle:
				return material.H6(th, "设置").Layout(gtx)
			case rowProfHead: // D110③：profile 区（当前 + 名字输入 + 三动作）
				return material.Subtitle1(th, "Profile").Layout(gtx)
			case rowProfCurrent:
				return fieldRow(gtx, th, "当前", func(gtx layout.Context) layout.Dimensions {
					txt := f.profiles.Current
					if len(f.profiles.Items) > 0 {
						txt += "（可用：" + strings.Join(f.profiles.Items, ", ") + "）"
					}
					l := material.Body1(th, txt)
					l.Color = textDim
					return l.Layout(gtx)
				})
			case rowProfName:
				return fieldRow(gtx, th, "Profile 名", editorBox(th, &f.profName, "新建 / 复制 / 切换的目标名"))
			case rowProfBtns:
				return f.profileButtonsRow(gtx, th)
			case rowSecModel: // D110②：provider 编辑区（动态行，重启生效）
				return material.Subtitle1(th, "模型服务（provider 列表）").Layout(gtx)
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
			case rowSecCore:
				return material.Subtitle1(th, "权限与思考").Layout(gtx)
			case rowPerm:
				return fieldRow(gtx, th, "权限等级", cycleBtn(th, &f.perm))
			case rowThink:
				return material.CheckBox(th, &f.think, "原生思考（/think 总开关，关时忽略推理档位）").Layout(gtx)
			case rowEffort:
				return fieldRow(gtx, th, "推理档位", cycleBtn(th, &f.effort))
			case rowSecLook:
				return material.Subtitle1(th, "外观与呼出").Layout(gtx)
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
			default:
				return f.actionsRow(gtx, th)
			}
		})
	}
}

// providerButtonsRow 单个 provider 的动作按钮：连通测试 / 设为主 / 删除（D110②）。
func (f *settingsForm) providerButtonsRow(gtx layout.Context, th *material.Theme, i int) layout.Dimensions {
	pf := f.provs[i]
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, c, label)
			b.TextSize = unit.Sp(12)
			return b.Layout(gtx)
		}
	}
	primary := "设为主"
	col := th.Fg
	if f.primaryIdx == i {
		primary, col = "★ 主", brandColor
	}
	pb := material.Button(th, &pf.setPrimary, primary)
	pb.TextSize = unit.Sp(12)
	pb.Color = col
	del := material.Button(th, &pf.del, "删除")
	del.TextSize = unit.Sp(12)
	if len(f.provs) <= 1 {
		del.Color = textDim // 仅剩一条不可删（providers 不可为空）
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(btn(&pf.test, "测试连通")),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, pb.Layout)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx, del.Layout)
		}),
	)
}

// providerAddRow 新增 provider 入口（编辑区尾部；Apply 校验名字等必填项）。
func (f *settingsForm) providerAddRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	b := material.Button(th, &f.provAdd, "＋ 新增 provider")
	b.TextSize = unit.Sp(13)
	return b.Layout(gtx)
}

// providerApplyRow provider 区应用按钮 + 反馈文案（成功绿/错误红 = 转写区语义色）。
func (f *settingsForm) providerApplyRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline, Spacing: layout.SpaceStart}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, &f.provApply, "应用 provider 变更")
			b.Background = brandColor
			b.Color = whiteText
			b.TextSize = unit.Sp(13)
			return b.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.provStatus
				col := textSystem
				switch {
				case text == "":
					text, col = "变更全量写回 config，重启生效；「测试连通」发真实最小请求。", textDim
				case f.provErr:
					col = textError
				}
				l := material.Body2(th, text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}

// profileButtonsRow profile 区三动作按钮（D110③）：新建 / 复制当前 / 切换（重启生效）。
func (f *settingsForm) profileButtonsRow(gtx layout.Context, th *material.Theme) layout.Dimensions {
	btn := func(c *widget.Clickable, label string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, c, label)
			b.TextSize = unit.Sp(13)
			return b.Layout(gtx)
		}
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

// fieldRow 一行：定宽标签列 + 控件（基线对齐，各行控件起点一致）。
func fieldRow(gtx layout.Context, th *material.Theme, label string, control layout.Widget) layout.Dimensions {
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = fieldLabelW
			gtx.Constraints.Max.X = fieldLabelW
			return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Body1(th, label).Layout(gtx)
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
		b := material.Button(th, &c.click, label)
		b.TextSize = unit.Sp(13)
		return b.Layout(gtx)
	}
}

// editorBox 文本档输入区（layout.Widget，供 fieldRow 装配）：定高满宽底衬
// （material v0.10 编辑器无内建边框——底衬即可编辑标记）。列表行纵向约束无界，
// 尺寸取行约束宽 + 固定高，不按 Max 铺底。
func editorBox(th *material.Theme, ed *widget.Editor, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := image.Point{X: gtx.Constraints.Max.X, Y: fieldH}
		paint.FillShape(gtx.Ops, th.Bg, clip.Rect(image.Rectangle{Max: size}).Op())
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
			b := material.Button(th, &f.save, "保存并应用")
			b.Background = brandColor
			b.Color = whiteText
			b.TextSize = unit.Sp(14)
			return b.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := f.status
				col := textSystem
				switch {
				case text == "":
					text, col = "保存后立即生效；provider 列表在「模型服务」区应用（重启生效）。", textDim
				case f.err:
					col = textError
				}
				l := material.Body2(th, text)
				l.Color = col
				return l.Layout(gtx)
			})
		}),
	)
}
