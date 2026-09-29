package uigui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/gesture"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// 窗口与布局常量（物理 px 经 gtx.Dp 换算——layout 坐标即物理 px，Dp 只换算尺寸）。
const (
	winWidthDp   = 608 // 默认窗宽：16 边距 + 576 三段行 + 16（行宽 = 设计稿，D49）
	winHeightDp  = 460
	winMinWidth  = 420
	winMinHeight = 240

	fadeBandDp   = 56 // 淡出带高（§15.3）
	inputRowDp   = 48 // 输入行元素高（D49 canvas 1:1）＝胶囊高＝logo/send 圆钮直径
	inputGapDp   = 12 // 三段间距与胶囊内元素间距（canvas spacing 12）
	inputPadDp   = 16 // 胶囊左右内边距（canvas padding 16）
	inputIconDp  = 20 // 胶囊内图标槽（canvas 20×20，灰占位不可点）
	pillTopDp    = 8  // 输入行上边距（下方 16）
	sideMarginDp = 16 // 左右边距
	rowGapDp     = 6  // 转写行间距
	bubblePadXDp = 12 // 气泡内边距
	bubblePadYDp = 7
	cardPadXDp   = 10 // 文本行卡内边距
	cardPadYDp   = 5
	radiusDp     = 12 // 气泡圆角
	cardRadiusDp = 8  // 文本行卡圆角
	statusChipDp = 20 // 状态行 chip 高

	// 边缘羽化（D45–D48/§15.1、D62）：把「内容自身由内向外渐隐」作为每像素 vis 因子
	// 并入整窗 ULW 位图——核心不透明、边带沿真轮廓 smoothstep 渐隐到轮廓（不向外堆光晕：
	// 旧 D45–D47 的向外环在轮廓线 d=0 有折点 →「饱和核心 + 外圈亮带」，与带边观感割裂）。
	// **响应式（D47）**：渐隐带宽按元素短边成比例再夹上下限，随元素尺寸/窗口缩放/DPI
	// 自适应，不引入固定 px 羽化宽。
	featherRatio = 0.05 // 渐隐带宽 = min(宽,高) × 该比例
	featherMinDp = 0    // 渐隐宽下限
	featherMaxDp = 5    // 渐隐宽上限

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
	// 确认态置灰右圆（D49：几何不随状态变，只换色）。
	disabledCircle = color.NRGBA{R: 0xD8, G: 0xDC, B: 0xE3, A: 0xFF}
)

// point/rect Win32 坐标对（中性定义：非 Windows 构建仅作占位类型）。
type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }

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

// 主窗口句柄与窗口线程入口（win32 补位的两个跨 goroutine 交接点）。
var (
	mainHWND uintptr // atomic 存取：事件循环写、窗口线程读
	win32Run atomic.Pointer[func(func())]
)

// onWindowThread 把修改性 Win32 调用送到 Gio 窗口线程执行（Window.Run）并等待完成。
// 【§15.6 铁律 1，堆栈实证】Gio runLoop 在 deliverEvent 的 select 中服务 driverFuncs，
// 但从客户端协程跨线程 SendMessage（SetWindowPos/SetWindowRgn 等内部回投窗口过程）
// 会永久阻塞——runLoop 停在 select、不泵消息。查询类调用不受此限。
func onWindowThread(f func()) {
	if r := win32Run.Load(); r != nil {
		(*r)(f)
		return
	}
	f() // 窗口未就绪（理论上不发生）：直接执行兜底
}

// 位置记忆（§15.1：拖拽 + 位置记忆，含多显示器工作区夹取；置顶态随存——
// §15.1 置顶开关）。posMu：拖动保存（帧循环）与菜单切换保存（托盘线程）互斥。
// Docked 停靠边（D50）：left/right = 停靠中（恢复按当前工作区重算停靠位、X/Y 忽略），
// 缺省/旧文件空键 = 未停靠。
type posRec struct {
	X, Y int32
	// TopMost 置顶态；nil = 旧文件/未设置 → 缺省置顶。
	TopMost *bool `json:"top_most,omitempty"`
	// Docked 停靠边；"" = 未停靠（omitted 保证旧文件兼容）。
	Docked string `json:"docked,omitempty"`
}

var posMu sync.Mutex

func loadPos(path string) (posRec, bool) {
	posMu.Lock()
	defer posMu.Unlock()
	src, err := os.ReadFile(path)
	if err != nil {
		return posRec{}, false
	}
	var p posRec
	if json.Unmarshal(src, &p) != nil {
		return posRec{}, false
	}
	return p, true
}

func savePos(path string, p posRec) {
	posMu.Lock()
	defer posMu.Unlock()
	src, _ := json.Marshal(p)
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, src, 0o644)
}

// monoFace 代码块等宽字体面（newTheme 从集合挑 Mono 系；零值 = 回落正文体，D65）。
// 与 brandColor 等同槽风格：主题组装期写定，帧循环只读。
var monoFace font.Font

// newTheme 主题组装（§15.4 骨架：系统中文字体优先——gofont 无 CJK，spike 实证路径；
// 失败回落 gofont）。字体集合恒补 Go Mono 系面（markdown 代码块等宽，D65；CJK 缺字
// 由 typesetting FontMap 自动回落中文字面）。
func newTheme() *material.Theme {
	th := material.NewTheme()
	faces := loadCJKFaces()
	if len(faces) == 0 {
		faces = gofont.Collection() // 自带 Go Mono
	} else {
		faces = append(faces, monoFontFaces()...)
	}
	monoFace = pickMonoFace(faces)
	th.Shaper = text.NewShaper(text.WithCollection(faces))
	return th
}

// monoFontFaces gofont 集合中的 Mono 系面（等宽拉丁；正体在前优先）。
func monoFontFaces() []text.FontFace {
	var out []text.FontFace
	for _, f := range gofont.Collection() {
		if strings.Contains(strings.ToLower(string(f.Font.Typeface)), "mono") {
			out = append(out, f)
		}
	}
	return out
}

// pickMonoFace 从字体集合挑等宽正体面（未找到 = 零值回落）。
func pickMonoFace(faces []text.FontFace) font.Font {
	for _, f := range faces {
		if strings.Contains(strings.ToLower(string(f.Font.Typeface)), "mono") &&
			f.Font.Weight == font.Normal && f.Font.Style == font.Regular {
			return f.Font
		}
	}
	for _, f := range faces {
		if strings.Contains(strings.ToLower(string(f.Font.Typeface)), "mono") {
			return f.Font
		}
	}
	return font.Font{}
}

// mdHeadingSp 标题字号阶梯（D65）：h1 20sp → h2 17sp → h3 15sp，h4 以下与正文同大
// （Weight 求粗仍区分；CJK 粗体面缺省时回落常规渲染）。
func mdHeadingSp(th *material.Theme, level int) unit.Sp {
	switch level {
	case 1:
		return th.TextSize * 20.0 / 16.0
	case 2:
		return th.TextSize * 17.0 / 16.0
	case 3:
		return th.TextSize * 15.0 / 16.0
	default:
		return th.TextSize * 14.0 / 16.0
	}
}

