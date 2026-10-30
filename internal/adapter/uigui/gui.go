// Package uigui Gio 悬浮球 GUI 前端（D43，壳内规格见 docs/gui-design.md §15，M5）：与 repl/tui 同权实现
// uiFrontend 六面（port.Presenter + Prompter + Confirmer + Say/Prompt/SetInterrupt/
// Close），装配根按 ui.kind=gui 换壳——薄壳零业务逻辑，D28 输出器装饰器自动继承。
//
// 并发模型（§15.5，照抄 uitui 修复后的口径，不重踩竞态）：
//   - 渲染状态机 model 由事件循环 goroutine 独占；Gio 窗口模式在 Window.Event
//     泵内排空 inbox（runWindow），headless（无窗口测试）经 runHeadless 排空，
//     两者共用 apply。
//   - Emit/Say/Confirm 从装配根 goroutine 投递 inbox，随后 Window.Invalidate
//     唤醒帧循环（Gio 文档：Invalidate is safe for concurrent use）。
//   - Next/Confirm 对调用方呈阻塞语义（channel 桥接）；EOF 与排队输入的优先级
//     结构与 uitui 一致：先取尽排队输入再判 EOF。
//   - 修改性原生调用（Win32 等）一律经窗口线程投递（§15.6 铁律 1；投递槽在平台实现内）。
//
// 平台边界（D111/S4b）：平台能力全部收在 `internal/adapter/uigui/platform`（纯 Go、不依赖
// gio；Windows 实测实现 + 非 Windows 降级实现），本包只经消费方接口 `u.plat` 触达，向平台
// 回传只经 `platform.Host` 的动作枚举。从 Gio 事件里提取原生句柄的一步留在本包
// （view_windows.go / view_other.go），平台自身不 import gio。窗口循环不调 app.Main——
// Windows 的 osMain 仅 select{}（Gio 自建窗口线程），库内调用会卡死装配根。
package uigui

// 本文件只留类型与壳的骨架（Status/Options/UI 与缩放、窗口尺寸的旋钮）；
// port 六面的实现见 gui_port.go，消息与分派见 gui_msg.go，无窗口循环见 gui_headless.go。

