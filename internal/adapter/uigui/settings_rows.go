package uigui

// 本文件只留尺寸常量、行种类与行模型（pageRows 的行计划）；帧骨架见 settings_frame.go，
// 行布局计算见 settings_row_layout.go，行体与控件见 settings_row_body.go。

import (
	"gioui.org/unit"
)

// fieldH 文本档高度下限（dp，经 gtx.Dp 消费）：单行编辑器 + 上下留白（material 无
// 内建输入框边框）。实际高由 editorBox 自适应（≥ 编辑器行高 + 留白，D113 验收补充）。
const fieldH = 40

// fieldLabelW 标签列宽（dp，经 gtx.Dp 消费）：对齐各行控件起点。
const fieldLabelW = 92

// rowKind 表单行类型（动态行计划：provider 编辑区行数随 providers 数量变化，D110②；
// S4d/D142 起按页分组——页序与页内容见 pageRows）。
type rowKind int

// rowInsetDp 每行上下左右的行腔留白（各页共用，D148）。
// 原值 14dp 让表单文字几乎贴住卡片描边，抬到 18dp 后行间与卡内都有了呼吸；
// 这是「设置页密度」的唯一旋钮，调它 = 同时调所有页，不要逐页微调。
const rowInsetDp = 18

// rowSectionTopDp 分区标题的**上**留白；下留白用 rowSectionGapDp。
//
// 间距要有亲疏（D209）：标签贴住它自己那一组。改前标题上下各 18dp、叠上卡片组界的
// 18dp 后正好浮在两组中间（上下各 36dp），读者分不清它属于上面还是下面那组——实测
// 在「模型」页「思考与推理」与上下两张卡之间完全对称。现在上留白 > 下留白：标题
// 视觉上归到下面那张卡，组与组之间的空档也明显大于卡内行距。
const (
	rowSectionTopDp = 30 // 标题与**上一组**之间（组间）
	rowSectionGapDp = 6  // 标题与**自己那一组**之间（组内）
)

// cardInnerDp 卡内四边留白（dp）：字段不贴着卡片描边，行距也由它承担。
const cardInnerDp = 14

