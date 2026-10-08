package uigui

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"math"

	"image"
	"image/color"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// 窗口与布局常量（物理 px 经 gtx.Dp 换算——layout 坐标即物理 px，Dp 只换算尺寸）。
const (
	winWidthDp   = 608 // 默认窗宽：16 边距 + 576 三段行 + 16（行宽 = 设计稿，D49）
	winHeightDp  = 460
	winMinWidth  = 420
	winMinHeight = 240

	fadeBandDp = 56 // 顶带高（§15.3）
	// 底部矮带高（D79，§15.3）：≈顶带 1/5，薄带；底带与**等高尾部留白**共用此单源取值。
	fadeBandBottomDp = 12
	inputRowDp       = 48 // 输入行元素高（D49 canvas 1:1）＝胶囊高＝logo/send 圆钮直径
	inputGapDp       = 12 // 三段间距与胶囊内元素间距（canvas spacing 12）
	inputPadDp       = 16 // 胶囊左右内边距（canvas padding 16）
	inputIconDp      = 20 // 胶囊内图标槽（canvas 20×20，灰占位不可点）
	pillTopDp        = 8  // 输入行上边距（下方 = inputRowBottomDp）
	inputRowBottomDp = 16 // 输入行下边距（D49 canvas）
	// inputRowBandDp 输入行带总高（D90 单源）＝行元素 + 上 8 下 16 透明边距：布局区高
	// 划分、球锚、心跳带、停靠恢复共用（旧为五处手写重复组合式，改几何须五处同步）。
	inputRowBandDp = inputRowDp + pillTopDp + inputRowBottomDp
	// inputPillExpandDp 展开态胶囊高（D106/S2b-4，修订⑸ = 160dp ≈ 6 行）：原地增高多行
	// 编辑；输入带随之增高 inputPillExpandDp−inputRowDp，转写区相应压缩。瞬时切换、
	// 不做高度动画。
	inputPillExpandDp = 160
	// pillToolTopDp 展开态工具行顶边距（D106 修订⑸）：图标避开胶囊 24dp 圆角曲线。
	pillToolTopDp    = 10
	sideMarginDp     = 16 // 左右边距
	confirmBtnDp     = 36 // D86：确认态三钮直径（行高 48 的 3/4）
	confirmBtnGapDp  = 8  // D86：三钮间距
	confirmBtnEdgeDp = 12 // D86：最右钮右缘到胶囊边缘（padding 16 − 4 圆形光学校正）
	confirmBtnOptDp  = 4  // D86：光学校正量（布局期负内边距实现上面的 12）
	rowGapDp         = 6  // 转写行间距
	bubblePadXDp     = 12 // 气泡内边距
	bubblePadYDp     = 7
	cardPadXDp       = 10 // 文本行卡内边距
	cardPadYDp       = 5
	radiusDp         = 12 // 气泡圆角
	cardRadiusDp     = 8  // 文本行卡圆角
	statusChipDp     = 20 // 状态行 chip 高
	statusPadXDp     = 10 // 状态行 chip 水平内边距
	statusGapDp      = 8  // 状态行带高（chip + 与转写区间隙）
	tipsPadXDp       = 10 // 悬停卡（hoverCard）内边距
	tipsPadYDp       = 6
	tipsRadiusDp     = 8  // 悬停卡圆角
	tipsUpGapDp      = 6  // 悬停卡与胶囊顶的间隙
	bubbleMinWDp     = 80 // 气泡最大宽下限（极窄窗兜底，D90 命名化）

	// 主窗像素尺寸夹取界（D90）：粗界给 config/settings 校验兜底；布局地板按
	// 240dp × DPI × scale 抬下限（宽 = 三段行最小构成、高 = 输入行带 + 状态行 +
	// 顶底带 + 少量内容余量——再小布局退化，不承诺可用）。
	winPxMinW     = 200
	winPxMinH     = 200
	winPxMaxW     = 3840
	winPxMaxH     = 2160
	layoutFloorDp = 240

	// 边缘羽化（D45–D48/§15.1、D62）：把「内容自身由内向外渐隐」作为每像素 vis 因子
	// 并入整窗 ULW 位图——核心不透明、边带沿真轮廓 smoothstep 渐隐到轮廓（不向外堆光晕：
	// 旧 D45–D47 的向外环在轮廓线 d=0 有折点 →「饱和核心 + 外圈亮带」，与带边观感割裂）。
	// **响应式（D47）**：渐隐带宽按元素短边成比例再夹上下限，随元素尺寸/窗口缩放/DPI
	// 自适应，不引入固定 px 羽化宽。D68：ULW 单通道后 A/B 观测（AQUARIUS_NO_FEATHER）
	// 结论 = 羽化观感仍优于硬切，保留；带宽收细（0.05→0.03、上限 5→3dp）。
	featherRatio = 0.03 // 渐隐带宽 = min(宽,高) × 该比例
	featherMinDp = 0    // 渐隐宽下限
	featherMaxDp = 3    // 渐隐宽上限

	// semiAlpha 统一半透明（D62：ULW SourceConstantAlpha 每帧随位图同拍提交；
	// D50 停靠淡化在其上插值到 dockAlpha）。
	semiAlpha byte = 235

	// dragClickSlackPx 拖窗/单击判定阈值（物理 px）：收起态单击球 = 展开，
	// 位移超阈值 = 拖窗。
	dragClickSlackPx = 4
)