import (
	"context"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/gesture"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// inputCap 输入缓冲容量：打字/外部输入快于装配根消费时暂存（同 uitui 口径）。
const inputCap = 256

// inboxCap 事件收件箱容量：流式 delta 高频投递，消费侧每事件全量排空；
// 满时 Emit 呈背压阻塞（事件循环恒在推进，不会死锁）。
const inboxCap = 1024

var (
	_ port.Presenter = (*UI)(nil)
	_ port.Prompter  = (*UI)(nil)
	_ port.Confirmer = (*UI)(nil)
	_ port.Asker     = (*UI)(nil)
)

// Status 状态行数据（浮层底栏生成时经回调现取）。自 D82 起与 uitui.Status 形状分叉：
// GUI 额外携带 logo 事实卡（§15.1/S1-1g）要的 profile 与会话事实快照（port.SessionFacts，
// app 侧原子发布零值安全）——tooltip 按 roadmap 只做 GUI（同 Q15 口径），TUI 不扩展。
type Status struct {
	Model      string
	Level      string
	Effort     string            // D34：推理档位（think off 时为空——effort 不显示）
	Unattended bool              // D185：无人值守开（状态行常驻标记；回来才明白为何一串「已拒绝」）
	Profile    string            // D82：当前 profile 名（D110 起装配根传真值）
	Facts      port.SessionFacts // D82：会话展示事实（零值 = 未就绪，事实卡回退启动提示）
}

// Options 装配选项（装配根注入）。
type Options struct {
	// Status 状态栏数据源；nil = 底栏只显示"思考中/生成中"。
	Status func() Status
	// PosFile 窗口位置记忆文件（§15.1）；空 = 不记忆。
	PosFile string
	// Interrupt 初始中断行为（装配根一般经 SetInterrupt 每轮注入，留 nil 即可）。
	Interrupt func()
	// Hotkey GUI 全局呼出快捷键（§15.1，如 "Alt+A"）；空 = 默认 Alt+A。
	Hotkey string
	// Theme 主题档（§15.4/D61）：system | light | dark；空 = system（跟随系统）。
	Theme string
	// Scale 元素缩放倍率（D90/§15.8）：默认 1.0，夹 [0.75,2.5]，越界回落 1.0；设置窗热更。
	Scale float64
	// FontSize 正文字号 sp（D90/§15.8）：默认 15，夹 [10,28]；最终字号 = FontSize × Scale。
	FontSize float64
	// WindowWidth/WindowHeight 主窗像素尺寸（D90/§15.8）：0 = 缺省 app.Size(unit.Dp(608/460))
	// 现行为；配置后按字面 px 建窗（挂接点 SetWindowPos 落地）。热改走 SetWindowSize，
	// 不回写本字段——config 是事实源。
	WindowWidth  int
	WindowHeight int
	// Settings 设置窗核心档快照数据源（开窗现取；同 Status 线程安全口径）；
	// nil = 设置窗空表单。
	Settings func() SettingsSnapshot
	// ApplySettings 设置窗单一写回调（§15.7 装配根实现）：持久化核心档文本键并
	// 返回待执行内核命令（diff 运行态；设置窗经 inCh 与键入同路径串行执行）；
	// nil = 设置窗只读占位。
	ApplySettings func(SettingsPatch) ([]port.Command, error)
	// Tree 会话树只读视图（D80/§7.5，前置 A）：分叉条（D81）每帧无锁读快照，取
	// 「同级集合 + 自身下标」；nil = 不渲染分叉条。实现方须线程安全（UI 事件循环 goroutine 调用）。
	Tree port.TreeView
	// Commands 命令清单只读数据面（D103/S2b-1）：补全浮层数据源，与 /help 同源
	// （port.CommandCatalog 反向端口，实现方 app.Session）；nil = 无补全。
	// 实现方须线程安全（UI 事件循环 goroutine 调用）。
	Commands port.CommandCatalog
	// Lister 会话列表只读数据面（D120⑥，S3 左栏）：会话历史窗左栏数据源
	// （port.SessionLister 反向端口，实现方 app.Session）；nil = 左栏空列表。
	// 实现方须线程安全（历史窗 goroutine 调用——快照读，无锁）。
	Lister port.SessionLister
	// Indexer 归档清单只读面（D249）：导入一个多会话归档时先取清单，弹勾选面板让用户
	// 决定导哪几个。nil = 不弹面板，选完文件直接投 /import（单会话口径）。
	// 只有适配器解容器（铁律 5）：界面拿的是「序号 + 标题 + 条数」，不碰 ZIP。
	Indexer port.ExchangeIndexer
	// Profiles profile 管理面（D110③/修订④）：设置窗 profile 区数据面与三动作
	//（新建/复制/切换，重启生效）；nil = 不渲染 profile 区。
	Profiles ProfilesManager
	// ProviderMgr provider 列表管理面（D110②/③）：设置窗 provider 编辑区数据面 +
	// 全量应用 + 连通性测试（重启生效）；nil = 不渲染编辑区。
	ProviderMgr ProviderManager
	// Welcome 欢迎 / 首次运行引导数据面（D141/§15.7）：winWelcome 次窗（设置窗入口 +
	// 首跑/标记缺失时自动开一次）；nil = 窗内为只读占位提示。
	Welcome WelcomeManager
	// Tools 工具与 MCP 管理面（S6/D143）：设置窗「工具与 MCP」页数据面 + 异步动作
	// （内置启停热生效 / 插件启停 / 授权撤销与预授权 / 信任撤销 / 日志）；nil = 该页只读占位。
	Tools ToolsAdmin
	// Secrets 密钥库管理面（S9-6b/D161）：设置窗「密钥库」页数据面 + 同步动作
	// （列出 / 新增覆盖 / 删除任意名字；本地小文件 IO，不走工具页那套异步通道）；
	// nil = 该页只读占位。
	Secrets SecretsAdmin
	// Skills 技能管理面（D202/D203）：设置窗「技能」页数据面 + 异步动作（安装/更新/
	// 卸载/重扫/技能目录增删）；nil = 该页只读占位。
	Skills SkillsAdmin
	// Characters 角色卡清单只读快照（§5.17）：设置窗「角色」页数据面。导入 / 删除 /
	// 开始对话不在此面——本页动作一律经 `/character` 命令通道（回执进主窗转写区）。
	// 逐帧问：装配根实现可自行节流读盘；nil = 该页只读占位。
	Characters func() CharactersSnapshot
	// PreviewPrompts 人格组合预览（S7/D144，装配根实现）：按设置窗当前（未保存）模块树 +
	// 引用展开最终 system 文本（纯计算，与新建会话同口径；不落盘、不阻断）。nil = 无预览行。
	PreviewPrompts func(nodes []PromptModuleView, persona []string, legacy string) string
	// ApplyPromptTemplate 人格模板写回（D154，装配根实现）：只改 config 的 system_prompt +
	// prompts 两键——历史窗遮罩「默认模板」页签保存走这条路，避免全量设置 patch 盖掉同时
	// 开着的设置窗刚改的其它字段。nil = 模板页签只读。
	ApplyPromptTemplate func(p PromptTemplatePatch) error
	// Voice 语音输入（D179）：录一段 → 转写 → 返回文本（不提交，由界面回填输入框）；
	// 阻塞调用，UI 在后台 goroutine 跑、经 inbox 回投。nil = 语音不可用（右钮保持发送）。
	Voice func(ctx context.Context) (string, error)
	// PreviewVoice 设置窗「语音」页试听（D179）：用表单草稿的音色/风格合成一句并播放。
	// 阻塞调用，设置窗在后台 goroutine 跑。nil = 试听不可用。
	PreviewVoice func(req port.SpeechRequest) error
	// Blobs 附件库（D187）：Ctrl+V 贴图把 PNG 字节直接入库，暂存为已入库附件项
	// （提交时不再回读文件系统）。nil = 粘贴图片不可用，其余附件入口照常。
	Blobs port.AttachmentStore
	// Version 构建版本号（装配根注入 `-ldflags -X main.version` 的那个值）：只给设置窗
	// 「关于」页展示，空 或 "dev" = 开发构建。不参与任何行为分支。
	Version string
}

// PromptTemplatePatch 人格模板的窄写回（D154；与 SettingsPatch 分开：全量 patch 带
// 主题/限额等无关字段，跨窗同时编辑会互相覆盖）。
type PromptTemplatePatch struct {
	SystemPrompt string             // 默认人格（= 内置 @default 正文；空 = 内置默认）
	Modules      []PromptModuleView // 模块树（空名行已跳过）
	Persona      []string           // 有序引用（拼接顺序）
}

// zoomKnobs 双缩放旋钮（D90/§15.8）：元素缩放 × 正文字号，原子槽整体换存（成对生效）。
type zoomKnobs struct {
	scale  float64
	fontSp float64
}

// 旋钮边界与缺省（D90）：字号阶梯以正文 15sp 为设计基准（§15.2 的 Sp 字面量按此解释）。
// 字号的上下界取 port.SettingsFontSizeMin/Max——设置窗表单校验用的是同一对数（见 D230），
// 两处各写一份就会漂移成「填得进去、显示不出来」。
const (
	zoomScaleMin = port.SettingsScaleMin
	zoomScaleMax = port.SettingsScaleMax
	fontSpMin    = port.SettingsFontSizeMin
	fontSpMax    = port.SettingsFontSizeMax
	fontSpBase   = 15.0
)

// clampScale 元素缩放夹取：越界/NaN 回落 1.0（NaN 与任何区间比较皆 false，须用否定式）。
func clampScale(v float64) float64 {
	if !(v >= zoomScaleMin && v <= zoomScaleMax) {
		return 1.0
	}
	return v
}

// clampFontSp 正文字号夹取：越界/NaN 回落 15。
func clampFontSp(v float64) float64 {
	if !(v >= fontSpMin && v <= fontSpMax) {
		return fontSpBase
	}
	return v
}

// clampKnobs 成对夹取（newUI 预存与 SetZoom 共用口径）。
func clampKnobs(scale, fontSp float64) zoomKnobs {
	return zoomKnobs{scale: clampScale(scale), fontSp: clampFontSp(fontSp)}
}

// UI GUI 前端句柄（装配根按 uiFrontend 使用）。
type UI struct {
	opts Options
	// plat 平台实现（D111/S4b）：所有平台能力经它触达——Windows 实测实现 / 非 Windows
	// 降级实现；构造后只读（测试可在 newUI 后替换为假实现）。headless（window=false）
	// 同样持有一个真实现（未起外壳线程，方法多为 no-op/中性回落）。
	plat platformAPI
	// hotkeyCfg 快捷键配置原子槽（设置窗保存热更新；托盘线程注册读，newUI 预存
	// opts.Hotkey——u.opts 本身只读不改，防跨线程裸写）。
	hotkeyCfg atomic.Value

	// zoom 双缩放旋钮原子槽（D90/§15.8，同 hotkeyCfg 口径）：设置窗 SetZoom 热更，
	// 帧循环每帧读（zoomedMetric 咽喉点）；Load 失败（未预存）= 缺省 1.0/15。
	zoom atomic.Value

	// 桥接通道（装配根 goroutine ↔ 事件循环 goroutine）。
	inbox   chan uiMsg
	inCh    chan port.UserInput
	wakeCh  chan struct{} // 空闲唤醒（D167）：容量 1，多次后台完成合并成一次
	eofCh   chan struct{} // 关闭 = 输入流结束（广播：Next/Confirm 同时唤醒，天然粘滞）
	eofOnce sync.Once
	eofErr  error         // 写于 close 之前；close 提供 happens-before，读者安全
	done    chan struct{} // 事件循环退出（close）

	interrupt  atomic.Pointer[func()]
	generating atomic.Bool // 生成中（SetInterrupt 非空 = 一轮 Turn 进行中；驱动停止键）
	// pasting 粘贴图片进行中（D187）：一次只跑一张（连按不排队），也用于阻止重入。
	pasting atomic.Bool

	// w Gio 窗口句柄（构造后只读；headless = nil）。
	w *app.Window

	// 窗口侧状态（仅帧循环 goroutine 读写；headless 不触碰，构造成零值可用）。
	th        *material.Theme
	editor    widget.Editor
	reasonEd  widget.Editor // D86：确认态原因编辑器（拒绝原因，可留空）
	logoDrag  gesture.Drag  // logo 圆钮：拖动移窗（§15.1 把手含 logo）
	logoRight logoRight     // D72 logo 右键菜单手势（武装-抬手；仅事件循环 goroutine 读写）
	tipShown  bool          // 当帧有 tips 在显（心跳判据 D53；仅事件循环 goroutine 读写）
	sendBtn   widget.Clickable
	stopBtn   widget.Clickable
	// 语音输入（D179）：空输入时右钮 = 录音键；录音中变停止键。recording 供帧循环
	// 读（点击路径与完成回投都在事件循环 goroutine 改）。
	recording    atomic.Bool
	voiceMu      sync.Mutex
	voiceCancel  context.CancelFunc
	allowBtn     widget.Clickable
	denyBtn      widget.Clickable
	elevateBtn   widget.Clickable    // D86：提升权限钮（仅工具确认且有下一档时布局）
	attachBtn    widget.Clickable    // D104：附件槽（文件选择框入口，D49 灰槽实装）
	attachClear  widget.Clickable    // D186：收起态汇总 chip（点击清空全部暂存）
	attachClicks []*widget.Clickable // D186：展开态逐项 chip（按暂存序号缓存，点删该项）
	// editAttClicks D188：编辑态已有附件的 chip（按节点内附件序号缓存，点击 = 标记删除/恢复）。
	editAttClicks map[int]*widget.Clickable
	expandBtn     widget.Clickable // D106：展开槽（输入框放大切换，D49 灰槽实装）
	expanded      bool             // D106：展开态（胶囊原地增高、编辑器多行；会话级不落盘）
	// 提问面板（D184，§15.2）：面板长在展开态胶囊里，字段只在事件循环 goroutine 读写。
	askEd        widget.Editor                     // 「自己输入」编辑器
	askList      layout.List                       // 中部滚动区（题干 + 选项 + 自由输入行）
	askOptClicks [askMaxOptions]widget.Clickable   // 选项行（每题按下标复用）
	askTabClicks [askMaxQuestions]widget.Clickable // 顶部进度段（点 = 跳题）
	askFreeClick widget.Clickable                  // 「自己输入」行（不占选项下标：9 项时不能撞车）
	askPrev      widget.Clickable                  // 上一步
	askNextBtn   widget.Clickable                  // 下一步 / 提交
	askIgnore    widget.Clickable                  // 忽略（放弃全部）
	askTag       struct{}                          // 面板键盘焦点标签
	askFocus     bool                              // 下一帧把焦点让给面板
	askFreeFocus bool                              // 下一帧把焦点让给「自己输入」
	askCaret     bool                              // 「自己输入」编辑器持焦点（fade pass 自绘 caret）
	// askOpen 提问面板是否打开（原子快照：事件循环写、外部只读）。m.ask 本身仅在事件循环
	// goroutine 读写，跨 goroutine 只经这个开关观察。
	askOpen atomic.Bool
	drag    gesture.Drag
	// D103/D250 补全浮层（仅事件循环 goroutine 读写）：命令相/参数相每帧重算（命令清单
	// 端口每帧拉取——动态命令随插件启停变化）；Tab 轮换期沿用 cycle 锁定的清单；
	// complRows 行矩形随帧登记（窗口系，提示行不登记）。
	complText      string          // 上一帧编辑框文本（变更 → 重置 dismissed/高亮/轮换）
	complSel       int             // 键盘高亮行下标（complList 内）
	complDismissed bool            // Esc 后搁置（文本变更即重开）
	complOpenNow   bool            // 本帧浮层开启
	complEsc       bool            // 本帧 Esc 已被浮层消费（updateEditor 编辑态让渡）
	complArmed     bool            // 行点击武装态（D72 口径）
	complPressPt   image.Point     // 武装按下点（窗口系）
	complList      []complItem     // 当前候选（drawCompl 呈现）
	complRows      []complRowRect  // 可填行的命中矩形（窗口系，随帧重登记）
	complTag       struct{}        // 行点击手势 tag
	cycle          complCycle      // Tab 轮换状态（readOnly 之外的唯一可变处）
	ihist          ihist           // 输入历史（↑↓ 只在收起态单行编辑时翻）
	hwnd           platform.Handle // 主窗句柄（非 Windows 恒 0）
	// revealPending 启动防闪（D78）：挂接即隐藏、首帧 ULW 提交成功才揭示（激活前台）。
	// 事件循环写（onHWND/fadePresent）、托盘线程读（showMain 呼出门）→ atomic。
	revealPending atomic.Bool
	x, y          int32 // 窗口屏幕坐标（拖动跟随 + 位置记忆）
	dragging      bool
	dragWin0      point // 按下时窗口左上角（屏幕坐标，绝对跟踪修回弹，§15.6 铁律 2）
	dragCur0      point // 按下时光标位置（屏幕坐标）

	// D55 帧内屏幕态记账：帧中一律只置 pending，由 commitWinGeom 一拍 flush（移窗经
	// Window.Run）。D62：形裁/alpha 通道退役（随 ULW 位图同拍提交），仅剩移窗。
	// 仅帧循环 goroutine 读写；启动路径（onHWND/restoreDock）不在帧内，直接调 Win32。
	movePending bool // u.x/u.y 已变、SetWindowPos 未发
	// framePhase 帧阶段回执（**仅测试注入**，生产恒 nil）：断言 D55–D62 次序契约——
	// compose → commit → submit → present（全帧合成先行、移窗 flush 在提交前、ULW 殿后）。
	framePhase func(phase string)
	// logoMenuHook 菜单请求回执（**仅测试注入**，生产恒 nil → requestLogoMenu 投
	// 托盘线程呈现原生菜单，D72）。
	logoMenuHook func()

	// D50 停靠（§15.1 停靠隐藏）：alpha/docked/dockArm/dockAn 仅事件循环 goroutine
	// 读写；dockHint = 「docked 且停靠边」的原子镜像（0=未停靠 1=left 2=right），
	// 供托盘线程（置顶开关）跨线程读取保存。
	alpha    byte   // 当前整窗不透明度（D62：ULW SourceConstantAlpha；semiAlpha ↔ dockAlpha 动画插值）
	docked   bool   // 停靠态（滑出中/已停：球只剩窄条 + 淡化）
	dockArm  bool   // 上一帧「停在可停靠边且光标在球上」（曾悬停 = 停靠布防）
	edgeNow  string // 当帧可停靠边（evalDockFrame 写入，"" = 不可停靠；心跳判据）
	dockHint atomic.Int32
	dockAn   dockAnim      // 停靠/召回动画（进度帧分支现算，见 stepAnim）
	armTick  chan struct{} // 收起/停靠心跳（nil = 未运行；关停 = close，见 armHeartbeat）

	// 转写区手工滚动（D44：不用 widget.List——需要每行绝对矩形登记形状）。
	transcriptScroll gesture.Scroll
	scrollPx         int  // 内容滚动偏移（物理 px，0 = 顶）
	followTail       bool // 尾随贴底（新内容贴输入栏；上滚即停，§15.3）
	contentH         int  // 内容总高（上一帧测得，物理 px）
	transH           int  // 转写视口高（transcript 布局期写；updateSel 消费者阶段读——一帧陈旧，D102 判缘用）

	// D102 拖选贴边自动滚动：selTick = 唤帧心跳（armHeartbeat 同模式，nil = 未运行）；
	// selScrollAcc = 分数步长累加器（防整除吞步）。仅事件循环 goroutine 读写。
	selTick      chan struct{}
	selScrollAcc float64

	// D71 滚轮手势钉点：转写区滚轮真有增量 → 手势进行中（fadePresent 把光标所在
	// 帧像素 alpha 顶到 ≥1 维持分层窗命中，光标停在透明间隙滚轮不流失到下层窗）；
	// 收起/停靠/光标移位即解除恢复逐像素穿透。仅事件循环 goroutine 读写。
	wheelCap    bool
	wheelAnchor point // 手势锚点（光标屏幕坐标；位置变化 = 手势结束）

	// 行选择（D63）：selRows 按行序缓存 Selectable（get-or-create；行文本变化经
	// SetText 幂等更新并自动清选区）。拖层把手带不覆盖转写区（window.go layout），
	// 行选手势独占气泡区指针。仅事件循环 goroutine 读写。
	selRows []*widget.Selectable

	// D91 跨块拖选观察者：sel = 状态机（候选/激活/锚/焦点/指纹）；keyRects = 上帧
	// 行键几何（量期记录、paint 期译窗口系，消费者阶段读——一帧陈旧是既定口径）；
	// keyFp = 结构指纹（fnv64 流式喂自 writeKeyRects，变化即清选：流式/开合/重排）；
	// selSpansBuf = 消费者阶段预算的逐键跨度（测量闭包绘制期读取）；
	// selScratch = Regions 每键转录暂存（rowSel.paint）。仅事件循环 goroutine 读写。
	sel         selState
	keyRects    []selGeom
	keyFp       uint64
	selSpansBuf []selSpan
	selScratch  []widget.Region

	// 气泡右键（D92）：bubbleRects = paint 期逐行登记的气泡底板矩形（随帧复位，
	// 消费者阶段读上一帧——与 keyRects 同口径的一帧陈旧）；bubbleRight = 武装-原位
	// 抬手手势态（D72 logoRight 同款）；bubbleMenu = 命中后组好的菜单上下文原子槽
	// （过线程到 shell 呈现）；bubbleMenuHook = 测试注入口（headless 不投 shell）。
	// 仅事件循环 goroutine 读写（bubbleMenu 原子槽除外）。
	bubbleRects    []bubbleHit
	bubbleRight    bubbleRight
	bubbleMenu     atomic.Pointer[bubbleMenuCtx]
	bubbleMenuHook func(*bubbleMenuCtx)
	// pendingCopy 挂起的剪贴板写入（D92）：copyMsg 记账，frame 内 flushCopy 经
	// gtx.Execute 落盘后清空。仅事件循环 goroutine 读写。
	pendingCopy string
	// rawView 查看原文窗内容原子槽（D99）：菜单分发处（托盘线程）Store，winRaw 次窗
	// 帧内 Load——跨线程只经原子快照，不共享可变态（§15.7 并发模型）。
	rawView atomic.Pointer[rawContent]
	// imageView 图片预览窗内容原子槽（D207）：主窗事件循环（点击格子的那一刻）Store，
	// winImage 次窗帧内 Load——同 rawView 口径，跨线程只经原子快照；窗单实例，
	// 已开 = 聚焦 + 原地换图。
	imageView atomic.Pointer[imageContent]
	// nodeAction 图侧菜单动作槽（D132）：shell 线程菜单分发 Store，历史窗帧首 Swap
	// 排空——复制 ID 要在 Gio 帧内落剪贴板、开遮罩/开小卡要碰图状态与帧内文本，都不能在分发处直连。
	nodeAction atomic.Pointer[nodeAction]

	// 分叉条点击件（D81）：按分叉条序缓存左右两个 Clickable（get-or-create）；分叉条
	// 消费 0 个行选键（selCount），故其增删不漂移其它行的选择键。仅事件循环 goroutine 读写。
	branchPrev []*widget.Clickable
	branchNext []*widget.Clickable

	// markdown 解析缓存（D65/D66）：助手定稿块原文 → 结构块（mdBlocks 维护，上限
	// mdCacheLimit）。仅事件循环 goroutine 读写。
	mdCache map[string][]mdBlock

	// 工具 chip 视图态（D67）：折叠开合 + 头部点击控件，按块序缓存（块只增不减 →
	// 序稳定；视图态不进 model，会话重建后复位折叠）。仅事件循环 goroutine 读写。
	chipOpen   map[int]bool
	chipClicks []*widget.Clickable

	// 转写区图片缩略图（D207）：imgLookup = 内容寻址 → 已解好的方图（nil 值 =
	// 解码失败的墓碑，不再重试）；imgLoading = 在途集合（同一张图只起一个 goroutine）；
	// imgOrder = 插入序（超上限从头淘汰）；imgClicks = 格子点击件（按「块序 + 图序」
	// 缓存，照 chipClick 口径）。字节来源走 opts.Blobs（已入库）或本地路径。
	// 仅事件循环 goroutine 读写。
	imgLookup  map[string]*thumbSrc
	imgLoading map[string]bool
	imgOrder   []string
	imgClicks  map[imgClickKey]*widget.Clickable

	// 思考块视图态（D182）：定稿思考默认折叠成一行，用户点开才铺正文。与 chip 同口径
	// ——按块序缓存、不进 model、清屏（会话重建）时复位。仅事件循环 goroutine 读写。
	thinkOpen   map[int]bool
	thinkClicks []*widget.Clickable

	// focusPending 唤出后把输入焦点交给编辑器（托盘/快捷键显示窗口后投 focusMsg，
	// 下帧 layout 执行 key.FocusCmd；仅事件循环 goroutine 读写）。
	reasonFocus  bool // D86：确认开启后把焦点让入原因框（一次性，同 focusPending 口径）
	focusPending bool
	// caretFocused 编辑器焦点态（真窗 pass 捕获，fade pass 读——D62 caret 自绘：
	// material.Editor 的 caret 由 gtx.Focused 门控，零值 Source 渲染不画 caret）。
	caretFocused bool
	// edSelRegions 编辑器选区行盒复用缓冲（D133 淡出源自绘选区，Regions 逐帧复用，
	// 免 per-frame 分配；仅帧循环 goroutine 读写）。
	edSelRegions []widget.Region
	// representLadder 揭示后补提交梯次的拍距（D115；D135 起为 UI 字段而非包级 var——
	// 包级 var 只能靠测试改包状态来禁用，等于把该路径在测试里整体屏蔽、-race 永远抓不到
	// 跨 goroutine 读帧循环字段的问题）。缺省 defaultRepresentLadder；测试置 nil 不启
	// goroutine。仅揭示路径读。
	representLadder []time.Duration

	// collapsed 收起态（§15.1 单组件：左键 logo 收起回球，仅渲染悬浮球；再单击球
	// 展开）。仅事件循环 goroutine 读写。
	collapsed bool

	// expandAn 展开/收起动画（D54）：collapsed 是逻辑态、即时翻转；渲染几何由
	// expandAn.barP/msgP 插值，静止态由 collapsed 推导（expandProgress）。
	expandAn expandAnim
	// pillFade/pillAlpha 胶囊内容显隐（D77）：独立 alpha 时间线 + 当帧定帧值
	//（stepExpand 在 layout 前算好，两遍 layout 同帧同值；静止由 collapsed 推导）。
	pillFade  pillFade
	pillAlpha float64

	// 形状与全帧合成：本帧可见元素的矩形清单，连同当帧 Metric 与窗口尺寸一起供离屏渲染
	// 与命中测试复用（D44/D62，坐标口径见 docs/gui-design.md §15.1、§15.3）。
	shapes      []drawShape // 本帧可见元素矩形（窗口系、物理 px；layout 坐标即物理）
	frameMetric unit.Metric // 当前帧 Metric（headless 同源渲染用）
	frameSize   image.Point // 当前帧窗口尺寸（物理 px）
	// bandTop/bandBottom 当前帧淡出带范围 [top, bottom)（D54 消息揭示带）：静息 = 顶带
	// [0, bandPx)；动画中带顶随 msgP 从转写区底升到 0。layout 每遍写入，两遍同帧同值。
	bandTop, bandBottom int
	// bandLowTop/bandLowEnd 底部矮带范围 [lowTop, lowEnd)（D79）：展开路径写转写区
	// 底缘 [transH−Dp(fadeBandBottomDp), transH)；收起态写 size.Y,size.Y（区间空 = 关）。
	bandLowTop, bandLowEnd int
	fade                   fadeState // headless 离屏渲染状态（全帧像素源，D62）
	fadeBuf                []byte    // 整窗预乘 BGRA 缓冲（ULW 位图）
	// fadeEmpty 本帧位图全透明（无登记元素）：fadeCompose 写（空帧仍提交 ULW 以清除
	// 上一帧像素——D62 后无独立 overlay 可隐藏）。
	fadeEmpty bool
	// inFadePass 淡出源渲染标记：跳过兜底窗口底色——位图像素只含可见元素
	//（气泡/输入栏），背景保持 headless 清屏的透明 → 间隙穿透（D44/D62）。
	inFadePass bool

	// m 渲染状态机：仅事件循环 goroutine 读写；测试经 drainSync 取 happens-before 后读。
	m *model

	// confirmShare 进行中的确认在窗间的**只读投影**（D157）：主窗事件循环在 startConfirm
	// 发布、replyConfirm 收尾时清空；历史窗每帧读它并在窗内画同步确认弹窗——跨窗只经原子
	// 快照 + Invalidate（§15.7），两窗显示的是**同一次**确认（内核侧只有一条应答通道）。
	confirmShare atomic.Pointer[confirmShare]

	// wins 功能窗注册表（§15.7/D60）：单实例防重开 + 退出收编。互斥锁保护，
	// 托盘线程/事件循环均可开窗；headless 构造成无开窗器（openWin no-op）。
	wins *winHost

	// skillPicks 技能导入选择框结果通道（平台线程 → 设置窗 goroutine，D203）：容量小，
	// 满即丢（结果只是「填进来源框」，丢了重选一次即可）；读出后 invalidate 设置窗重绘。
	skillPicks chan skillPathPick
	// charPicks 角色卡选择框结果通道（平台线程 → 设置窗 goroutine，§5.17）：口径同 skillPicks。
	charPicks chan charPathPick
	// filePicks 会话进出的文件框结果通道（平台线程 → 历史窗 goroutine）：满即丢；
	// 读出后由帧首投一条内核命令（壳内不做搬运）。
	filePicks chan filePick

	// pal 主题原子快照（spawn 次窗时读取——applyTheme 在 goroutine 启动前写初始值，
	// 运行时仅事件循环 goroutine 写；§15.7 跨窗只经原子快照）。
	pal atomic.Pointer[palette]
	// themeMode 当前主题档归一值（system|light|dark）：初始写于 goroutine 启动前、
	// 运行时仅事件循环 goroutine 读写（sysThemeMsg 判定 system 档才重解析）。
	themeMode string
}

// New 启动 GUI 前端（Gio 窗口事件循环即刻在后台运行，退出经 Close 收尾）。
func New(opts Options) *UI {
	return newUI(opts, true)
}

// newUI 构造；window=false = headless（§15.5 无窗口逻辑测试：桥接层照常运转，
// 只是没有帧循环与窗口侧控件）。
func newUI(opts Options, window bool) *UI {
	u := &UI{
		opts:   opts,
		inbox:  make(chan uiMsg, inboxCap),
		inCh:   make(chan port.UserInput, inputCap),
		wakeCh: make(chan struct{}, 1),
		eofCh:  make(chan struct{}),
		done:   make(chan struct{}),
	}
	u.m = newModel(u)
	u.plat = newPlatform(opts, window)         // 平台实现（profile/主题解析前就绪）
	u.representLadder = defaultRepresentLadder // 揭示补提交梯次拍距（D115/D135）
	u.editor.Submit = true                     // Enter → SubmitEvent（Shift+Enter 仍换行，§15.2）
	u.editor.SingleLine = true
	u.followTail = true   // 初始尾随贴底（新内容贴输入栏，§15.1）
	u.alpha = semiAlpha   // 整窗不透明度起点（D62：随首帧 ULW 生效；D50 动画在其上插值）
	u.focusPending = true // 初始焦点入输入栏（D63：编辑器不再每帧回投常驻焦点）
	u.wins = newWinHost(nil)
	u.skillPicks = make(chan skillPathPick, 4)
	u.charPicks = make(chan charPathPick, 4)
	u.filePicks = make(chan filePick, 4)
	u.applyTheme(opts.Theme)                            // 主题初始应用（goroutine 启动前，无并发；§15.4/D61）
	u.hotkeyCfg.Store(opts.Hotkey)                      // 快捷键槽预存（托盘线程 hotkeySetting 读）
	u.zoom.Store(clampKnobs(opts.Scale, opts.FontSize)) // 缩放槽预存（D90，夹取回落）
	if f := opts.Interrupt; f != nil {
		u.SetInterrupt(f)
	}
	if window {
		w := new(app.Window)
		u.w = w                         // go 前发布：post 侧读取无竞争
		u.wins.spawn = u.spawnSecondary // 功能窗开窗器（§15.7；仅窗口模式）
		go u.runWindow(w)
		u.startShell() // 托盘 + 全局快捷键线程（§15.1；仅窗口模式，headless 不起）
	} else {
		go u.runHeadless()
	}
	return u
}

// zoomLoad 当前旋钮（原子读；未预存 = 缺省 1.0/15——零值 UI 直接可用，headless 测试口径）。
func (u *UI) zoomLoad() zoomKnobs {
	if v, ok := u.zoom.Load().(zoomKnobs); ok {
		return v
	}
	return zoomKnobs{scale: 1.0, fontSp: fontSpBase}
}

// SetZoom 热更双旋钮（D90，设置窗调；任意 goroutine）：原子换存，下一帧 Metric 咽喉点
// 生效——元素几何与字号同步重排；fadeState 按 PxPerDp/PxPerSp 变化自动重建离屏窗。
func (u *UI) SetZoom(scale, fontSp float64) {
	u.zoom.Store(clampKnobs(scale, fontSp))
}

// SetWindowSize 热改主窗像素尺寸（D90，设置窗调；任意 goroutine）：按实测 DPI × 当前
// 缩放夹取后经窗口线程 SetWindowPos（铁律 1），Gio 收 WM_SIZE 下帧重排。停靠位与窗宽
// 无关（球锚左定，dockSlidePos 口径），无需重锚；0/负值忽略。
func (u *UI) SetWindowSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	w, h = clampWindowPx(w, h, u.plat.MainDPI(), u.zoomLoad().scale)
	u.resizeWindowTo(int32(w), int32(h))
}

// OpenWelcome 打开欢迎 / 首次运行引导窗（D141，任意 goroutine）：设置窗入口与
// 「首跑 / ui.welcomed 缺失」的自动打开共用。单实例防重开由注册表保证；headless /
// 无开窗器（spawn == nil）= no-op（§15.5：GUI 不进 CI 图形路径）。
func (u *UI) OpenWelcome() {
	u.wins.openWin(winWelcome)
}

// zoomedMetric 缩放 Metric（D90 咽喉点，§15.8）：PxPerDp ×= scale（元素几何等比）、
// PxPerSp ×= scale × font/15（Sp 字面量按「正文 15 的等比阶梯」解释——最终字号 =
// font × scale）。窗口 px 画布不缩放；layout 随后把缩放后值存进 frameMetric，派生几何
// （球锚/行锚/确认钮/fade headless）零改动自动一致。缺省（1.0/15）恒等。
func (u *UI) zoomedMetric(m unit.Metric) unit.Metric {
	z := u.zoomLoad()
	m.PxPerDp *= float32(z.scale)
	m.PxPerSp *= float32(z.scale * z.fontSp / fontSpBase)
	return m
}
