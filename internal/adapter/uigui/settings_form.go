package uigui

// 设置窗表单状态 settingsForm：字段、按快照无关的骨架构造与按快照填表。
// 页模型与行渲染见 settings_rows.go；provider 编辑组见 settings_provider_form.go。

import (
	"fmt"
	"strconv"
	"strings"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/widget"
)

// settingsForm 设置窗表单状态（次窗事件循环 goroutine 独占；控件即状态，不跨窗
// 共享）。S4d/D142 起内容分页：page = 当前页、pages = 每页各自的 material.List
// （滚动位置按页保留）、navBtns = 导航栏点击件；控件事先常驻表单对象，**切页只是换
// 渲染**——跨页编辑天然保留。行模型为动态行计划（pageRows）：行数随 provider 数量
// 变化（D110② 编辑区）。
type settingsForm struct {
	// 分页（S4d/D142；D162 起按注册表长度构造——页数不再是编译期常量）。
	page    int                // 当前页（pageGeneral..pageAbout）
	pages   []widget.List      // 每页一个滚动列表（构造处定轴向）
	navBtns []widget.Clickable // 左侧导航栏项（按页缓存）

	// 工具与 MCP 页：内置工具逐条开关 + 已连 server 的状态与动作（D143）。
	tools       ToolsAdmin                   // nil = 只读占位（未接线）
	toolsSnap   ToolsSnapshot                // 每帧原子快照（本窗 goroutine 独占）
	toolsRev    int64                        // 上帧 Revision（变了才重建行/按钮）
	toolBtns    map[string]*widget.Clickable // 动作按钮（get-or-create 按 key）
	toolRes     chan toolActionResult        // 异步动作结果（回灌状态行）
	toolWait    int                          // 在途异步动作数（UI goroutine 增减）
	toolsStatus string                       // 反馈文案（状态行）
	toolsErr    bool                         // 反馈是否为错误

	// 技能页（D202/D203）：数据面见 settings_skills.go。安装/更新会联网与拉起 git，
	// 故与工具页同款异步（旁路 goroutine + 结果通道 + Invalidate）；卸载走行内二次确认。
	skills        SkillsAdmin                  // nil = 只读占位（未接线）
	skillsSnap    SkillsSnapshot               // 每帧原子快照（Revision 变了才重建）
	skillsRev     int64                        // 上帧 Revision（起点 −1 = 首帧必采纳）
	skillBtns     map[string]*widget.Clickable // 动作按钮（get-or-create 按 key）
	skillSource   widget.Editor                // 安装来源（路径或链接；选择框只填这里）
	skillInstall  widget.Clickable             // 「安装」按钮
	skillPickFile widget.Clickable             // 「选择文件…」
	skillPickDir  widget.Clickable             // 「选择文件夹…」
	skillDirAdd   widget.Clickable             // 「＋ 添加技能目录」
	skillDirInput widget.Editor                // 新增技能目录输入档
	skillRescan   widget.Clickable             // 「重新扫描」
	skillRes      chan skillActionResult       // 异步动作结果（回灌状态行）
	skillWait     int                          // 在途异步动作数（UI goroutine 增减）
	skillsStatus  string                       // 反馈文案（状态行）
	skillsErr     bool                         // 反馈是否为错误
	skillConfirm  string                       // 待卸载的技能名（"" = 无；行内二次确认）

	// 角色页（§5.17）：清单经 Options.Characters 现取（装配根节流读盘），动作经
	// `/character` 命令通道（不直连管理面）；头像异步解码（缓存实例本窗独占，见 avatar_cache.go）。
	charWired      bool                         // Options.Characters 已接线（未接线 = 只读占位）
	charactersSnap CharactersSnapshot           // 本页快照（进入本页时现取）
	charBtns       map[string]*widget.Clickable // 动作按钮（get-or-create 按 key）
	charSource     widget.Editor                // 卡文件来源（路径；选择框只填这里）
	charImport     widget.Clickable             // 「导入」按钮
	charPick       widget.Clickable             // 「选择文件…」
	charConfirm    string                       // 待删除的 slug（"" = 无；行内二次确认）
	charStatus     string                       // 反馈文案（状态行）
	charErr        bool                         // 反馈是否为错误
	charAvatars    *avatarCache                 // 头像缓存（本窗独占；结果经通道回灌）

	// 密钥库页（S9-6b/D161）：数据面见 settings_secrets.go。动作**同步**（本地小文件 IO），
	// 故没有工具页那套结果通道；`secretBtns` 按键名分派（动作按钮 + 行内二次确认）。
	secrets       SecretsAdmin                 // nil = 只读占位（未接线）
	secretsSnap   SecretsSnapshot              // 每帧原子快照（Revision 变了才重建）
	secretsRev    int64                        // 上帧 Revision
	secretBtns    map[string]*widget.Clickable // 动作按钮（get-or-create 按 key）
	secretAdd     widget.Clickable             // 「＋ 存入」按钮
	secretName    widget.Editor                // 新增/覆盖的键名（单行）
	secretValue   widget.Editor                // 新增/覆盖的值（单行；动作后恒清空）
	secretsStatus string                       // 反馈文案（状态行；不含值）
	secretsErr    bool                         // 反馈是否为错误
	secretConfirm string                       // 待删目标名（"" = 无；行内二次确认）
	// importStatus/importErr 一键导入的反馈。导入入口已从界面撤下（D206），这两个槽位与
	// 动作分支保留：功能仍在，只是不给按钮。
	importStatus string
	importErr    bool

	// 核心档控件（模型名/思考/档位/外观；provider 编辑走 provs）。
	model    widget.Editor
	hotkey   widget.Editor
	fontSize widget.Editor // D90：正文字号 sp（数值，空 = 不改）
	winW     widget.Editor // D90：主窗像素宽（数值，空 = 不改）
	winH     widget.Editor // D90：主窗像素高
	profName widget.Editor // D110③：profile 名（新建/复制/切换共用输入档）
	think    widget.Bool
	unattend widget.Bool // D185：无人值守开关（常规页权限区）
	perm     selectField
	effort   selectField
	theme    selectField
	scale    selectField // D90：元素缩放（cycle 百分比档）
	save     widget.Clickable
	// D141：欢迎 / 首次运行引导入口（设置窗是唯一入口——托盘与球右键菜单都不带此项）。
	welcomeOpen widget.Clickable

	// 全量分组页控件（S4/D110③）：输出 / 限制 / 提示词 / 高级 raw JSON。
	notify     widget.Bool
	tts        widget.Bool
	maxTurns   widget.Editor
	maxCtx     widget.Editor
	compact    widget.Editor
	toolChars  widget.Editor
	toolTo     widget.Editor
	backupKeep widget.Editor // D254：会话树保留的备份代数（「常规」页存储区）
	rawCfg     widget.Editor // 多行：config.json 原文（Q10）

	// 人格模块树（S7/D144，S7b/D145 独立成页）：编辑区实现收在 promptEditor 里，
	// 与历史窗遮罩的「默认模板」页签共用（D154）——字段仍按原名提升，设置页侧
	// 代码不必改称。
	promptEditor

	// provider 编辑区（D110②）：每条一组控件 + 测试/设主/删除；provApply 全量应用。
	// S11-7② 起按卡片折叠（D176 口径二）：每张卡片一行摘要，同一时刻只展开一张
	// （provOpen = 展开的下标；-1 = 全收起）。
	provs       []*providerForm
	primaryIdx  int // 当前 primary（provs 下标；应用时取该条名字）
	provOpen    int // 展开的卡片下标（-1 = 全收起；界面状态，不进 config）
	provAdd     widget.Clickable
	provApply   widget.Clickable
	provStatus  string // provider 区反馈（"" = 默认提示）
	provErr     bool
	provFetched bool             // 本窗会话已自动拉过一轮服务端清单（D181：一窗一次）
	testResults chan testOutcome // 连通性测试异步结果（测试 goroutine 写、帧循环排空）
	win         *app.Window      // 结果回灌的 Invalidate（headless = nil）

	// 降级链（D176 口径三）：链是列表不是文本框——每项 provider / model，可上移/下移/删除/
	// 添加。chain/snapChain 以「磁盘快照」为基线（保存 diff + 未改动判定），链改动随
	// 「保存并应用」写回（重启生效，与 provider 变更同口径）。
	chain     []*fallbackForm
	snapChain []FallbackLink
	chainAdd  widget.Clickable
	// chainChoices 每项的 provider 下拉选项（快照现取：provider 列表可能被「模型服务」页改过，
	// 但那一改要重启才生效，故以开窗快照为准，不每帧重取）。
	chainChoices []ProviderChoice

	// 枚举选择器弹层（S4e）：全窗唯一的模态，画在 settingsFrame 最后一层。
	pick pickerState

	// 语音页（D179/D222）：识别/合成各是一份完整配置——服务可借（选择器，选项同
	// chainChoices），端点与密钥也能自己填；音色与风格是自由文本；试听在后台合成一句，
	// 结果经 voiceRes 回灌行内状态。
	asrProv     selectField
	asrKind     selectField
	asrBaseURL  widget.Editor
	asrKey      widget.Editor // 只写不回显（空 = 保持 / 借所选服务的）
	asrKeyUse   widget.Clickable
	asrModel    widget.Editor
	asrPath     widget.Editor // 本地模型文件路径（whisper 用）
	asrLang     selectField
	ttsProv     selectField
	ttsKind     selectField
	ttsBaseURL  widget.Editor
	ttsKey      widget.Editor
	ttsKeyUse   widget.Clickable
	ttsModel    widget.Editor
	ttsVoice    widget.Editor
	ttsDesign   widget.Editor
	ttsStyle    widget.Editor
	ttsTags     widget.Editor
	voiceTry    widget.Clickable
	voiceRes    chan error
	voiceBusy   bool   // 试听合成在途
	voiceStatus string // 试听反馈（成功/失败）
	voiceErr    bool

	// 联网页（D200）：搜索与抓取各自一组有序提供商（顺序即优先级），加本地抓取的
	// 网络与安全参数。密钥只写不回显（空 = 保持），与 provider 密钥池同口径。
	webSearch    []*webProviderForm
	webFetch     []*webProviderForm
	webSearchAdd widget.Clickable
	webFetchAdd  widget.Clickable
	webCount     widget.Editor // 缺省条数（空 = 不改）
	webMaxChars  widget.Editor // 正文上限（空 = 不改）
	webProxy     widget.Editor
	webUA        widget.Editor
	webTimeout   widget.Editor
	webRobots    widget.Bool // 遵守 robots.txt
	webPrivate   widget.Bool // 允许私网/回环目标（调试）

	// profile 区：新建 / 复制 / 切换配置档，切换须重启才生效（D110③）。
	profCreate widget.Clickable
	profCopy   widget.Clickable
	profSwitch widget.Clickable

	// 采样参数（D204）：整链缺省的四项（空 = 不发送）。
	samplingTemp widget.Editor
	samplingTopP widget.Editor
	samplingMax  widget.Editor
	samplingStop widget.Editor

	// 关于页（D208）：纯信息页——没有配置项，不进快照也不会进 patch。aboutVersion 取
	// 装配根注入的 Options.Version；图标缓存与两个动作件、反馈文案挂在这里（本窗独占）。
	aboutVersion string
	aboutIcon    aboutIconCache
	aboutOpen    widget.Clickable
	aboutCopy    widget.Clickable
	aboutStatus  string
	aboutErr     bool

	snap     SettingsSnapshot // 开窗快照（保存 diff 基准 + 变更提示）
	profiles ProfilesSnapshot // profile 区数据面（动作后刷新，D110③）
	status   string           // 保存反馈（本 goroutine 独占；"" = 默认提示）
	err      bool             // 反馈为错误态（红字）
	readOnly bool             // 无写回调：保存仅报只读占位
	focused  bool             // 首帧焦点入首档（其余点击自聚焦，editor.go FocusCmd）
}