// loadCJKFaces 加载 Windows 系统中文字体（§15.6 spike 实证：msyh.ttc → opentype）。
func loadCJKFaces() []text.FontFace {
	for _, p := range []string{
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
		`C:\Windows\Fonts\simsun.ttc`,
	} {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(src)
		if err != nil || len(faces) == 0 {
			continue
		}
		return faces
	}
	return nil
}

// runWindow Gio 窗口事件循环（本 goroutine 独占状态机与窗口侧控件）。
// 不调 app.Main：Windows 的 osMain 仅 select{}（Gio 自建带锁线程跑窗口消息），
// 库内调用会把装配根卡死。
func (u *UI) runWindow(w *app.Window) {
	defer close(u.done)
	runFn := w.Run
	win32Run.Store(&runFn) // 修改性 Win32 调用统一走窗口线程（§15.6 铁律 1）
	w.Option(
		app.Title("Aquarius"),
		app.Size(unit.Dp(winWidthDp), unit.Dp(winHeightDp)),
		app.MinSize(unit.Dp(winMinWidth), unit.Dp(winMinHeight)),
		app.Decorated(false), // 无边框 = 悬浮球形态前提（§15.1）
		app.TopMost(true),    // 悬浮球常驻顶层
	)
	u.th = newTheme()
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
			//（装配根收尾/系统关闭）——清托盘图标防悬浮区残留。
			trayDelete()
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
	u.stepExpand(time.Now())
	u.stepAnim() // D50：停靠动画每帧前推（layout 被 headless 二次调用，进度只能放帧里、且在两遍 layout 之前）
	u.layout(gtx)
	u.phase("compose")
	composed := u.fadeCompose() // 全帧合成：headless 同布局重渲 → av = vis × g(y) × alpha 预乘（D62）
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

// commitWinGeom 一拍提交本帧屏幕态（移窗，经 Window.Run，§15.6 铁律 1）。
// D55：帧内改动一律只记账，由这里统一 flush——分散发起会各占一拍。D62：形裁/alpha
// 通道退役（随位图同拍提交），仅剩移窗。
func (u *UI) commitWinGeom() {
	if u.movePending {
		u.movePending = false
		moveWindowTo(u.x, u.y)
	}
}

// requestMove 帧内位移改动记账（D55）：由 commitWinGeom 统一提交。
// 启动路径（onHWND/restoreDock）不在帧内，直接调用 moveWindowTo。
func (u *UI) requestMove() { u.movePending = true }

// onHWND Win32ViewEvent 投递的窗口句柄：置顶断言 + 位置记忆恢复（§15.1/D44/D62）。
// 统一半透明不在此下发（D62：首帧 ULW 随 SourceConstantAlpha 生效；分层窗在首次 ULW
// 前不显示，启动无白闪）。
func (u *UI) onHWND(h uintptr) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	atomic.StoreUintptr(&mainHWND, h)
	ensureLayeredStyle(h)  // 分层窗首次 ULW 前不显示 → 启动无白闪（D62；非 Windows no-op）
	subclassCloseToHide(h) // 关窗（Alt+F4）= 隐藏（§15.1；非 Windows 为 no-op 桩）
	hideFromTaskbar(h)     // 不进任务栏与 Alt+Tab（D51；非 Windows 为 no-op 桩）
	// 置顶断言 + 记忆恢复（§15.1 置顶开关）：缺省置顶、菜单切换态随记忆回来——
	// 本端 SetWindowPos 断言，不依赖 Gio 的 TopMost 应用（实测会意外丢失、原因未明）。
	on := true
	if u.opts.PosFile != "" {
		if p, found := loadPos(u.opts.PosFile); found && p.TopMost != nil {
			on = *p.TopMost
		}
	}
	platformSetTopMost(on)
	rc, ok := windowRectPx()
	if !ok {
		return
	}
	w, ht := rc.right-rc.left, rc.bottom-rc.top
	u.x, u.y = rc.left, rc.top
	if u.opts.PosFile != "" {
		if p, found := loadPos(u.opts.PosFile); found {
			// D50：停靠记忆优先（停靠位重算，X/Y 忽略）；失败/非停靠走普通恢复——
			// D52：锚点 = 输入栏包围盒（非停靠恢复恒为展开态，posRec 无 collapsed 键），
			// 与拖动夹取同口径（重启不跳位）；frameMetric 未就绪 → restorePx 窗高比例。
			if (p.Docked == "left" || p.Docked == "right") && u.restoreDock(p, rc) {
				return
			}
			nx, ny := p.X, p.Y
			if work, wok := platformWorkArea(point{x: p.X + w/2, y: p.Y + ht/2}); wok {
				px := restorePx(ht)
				top := int(ht) - int(px(inputRowDp+pillTopDp+16)) + int(px(pillTopDp))
				a := image.Rect(int(px(sideMarginDp)), top, int(w)-int(px(sideMarginDp)), top+int(px(inputRowDp)))
				c := clampAnchor(point{x: p.X, y: p.Y}, a, work)
				nx, ny = c.x, c.y
			}
			moveWindowTo(nx, ny)
			u.x, u.y = nx, ny
		}
	}
	// 首帧 ULW 在帧循环提交位图后窗口方显示（分层窗首次 ULW 前不显示，D62）。
	// 本函数不在帧内，位移直接下发（帧内的改动一律记账，见 requestMove）。
}