// 主题令牌活动槽（§15.4/D61：深浅两版预设与 applyPalette 见 theme.go——
// 预设纯数据，自绘路径读活动槽、material 主题经同一快照取默认色）。
var (
	brandColor = color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF}
	pillBg     = color.NRGBA{R: 0xFA, G: 0xFA, B: 0xFC, A: 0xFF} // 输入栏浅白
	windowBg   = color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF3, A: 0xFF} // 兜底背景（非 Windows 降级形态）
	textDim    = color.NRGBA{R: 0x8A, G: 0x8F, B: 0x98, A: 0xFF}
	textMuted  = color.NRGBA{R: 0x6B, G: 0x70, B: 0x78, A: 0xFF}
	textError  = color.NRGBA{R: 0xD9, G: 0x3A, B: 0x3A, A: 0xFF}
	textNotice = color.NRGBA{R: 0xC0, G: 0x77, B: 0x00, A: 0xFF}
	textSystem = color.NRGBA{R: 0x8E, G: 0x6B, B: 0xC4, A: 0xFF}

	// 行卡底色（全部实色——位图合成下元素外无底板，半透明只能整窗 SourceConstantAlpha 叠加）。
	cardThinking = color.NRGBA{R: 0xE9, G: 0xEB, B: 0xF0, A: 0xFF}
	cardTool     = color.NRGBA{R: 0xE7, G: 0xEA, B: 0xEF, A: 0xFF}
	cardNotice   = color.NRGBA{R: 0xFD, G: 0xF2, B: 0xDC, A: 0xFF}
	cardError    = color.NRGBA{R: 0xFB, G: 0xE4, B: 0xE4, A: 0xFF}
	cardSystem   = color.NRGBA{R: 0xF1, G: 0xEB, B: 0xFA, A: 0xFF}
	whiteText    = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// 图标槽占位灰（canvas 稿 #94A3B8；配色为占位，D49）。
	iconDim = color.NRGBA{R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF}
	// 确认态三钮（D86；令牌槽由主题预设回填，两版同值——实色圆白字两主题对比度均足）。
	actionDeny    = color.NRGBA{R: 0xD9, G: 0x3A, B: 0x3A, A: 0xFF}
	actionAllow   = color.NRGBA{R: 0x2E, G: 0xA8, B: 0x57, A: 0xFF}
	actionElevate = color.NRGBA{R: 0xF5, G: 0xA6, B: 0x23, A: 0xFF}
	// depthMap 深度色带活动槽（D120§3.2）：applyPalette 回填、关系图帧读取。
	depthMap [4]color.NRGBA
)

// point/rect 屏幕几何（中性定义：小写字段，自绘几何用；与平台 DTO 在门面处转换）。
type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }

// 平台 DTO ↔ 内部几何（字段名不同，仅此两处转换）。
func toPoint(p platform.Point) point { return point{x: p.X, y: p.Y} }
func toRect(r platform.Rect) rect {
	return rect{left: r.Left, top: r.Top, right: r.Right, bottom: r.Bottom}
}