// newSettingsFormState 表单控件与固定档的构造骨架（快照无关：列表轴向 / 枚举档 /
// 单行档 / 结果通道）。与快照填表分开 = 开窗路径与测试共用同一构造口径（S4c：
// 列表轴向属构造不变量——轴向错配会让 material.List 按视口**宽**迭代，只画首行）。
func newSettingsFormState() *settingsForm {
	f := &settingsForm{
		provOpen:    -1, // S11-7②：开窗全收起（第一张也不自动展开——展开是用户的动作）
		perm:        selectField{displays: permLevels, values: permLevels},
		effort:      selectField{displays: effortLevels, values: effortValues},
		theme:       selectField{displays: themeLevels, values: themeValues},
		scale:       selectField{displays: zoomLevels, values: zoomValues},
		asrLang:     selectField{displays: langLevels, values: langValues},
		asrKind:     selectField{displays: asrKindLevels, values: asrKindValues},
		ttsKind:     selectField{displays: ttsKindLevels, values: ttsKindValues},
		testResults: make(chan testOutcome, 8),
		toolRes:     make(chan toolActionResult, 16),
		skillRes:    make(chan skillActionResult, 16),
		voiceRes:    make(chan error, 2),
		toolBtns:    map[string]*widget.Clickable{},
		skillBtns:   map[string]*widget.Clickable{},
		secretBtns:  map[string]*widget.Clickable{},
		// 密钥库快照「上次 Revision」起点 −1：**首帧必采纳**。若起点取 0，库
		// Revision 恰为 0（空库、或本进程 provider 写路径直接落库未经过本管理面）
		// 时会被当「没变」而跳过，窗口打开即显示空列表。
		secretsRev: -1,
		// 技能页快照同理：起点 −1 让首帧必采纳（空安装面 Revision 常为 0）。
		skillsRev: -1,
		// 角色页头像异步解码的结果通道与记账（§5.17）。
		charBtns: map[string]*widget.Clickable{},
	}
	// 头像缓存：异步结果回灌后催本窗重绘。回调**调用时**现取 f.win（次窗在
	// newSettingsForm 返回后才赋值 runSecondary 的 w），故闭包捕获 f 而非 win 本身。
	f.charAvatars = newAvatarCache(charAvatarPx, func() {
		if f.win != nil {
			f.win.Invalidate()
		}
	})
	f.promptEditor.init()
	// S4c 防线按页复制：行列表轴向恒纵向（material.List 未设轴向 = Horizontal，
	// 按视口**宽**迭代 → 只画首行、其余空白窗底）。页数按注册表长度给（D162）。
	f.pages = make([]widget.List, settingsPageCount)
	f.navBtns = make([]widget.Clickable, settingsPageCount)
	for i := range f.pages {
		f.pages[i].Axis = layout.Vertical
	}
	for _, ed := range []*widget.Editor{&f.model, &f.hotkey, &f.fontSize, &f.winW, &f.winH, &f.profName} {
		ed.SingleLine = true // 表单单行档（Enter 无提交语义，保存走按钮）
	}
	// 密钥库页（S9-6b）：键名与值都是单行档；值档掩码显示（肩上可视读，落盘/日志仍不回显）。
	f.secretName.SingleLine = true
	f.secretValue.SingleLine = true
	f.secretValue.Mask = '•'
	return f
}