// layout 悬浮窗布局：背景（兜底 + 整窗拖动）| 转写区（手工布局 + 滚动）/ 状态行 /
// 输入栏——自底向上定高，全部绝对坐标登记形状（§15.1/D44/D62）。
// 本函数也被 fadeCompose 以零值 Source 二次调用（纯渲染，无事件消费）；移窗不在这里
// 下发——帧内只记账，由 commitWinGeom 统一 flush（D55）。
func (u *UI) layout(gtx layout.Context) layout.Dimensions {
	if u.focusPending && !u.collapsed {
		u.focusPending = false
		gtx.Execute(key.FocusCmd{Tag: &u.editor}) // 唤出（托盘/快捷键）后焦点进输入栏
	}
	if !u.collapsed {
		u.updateEditor(gtx) // 收起态不消费按键（编辑器不可见，防隐形收字）
	}
	u.updateClicks(gtx)
	u.updateDrag(gtx)
	u.updateLogo(gtx)
	switch {
	case u.expandAn.active:
		u.dockArm = false // D54：动画期间不做停靠评估（球位在动，布防无意义）
	case u.collapsed || u.docked:
		u.evalDockFrame() // D50：收起/停靠态逐帧评估停靠（光标直采，layout 前置状态已更新）
	default:
		u.dockArm = false // 展开态不可停靠：清布防残留（防收起瞬间误触发滑出）
	}
	u.armHeartbeat() // D50：布防心跳（移出球后无指针事件 → 主动唤帧完成移开判定）

	size := gtx.Constraints.Max
	u.frameMetric = gtx.Metric
	u.frameSize = size
	u.shapes = u.shapes[:0]
	// D54：淡出带缺省 = §15.3 静息顶带（收起态无转写区，带只作兜底口径）。
	u.bandTop, u.bandBottom = 0, gtx.Dp(fadeBandDp)

	// D54：collapsed 是逻辑态、即时翻转；动画中走全量 layout 带几何插值（收尾 barP=0
	// 时几何 == layoutCollapsed 的球，切换无缝）。
	if u.collapsed && !u.expandAn.active {
		u.layoutCollapsed(gtx, size)
		return layout.Dimensions{Size: size}
	}

	inputH := gtx.Dp(inputRowDp + pillTopDp + 16) // 输入行 + 上 8 下 16 边距
	statusH := 0
	if u.statusText() != "" {
		statusH = gtx.Dp(statusChipDp + 8)
	}
	transH := size.Y - inputH - statusH
	if transH < 0 {
		transH = 0
	}
	// D54 消息揭示带：msgP 驱动带顶从 transH（全隐）升到 0（静息，与 §15.3 顶带重合），
	// 带底夹在 transH 内（不压状态行/输入行）。静态读，两遍 layout 同帧同值。
	_, msgP := u.expandProgress()
	u.bandTop, u.bandBottom = revealBand(msgP, transH, gtx.Dp(fadeBandDp))

	dims := layout.Stack{Alignment: layout.N}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			// 兜底背景（非 Windows 降级形态——Windows 上位图以 alpha 表达间隙，
			// 淡出源渲染跳过底色）+ 拖动把手带 = 状态行 + 输入栏（[transH, 窗底)）。
			// **转写区不注册拖层**（§15.3 只滚不拖窗；D63：gesture.Drag 超出 slop
			// 即 pointer.Grab 且先到先得——拖层若覆盖气泡，会在行选手势前抢走
			// Drag/Release 事件，选区永远无法延伸）。
			if !u.inFadePass {
				paint.Fill(gtx.Ops, windowBg)
			}
			st := clip.Rect{Min: image.Pt(0, transH), Max: size}.Push(gtx.Ops)
			u.drag.Add(gtx.Ops)
			st.Pop()
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if transH > 0 {
				u.transcript(gtx, size.X, transH)
			}
			if statusH > 0 {
				off := op.Offset(image.Pt(0, transH)).Push(gtx.Ops)
				u.statusChip(gtx, size.X, statusH, transH)
				off.Pop()
			}
			off := op.Offset(image.Pt(0, transH+statusH)).Push(gtx.Ops)
			u.inputBar(gtx, size.X, transH+statusH)
			off.Pop()
			return layout.Dimensions{Size: size}
		}),
	)
	return dims
}

// layoutCollapsed 收起态（§15.1 单组件"左键 logo 收起回球"）：只渲染 logo 悬浮球——
// 球位 = 展开态（无状态行）的 logo 位置（换形不跳动），窗口尺寸不变，球外区域位图
// alpha=0 透明且点击穿透；拖动把手 = 整窗背景层（事件只能落在球像素上），单击球
// （位移小于 dragClickSlackPx）再展开、移动则拖窗。
func (u *UI) layoutCollapsed(gtx layout.Context, size image.Point) {
	if !u.inFadePass {
		paint.Fill(gtx.Ops, windowBg)
	}
	dst := clip.Rect{Max: size}.Push(gtx.Ops)
	u.drag.Add(gtx.Ops)
	dst.Pop()

	// 球 = 展开态 logo 圆钮本身（D49 三段式：⌀ = 行高、x = 侧边距、y = 行内 logo 位）
	// → 换形不跳动；窗口尺寸不变，球外区域位图 alpha=0 透明。D50：ballRect 与停靠锚点同源。
	r := ballRect(size, gtx.Dp)
	drawLogo(gtx, r) // §15.2 logo 实装（品牌色圆钮 + 内嵌图标）
	u.record(r, r.Dx()/2, brandColor, image.Rectangle{Max: u.frameSize})
}

// updateScroll 滚动手势 + 当帧边界钳制 + 尾随（§15.3 流式内容贴底）。
// d>0 = 向下滚（往新内容）；d<0 = 上滚离开底部 → 停止尾随；滚回底部 → 恢复。
// 边界由本函数钳制（含 fling 溢出）——不交给 ScrollRange（见下方滞后说明）。
func (u *UI) updateScroll(gtx layout.Context, viewH, total int) {
	overflow := total - viewH
	if overflow < 0 {
		overflow = 0
	}
	// 滚动范围按**轴向**绑定（gesture.Scroll.Update 的 scrollx/scrolly 两参）：垂直手势
	// 累加 e.Scroll.Y，夹取范围必须给 scrolly——Gio 自家 layout/list.go 即此法
	//（`Axis==Vertical` 时把 bounds 换到 Y）。绑到 scrollx 会被 Y 侧 {0,0} 夹成 0，
	// 滚轮永不生效（实测缺陷：手工滚动从未生效）。
	// 范围只按 overflow 取、**不随 scrollPx 走**：过滤器在事件到来时取的是上一帧登记的
	// 边界，随位置走会滞后一帧——边界处反向的首格会被旧边界误夹成 0 而丢失；反正边界
	// 由下面的钳制负责，过滤器只需够宽（|d| ≤ overflow 一格不可能超过）。
	d := u.transcriptScroll.Update(gtx.Metric, gtx.Source, gtx.Now, gesture.Vertical,
		pointer.ScrollRange{}, // 横向不滚（X 夹到 0）
		pointer.ScrollRange{Min: -overflow, Max: overflow})
	u.scrollPx += d
	if u.scrollPx < 0 {
		u.scrollPx = 0
	}
	if u.scrollPx > overflow {
		u.scrollPx = overflow
	}
	if d < 0 {
		u.followTail = false
	} else if d > 0 && u.scrollPx >= overflow {
		u.followTail = true
	}
	if u.followTail {
		u.scrollPx = overflow // 尾随：新内容永远贴底
	}
}

// transcript 转写区：两遍布局（量高 → 滚动定界 → 绘制），**底部锚定**——新消息贴着
// 输入栏出现（§15.1"提交后在胶囊上方出现"），旧消息上滚进顶部淡出带渐隐（§15.3）；
// 尾随贴底，用户上滚即停跟随。手工纵排（需要每行绝对矩形登记形状，D44）。
func (u *UI) transcript(gtx layout.Context, w, h int) {
	items := u.frameItems()
	if len(items) == 0 {
		// 空态不渲染任何元素（悬浮球只剩输入栏，区域全透；转写浮层"提交后出现"，§15.1）。
		u.contentH = 0
		u.scrollPx = 0
		return
	}
	viewport := image.Rectangle{Max: image.Pt(w, h)}
	st := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	defer st.Pop()
	u.transcriptScroll.Add(gtx.Ops) // 视口滚动手势

	gap := gtx.Dp(rowGapDp)
	// ① 量高（文本只排一次，录宏供绘制复用）；行选状态按序挂接（D63）。
	rows := make([]measuredRow, len(items))
	total := 0
	for i := range items {
		rows[i] = u.measureRow(gtx, items[i], w, u.selFor(i))
		total += rows[i].height()
	}
	total += gap * (len(rows) - 1)

	// ② 滚动定界：手势 + 当帧真实内容高（无一帧滞后），尾随贴底。
	u.updateScroll(gtx, h, total)

	// ③ 绘制：底部锚定基线——内容矮时贴底（信息悬在输入栏上方），超出视口后
	// 顶出上沿、上滚进淡出带（base 归零后退化为标准滚动）。
	base := h - total
	if base < 0 {
		base = 0
	}
	y := base - u.scrollPx
	for i := range rows {
		y += u.paintRow(gtx, rows[i], w, y, viewport) + gap
	}
	u.contentH = total
}