// —— 平台调用门面（uigui 内部一律经这些方法；实现见 platform 包）——

// cursorPos 光标屏幕坐标（拖动增量的绝对基准，§15.6 铁律 2）。
func (u *UI) cursorPos() point { return toPoint(u.plat.CursorPos()) }

// windowRect 主窗物理像素矩形（拖动基准与 ULW 定位）。
func (u *UI) windowRect() (rect, bool) {
	r, ok := u.plat.WindowRect()
	return toRect(r), ok
}

// moveWindowTo 移动主窗（不改尺寸；启动路径不在帧内，直接下发）。
func (u *UI) moveWindowTo(x, y int32) { u.plat.MoveWindow(x, y) }

// resizeWindowTo 改主窗尺寸（保位，D90）。
func (u *UI) resizeWindowTo(w, h int32) { u.plat.ResizeWindow(w, h) }

// workArea 最近显示器工作区（锚点夹取/吸附/停靠共用口径）。
func (u *UI) workArea(p point) (rect, bool) {
	r, ok := u.plat.WorkArea(platform.Point{X: p.x, Y: p.y})
	return toRect(r), ok
}

// monitorAt 点上是否有显示器（D50 停靠外侧边判定）。
func (u *UI) monitorAt(p point) bool {
	return u.plat.MonitorAt(platform.Point{X: p.x, Y: p.y})
}

// windowFromPoint 光标处的顶层窗口（OS 命中直证，D85）。
func (u *UI) windowFromPoint(p point) platform.Handle {
	return u.plat.WindowFromPoint(platform.Point{X: p.x, Y: p.y})
}

// restorePx 恢复期 dp→px 换算（D90 治本：DPI 对指定窗直查；取不到回落 1.0）。
func (u *UI) restorePx(h platform.Handle) func(dp int) int32 {
	scale := u.plat.WindowDPI(h)
	return func(dp int) int32 { return int32(math.Round(float64(dp) * scale)) }
}

// drawShape 布局期收集的可见元素：outline = 元素**真实轮廓**（未按视口/淡出带裁剪，
// 边缘渐隐沿此取边 → 不沿裁切线描边，消除横缝）；clip = 可见裁剪区（转写区视口/整窗，
// 渐隐只在此区内落笔）；radius = 圆角半径；fill = 元素自身底色（headless 不可用时的兜底
// 取色——见 writePremulFill）。全帧合成的 vis 覆盖度（fadeFrame）由此推导（§15.1/D45–D48）。
type drawShape struct {
	outline image.Rectangle
	clip    image.Rectangle
	radius  int // 圆角半径（px）
	fill    color.NRGBA
}

// 【S4b 注】原先的包级全局态（mainHWND / win32Run 窗口线程投递槽）已随平台面迁入
// platform 包，经 u.plat 触达；本文件只留帧循环视角的本地副本 u.hwnd。

// runWindow Gio 窗口事件循环（本 goroutine 独占状态机与窗口侧控件）。
// 不调 app.Main：Windows 的 osMain 仅 select{}（Gio 自建带锁线程跑窗口消息），
// 库内调用会把装配根卡死。
func (u *UI) runWindow(w *app.Window) {
	defer close(u.done)
	u.plat.SetMainRun(w.Run) // 修改性原生调用统一走窗口线程（§15.6 铁律 1）
	decorated := !u.plat.NativeWindowControl()
	w.Option(
		app.Title("Aquarius"),
		app.Size(unit.Dp(winWidthDp), unit.Dp(winHeightDp)),
		app.MinSize(unit.Dp(winMinWidth), unit.Dp(winMinHeight)),
		// 无边框 = 悬浮球形态前提（§15.1）；平台不能自管窗口位置时（非 Windows 降级）
		// 改常规装饰窗——否则用户无法移动/缩放窗口（已知降级，D111 修订④）。
		app.Decorated(decorated),
		app.TopMost(u.plat.TopMost()), // 悬浮球常驻顶层（平台缺此能力时恒真）
	)
	u.th = newTheme(u.plat.SystemFontCandidates())
	if p := u.pal.Load(); p != nil { // 初始主题快照校正默认色（§15.4/D61）
		u.th.Palette.Fg, u.th.Palette.Bg = p.fg, p.bg
	}
	var ops op.Ops
	for {
		ev := w.Event()
		// 先应用排队消息再渲染（§15.5：每事件全量排空 inbox）。
		if !u.drainInbox() {
			return
		}
		switch e := ev.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			u.frame(gtx, func() { e.Frame(&ops) })
		case app.DestroyEvent:
			// 用户关窗 = 输入流结束（Next → EOF → 装配根退出，退出码 0；
			// 创建失败 Err 非空 → Next 上抛，退出码 1）。
			// 正常 Alt+F4 已被 WM_CLOSE 拦截为隐藏（§15.1），走到这里 = 真销毁
			//（装配根收尾/系统关闭）——清托盘图标防悬浮区残留（S4b-3 迁入平台面）。
			u.plat.DropTray()
			u.signalEOF(e.Err)
			return
		default:
			if h, ok := u.viewEvent(ev); ok {
				u.onHWND(h)
			}
		}
	}
}