const (
	rowTitle   rowKind = iota // 页标题（页名）
	rowSection                // 分组小标题（text = 分组名）
	rowProfCurrent
	rowProfName
	rowProfBtns
	rowWelcome // D141：欢迎 / 首次运行引导入口（两个菜单都不带，唯此一处 + 首跑自动）
	rowModelName
	rowPerm
	rowUnattended // D185：无人值守开关（权限区内，紧跟权限等级）
	rowThink
	rowEffort
	rowHotkey
	rowTheme
	rowScale
	rowFontSize
	rowWinSize
	rowNotify
	rowTTS
	rowBackupKeep // D254：会话树保留的备份代数（「常规」页存储区）
	rowMaxTurns
	rowMaxCtx
	rowCompact
	rowToolChars
	rowToolTo
	rowSysPrompt
	rowRaw
	// S11-7③/D176 口径二：「模型服务」页 provider 卡片。
	rowProvSummary // 折叠卡片收起态摘要行（点击展开/收起）
	rowProvName    // 名称（单行档）
	rowProvKind    // 协议（选择器：chat/responses/anthropic）
	rowProvBase    // 接口地址
	rowProvModels  // 「模型列表」块头（标题 + 「获取模型」按钮 + 拉取反馈；D181）
	rowProvTok
	rowProvUnsup
	rowProvHead // 展开区内小标题（text = 分区名，如「密钥池」）
	rowProvKey  // 密钥池单行（field = keyForms 下标）
	rowProvKeyAdd
	rowProvModel      // 模型列表单行（field = modelForms 下标）
	rowProvModelPager // 模型列表分页条（prov = 下标；仅一页装不下时出现）
	rowProvModelAdd
	rowProvBtns
	rowProvAdd
	rowProvApply
	// 模型页·降级链：一项一行，链窄于当前模型能力时顶部出警示行（D176）。
	rowChainNote // 链窄于当前模型能力时的警示（text = 文案）
	rowChainItem // 降级链一项（field = f.chain 下标）
	rowChainAdd  // 「＋ 添加降级项」
	// D204：采样参数（「模型」页），整链缺省 + 逐模型覆盖在「模型服务」页。
	rowSamplingTemp // temperature（单行档；空 = 不发送）
	rowSamplingTopP // top_p（单行档；空 = 不发送）
	rowSamplingMax  // max_tokens（单行档；空 = 不发送）
	rowSamplingStop // stop（JSON 数组文本，如 ["\n"]）
	rowSamplingNote // 说明（0 与留空的区别）
	// 工具与 MCP 页：内置工具逐条开关 + 已连 server 的状态行与动作按钮（D143）。
	rowBuiltin     // 内置工具行（name = 工具名；field = 快照下标）
	rowPlugin      // 插件行（name = 插件名；field = 快照下标）
	rowToolsEmpty  // 占位/空态（text = 文案）
	rowToolsStatus // 异步动作反馈状态行
	// 技能页：技能根目录、已装技能（行内更新/卸载）、安装来源输入档（D202/D203）。
	rowSkillItem   // 已安装技能一行（field = Skills 下标；行内更新/卸载）
	rowSkillEmpty  // 空态/未接线占位（text = 文案）
	rowSkillNote   // 说明（text 忽略；文案在 Body 内）
	rowSkillSource // 安装来源输入档（+「安装」按钮）
	rowSkillPick   // 「选择文件…」/「选择文件夹…」（原生选择框只填来源框）
	rowSkillDir    // 技能根一项（field = Dirs 下标）
	rowSkillDirAdd // 新增额外技能根（输入档 + 「添加」）
	rowSkillRescan // 「重新扫描」页级动作
	rowSkillWarn   // 未加载条目一项（field = Warnings 下标）
	rowSkillStatus // 异步动作反馈状态行
	// 角色页（§5.17）：已导入角色卡清单 + 导入入口（动作经 /character 命令通道）。
	rowCharItem   // 已导入角色卡一行（field = Items 下标；行内开始对话 / 删除）
	rowCharEmpty  // 空态/未接线占位（text = 文案）
	rowCharWarn   // 读目录失败提示（text = 文案）
	rowCharNote   // 说明（text 忽略；文案在 Body 内）
	rowCharSource // 卡文件来源输入档（+「导入」按钮）
	rowCharPick   // 「选择文件…」（原生选择框只填来源框）
	rowCharStatus // 动作反馈状态行
	// S7/D144：人格模块树（「系统提示词」页，S7b/D145 独立成页）。
	rowPromptNote    // 说明（text = 文案）
	rowPromptGrid    // 模块库三列网格（D148；整块占一行，模块数不影响行数）
	rowPromptRef     // 人格引用行（field = promptRefs 下标）
	rowPromptRefAdd  // 「＋ 添加引用」
	rowPromptBuiltin // 内置只读模块行（field = builtins 下标）
	rowPromptPreview // 组合预览（只读全文）
	// S9-6b/D161：密钥库页（名字 + 尾 4 位掩码；值永不回显）。
	rowSecretEntry  // 已存条目行（name = 键名；行内删除/二次确认）
	rowSecretEmpty  // 空态/未接线占位（text = 文案）
	rowSecretName   // 新增/覆盖的键名（单行编辑档）
	rowSecretValue  // 新增/覆盖的值（单行编辑档；动作后恒清空）
	rowSecretAdd    // 「存入」按钮行
	rowSecretStatus // 存入/删除反馈状态行
	// D179：语音页（识别 / 合成 / 音色与风格 / 试听）。D222 起每路另有端点与密钥。
	rowAudioASRProvider // 识别服务（选择器选 provider）
	rowAudioASRKind     // 识别方式（选择器：对话接口 / 本地 whisper / OpenAI / FunASR）
	rowAudioASREndpoint // 识别端点（空 = 用所选服务的）
	rowAudioASRKey      // 识别密钥（只写不回显）
	rowAudioASRModel    // 识别模型（单行档）
	rowAudioASRPath     // 本地识别模型文件路径（whisper 用）
	rowAudioLang        // 录音语种提示（选择器）
	rowAudioTTSProvider // 合成服务（选择器选 provider）
	rowAudioTTSKind     // 合成方式（选择器：对话接口 / VITS / GPT-SoVITS）
	rowAudioTTSEndpoint // 合成端点（空 = 用所选服务的）
	rowAudioTTSKey      // 合成密钥（只写不回显）
	rowAudioTTSModel    // 合成模型（单行档）
	rowAudioVoice       // 音色（预置名 / 克隆样本路径）
	rowAudioDesign      // 音色描述（音色设计模型）
	rowAudioStyle       // 风格指令
	rowAudioTags        // 风格标签
	rowAudioPreview     // 试听按钮 + 行内反馈
	rowAudioNote        // 说明（重启生效 / 各字段适用模型）
	// D200：联网页（搜索 / 抓取提供商 + 本地抓取参数）。
	rowWebSearchItem  // 搜索提供商一项（field = webSearch 下标）
	rowWebSearchAdd   // 「＋ 添加搜索提供商」
	rowWebSearchCount // 缺省条数
	rowWebFetchItem   // 抓取提供商一项（field = webFetch 下标）
	rowWebFetchAdd    // 「＋ 添加抓取提供商」
	rowWebFetchChars  // 单次回填正文上限
	rowWebProxy       // 本地抓取代理
	rowWebUA          // 本地抓取 UA
	rowWebRobots      // 遵守 robots.txt
	rowWebPrivate     // 允许私网/回环目标（调试）
	rowWebTimeout     // 本地抓取超时
	rowWebNote        // 说明（降级顺序 / 重启生效 / 密钥形态）
	// 关于页：版本、运行环境、项目主页与版权，只展示不写配置（D208）。
	rowAboutHero      // 图标 + 名字 + 一句话（不落卡）
	rowAboutVersion   // 构建版本
	rowAboutRuntime   // 运行环境（平台 · Go 版本）
	rowAboutProject   // 项目主页（链接全文 + 打开 / 复制链接）
	rowAboutCopyright // 版权
	rowAboutLicense   // 许可证
	rowAboutNote      // 说明（隐私口径 / 图标与产品名归属；不落卡）
	rowAboutStatus    // 打开 / 复制反馈（不落卡）
)

// provModelCandMax 候选 chip 的上限（D153；欢迎窗「获取模型」仍用它）：服务端可能列出
// 几百个模型，全部铺开会把引导页推成一面墙。设置窗的模型列表已改成「全量装进表 + 分页」，
// 不再受这个上限约束（D181）。
const (
	provModelCandMax = 12 // 欢迎窗单次「获取模型」铺成候选 chip 的上限
	relModelAutoFill = 8  // 欢迎窗自动填入模型名行的上限
)

// provModelPageSize 设置窗「模型列表」每页行数（D181）：服务端清单可能有几百个，
// 一屏全铺既找不到东西也拖慢排版——按页切，页内仍然只有可见的那些行在排版。
const provModelPageSize = 8

// rowDesc 一行的类型与定位。
type rowDesc struct {
	kind  rowKind
	text  string // rowSection / rowTitle / rowProvHead 的文案
	prov  int    // provider 行：provider 下标
	field int    // rowProvKey/rowProvModel：密钥/模型行下标
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