// selFor 行选状态 get-or-create（D63）：按行序缓存——跨帧持久（选中态/焦点不丢），
// 行文本变化由 LabelStyle.Layout 的 SetText 幂等更新并自动清选区（流式行、会话切换
// 同路径）。仅事件循环 goroutine 调用。
func (u *UI) selFor(i int) *widget.Selectable {
	for len(u.selRows) <= i {
		u.selRows = append(u.selRows, new(widget.Selectable))
	}
	return u.selRows[i]
}

// measuredRow 量高后的转写行（文本录宏 + 样式令牌）。
type measuredRow struct {
	txt                op.CallOp
	dims               image.Point
	bg                 color.NRGBA
	radius, padX, padY int
	right              bool
}

// height 行总高（含上下内边距，px）。
func (m measuredRow) height() int { return m.dims.Y + 2*m.padY }

// measureRow 量高 + 取样式（文本录入宏，不在本步落 ops；sel = 行选状态，D63）。
func (u *UI) measureRow(gtx layout.Context, it blockView, w int, sel *widget.Selectable) measuredRow {
	label, bg, radius, rightAlign, bubble := u.rowStyle(gtx, it, sel)
	padX, padY := gtx.Dp(cardPadXDp), gtx.Dp(cardPadYDp)
	if bubble {
		padX, padY = gtx.Dp(bubblePadXDp), gtx.Dp(bubblePadYDp)
	}
	maxW := w - 2*gtx.Dp(sideMarginDp)
	if maxW < gtx.Dp(80) {
		maxW = gtx.Dp(80)
	}
	cs := gtx
	cs.Constraints = layout.Constraints{Min: image.Point{}, Max: image.Pt(maxW-2*padX, 1<<30)}
	m := op.Record(gtx.Ops)
	dims := label(cs)
	return measuredRow{
		txt: m.Stop(), dims: dims.Size, bg: bg, radius: radius,
		padX: padX, padY: padY, right: rightAlign,
	}
}

// paintRow 绘制底板 + 文本并登记形状；y 为视口内绝对坐标（可为负）。
// 返回行总高（px）。
func (u *UI) paintRow(gtx layout.Context, mr measuredRow, w, y int, viewport image.Rectangle) int {
	x := gtx.Dp(sideMarginDp)
	if mr.right {
		x = w - gtx.Dp(sideMarginDp) - mr.dims.X - 2*mr.padX
	}
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+mr.dims.X+2*mr.padX, y+mr.height()),
	}
	st := clip.UniformRRect(bgRect, mr.radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, mr.bg)
	inner := op.Offset(image.Pt(bgRect.Min.X+mr.padX, bgRect.Min.Y+mr.padY)).Push(gtx.Ops)
	mr.txt.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect, mr.radius, mr.bg, viewport)
	return bgRect.Dy()
}

// rowStyle 每种块的渲染样式：文本控件、底板色、圆角、是否右对齐、是否气泡。
// sel 非 nil 时文本行挂行选状态（D63）——除思考头部（元信息）外全部可选。
func (u *UI) rowStyle(gtx layout.Context, it blockView, sel *widget.Selectable) (layout.Widget, color.NRGBA, int, bool, bool) {
	radiusDp, cardR := gtx.Dp(radiusDp), gtx.Dp(cardRadiusDp)
	switch it.kind {
	case blockUser: // 用户气泡：品牌色底白字、右对齐（§15.3 双色气泡）
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.Color = whiteText
			s.State = sel
			return s.Layout(gtx)
		}, brandColor, radiusDp, true, true
	case blockAssistant: // 助手气泡：浅白底、左对齐
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.State = sel
			return s.Layout(gtx)
		}, pillBg, radiusDp, false, true
	case blockThinking: // 思考行：头部（元信息，不选）+ 正文
		header := "已思考"
		if it.live {
			header = "思考中…"
		} else if it.secs >= 0 {
			header = fmt.Sprintf("已思考 · %ds", it.secs)
		}
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					s := material.Caption(u.th, header)
					s.Color = textDim
					return s.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, it.text)
					s.Color = textDim
					s.State = sel
					return s.Layout(gtx)
				}),
			)
		}, cardThinking, cardR, false, false
	case blockTool:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(u.th, it.text)
			l.Color = textMuted
			l.State = sel
			return l.Layout(gtx)
		}, cardTool, cardR, false, false
	case blockNotice:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(u.th, it.text)
			l.Color = textNotice
			l.State = sel
			return l.Layout(gtx)
		}, cardNotice, cardR, false, false
	case blockError:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(u.th, it.text)
			l.Color = textError
			l.State = sel
			return l.Layout(gtx)
		}, cardError, cardR, false, false
	case blockSystem:
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(u.th, it.text)
			l.Color = textSystem
			l.State = sel
			return l.Layout(gtx)
		}, cardSystem, cardR, false, false
	case blockHeading: // markdown 标题（D65）：同气泡大字——h1 20sp 递减，Weight 求粗
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body1(u.th, it.text)
			s.TextSize = mdHeadingSp(u.th, it.level)
			s.Font.Weight = font.SemiBold
			s.State = sel
			return s.Layout(gtx)
		}, pillBg, radiusDp, false, true
	case blockCode: // markdown 代码块（D65）：等宽 + 深底卡；逐字保真不解转义
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.Font = monoFace
			s.TextSize = u.th.TextSize * 13.0 / 16.0
			s.State = sel
			return s.Layout(gtx)
		}, cardTool, cardR, false, true
	case blockRule: // markdown 分隔线（D65）：弱化短行——文本行承载，行机制零特例
		return func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(u.th, "· · · · · ·")
			l.Color = textDim
			l.State = sel
			return l.Layout(gtx)
		}, cardTool, cardR, false, false
	default: // blockPlain（Say/命令输出/提示）：底板浅白、正文默认色
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.State = sel
			return s.Layout(gtx)
		}, pillBg, cardR, false, false
	}
}

// statusChip 状态行 chip（§15.1：仅生成时显示"思考中/生成中 · 模型 · 权限档"），
// 右对齐小胶囊（自带底板 → 位图可见 + 可作拖拽把手）。
func (u *UI) statusChip(gtx layout.Context, w, h, absY int) {
	txt := u.statusText()
	if txt == "" {
		return
	}
	label := func(gtx layout.Context) layout.Dimensions {
		s := material.Caption(u.th, txt)
		s.Color = textMuted
		return s.Layout(gtx)
	}
	m := op.Record(gtx.Ops)
	dims := label(gtx)
	txtOp := m.Stop()
	padX := gtx.Dp(10)
	chipH := gtx.Dp(statusChipDp)
	x := w - gtx.Dp(sideMarginDp) - dims.Size.X - 2*padX
	y := (h - chipH) / 2
	bgRect := image.Rectangle{Min: image.Pt(x, y), Max: image.Pt(x+dims.Size.X+2*padX, y+chipH)}
	st := clip.UniformRRect(bgRect, chipH/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, pillBg)
	inner := op.Offset(image.Pt(bgRect.Min.X+padX, bgRect.Min.Y+(chipH-dims.Size.Y)/2)).Push(gtx.Ops)
	txtOp.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), chipH/2, pillBg, image.Rectangle{Max: u.frameSize})
}