// frame 单帧推进与提交（runWindow 帧事件调；抽成方法 = 无窗口可测的次序契约，D55–D62）。
// 次序固定：两套动画进度 → 两遍 layout（同帧同进度）→ 全帧合成（唯一慢段、不改屏幕态）
// → 移窗一拍 flush → 绘制提交（事件路由/帧节奏）→ ULW 上屏。
// 【§15.6 铁律 3｜D62 单通道】D44–D59 期主窗内容（bitblt swapchain）/ 形裁
// （SetWindowRgn）/ 效果层（overlay ULW）三通道并存、DWM 采样点各异，最快动画段存在
// 亚帧通道错位（同一次序不同帧结果不一致 = 窗口线程队列与 Gio ack→Present 的交错竞态，
// 三轮截图迭代实证非次序可治）。D62 起像素单通道化——形状/效果/透明度全部并入同一张
// 整窗 ULW 位图，错位结构性不可能；次序仅剩工程约束：
//   - 全帧合成（离屏重渲 + 预乘）先跑完，不与提交交错；
//   - 移窗先于 present（fadePresent 取实测窗口矩形定位）；
//   - `e.Frame` 保留（事件路由 / IME / vblank 帧节奏，其画面被 ULW 位图覆盖）；
//   - ULW 殿后提交（主窗 HWND，SourceConstantAlpha = u.alpha）。
func (u *UI) frame(gtx layout.Context, submit func()) {
	// D90 咽喉点（§15.8）：缩放只在此一处进布局——layout 与 fadeCompose 二次调用都
	// 消费 frameMetric（已缩放），不会重复缩放；窗口 px 画布与 Constraints 不动。
	gtx.Metric = u.zoomedMetric(gtx.Metric)
	u.flushCopy(gtx) // D92：菜单分发的复制请求在此（Gio 帧）落剪贴板
	u.stepExpand(time.Now())
	u.stepAnim() // D50：停靠动画每帧前推（layout 被 headless 二次调用，进度只能放帧里、且在两遍 layout 之前）
	u.layout(gtx)
	u.phase("compose")
	// 平台不自管像素提交时（非 Windows 降级）没有 ULW 可交：跳过离屏全帧合成通道，
	// 内容由 Gio 自身表面渲染（D111③）——省一遍全帧重渲。
	composed := false
	if u.plat.NativeWindowControl() {
		composed = u.fadeCompose() // 全帧合成：headless 同布局重渲 → av = vis × g(y) × alpha 预乘（D62）
	}
	u.phase("commit")
	u.commitWinGeom() // 移窗一拍 flush（先于 present：定位取实测矩形）
	submit()
	u.phase("present")
	u.fadePresent(composed) // 整窗 ULW 提交（D62：位图 alpha 即形状/命中，单通道无错位）
}

// phase 帧阶段回执（仅测试注入，生产恒 nil）：断言 D55–D62 次序契约。
func (u *UI) phase(name string) {
	if u.framePhase != nil {
		u.framePhase(name)
	}
}