// newSettingsForm 开窗现取快照填表：读回调缺失 = 空表单，写回调缺失 = 只读占位。
func newSettingsForm(u *UI) *settingsForm {
	f := newSettingsFormState()
	f.aboutVersion = u.opts.Version // 关于页的构建版本（空/dev = 开发构建）
	f.readOnly = u.opts.ApplySettings == nil
	if u.opts.Profiles != nil {
		f.profiles = u.opts.Profiles.Snapshot() // D110③：profile 区数据面
	}
	if u.opts.ProviderMgr != nil {
		snap := u.opts.ProviderMgr.Snapshot()
		f.loadProviders(snap)    // D110②：provider 编辑区
		f.loadChainChoices(snap) // D176 口径三：降级链的 provider 选项
	}
	f.tools = u.opts.Tools     // S6/D143：工具与 MCP 管理面（nil = 只读占位）
	f.secrets = u.opts.Secrets // S9-6b：密钥库管理面（nil = 只读占位）
	f.syncSecretsSnapshot()    // 开窗即取一次（帧首同款；动作后再取）
	f.skills = u.opts.Skills   // D202/D203：技能管理面（nil = 只读占位）
	f.syncSkillsSnapshot()     // 开窗即取一次（帧首同款；动作后再取）
	f.skillSource.SingleLine = true
	f.skillDirInput.SingleLine = true
	if u.opts.Characters != nil { // §5.17：角色卡清单（nil = 只读占位）
		f.charWired = true
		f.charactersSnap = u.opts.Characters() // 开窗即取一次（进入本页时帧首同款再取）
	}
	f.charSource.SingleLine = true
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
	for _, ed := range []*widget.Editor{&f.maxTurns, &f.maxCtx, &f.compact, &f.toolChars, &f.toolTo, &f.backupKeep,
		&f.asrModel, &f.asrPath, &f.asrBaseURL, &f.asrKey, &f.ttsModel, &f.ttsBaseURL, &f.ttsKey,
		&f.ttsVoice, &f.ttsDesign, &f.ttsStyle, &f.ttsTags,
		&f.webCount, &f.webMaxChars, &f.webProxy, &f.webUA, &f.webTimeout,
		&f.samplingTemp, &f.samplingTopP, &f.samplingMax, &f.samplingStop} {
		ed.SingleLine = true
	}
	f.asrKey.Mask = '•' // 只写不回显（D35 同口径）
	f.ttsKey.Mask = '•'
	// f.sysPrompt / f.rawCfg 保持多行（表单仅有的多行档）。
	f.snap = s
	f.model.SetText(s.Model)
	// api_key 只写不回显（D35）：字段恒空，留空 = 保持不变。
	f.hotkey.SetText(s.Hotkey)
	f.think.Value = s.Think
	f.unattend.Value = s.Unattended
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
	// 通用页与分组页的开关/数值项填表（D110③ 起 config 全量字段都在设置窗可见）。
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
	if s.BackupKeep > 0 {
		f.backupKeep.SetText(strconv.Itoa(s.BackupKeep))
	}
	f.sysPrompt.SetText(s.SystemPrompt)
	f.rawCfg.SetText(s.RawJSON)
	f.builtins = s.Builtins
	// 语音页填表（D179/D222）：provider 选项来自本窗的 provider 快照；未配置 = 首项。
	// 端点原样回填；密钥框恒空（只写不回显）。
	f.setAudioProviderChoices(s.ASRProvider, &f.asrProv)
	f.setAudioProviderChoices(s.TTSProvider, &f.ttsProv)
	f.asrLang.setValue(s.ASRLang)
	f.asrKind.setValue(s.ASRKind)
	f.ttsKind.setValue(s.TTSKind)
	f.asrBaseURL.SetText(s.ASRBaseURL)
	f.ttsBaseURL.SetText(s.TTSBaseURL)
	f.asrModel.SetText(s.ASRModel)
	f.asrPath.SetText(s.ASRModelPath)
	f.ttsModel.SetText(s.TTSModel)
	f.ttsVoice.SetText(s.TTSVoice)
	f.ttsDesign.SetText(s.TTSDesign)
	f.ttsStyle.SetText(s.TTSStyle)
	f.ttsTags.SetText(s.TTSTags)
	f.loadPrompts(s.PromptModules, s.PromptPersona)
	f.loadFallback(s.Fallback, f.chainChoices) // D176 口径三：降级链
	f.loadWeb(s)                               // D200：联网提供商与本地抓取参数
	// D204：采样参数填表（空串 = 不发送，保持空）。
	f.samplingTemp.SetText(s.SamplingTemperature)
	f.samplingTopP.SetText(s.SamplingTopP)
	f.samplingMax.SetText(s.SamplingMaxTokens)
	f.samplingStop.SetText(s.SamplingStop)
	return f
}

// splitRefList 逗号/分号/空白分隔的引用名列表（去空、去重且保序）。
func splitRefList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, name := range fields {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}