// inputBar 输入栏（§15.2 骨架）：logo（拖拽把手）｜编辑器（确认态 = 提示行）｜
// 发送/停止；确认态整体切换为 [允许/拒绝] 按钮组（非模态）。
// inputBar 输入行（D49 三段式，§15.2）：[logo ⌀48] 12 [输入胶囊] 12 [send ⌀48]——三段等高
// 独立成形（各自 record → vis/羽化，间隙透明且点击穿透），中间胶囊吃掉全部剩余宽度
// （响应式：窗口宽变化只伸缩它，字号/圆钮尺寸不随窗口变）。坐标原点 = 输入行段左上，
// w = 窗口宽（均经 gtx.Dp 换算为物理 px）。D54：绘制顺序 = 胶囊 → 右钮 → logo（动画
// p=0 时 logo 盖住前两者），几何按展开进度插值。
func (u *UI) inputBar(gtx layout.Context, w, absY int) {
	rowH := gtx.Dp(inputRowDp)
	logo, pillEnd, sendEnd := inputRowRects(w, gtx.Dp(pillTopDp), rowH,
		gtx.Dp(inputGapDp), gtx.Dp(sideMarginDp))
	// D54 展开/收起动画：p=0 时胶囊与右钮都退化为 logo 位置的同尺寸圆（被 logo 盖住）、
	// p=1 = D49 终位；过冲 p>1（easeOutBack）沿同一式外推，让它们越出终位再回落——
	// 外推对贴边元素没有护栏，右钮会越过窗宽被窗边切平，故插值后夹回窗内（D57）。
	pill, send := lerpRowRects(logo, pillEnd, sendEnd, u.barP())
	pill, send = clampRowX(pill, w), clampRowX(send, w)
	clipRect := image.Rectangle{Max: u.frameSize}
	inAnim := u.expandAn.active

	// 输入胶囊（**先画**，D54 绘制顺序 = 胶囊 → 右钮 → logo）：内容**按终位整盒排版**
	// （原点 + 终宽都取 pillEnd → 文字图标不挤压、不位移），只按当前胶囊矩形裁剪 →
	// 胶囊生长即揭示内容。
	paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(pill, rowH/2).Op(gtx.Ops))
	pst := clip.UniformRRect(pill, rowH/2).Push(gtx.Ops)
	inner := op.Offset(pillEnd.Min).Push(gtx.Ops)
	gtxC := gtx
	gtxC.Constraints = layout.Exact(pillEnd.Size())
	u.pillContent(gtxC)
	inner.Pop()
	pst.Pop()
	u.record(pill.Add(image.Pt(0, absY)), rowH/2, pillBg, clipRect)

	// 右圆钮（恒在，几何不随状态变、动画期只平移，D49）：idle = 发送、生成中 = 停止、
	// 确认态 = 置灰不可点。
	fill, stop, cl := brandColor, false, &u.sendBtn
	switch {
	case u.m.confirm != nil:
		fill, cl = disabledCircle, nil
	case u.generating.Load():
		fill, stop, cl = textError, true, &u.stopBtn
	}
	off := op.Offset(send.Min).Push(gtx.Ops)
	gtxS := gtx
	gtxS.Constraints = layout.Exact(send.Size())
	actionCircle(gtxS, cl, fill, stop)
	off.Pop()
	u.record(send.Add(image.Pt(0, absY)), rowH/2, fill, clipRect)

	// logo 圆钮（**最后画**，D54）：p=0 时盖住胶囊/右钮 → 像素与 layoutCollapsed 的球
	// 一致（收尾切收起态无缝）；p=1 三段不重叠、顺序无副作用。悬停 tips、拖动移窗、单击互切。
	drawLogo(gtx, logo)
	gst := clip.Rect(logo).Push(gtx.Ops)
	u.logoHover.Add(gtx.Ops)
	u.logoDrag.Add(gtx.Ops)
	gst.Pop()
	u.record(logo.Add(image.Pt(0, absY)), rowH/2, brandColor, clipRect)

	// 悬浮 tips（§15.1 启动提示 / §15.2 发送·停止键）：独立底板元素随位图。
	// 显隐 = 事件态 × 光标直采（D53）：分层窗按像素 alpha 命中，光标移到透明像素/
	// 窗外后零 pointer 事件，Hover 收不到 Leave → 实测移开不消；直采离钮即熄，
	// tipShown 并入 heartbeatNeed 唤帧复评（D50 心跳底座复用）。动画期抑制（D54）。
	shown := false
	if !inAnim {
		cur := cursorPos()
		if u.logoHovered && u.overInputBtn(false, cur) {
			u.hoverTip(gtx, absY, startupHint, false)
			shown = true
		}
		if u.m.confirm == nil && u.sendBtn.Hovered() && !u.generating.Load() &&
			u.overInputBtn(true, cur) {
			u.hoverTip(gtx, absY, "发送", true)
			shown = true
		}
		if u.m.confirm == nil && u.generating.Load() && u.stopBtn.Hovered() &&
			u.overInputBtn(true, cur) {
			u.hoverTip(gtx, absY, "停止", true)
			shown = true
		}
	}
	u.tipShown = shown
}

// inputRowRects 三段几何（纯逻辑，可测，D49/§15.2）：拓扑 = 边距 | logo | 间隙 | 胶囊 |
// 间隙 | send | 边距——两圆钮贴边距定宽（⌀ = rowH），胶囊吃掉全部剩余宽度（`grow`）。
// 参数均为已换算的 px。
func inputRowRects(w, top, rowH, gap, margin int) (logo, pill, send image.Rectangle) {
	logo = image.Rect(margin, top, margin+rowH, top+rowH)
	send = image.Rect(w-margin-rowH, top, w-margin, top+rowH)
	pill = image.Rect(margin+rowH+gap, top, w-margin-rowH-gap, top+rowH)
	return
}

// startupHint 启动提示（装配根对 GUI 不再发 Say 启动行，§15.1）。
const startupHint = "Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成"

// tipBg 悬浮 tips 底色（实色——位图合成下元素必须自带底板）。
var tipBg = color.NRGBA{R: 0x26, G: 0x2A, B: 0x2E, A: 0xFF}