// requestMove 帧内位移改动记账（D55）：由 commitWinGeom 统一提交。
// 启动路径（onHWND/restoreDock）不在帧内，直接调用 moveWindowTo。
func (u *UI) requestMove() { u.movePending = true }

// onHWND Win32ViewEvent 投递的窗口句柄：启动防闪（D78 挂接即隐藏、首帧 ULW 成功才
// 揭示）+ 置顶断言 + 位置记忆恢复（§15.1/D44/D62）。统一半透明不在此下发（D62：首帧
// ULW 随 SourceConstantAlpha 生效）。
func (u *UI) onHWND(h platform.Handle) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	u.plat.AttachMain(h)
	if !u.plat.NativeWindowControl() {
		return // 非 Windows 无原生窗口可接管（无句柄语义）：常规窗形态照常渲染
	}
	// D78 启动防闪：挂接即隐藏（最早可接管点——Gio Configure(ShowWindow) 早于本事件、
	// 无可挂钩点，其间亚帧间隙接受）并置揭示待定；首帧 ULW 提交成功才揭示（fadePresent）。
	// 【顺序敏感】先 SW_HIDE 再挂 WS_EX_LAYERED：层样式在**可见态**挂接、随后首帧 ULW 前
	// 被隐藏，UpdateLayeredWindow 将永久失败（errno=87，重新显示也不恢复）——revealPending
	// 永不清零、窗口永不揭示且呼出门死锁（实机「找不到窗口」）。隐藏态挂接则全链路正常。
	u.revealPending.Store(true)
	u.plat.HideUntilFirstPresent(h)
	u.plat.EnsureLayered(h)       // 分层样式（D62 双保险）——须在隐藏后挂
	u.plat.SubclassCloseToHide(h) // 关窗（Alt+F4）= 隐藏（§15.1）
	u.plat.HideFromTaskbar(h)     // 不进任务栏与 Alt+Tab（D51）
	// 置顶断言 + 记忆恢复（§15.1 置顶开关）：缺省置顶、菜单切换态随记忆回来——
	// 本端 SetWindowPos 断言，不依赖 Gio 的 TopMost 应用（实测会意外丢失、原因未明）。
	on := true
	if u.opts.PosFile != "" {
		if p, found := loadPos(u.opts.PosFile); found && p.TopMost != nil {
			on = *p.TopMost
		}
	}
	u.plat.SetTopMost(on)
	// D90：配置像素尺寸先落（app.Size 只收 dp、建窗期 DPI 未就绪无法换算，px 口径在
	// 挂接点经窗口线程补投）；同步等待完成后，位置恢复/停靠重算按落定矩形取值。
	u.applyConfiguredSize()
	rc, ok := u.windowRect()
	if !ok {
		return
	}
	w, ht := rc.right-rc.left, rc.bottom-rc.top
	u.x, u.y = rc.left, rc.top
	if u.opts.PosFile != "" {
		if p, found := loadPos(u.opts.PosFile); found {
			// D50：停靠记忆优先（停靠位重算，X/Y 忽略）；失败/非停靠走普通恢复——
			// D52：锚点 = 输入栏包围盒（非停靠恢复恒为展开态，posRec 无 collapsed 键），
			// 与拖动夹取同口径（重启不跳位）；frameMetric 未就绪 → restorePx 直查 DPI（D90）。
			if (p.Docked == "left" || p.Docked == "right") && u.restoreDock(p, rc) {
				return
			}
			nx, ny := p.X, p.Y
			if work, wok := u.workArea(point{x: p.X + w/2, y: p.Y + ht/2}); wok {
				px := u.restorePx(h)
				top := int(ht) - int(px(inputRowBandDp)) + int(px(pillTopDp))
				a := image.Rect(int(px(sideMarginDp)), top, int(w)-int(px(sideMarginDp)), top+int(px(inputRowDp)))
				c := clampAnchor(point{x: p.X, y: p.Y}, a, work)
				nx, ny = c.x, c.y
			}
			u.moveWindowTo(nx, ny)
			u.x, u.y = nx, ny
		}
	}
	// 首帧 ULW 提交成功窗口才揭示（D78：挂接即隐藏、revealPending 至此清零）。
	// 本函数不在帧内，位移直接下发（帧内的改动一律记账，见 requestMove）。
}
