// Package uigui Gio 悬浮球 GUI 前端（DESIGN D43/§15，M5）：与 repl/tui 同权实现
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

import (
	"context"
	"image"
	"io"
	"strings"
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

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
const (
	zoomScaleMin = 0.75
	zoomScaleMax = 2.5
	fontSpMin    = 10.0
	fontSpMax    = 28.0
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
	// D103 补全浮层（仅事件循环 goroutine 读写）：词法相/过滤清单每帧重算（命令清单
	// 端口每帧拉取——动态命令随插件启停变化）；complRows 行矩形随帧登记（窗口系）。
	complText      string            // 上一帧编辑框文本（变更 → 重置 dismissed/高亮）
	complSel       int               // 键盘高亮行下标（complList 内）
	complDismissed bool              // Esc 后搁置（文本变更即重开）
	complOpenNow   bool              // 本帧浮层开启（updateEditor Enter 接管判据）
	complEsc       bool              // 本帧 Esc 已被浮层消费（updateEditor 编辑态让渡）
	complArmed     bool              // 行点击武装态（D72 口径）
	complPressPt   image.Point       // 武装按下点（窗口系）
	complList      []complItem       // 过滤后清单（drawCompl 呈现）
	complRows      []image.Rectangle // 行命中矩形（窗口系，随帧重登记）
	complTag       struct{}          // 行点击手势 tag
	hwnd           platform.Handle   // 主窗句柄（非 Windows 恒 0）
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

	// 形状与全帧合成（D44/D62/§15.1、§15.3）。
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

// Emit 呈现 Turn 事件（投递事件循环；线程安全）。
func (u *UI) Emit(_ context.Context, ev port.Event) error {
	u.post(eventMsg{ev: ev})
	return nil
}

// Say 输出一行会话文本（命令输出、启动提示）→ 转写纯文本块。
func (u *UI) Say(text string) {
	if text == "" {
		return
	}
	u.post(sayMsg{text: text})
}

// Prompt 输入提示：GUI 输入栏常驻，等价 no-op（保持前端接口同形）。
func (u *UI) Prompt() {}

// SetInterrupt 注入取消行为（装配根每轮 Turn 前设为取消该轮，nil = 无轮进行）；
// 同时驱动"生成中"标志（§15.2 发送键 ↔ 停止键、底栏状态行）。原子换，帧循环读。
func (u *UI) SetInterrupt(fn func()) {
	if fn == nil {
		nop := func() {}
		u.interrupt.Store(&nop)
		u.generating.Store(false)
		return
	}
	u.interrupt.Store(&fn)
	u.generating.Store(true)
}

// interruptNow 触发当前轮取消（停止键调用）；无轮进行时 no-op。
func (u *UI) interruptNow() {
	if f := u.interrupt.Load(); f != nil {
		(*f)()
	}
}

// Close 排空队列后结束事件循环并等待收尾（幂等；装配根按 io.Closer 调用）。
// 先投 drain 并等其处理：post 是异步 FIFO，保证此前 Emit/Say 全部落进状态机，
// 再投 quit 结束循环（对齐 uitui Close 的排空语义）。
func (u *UI) Close() error {
	u.cancelVoice()   // 录音中退出：先停采集（转写结果无人接收，随进程结束）
	u.plat.DropTray() // 托盘图标随前端收尾清理（S4b：平台面）（真销毁未走到 DestroyEvent 的路径防残留）
	u.wins.closeAll() // 功能窗收编（§15.7：退出 → 关闭全部次窗；headless 无开窗 = no-op）
	done := make(chan struct{})
	if !u.post(drainMsg{done: done}) {
		return nil // 事件循环已退出（如窗口已关）
	}
	select {
	case <-done:
	case <-u.done:
		return nil
	}
	if !u.post(quitMsg{}) {
		return nil
	}
	<-u.done
	return nil
}

// post 投递桥接消息并唤醒帧循环；事件循环已退出时静默丢弃（false）。
// 先非阻塞探一次 done：Close 之后 inbox 仍有空位，单靠 select 会与 done 同时就绪而
// 随机入队（消息永不处理，调用方可能永久等待）。
func (u *UI) post(m uiMsg) bool {
	select {
	case <-u.done:
		return false
	default:
	}
	select {
	case u.inbox <- m:
		if u.w != nil {
			u.w.Invalidate() // 并发安全；headless 由 runHeadless 阻塞等 inbox
		}
		return true
	case <-u.done:
		return false
	}
}

// confirmShare 进行中的确认在窗间的只读投影（D157）。**指针身份即「哪一次确认」**：
// 历史窗的应答带着自己 Load 到的那个指针回灌，主窗据此丢弃迟到应答——否则一次「上一条
// 确认的点击」可能落在刚好新起的下一条确认上。构造后只读（跨窗共享，不得改字段）。
type confirmShare struct {
	prompt string // 确认问句（与输入栏确认态同源文本）
}

// publishConfirm 发布 / 撤下（nil）进行中的确认投影，并催历史窗重绘（D157）。
// 仅主窗事件循环 goroutine 调用（headless 测试经 apply/drainSync 同路径）。
func (u *UI) publishConfirm(p *confirmShare) {
	u.confirmShare.Store(p)
	if u.wins != nil {
		u.wins.invalidateKind(winHistory)
	}
}

// signalEOF 标记输入流结束（幂等；close 广播给 Next/Confirm）。
// err 非空 = 底层读取错误（Next 原样上抛，装配根以退出码 1 结束）。
func (u *UI) signalEOF(err error) {
	u.eofOnce.Do(func() {
		u.eofErr = err
		close(u.eofCh)
	})
}

// Wake 空闲唤醒（D167）：后台任务完成等外部生产者敲醒 Next。非阻塞、容量 1——
// 多次完成合并成一次唤醒；Confirm 不读这个通道，不会被误当成 y/N 回答。
func (u *UI) Wake() {
	select {
	case u.wakeCh <- struct{}{}:
	default:
	}
}

// Next 阻塞读取下一条输入（斜杠命令解析与 repl 同口径）；
// io.EOF = 用户关窗（DestroyEvent）/ 输入流结束；扫描错误原样上抛；ctx 取消原样上抛。
// 广播到达时先取尽排队输入（eofMsg 经 inbox 排队，先于它的提交必已入队），
// 取空才返回 EOF——任何 select 次序下都不丢输入、也不吞 EOF（uitui 审查修复同款）。
func (u *UI) Next(ctx context.Context) (port.UserInput, error) {
	for {
		select {
		case in := <-u.inCh:
			return in, nil
		default:
		}
		select {
		case in := <-u.inCh:
			return in, nil
		case <-u.wakeCh:
			return port.UserInput{Wake: true}, nil
		case <-u.eofCh:
			select {
			case in := <-u.inCh:
				return in, nil
			default:
				if u.eofErr != nil {
					return port.UserInput{}, u.eofErr
				}
				return port.UserInput{}, io.EOF
			}
		case <-ctx.Done():
			return port.UserInput{}, ctx.Err()
		}
	}
}

// Confirm 逐次确认：状态机记提示行、输入栏切确认态（§15.2 非模态按钮组）；
// 应答三路，优先级：模型侧已应答（reply）→ 排队输入 → 输入流结束（拒，
// 对齐 repl"不替用户做破坏性决定"）。取排队输入或走 EOF 时同步发 confirmResultMsg
// 关闭模型侧确认态，避免残留（uitui 审查修复同款结构）。
func (u *UI) Confirm(ctx context.Context, prompt string) (port.ConfirmAnswer, error) {
	if err := ctx.Err(); err != nil {
		return port.ConfirmAnswer{}, err
	}
	reply := make(chan port.ConfirmAnswer, 1) // 缓冲 1：取消后无人接收，事件循环侧非阻塞投递
	u.post(confirmMsg{prompt: prompt, reply: reply})
	for {
		select { // 模型侧应答优先（避免多取一行）
		case ans := <-reply:
			return ans, nil
		default:
		}
		select { // 排队输入优先于 EOF：eofCh 一旦关闭恒就绪，与 inCh 同时就绪时
		// select 随机选中会把 y 误拒——必须先非阻塞取（与 Next 同款结构）。
		case in := <-u.inCh:
			ans := port.ConfirmAnswer{Allow: isYes(in.Text)}
			u.post(confirmResultMsg{ans: ans})
			return ans, nil
		default:
		}
		select {
		case ans := <-reply:
			return ans, nil
		case in := <-u.inCh:
			ans := port.ConfirmAnswer{Allow: isYes(in.Text)}
			u.post(confirmResultMsg{ans: ans})
			return ans, nil
		case <-u.eofCh:
			ans := port.ConfirmAnswer{}
			u.post(confirmResultMsg{ans: ans})
			return ans, nil
		case <-ctx.Done():
			return port.ConfirmAnswer{}, ctx.Err()
		}
	}
}

// uiMsg 桥接消息（事件循环消费）。
type uiMsg any

// 事件循环与外部世界的桥接消息。
type (
	// eventMsg Turn 事件（Emit 投递）。
	eventMsg struct{ ev port.Event }
	// sayMsg 纯文本行（Say 投递）。
	sayMsg struct{ text string }
	// inputMsg 注入一行提交（headless 测试与外部输入泵用；窗口侧走 model.submit）。
	inputMsg struct{ text string }
	// editMsg 进入编辑态（D92/D97/D107 气泡右键编辑三方式经 shell 菜单分发投递）：目标
	// 节点 + 预填原文 + 修订方式（zero = Fresh）+ 正文分片序号（D107②，-1 = 缺省合段）；
	// D188 另带目标节点的已有附件快照（数据面 = port.TreeView.NodeAttachments）。
	// 提交/Esc 的生命周期见 model.editTarget 与 cancelEdit。
	editMsg struct {
		id   conversation.MessageID
		text string
		mode conversation.KeepMode
		part int
		atts []port.AttachmentView
	}
	// copyMsg 写剪贴板请求（D92 气泡菜单复制项经 shell 线程分发投递）：记账到
	// pendingCopy，下一帧经 gtx.Execute(clipboard.WriteCmd) 落盘（剪贴板写入必须在
	// Gio 帧上下文执行，shell 线程不可直达）。
	copyMsg struct{ text string }
	// quoteMsg 引用请求（D98 气泡菜单「引用」经 shell 线程分发投递）：文本口径与复制
	// 一致（选区优先/整条渲染文本，D98①），Gio 侧经 quoteInto 追加引用块进输入框。
	quoteMsg struct{ text string }
	// attachMsg 文件选择框 / 拖拽的结果（D104/D186：shell 或平台线程 →
	// Gio inbox → 暂存列表；chip 呈现，发送随 Enter 走 Raw）。
	attachMsg struct{ paths []string }
	// pasteMsg 剪贴板贴图结果（D187）：ref = 已入附件库的引用，thumb = 缩略图
	// （nil = 解码超预算/失败，chip 回落回形针）；err / tooLarge / full 三选一 = 失败。
	pasteMsg struct {
		ref      *conversation.BlobRef
		thumb    *thumbSrc
		err      error
		tooLarge int64
		full     bool
	}
	// thumbMsg 路径附件的缩略图就绪（D187）：key = 暂存去重键（同一次输入的同一文件）。
	thumbMsg struct {
		key   string
		thumb *thumbSrc
	}
	// askMsg 提问请求（D184：ask_user 经 port.Asker 投递，输入栏原地展开成面板）。
	askMsg struct {
		req   port.AskRequest
		reply chan port.AskAnswer
	}
	// askDoneMsg 提问面板收尾（Ask 侧因取消/关窗自行收场时投递，撤下没人等的面板）。
	askDoneMsg struct{}
	// confirmMsg 确认请求（Confirm 投递）。
	confirmMsg struct {
		prompt string
		reply  chan port.ConfirmAnswer
	}
	// confirmResultMsg Confirm 侧自行得出应答后的确认态收尾（关态 + 记转写）。
	// share 非空 = 次窗（历史窗确认弹窗）的应答，其指针身份须与当前确认一致才生效（D157）。
	confirmResultMsg struct {
		ans   port.ConfirmAnswer
		share *confirmShare
	}
	// drainMsg 排空标记（Close 投递：处理到它即代表此前 post 全部落进状态机）。
	drainMsg struct{ done chan struct{} }
	// eofMsg 输入流结束（关窗/外部泵投递；err 非空 = 读取错误，Next 原样上抛）。
	eofMsg struct{ err error }
	// quitMsg 结束事件循环（Close 在 drain 之后投递）。
	quitMsg struct{}
	// showExpandMsg 呼出（托盘/快捷键从隐藏唤起）：展开输入栏 + 焦点入栏（§15.1）。
	showExpandMsg struct{}
	// toggleExpandMsg 快捷键在窗口可见时：展开 ↔ 收起互切（§15.1）。
	toggleExpandMsg struct{}
	// themeMsg 主题档切换（设置窗保存/启动校正；mode = system|light|dark）。
	themeMsg struct{ mode string }
	// sysThemeMsg 系统深浅变化（主窗 WM_SETTINGCHANGE 广播 → 仅 system 档重解析）。
	sysThemeMsg struct{}
	// voiceMsg 语音键结果（D179）：text = 转写文本（回填输入框、不自动发送）；
	// err 非空 = 录音/转写失败（状态行提示）。
	voiceMsg struct {
		text string
		err  error
	}
)

// apply 把桥接消息应用到状态机（仅事件循环 goroutine 调用）；false = 循环应退出。
func (u *UI) apply(msg uiMsg) bool {
	switch m := msg.(type) {
	case eventMsg:
		u.m.handleEvent(m.ev)
	case sayMsg:
		u.m.say(m.text)
	case inputMsg:
		u.m.submit(m.text)
	case editMsg:
		// D92/D97 编辑态入口：预填原文 + 修订方式 + 焦点入编辑框（同唤出口径——layout
		// 次帧执行 FocusCmd）；提交/Esc 的收尾见 model.submit 与 cancelEdit。
		// D106 修订⑷：多行原文先展开再预填（SingleLine=false 下 SetText 保真——
		// 原顺序会在进入编辑态就压平换行）。
		u.m.editTarget = m.id
		u.m.editMode = m.mode
		u.m.editPart = m.part // D107②：编辑目标正文分片序号（提交转 --part token）
		// D188：进编辑态先清掉上一次的删除标记，再挂上目标节点的已有附件；已在暂存的
		// 新附件保留（它们就在 chip 排里，用户看得见，提交时一并加进这次修订）。
		u.m.editAtts = m.atts
		u.m.editDropped = nil
		if strings.Contains(m.text, "\n") || len(m.atts) > 0 {
			u.setExpanded(true)
		}
		u.editor.SetText(m.text)
		u.focusPending = true
	case copyMsg:
		u.pendingCopy = m.text // D92：记账，frame 内 flushCopy 落剪贴板
	case quoteMsg:
		// D98 引用："> " 引用块追加进输入框 + 焦点入框；不进编辑态（editTarget 不动，
		// 引用是起草新输入、非修订历史）。
		u.editor.SetText(quoteInto(u.editor.Text(), m.text))
		u.focusPending = true
	case attachMsg:
		// D186 多附件暂存：一次选中多个就都暂存（超上限的后几项逐条提示）；已有同名项
		// 不重复加。焦点入框便于接着写问题；发送随 Enter 走 Raw（model.submit 暂存分支）。
		for _, p := range m.paths {
			u.stagePath(p)
		}
		u.focusPending = true
	case pasteMsg:
		u.finishPaste(m)
	case thumbMsg:
		// 缩略图迟到也是常态（用户已经删掉那条附件）：按去重键找，找不到就丢。
		for i := range u.m.stagedFiles {
			if u.m.stagedFiles[i].key() == m.key {
				u.m.stagedFiles[i].thumb = m.thumb
				break
			}
		}
	case imgMsg:
		// D207：转写区缩略图就绪（内容寻址键；迟到与重投都按首份为准）。
		u.applyImg(m)
	case askMsg:
		u.m.startAsk(m.req, m.reply)
	case askDoneMsg:
		u.m.endAsk()
		if u.expanded {
			u.setExpanded(false)
		}
	case confirmMsg:
		u.m.startConfirm(m.prompt, m.reply)
	case confirmResultMsg:
		// D157：带快照身份的应答（历史窗弹窗）只对**同一次**确认生效——迟到应答丢弃，
		// 不得误答刚好新起的下一条确认。无身份（Confirm 侧自行收尾）照旧直接应答。
		if m.share != nil && (u.m.confirm == nil || u.m.confirm.share != m.share) {
			break
		}
		u.m.replyConfirm(m.ans)
	case eofMsg:
		u.signalEOF(m.err)
	case drainMsg:
		close(m.done)
	case showExpandMsg:
		u.beginExpand() // 呼出 = 召回 + 展开（D50 召回、D54 动画）
	case toggleExpandMsg:
		u.toggleExpand() // 互切；动画中反向续跑（D54）
	case themeMsg:
		u.applyTheme(m.mode) // 热生效（下一帧重绘，§15.4；事件循环 goroutine 独占）
	case sysThemeMsg:
		if u.themeMode == "system" {
			u.applyTheme("system") // 仅 system 档跟随；固定档忽略广播
		}
	case voiceMsg:
		u.finishVoice(m.text, m.err)
	case quitMsg:
		return false
	}
	return true
}

// drainInbox 全量排空收件箱（每事件先应用再渲染，§15.5）；false = 循环应退出。
func (u *UI) drainInbox() bool {
	for {
		select {
		case msg := <-u.inbox:
			if !u.apply(msg) {
				return false
			}
		default:
			return true
		}
	}
}

// runHeadless 无窗口事件循环（§15.5：桥接层 headless 测试；无帧渲染）。
func (u *UI) runHeadless() {
	defer close(u.done)
	for {
		select {
		case msg := <-u.inbox:
			if !u.apply(msg) {
				return
			}
		}
	}
}

// parseInput 单行输入解析（斜杠开头 = 命令；与 repl.Next 同口径）。
func parseInput(line string) port.UserInput {
	line = strings.TrimSpace(line)
	if line == "" {
		return port.UserInput{}
	}
	if strings.HasPrefix(line, "/") {
		fields := strings.Fields(line)
		return port.UserInput{Command: &port.Command{
			Name: strings.TrimPrefix(fields[0], "/"),
			Args: fields[1:],
		}}
	}
	return port.UserInput{Text: line}
}

// quoteInto 把引用文本组为单行引用前缀追加到 existing（D98）："> " + 文本，文本内
// 换行压为空格——输入框 SingleLine（D49），多行引用无法成块（Gio SetText/Insert 在
// SingleLine 下均把 \n 替换为空格），沿 D92 后果②「所见即所发」口径；2b-4 展开态
// 多行编辑区落地后可升级为整块引用。existing 非空以空格衔接。
func quoteInto(existing, quoted string) string {
	line := "> " + strings.ReplaceAll(quoted, "\n", " ")
	if existing == "" {
		return line
	}
	return existing + " " + line
}

// isYes y/yes（大小写不敏感）为同意——与 repl.Confirm 语义一致。
func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}