// hoverTip 悬浮提示卡片：画在胶囊上沿之上（输入栏段局部坐标，可为负 → 溢出到
// 转写区之上，无遮挡裁剪）；自带底板并登记形状。rightAlign=右对齐到胶囊内边距
// （发送键），false=左对齐（logo）。
func (u *UI) hoverTip(gtx layout.Context, absY int, text string, rightAlign bool) {
	label := func(gtx layout.Context) layout.Dimensions {
		s := material.Caption(u.th, text)
		s.Color = whiteText
		return s.Layout(gtx)
	}
	m := op.Record(gtx.Ops)
	dims := label(gtx)
	txt := m.Stop()
	padX, padY := gtx.Dp(10), gtx.Dp(6)
	radius := gtx.Dp(8)
	x := gtx.Dp(sideMarginDp)
	if rightAlign {
		x = gtx.Constraints.Max.X - gtx.Dp(sideMarginDp) - dims.Size.X - 2*padX
		if x < 0 {
			x = 0
		}
	}
	y := gtx.Dp(pillTopDp) - dims.Size.Y - 2*padY - gtx.Dp(6)
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+dims.Size.X+2*padX, y+dims.Size.Y+2*padY),
	}
	st := clip.UniformRRect(bgRect, radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, tipBg)
	inner := op.Offset(image.Pt(bgRect.Min.X+padX, bgRect.Min.Y+padY)).Push(gtx.Ops)
	txt.Add(gtx.Ops)
	inner.Pop()
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), radius, tipBg, image.Rectangle{Max: u.frameSize})
}

// pillContent 胶囊内横排（坐标原点 = 胶囊左上，约束 = 胶囊尺寸；D49/§15.2）：
// [pad16][附件槽20][12][文字 grow][12][展开槽20][12][动作区][pad16]。图标槽是 canvas 的
// 20dp 灰占位（附件/展开未实现、不可点，实现时启用）；动作区仅确认态有内容（[允许/拒绝]）——
// 生成中停止在右圆、胶囊内不占位，idle 时胶囊内只有占位文字。
func (u *UI) pillContent(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(inputPadDp), Right: unit.Dp(inputPadDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{
			Axis:      layout.Horizontal,
			Alignment: layout.Middle,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconSlot(gtx, drawPaperclip)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				inset := layout.Inset{Left: unit.Dp(inputGapDp), Right: unit.Dp(inputGapDp)}
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// 约束 Min.Y 被 Exact 拉满会把编辑器/提示行顶到盒顶（Flex Middle 对满高盒
					// 无效 → 文本视觉偏上）：放开 Min 让其返回自然行高，由 Flex 垂直居中（§15.2）。
					gtx.Constraints.Min.Y = 0
					if u.m.confirm != nil {
						return material.Body2(u.th, u.m.confirm.prompt).Layout(gtx)
					}
					ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
					ed.TextSize = unit.Sp(15)
					dims := ed.Layout(gtx)
					if u.inFadePass && u.caretFocused {
						u.drawCaret(gtx, dims) // D62：fade pass 无 Focused 态，caret 自绘
					}
					return dims
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return iconSlot(gtx, drawMaximize)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: unit.Dp(inputGapDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
						layout.Rigid(u.actionBtn(&u.allowBtn, "允许", brandColor)),
						layout.Rigid(u.actionBtn(&u.denyBtn, "拒绝", textMuted)),
					)
				})
			}),
		)
	})
}

// iconSlot 胶囊内 20dp 图标槽（D49/§15.2：canvas 20×20 灰占位、不可点——附件/展开实现时启用）。
func iconSlot(gtx layout.Context, draw func(gtx layout.Context, box image.Rectangle)) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	box := image.Rectangle{Max: image.Pt(d, d)}
	draw(gtx, box)
	return layout.Dimensions{Size: box.Size()}
}

// drawMaximize 展开图标占位（灰、不可点）：四角括号——canvas 原几何（内缩 2、臂长 6、
// 臂厚 2，稿内 1:1 即 dp），只把配色换成占位灰。
func drawMaximize(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	in, arm, th := gtx.Dp(2), gtx.Dp(6), gtx.Dp(2)
	x0, y0 := box.Min.X, box.Min.Y
	x1, y1 := box.Max.X, box.Max.Y
	for _, r := range []image.Rectangle{
		image.Rect(x0+in, y0+in, x0+in+arm, y0+in+th), // 左上·横
		image.Rect(x0+in, y0+in, x0+in+th, y0+in+arm), // 左上·竖
		image.Rect(x1-in-arm, y0+in, x1-in, y0+in+th), // 右上·横
		image.Rect(x1-in-th, y0+in, x1-in, y0+in+arm), // 右上·竖
		image.Rect(x0+in, y1-in-th, x0+in+arm, y1-in), // 左下·横
		image.Rect(x0+in, y1-in-arm, x0+in+th, y1-in), // 左下·竖
		image.Rect(x1-in-arm, y1-in-th, x1-in, y1-in), // 右下·横
		image.Rect(x1-in-th, y1-in-arm, x1-in, y1-in), // 右下·竖
	} {
		paint.FillShape(gtx.Ops, iconDim, clip.Rect(r).Op())
	}
}

// cubicArc 以 c 为圆心、从 p 到 q 的 90° 圆弧（三次贝塞尔逼近，k = 0.5523）：
// 切向 = 半径向量 ±90°（方向由 p→q 相对 c 的旋转取号），控制点 = 出点沿切向前伸、
// 入点沿切向回退。占位图标折线用。
func cubicArc(path *clip.Path, c, p, q f32.Point) {
	const k = 0.5523
	r1 := f32.Point{X: p.X - c.X, Y: p.Y - c.Y}
	r2 := f32.Point{X: q.X - c.X, Y: q.Y - c.Y}
	t1 := f32.Point{X: -r1.Y, Y: r1.X}
	t2 := f32.Point{X: -r2.Y, Y: r2.X}
	if r1.X*r2.Y-r1.Y*r2.X < 0 {
		t1 = f32.Point{X: r1.Y, Y: -r1.X}
		t2 = f32.Point{X: r2.Y, Y: -r2.X}
	}
	path.CubeTo(
		f32.Point{X: p.X + k*t1.X, Y: p.Y + k*t1.Y},
		f32.Point{X: q.X - k*t2.X, Y: q.Y - k*t2.Y},
		q,
	)
}

// drawPaperclip 附件图标占位（灰、不可点）：回形针折线 = 三段直线 + 三段半圆（24 视图的
// 中心线 (2.005,1.39)–(21.44,22) 等比 s=0.8 缩进 20dp 槽居中，描边 2dp 按稿）。
func drawPaperclip(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	const s = 0.8
	const bw, bh = 15.548, 16.488 // 缩后中心线尺寸
	ox := float32(box.Min.X) + (float32(box.Dx())-bw)/2 - 2.005*s
	oy := float32(box.Min.Y) + (float32(box.Dy())-bh)/2 - 1.39*s
	at := func(x, y float32) f32.Point { return f32.Point{X: ox + x*s, Y: oy + y*s} }

	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(at(21.44, 11.05)) // 外端（右侧中）
	p.LineTo(at(12.25, 20.24)) // 沿外线下行到底
	c1 := at(8.005, 16)        // 大半圆 r=6（底 → 左）
	m1 := at(3.762, 20.243)
	cubicArc(&p, c1, at(12.25, 20.24), m1)
	cubicArc(&p, c1, m1, at(3.76, 11.75))
	p.LineTo(at(12.95, 2.56)) // 上行到顶
	c2 := at(15.78, 5.39)     // 中半圆 r=4（顶 → 右）
	m2 := at(18.61, 2.56)
	cubicArc(&p, c2, at(12.95, 2.56), m2)
	cubicArc(&p, c2, m2, at(18.61, 8.22))
	p.LineTo(at(9.41, 17.41)) // 内线斜下
	c3 := at(7.995, 15.995)   // 小半圆 r=2（内底 → 内左）
	m3 := at(6.58, 17.41)
	cubicArc(&p, c3, at(9.41, 17.41), m3)
	cubicArc(&p, c3, m3, at(6.58, 14.58))
	p.LineTo(at(15.07, 6.1)) // 内端（收尾）
	paint.FillShape(gtx.Ops, iconDim, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// record 登记可见元素（D44–D48、D62）：保存真实轮廓 + 可见裁剪区 + 底色，全帧合成的
// vis 覆盖度（fadeFrame）由此推导。零半径、裁剪后为空的元素不登记；带内元素**必须登记**
// （D62：带渐变只作用于登记形状的像素——取代旧「带整行单绘」机制）。
func (u *UI) record(abs image.Rectangle, radius int, fill color.NRGBA, clipRect image.Rectangle) {
	if radius <= 0 || abs.Empty() {
		return
	}
	vis := abs.Intersect(clipRect)
	if vis.Empty() {
		return
	}
	u.shapes = append(u.shapes, drawShape{outline: abs, clip: clipRect, radius: radius, fill: fill})
}

// featherWidth 元素边缘渐隐带的宽（px，D47/D48 响应式）：按元素**短边**成比例，
// 夹到 [Dp(featherMinDp), Dp(featherMaxDp)]，且不超过短边的 1/3（再大元素整体被吃掉）
// ——元素尺寸/窗口缩放/DPI 变化时自动跟随，不再用固定 px。纯逻辑，可测。
func featherWidth(m unit.Metric, w, h int) int {
	short := w
	if h < short {
		short = h
	}
	f := int(float64(short)*featherRatio + 0.5)
	if lo := m.Dp(featherMinDp); f < lo {
		f = lo
	}
	if hi := m.Dp(featherMaxDp); f > hi {
		f = hi
	}
	if lim := short / 3; f > lim {
		f = lim
	}
	return f
}

// blockView 渲染期块视图（live = 本帧实时追加的思考/草稿，非定稿块；
// level = 标题级别，markdown 派生行专用，D65）。
type blockView struct {
	kind  blockKind
	text  string
	secs  int
	live  bool
	level int
}

// frameItems 定稿块 + 实时思考/草稿（流式可见；对齐 D33"流式原样、定稿渲染"口径）。
// 助手定稿块经 markdown 展开为多行（D65），live 草稿原样单行。
func (u *UI) frameItems() []blockView {
	items := make([]blockView, 0, len(u.m.blocks)+2)
	for _, b := range u.m.blocks {
		if b.kind == blockAssistant {
			items = append(items, u.mdViews(b.text)...)
			continue
		}
		items = append(items, blockView{kind: b.kind, text: b.text, secs: b.secs})
	}
	if u.m.think.Len() > 0 {
		items = append(items, blockView{kind: blockThinking, text: u.m.think.String(), live: true})
	}
	if u.m.drafting || u.m.draft.Len() > 0 {
		items = append(items, blockView{
			kind: blockAssistant,
			text: strings.TrimRight(u.m.draft.String(), "\n"),
			live: true,
		})
	}
	return items
}

// statusText 状态行文本（数据源同 uitui Status 回调：Model/Level/Effort 现取）。
func (u *UI) statusText() string {
	if !u.generating.Load() {
		return ""
	}
	phase := "生成中"
	if u.m.think.Len() > 0 {
		phase = "思考中"
	}
	parts := []string{phase}
	if u.opts.Status != nil {
		st := u.opts.Status()
		if st.Model != "" {
			parts = append(parts, st.Model)
		}
		if st.Level != "" {
			parts = append(parts, st.Level)
		}
		if st.Effort != "" {
			parts = append(parts, st.Effort)
		}
	}
	return strings.Join(parts, " · ")
}

// updateEditor 消费编辑器事件（Enter → SubmitEvent → 提交）。material.Editor.Layout
// 内部也会 Update 但丢弃返回的 SubmitEvent——必须在渲染前自己循环取尽。
func (u *UI) updateEditor(gtx layout.Context) {
	if u.m.confirm != nil {
		return // 确认态：输入栏是按钮组，编辑器不消费按键（§15.2）
	}
	if !u.inFadePass {
		u.caretFocused = gtx.Focused(&u.editor) // 真窗 pass 捕获（fade pass 零 Source 恒 false）
	}
	// 编辑器不再每帧回投常驻焦点（D63）：无条件的 FocusCmd 会与行获焦竞态——
	// Selectable.Focused() 滞后一帧，编辑器会把刚点选的行的焦点抢回（选区隐没、
	// Ctrl+C 失效）。焦点来源收口为：focusPending（呼出/展开，anim.go）、编辑器
	// 自带点击取焦、newUI 初始焦点。
	for {
		evt, ok := u.editor.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := evt.(widget.SubmitEvent); isSubmit {
			u.submitEditor()
		}
	}
}

// drawCaret 淡出源渲染的 caret 自绘（D62）：material.Editor 的 caret 由 gtx.Focused
// 门控（零值 Source 恒 false，见 editor.layout），fade pass 里编辑器不画 caret——按
// CaretCoords（相对编辑器原点、y = 基线）补画一根 2px 实心杆，高度按行高近似
// （CaretInfo 未导出，asc/desc 用 0.8/0.2 行高拆分）。常显不闪：闪烁由真窗 pass 的
// InvalidateCmd 驱动，fade pass 无事件源、帧率随唤帧走，跟闪会冻在半相位。
func (u *UI) drawCaret(gtx layout.Context, dims layout.Dimensions) {
	c := u.editor.CaretCoords()
	asc := int(float64(dims.Size.Y) * 0.8)
	rect := image.Rect(int(c.X)-1, int(c.Y)-asc, int(c.X)+1, int(c.Y)+dims.Size.Y-asc)
	rect.Min.X = max(rect.Min.X, 0) // 空文本时 caret 贴原点，杆宽一半越出编辑器盒
	if rect.Empty() {
		return
	}
	cl := clip.Rect(rect).Push(gtx.Ops) // 只裁 caret 杆——paint.Fill 覆盖整个当前裁剪区
	paint.Fill(gtx.Ops, u.th.Palette.Fg)
	cl.Pop()
}

// submitEditor 提交编辑器内容（发送键与 Enter 同路）；确认态由 model.submit 路由应答。
func (u *UI) submitEditor() {
	text := strings.TrimSpace(u.editor.Text())
	if text == "" {
		return
	}
	u.editor.SetText("")
	u.m.submit(text)
}

// updateClicks 控件行为：发送/停止/允许/拒绝（logo 手势与悬停在 updateLogo）。
// D54：动画期间几何在动（右钮半程在飞），不接受点击。
func (u *UI) updateClicks(gtx layout.Context) {
	if u.expandAn.active {
		return
	}
	if u.sendBtn.Clicked(gtx) {
		u.submitEditor()
	}
	if u.stopBtn.Clicked(gtx) {
		u.interruptNow() // 生成中发送键变停止键（§15.2）
	}
	if u.allowBtn.Clicked(gtx) {
		u.m.replyConfirm(true)
	}
	if u.denyBtn.Clicked(gtx) {
		u.m.replyConfirm(false)
	}
}

// updateDrag 拖动定位（§15.6 铁律 2：按下记「窗口左上角 + 光标屏幕坐标」按差值
// 定位——窗口移动会改变指针本地坐标，本地增量法与移动互为反馈会回弹）。
// 背景把手 = 输入栏空白/状态行/收起态整窗（气泡区是滚动区，§15.1）；
// 收起态单击球（位移小于阈值）= 再展开。
func (u *UI) updateDrag(gtx layout.Context) {
	for {
		ev, ok := u.drag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			u.undockInstant() // D50：按下即脱离停靠（拖动/点击都从贴齐亮态起）
			u.beginDrag()
		case pointer.Drag:
			u.moveDrag()
		case pointer.Release, pointer.Cancel:
			was := u.dragging
			u.endDrag()
			// 收起态：单击球 = 再展开；移动 = 拖窗（§15.1）。
			// D54：动画中本把手与 logo 钮重叠，展开语义归 logo 的互切（防背景误触发反向）。
			if ev.Kind == pointer.Release && was && u.collapsed &&
				!u.expandAn.active && u.clickHeld() {
				u.beginExpand() // 展开即入焦点（含）
			}
		}
	}
}

// updateLogo logo 圆钮手势（§15.1 把手含 logo）：悬停驱动启动 tips；拖动移窗；
// 单击（位移小于 dragClickSlackPx）= 收起回球。收起态圆钮区不存在（事件归背景
// 把手），本循环空转并清悬停态。
func (u *UI) updateLogo(gtx layout.Context) {
	u.logoHovered = u.logoHover.Update(gtx.Source)
	if u.collapsed {
		u.logoHovered = false
	}
	for {
		ev, ok := u.logoDrag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			u.beginDrag()
		case pointer.Drag:
			u.moveDrag()
		case pointer.Release, pointer.Cancel:
			was := u.dragging
			u.endDrag()
			if ev.Kind == pointer.Release && was && u.clickHeld() {
				u.toggleExpand() // 左键 logo = 互切（§15.1；D54 动画中反向续跑）
			}
		}
	}
}

// beginDrag 记录拖动基准（按下；窗口未就绪则忽略本次触发）。
// beginDrag 记录拖动基准（窗口左上角 + 光标位置，铁律 2 绝对跟踪）。
func (u *UI) beginDrag() {
	if rc, ok := windowRectPx(); ok {
		u.dragWin0 = point{x: rc.left, y: rc.top}
		u.dragCur0 = cursorPos()
		u.dragging = true
	}
}

// moveDrag 主窗跟随光标（拖动中，铁律 2 绝对跟踪）；可见锚点实时夹取（D50 不出桌面）。
func (u *UI) moveDrag() {
	if !u.dragging {
		return
	}
	cur := cursorPos()
	x := u.dragWin0.x + (cur.x - u.dragCur0.x)
	y := u.dragWin0.y + (cur.y - u.dragCur0.y)
	u.x, u.y = u.clampPos(x, y)
	u.requestMove() // D55：commitWinGeom 一拍提交（本帧内只记账）
}

// endDrag 抬起/取消收尾：锚点夹取 + 四边吸附贴齐（D50）+ 持久化位置。
// 按下时已脱离停靠（undockInstant），故落盘恒为未停靠。
// 纯点击（位移 ≤ dragClickSlackPx）跳过夹取/吸附：「点击脱离停靠」把窗口落在半出屏
// 贴边位（球锚点越界合法），若再按抬手时的态夹一次，展开态整窗锚点会把它推离边缘、
// 收起后球离边超 snapDp → 布防/停靠断链（D50 实测缺陷修订，§15.1）。
func (u *UI) endDrag() {
	if u.dragging {
		if !u.clickHeld() {
			u.x, u.y = u.clampPos(u.x, u.y)
			if a, ok := u.anchorFor(); ok {
				pos := point{x: u.x, y: u.y}
				if work, wok := platformWorkArea(anchorCenter(pos, a)); wok {
					if d := snapDelta(pos, a, work, int32(u.frameMetric.Dp(snapDp))); d != (point{}) {
						u.x += d.x
						u.y += d.y
					}
				}
			}
			u.requestMove() // D55：吸附后落位记账，commitWinGeom 一拍提交
		}
		// 抬手（点击/拖动）=「曾悬停」的证据：直接布防（D50 拍板"拖到可停靠区移开也重停"），
		// 贴边由下一瞬的 evalDockFrame 校验 edge，不贴边/展开态自然清掉。
		u.dockArm = true
		u.savePosRec("")
	}
	u.dragging = false
}

// clickHeld 抬起时位移小于阈值 = 单击（与拖窗判定互斥）。
func (u *UI) clickHeld() bool {
	cur := cursorPos()
	dx, dy := cur.x-u.dragCur0.x, cur.y-u.dragCur0.y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx <= dragClickSlackPx && dy <= dragClickSlackPx
}

// actionBtn 动作键（停止/允许/拒绝；主题色底白字）。
func (u *UI) actionBtn(cl *widget.Clickable, label string, bg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.Button(u.th, cl, label)
		b.Background = bg
		b.Color = whiteText
		b.TextSize = unit.Sp(14)
		b.Inset = layout.Inset{
			Left: unit.Dp(14), Right: unit.Dp(14), Top: unit.Dp(8), Bottom: unit.Dp(8),
		}
		return b.Layout(gtx)
	}
}

// actionCircle 右侧主操作圆钮（D49/§15.2：⌀ = 行高，三态恒在同一位置、几何不随状态变）：
// idle = 发送（向上箭头）、生成中 = 停止（方块图标）、确认态 = 置灰（cl=nil 不可点）。
// 坐标 = 当前原点（调用方偏移到 send 位）；Gio Clickable 在原点登记面积（无居中），
// 走同一 updateClicks 路径。
func actionCircle(gtx layout.Context, cl *widget.Clickable, fill color.NRGBA, stop bool) layout.Dimensions {
	d := gtx.Dp(inputRowDp)
	draw := func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, d/2).Op(gtx.Ops))
		if stop {
			// 停止 = 圆角方块。
			s := d / 3
			r := image.Rectangle{
				Min: image.Pt((d-s)/2, (d-s)/2),
				Max: image.Pt((d+s)/2, (d+s)/2),
			}
			paint.FillShape(gtx.Ops, whiteText, clip.UniformRRect(r, gtx.Dp(2)).Op(gtx.Ops))
			return layout.Dimensions{Size: image.Pt(d, d)}
		}
		// 向上箭头 = 竖杆 + 人字头（白色描边）。
		cx, cy := float32(d)/2, float32(d)/2
		a := float32(d) * 0.18
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(cx, cy+a)) // 竖杆
		p.LineTo(f32.Pt(cx, cy-a))
		p.MoveTo(f32.Pt(cx-a, cy)) // 人字头
		p.LineTo(f32.Pt(cx, cy-a))
		p.LineTo(f32.Pt(cx+a, cy))
		paint.FillShape(gtx.Ops, whiteText,
			clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
		return layout.Dimensions{Size: image.Pt(d, d)}
	}
	if cl == nil {
		return draw(gtx)
	}
	return cl.Layout(gtx, draw)
}
