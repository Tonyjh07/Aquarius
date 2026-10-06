package uigui

import (
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"image"
	"image/color"
	"io"
	"math"
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
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/input"
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
	"github.com/Tonyjh07/Aquarius/internal/port"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
	// D90 咽喉点（§15.8）：缩放只在此一处进布局——layout 与 fadeCompose 二次调用都
	// 消费 frameMetric（已缩放），不会重复缩放；窗口 px 画布与 Constraints 不动。
	gtx.Metric = u.zoomedMetric(gtx.Metric)
	u.flushCopy(gtx) // D92：菜单分发的复制请求在此（Gio 帧）落剪贴板
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

// onHWND Win32ViewEvent 投递的窗口句柄：启动防闪（D78 挂接即隐藏、首帧 ULW 成功才
// 揭示）+ 置顶断言 + 位置记忆恢复（§15.1/D44/D62）。统一半透明不在此下发（D62：首帧
// ULW 随 SourceConstantAlpha 生效）。
func (u *UI) onHWND(h uintptr) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	atomic.StoreUintptr(&mainHWND, h)
	// D78 启动防闪：挂接即隐藏（最早可接管点——Gio Configure(ShowWindow) 早于本事件、
	// 无可挂钩点，其间亚帧间隙接受）并置揭示待定；首帧 ULW 提交成功才揭示（fadePresent）。
	// 【顺序敏感】先 SW_HIDE 再挂 WS_EX_LAYERED：层样式在**可见态**挂接、随后首帧 ULW 前
	// 被隐藏，UpdateLayeredWindow 将永久失败（errno=87，重新显示也不恢复）——revealPending
	// 永不清零、窗口永不揭示且呼出门死锁（实机「找不到窗口」）。隐藏态挂接则全链路正常。
	u.revealPending.Store(true)
	hideUntilFirstPresent(h)
	ensureLayeredStyle(h)  // 分层样式（D62 双保险；非 Windows no-op）——须在隐藏后挂
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
	// D90：配置像素尺寸先落（app.Size 只收 dp、建窗期 DPI 未就绪无法换算，px 口径在
	// 挂接点经窗口线程补投）；同步等待完成后，位置恢复/停靠重算按落定矩形取值。
	u.applyConfiguredSize()
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
			// 与拖动夹取同口径（重启不跳位）；frameMetric 未就绪 → restorePx 直查 DPI（D90）。
			if (p.Docked == "left" || p.Docked == "right") && u.restoreDock(p, rc) {
				return
			}
			nx, ny := p.X, p.Y
			if work, wok := platformWorkArea(point{x: p.X + w/2, y: p.Y + ht/2}); wok {
				px := restorePx(h)
				top := int(ht) - int(px(inputRowBandDp)) + int(px(pillTopDp))
				a := image.Rect(int(px(sideMarginDp)), top, int(w)-int(px(sideMarginDp)), top+int(px(inputRowDp)))
				c := clampAnchor(point{x: p.X, y: p.Y}, a, work)
				nx, ny = c.x, c.y
			}
			moveWindowTo(nx, ny)
			u.x, u.y = nx, ny
		}
	}
	// 首帧 ULW 提交成功窗口才揭示（D78：挂接即隐藏、revealPending 至此清零）。
	// 本函数不在帧内，位移直接下发（帧内的改动一律记账，见 requestMove）。
}

// applyConfiguredSize 启动期把配置像素尺寸落到 OS 窗口（D90/§15.8）：WindowWidth/
// Height 均非 0 才生效（0 = 缺省 dp 建窗，现行为）。夹取按挂接点实测 DPI × 当前缩放。
func (u *UI) applyConfiguredSize() {
	if u.opts.WindowWidth <= 0 || u.opts.WindowHeight <= 0 || u.hwnd == 0 {
		return
	}
	w, h := clampWindowPx(u.opts.WindowWidth, u.opts.WindowHeight,
		platformWindowDPI(u.hwnd), u.zoomLoad().scale)
	resizeWindowTo(int32(w), int32(h))
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
		u.updateSel(gtx)    // D91 跨块拖选消费者：先于编辑器（Ctrl+C 抢先，焦点纪律同帧生效）
		u.updateCompl(gtx)  // D103 补全浮层消费者：先于编辑器（Enter 接管 SubmitEvent、Esc 让渡）
		u.updateEditor(gtx) // 收起态不消费按键（编辑器不可见，防隐形收字）
	}
	u.updateClicks(gtx)
	u.updateDrag(gtx)
	u.updateLogo(gtx)
	if !u.collapsed {
		u.updateBubbleRight(gtx) // D92：气泡右键消费者（事件仅展开态注册，收起态无）
	}
	// D71 滚轮手势钉点复评：收起/停靠/光标移位 = 手势结束 → 解除钉点、恢复逐像素
	// 穿透。事件静默时无帧可跑：钉点残留原像素至下一帧，任意本窗事件到达即自愈。
	if u.wheelCap && (u.collapsed || u.docked || cursorPos() != u.wheelAnchor) {
		u.wheelCap = false
	}
	switch {
	case u.expandAn.active:
		// D54：动画期间不做停靠评估（球位在动）。布防只在**展开方向**清；收起方向
		// 保留 endDrag 抬手时的布防证据（「曾悬停」，§15.1）——收起动画 540ms 内
		// 光标通常已移开，清掉则动画结束首帧重新布防要求光标在球上 → 不悬停就
		// 永不停靠（收回后不自动吸附/停靠的实测缺陷）。
		if u.expandAn.expand {
			u.dockArm = false
		}
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
	// D79：底部矮带缺省**关**（收起态无转写区）——区间空即无带；展开路径按转写区底重设。
	u.bandLowTop, u.bandLowEnd = size.Y, size.Y

	// D54：collapsed 是逻辑态、即时翻转；动画中走全量 layout 带几何插值（收尾 barP=0
	// 时几何 == layoutCollapsed 的球，切换无缝）。
	if u.collapsed && !u.expandAn.active {
		u.layoutCollapsed(gtx, size)
		return layout.Dimensions{Size: size}
	}

	// 输入行带（D90 单源）+ 展开态增高（D106：与 inputBar 共用 inputExtra 单源，两遍
	// layout 同帧一致）；statusH 照旧在带之上。
	inputH := gtx.Dp(inputRowBandDp) + u.inputExtraPx(gtx)
	statusH := 0
	if u.statusText() != "" {
		statusH = gtx.Dp(statusChipDp + statusGapDp)
	}
	transH := size.Y - inputH - statusH
	if transH < 0 {
		transH = 0
	}
	// D54 消息揭示带：msgP 驱动带顶从 transH（全隐）升到 0（静息，与 §15.3 顶带重合），
	// 带底夹在 transH 内（不压状态行/输入行）。静态读，两遍 layout 同帧同值。
	_, msgP := u.expandProgress()
	bandPx, lowPx := u.bandHeightsClamped(gtx, transH) // D90：极矮窗带高夹 ≤ transH/4
	u.bandTop, u.bandBottom = revealBand(msgP, transH, bandPx)
	// D79 底部矮带：**常驻转写区底缘** [transH−带高, transH)——底缘内容渐隐不硬切；
	// 带止于 transH（状态行/输入栏在其下，不受淡化）。揭示动画（D54）期间照常驻留。
	u.bandLowTop, u.bandLowEnd = transH-lowPx, transH

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

// bandHeightsClamped 当前帧顶/底带高（D90）：极矮窗（transH ≯ 0 或带高 > transH/4）时
// 带高夹 ≤ transH/4——防整片转写区被带吞掉（fade 层 clampedLowBand 预留的尺寸约束
// 归口在此收口）。带写入（layout bandTop/bandBottom/bandLowTop/bandLowEnd）与头部/
// 尾部留白（transcript pad/lowPad）同源取值，两遍 layout 同帧同值。
func (u *UI) bandHeightsClamped(gtx layout.Context, transH int) (top, low int) {
	if transH <= 0 {
		return 0, 0
	}
	top, low = gtx.Dp(fadeBandDp), gtx.Dp(fadeBandBottomDp)
	if maxBand := transH / 4; top > maxBand {
		top = maxBand
	}
	if maxBand := transH / 4; low > maxBand {
		low = maxBand
	}
	return top, low
}

// clampWindowPx 窗口像素夹取（D90）：粗界 [200,3840]×[200,2160]，再按布局地板
// （layoutFloorDp × DPI × scale）抬下限。纯函数；config 侧 DPI/缩放未知时传 1,1（仅粗界）。
func clampWindowPx(w, h int, dpi, scale float64) (int, int) {
	w = max(winPxMinW, min(w, winPxMaxW))
	h = max(winPxMinH, min(h, winPxMaxH))
	if floor := int(math.Round(layoutFloorDp * dpi * scale)); floor > 0 {
		w = max(w, floor)
		h = max(h, floor)
	}
	return w, h
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
	// D72：收起态热区 = 球矩形（注册整窗 clip——事件本只落球像素，D62；抬起按球
	// 矩形门控，拖到窗内透明处/窗外不弹）。
	u.logoRight.add(gtx.Ops, ballRect(size, gtx.Dp))
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
	if d != 0 {
		// D71 滚轮手势钉点：真有增量 = 手势进行中；锚点随当前光标刷新（本帧顶部
		// 复评若已因移位解除，此处即在新位置重挂）。钉点由 fadePresent 落笔。
		u.wheelCap = true
		u.wheelAnchor = cursorPos()
	}
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
		u.transH = 0
		u.keyRects = u.keyRects[:0]       // 键清零 → 指针值（0）与任何选区指纹都不同，选区下帧自愈
		u.bubbleRects = u.bubbleRects[:0] // D92：气泡矩形随帧复位
		u.keyFp = 0
		return
	}
	u.transH = h // D102：贴边自动滚动判缘（消费者阶段读上一帧值）
	viewport := image.Rectangle{Max: image.Pt(w, h)}
	st := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	defer st.Pop()
	u.transcriptScroll.Add(gtx.Ops) // 视口滚动手势
	// D91 ① 跨块拖选观察者热区：转写视口整片（clip 无平移 = 窗口系坐标，与行 keyRects
	// 同一坐标系；注册于同组内与滚动手势同一命中链——根级注册会被行组命中跳过）。
	event.Op(gtx.Ops, &u.sel)
	// D92 气泡右键手势：同组命中链（同 D91 ①——根级注册不可达）；坐标 = 窗口系。
	u.bubbleRight.add(gtx.Ops)

	gap := gtx.Dp(rowGapDp)
	// ① 量高（文本只排一次，录宏供绘制复用）；行选几何按（行,块）双键挂接（D63/D66/
	// D91 ②）：线性序号 base 随条目 selCount 累计，rowSel 消费于 measureRow 内
	//（note 记行盒、layout 画高亮）。跨度缓冲由 updateSel 在消费者阶段从上帧
	// keyRects 预算——此处量期只读。
	rows := make([]measuredRow, len(items))
	total, totalKeys := 0, 0
	for i := range items {
		items[i].keyBase = totalKeys // D92：整条复制按键区间取渲染文本
		rs := &rowSel{u: u, base: totalKeys, n: items[i].selCount()}
		rows[i] = u.measureRow(gtx, items[i], w, rs)
		total += rows[i].height()
		totalKeys += rs.n
	}
	total += gap * (len(rows) - 1)

	// D74 顶部 headroom：滚动内容头部垫一个**淡出带高**的空白（计入 total → 参与
	// overflow/钳制与 base）。滚到最上（scrollPx=0）时首行落在 y ≥ pad、完整在带下
	// 可读——否则首行困在带内而 scrollPx 不可为负，永远半透明（= 淡出遮挡内容）。
	// 空白随内容滚（非视口固定留白，否则渐隐作用于空白而失效）；短内容时 pad 一并
	// 制造 overflow，拥挤内容同样能往上滚出让出带外。
	pad, lowPad := u.bandHeightsClamped(gtx, h) // D90：与 layout 带写入同源（极矮窗夹取一致）
	total += pad

	// D79 尾部留白：滚动内容尾部垫一个**底部矮带高**的空白（等高、单源、随内容滚，
	// D74 同款）——贴底/尾随时末行底 = h − lowPad，正好停在底带**之外**（带里只剩
	// 空白，渐隐作用于内容而非留白）；上滚离底（scrollPx=0）时内容延伸进带内 → 底缘
	// 渐隐而非硬切。计入 total → 同样参与 overflow/钳制与 base。
	total += lowPad

	// ② 滚动定界：手势 + 当帧真实内容高（无一帧滞后），尾随贴底。
	u.updateScroll(gtx, h, total)

	// ③ 绘制：底部锚定基线——内容矮时贴底（信息悬在输入栏上方），超出视口后
	// 顶出上沿、上滚进淡出带（base 归零后退化为标准滚动）。起点 +pad 抵消头部
	// 空白，底钉只整体上移一个尾部留白（base + pad + 行高总和 + lowPad == h →
	// 尾随时末行底 = h − lowPad，恰在底带之上，D79）。
	base := h - total
	if base < 0 {
		base = 0
	}
	// 键几何落盘（D91 ②）：本帧量期记录 → 行原点定后译窗口系；复位[:0]保容量
	//（行盒缓冲逐帧复用），指纹流式喂哈希（行序 = 键序）。消费者阶段（下一帧
	// updateSel）读到的即上一帧整帧几何——一帧陈旧是既定口径。
	u.keyRects = u.keyRects[:0]
	u.bubbleRects = u.bubbleRects[:0] // D92：气泡矩形随帧复位（paint 期重登记）
	fph := fnv.New64a()
	y := base - u.scrollPx + pad
	for i := range rows {
		rh := u.paintRow(gtx, rows[i], items[i], w, y, viewport, fph)
		y += rh + gap
	}
	u.keyRects = u.keyRects[:totalKeys] // 结构缩小时清尾（writeKeyRects 按 base 定位不越界）
	u.keyFp = fph.Sum64()
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

// measuredRow 量高后的转写行（文本录宏 + 样式令牌；sel = 行选几何挂接件，
// D91 ②——paint 期译 keyRects）。
type measuredRow struct {
	txt                op.CallOp
	dims               image.Point
	bg                 color.NRGBA
	radius, padX, padY int
	right              bool
	sel                *rowSel
}

// height 行总高（含上下内边距，px）。
func (m measuredRow) height() int { return m.dims.Y + 2*m.padY }

// measureRow 量高 + 取样式（文本录入宏，不在本步落 ops；rs = 行选几何挂接件，
// D63/D66 双键：k 为复合行内块序，nil = 测试直通）。
func (u *UI) measureRow(gtx layout.Context, it blockView, w int, rs *rowSel) measuredRow {
	label, bg, radius, rightAlign, bubble := u.rowStyle(gtx, it, rs)
	padX, padY := gtx.Dp(cardPadXDp), gtx.Dp(cardPadYDp)
	if bubble {
		padX, padY = gtx.Dp(bubblePadXDp), gtx.Dp(bubblePadYDp)
	}
	maxW := w - 2*gtx.Dp(sideMarginDp)
	if maxW < gtx.Dp(bubbleMinWDp) {
		maxW = gtx.Dp(bubbleMinWDp)
	}
	cs := gtx
	cs.Constraints = layout.Constraints{Min: image.Point{}, Max: image.Pt(maxW-2*padX, 1<<30)}
	m := op.Record(gtx.Ops)
	dims := label(cs)
	return measuredRow{
		txt: m.Stop(), dims: dims.Size, bg: bg, radius: radius,
		padX: padX, padY: padY, right: rightAlign, sel: rs,
	}
}

// paintRow 绘制底板 + 文本并登记形状；y 为视口内绝对坐标（可为负）。
// 顺带把本行量期记录译成窗口系键几何（D91 ②）并喂结构指纹；menuable 条目登记
// 气泡底板矩形（D92 右键命中）。返回行总高（px）。
func (u *UI) paintRow(gtx layout.Context, mr measuredRow, it blockView, w, y int, viewport image.Rectangle, fph hash.Hash64) int {
	x := gtx.Dp(sideMarginDp)
	if mr.right {
		x = w - gtx.Dp(sideMarginDp) - mr.dims.X - 2*mr.padX
	}
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+mr.dims.X+2*mr.padX, y+mr.height()),
	}
	if mr.sel != nil {
		u.writeKeyRects(mr.sel, image.Pt(bgRect.Min.X+mr.padX, bgRect.Min.Y+mr.padY), fph)
	}
	if it.menuable() {
		u.bubbleRects = append(u.bubbleRects, bubbleHit{
			rect: bgRect, bi: it.bi, id: it.id, kind: it.kind,
			keyBase: it.keyBase, keyN: it.selCount(),
		})
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
// rs 行选几何挂接件（D63；D66 双键 k = 复合行内块序，D91 ② 记行盒/画高亮）——
// 除思考头部（元信息）外全部可选；nil 挂接件 = 测试直通。
func (u *UI) rowStyle(gtx layout.Context, it blockView, rs *rowSel) (layout.Widget, color.NRGBA, int, bool, bool) {
	radiusDp, cardR := gtx.Dp(radiusDp), gtx.Dp(cardRadiusDp)
	switch it.kind {
	case blockUser: // 用户气泡：品牌色底白字、右对齐（§15.3 双色气泡）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, it.text)
				s.Color = whiteText
				s.State = rs.sel(0)
				return s.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, brandColor, radiusDp, true, true
	case blockAssistant: // 助手气泡：浅白底、左对齐；markdown 复合行 = 垂直多块共底板（D66）
		blocks := it.md
		if len(blocks) == 0 {
			blocks = []mdBlock{{kind: mdPara, text: it.text}} // live 草稿/兜底：纯文本单块
		}
		// D91 ② 手排纵列：量期即知块偏移（Flex 的 Offset 回放期才施加，量期不可知
		// ——行盒/高亮定位须块内偏移）；尺寸口径与 Flex Rigid 逐像素一致。
		return func(gtx layout.Context) layout.Dimensions {
			vs := beginVStack(gtx)
			for k, b := range blocks {
				if k > 0 {
					vs.gap(mdBlockGapDp)
				}
				off := image.Pt(0, vs.y) // add 的 offY 即当前 y（先 gap 后取）
				vs.add(u.mdBlockWidget(b, rs, k, off))
			}
			return vs.dims()
		}, pillBg, radiusDp, false, true
	case blockThinking: // 思考行：头部（元信息，不选）+ 正文
		header := "已思考"
		if it.live {
			header = "思考中…"
		} else if it.secs >= 0 {
			header = fmt.Sprintf("已思考 · %ds", it.secs)
		}
		return func(gtx layout.Context) layout.Dimensions {
			vs := beginVStack(gtx)
			vs.add(func(gtx layout.Context) layout.Dimensions {
				s := material.Caption(u.th, header)
				s.Color = textDim
				return s.Layout(gtx)
			})
			off := image.Pt(0, vs.y)
			vs.add(func(gtx layout.Context) layout.Dimensions {
				return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, it.text)
					s.Color = textDim
					s.State = rs.sel(0)
					return s.Layout(gtx)
				}, 0, off, image.Point{})
			})
			return vs.dims()
		}, cardThinking, cardR, false, false
	case blockTool:
		if it.chip == nil { // 兜底：无 chip 的工具行（防御，正常路径都有 chip）
			return func(gtx layout.Context) layout.Dimensions {
				return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, it.text)
					l.Color = textMuted
					l.State = rs.sel(0)
					return l.Layout(gtx)
				}, 0, image.Point{}, image.Point{})
			}, cardTool, cardR, false, false
		}
		return u.toolChipRow(it, rs, cardR)
	case blockBranch: // 分叉条（D81）：气泡下方 `◀ i/n ▶`，与气泡同向对齐
		if it.branch == nil { // 防御：无数据的空行不渲染也不登记形状
			return func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{}
			}, cardTool, 0, false, false
		}
		return u.branchRow(it.branch, cardR)
	case blockNotice:
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, it.text)
				l.Color = textNotice
				l.State = rs.sel(0)
				return l.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, cardNotice, cardR, false, false
	case blockError:
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, it.text)
				l.Color = textError
				l.State = rs.sel(0)
				return l.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, cardError, cardR, false, false
	case blockSystem:
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, it.text)
				l.Color = textSystem
				l.State = rs.sel(0)
				return l.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, cardSystem, cardR, false, false
	default: // blockPlain（Say/命令输出/提示）：底板浅白、正文默认色
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, it.text)
				s.State = rs.sel(0)
				return s.Layout(gtx)
			}, 0, image.Point{}, image.Point{})
		}, pillBg, cardR, false, false
	}
}

// mdBlockGapDp 气泡内 markdown 块间距（D66 复合行）。
const mdBlockGapDp unit.Dp = 4

// mdCodeInsetDp 代码小卡内边距（D66：卡嵌气泡内）。
const mdCodeInsetDp unit.Dp = 6

// mdBlockWidget 单个 markdown 块的行内部件（D66 复合行分派；rs/k = 该块行选挂接
// 件与块序，off = 块原点相对行内容原点（vstack 量期已知，供 note；高亮坐标走
// ops 局部变换——vstack 的 Offset 已在变换内，D91 ②③）。
func (u *UI) mdBlockWidget(b mdBlock, rs *rowSel, k int, off image.Point) layout.Widget {
	switch b.kind {
	case mdHeading: // 大字求粗（D65 阶梯；CJK 粗体面缺省回落常规）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body1(u.th, b.text)
				s.TextSize = mdHeadingSp(u.th, b.level)
				s.Font.Weight = font.SemiBold
				s.State = rs.sel(k)
				return s.Layout(gtx)
			}, k, off, image.Point{})
		}
	case mdCode: // 等宽小卡：录宏量高 → 画底 →（高亮，卡内衬偏移）→ 重放（同 paintRow 次序，卡嵌气泡内）
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, b.text)
			s.Font = monoFace
			s.TextSize = u.th.TextSize * 13.0 / 16.0
			s.State = rs.sel(k)
			m := op.Record(gtx.Ops)
			dims := layout.UniformInset(mdCodeInsetDp).Layout(gtx, s.Layout)
			txt := m.Stop()
			st := clip.UniformRRect(image.Rectangle{Max: dims.Size}, gtx.Dp(radiusDp)).Push(gtx.Ops)
			paint.Fill(gtx.Ops, cardTool)
			st.Pop()
			inset := gtx.Dp(mdCodeInsetDp)
			if rs != nil {
				rs.note(k, off.Add(image.Pt(inset, inset))) // 文本在内衬原点（内容系）
				rs.paint(gtx, k, image.Pt(inset, inset))    // 高亮局部 = 盒 + 内衬（文本回放前落 ops）
			}
			txt.Add(gtx.Ops)
			return dims
		}
	case mdQuote: // 引用：暗色正文（前缀已在文本）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, b.text)
				s.Color = textDim
				s.State = rs.sel(k)
				return s.Layout(gtx)
			}, k, off, image.Point{})
		}
	case mdRule: // 分隔线：暗点行
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, "· · · · · ·")
				l.Color = textDim
				l.State = rs.sel(k)
				return l.Layout(gtx)
			}, k, off, image.Point{})
		}
	default: // mdPara / mdListItem：正文（列表前缀已在文本）
		return func(gtx layout.Context) layout.Dimensions {
			return rs.layout(gtx, func(gtx layout.Context) layout.Dimensions {
				s := material.Body2(u.th, b.text)
				s.State = rs.sel(k)
				return s.Layout(gtx)
			}, k, off, image.Point{})
		}
	}
}

// chipClick 工具 chip 头部点击控件 get-or-create（D67：按块序缓存，块只增不减）。
func (u *UI) chipClick(i int) *widget.Clickable {
	for len(u.chipClicks) <= i {
		u.chipClicks = append(u.chipClicks, new(widget.Clickable))
	}
	return u.chipClicks[i]
}

// chipIsOpen chip 展开态（D67）：用户开合按块序缓存；待确认 chip 强制展开
// （权限问句不可折叠隐藏），应答后回落用户选择。
func (u *UI) chipIsOpen(idx int, c *toolChip) bool {
	if c != nil && c.confirmQ != "" && c.confirmA == "" && u.m.confirm != nil {
		return true
	}
	return u.chipOpen[idx]
}

// chipHeaderText chip 头部行（D67）：▸/▾ 开合指示 + 🔧 名称 + 参数首行预览 + 状态
// （… 运行中 / 待确认 / ✓ / ✗）。
func chipHeaderText(c *toolChip, open bool) string {
	arrow, status := "▸", "…"
	if open {
		arrow = "▾"
	}
	if c.done {
		status = "✓"
		if !c.ok {
			status = "✗"
		}
	} else if c.confirmQ != "" {
		status = "待确认"
	}
	args := strings.TrimSpace(c.args)
	if i := strings.IndexByte(args, '\n'); i >= 0 {
		args = args[:i]
	}
	if r := []rune(args); len(r) > 40 {
		args = string(r[:40]) + "…"
	}
	line := "🔧 " + c.name
	if args != "" {
		line += " · " + args
	}
	return arrow + " " + line + " · " + status
}

// chipCopyText chip 的复制文本（D100④）：工具名 + 参数 + 结果——与展开体同源数据，
// 剔除箭头/状态/问答着色等 UI 修饰。
func chipCopyText(c *toolChip) string {
	var b strings.Builder
	b.WriteString(c.name)
	if s := strings.TrimSpace(c.args); s != "" {
		b.WriteString("\n")
		b.WriteString(s)
	}
	if c.done && c.result != "" {
		mark := "结果 ✓"
		if !c.ok {
			mark = "结果 ✗"
		}
		b.WriteString("\n")
		b.WriteString(mark)
		b.WriteString("\n")
		b.WriteString(c.result)
	}
	return b.String()
}

// chipRaw chip 的查看原文内容（D100④）：调用/结果组为 JSON（参数为合法 JSON 时内嵌
// 原值，否则降级为字符串字段）。
func chipRaw(c *toolChip) rawContent {
	payload := struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		ArgsText  string          `json:"arguments_text,omitempty"`
		Done      bool            `json:"done"`
		OK        bool            `json:"ok,omitempty"`
		Result    string          `json:"result,omitempty"`
	}{Tool: c.name, Done: c.done, OK: c.ok, Result: c.result}
	if json.Valid([]byte(c.args)) {
		payload.Arguments = json.RawMessage(c.args)
	} else {
		payload.ArgsText = c.args
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		b = []byte(c.name + "\n" + c.args + "\n" + c.result)
	}
	return rawContent{title: "工具调用 · " + c.name, text: string(b)}
}

// toolChipRow 工具合并 chip（D67）：头部行可点击折叠/展开；展开体 = 参数（等宽全文）+
// 权限问答（暗色）+ 结果全文。头部为点击热区不挂行选；参数/结果可选
// （键位固定 2 = selCount，开合不漂移后续行序号）。手排纵列（D91 ②：量期知块偏移；
// 折叠时参数/结果不铺开 → note 不落，指纹随开合变化即清选）。
func (u *UI) toolChipRow(it blockView, rs *rowSel, cardR int) (layout.Widget, color.NRGBA, int, bool, bool) {
	c := it.chip
	cl := u.chipClick(it.chipIdx)
	expanded := u.chipIsOpen(it.chipIdx, c)
	header := chipHeaderText(c, expanded)
	return func(gtx layout.Context) layout.Dimensions {
		vs := beginVStack(gtx)
		vs.add(func(gtx layout.Context) layout.Dimensions {
			return cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, header)
				l.Color = textMuted
				return l.Layout(gtx)
			})
		})
		if expanded {
			add := func(k int, w layout.Widget) {
				vs.gap(mdBlockGapDp)
				off := image.Pt(0, vs.y)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					return rs.layout(gtx, w, k, off, image.Point{})
				})
			}
			if strings.TrimSpace(c.args) != "" {
				add(0, func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, c.args)
					s.Font = monoFace
					s.TextSize = u.th.TextSize * 13.0 / 16.0
					s.State = rs.sel(0)
					return s.Layout(gtx)
				})
			}
			if c.confirmQ != "" {
				qa := c.confirmQ
				if c.confirmA != "" {
					qa += " → " + c.confirmA
				}
				vs.gap(mdBlockGapDp)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, qa)
					l.Color = textDim
					return l.Layout(gtx)
				})
			}
			switch {
			case c.done:
				mark := "结果 ✓"
				if !c.ok {
					mark = "结果 ✗"
				}
				vs.gap(mdBlockGapDp)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, mark)
					l.Color = textDim
					return l.Layout(gtx)
				})
				add(1, func(gtx layout.Context) layout.Dimensions {
					s := material.Body2(u.th, c.result)
					s.Color = textMuted
					s.State = rs.sel(1)
					return s.Layout(gtx)
				})
			case c.confirmQ == "": // 运行中且无待确认（展开查看时）
				vs.gap(mdBlockGapDp)
				vs.add(func(gtx layout.Context) layout.Dimensions {
					l := material.Caption(u.th, "运行中…")
					l.Color = textDim
					return l.Layout(gtx)
				})
			}
		}
		return vs.dims()
	}, cardTool, cardR, false, false
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
	padX := gtx.Dp(statusPadXDp)
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
	// D106：展开态胶囊原地增高（瞬时切换；动画期照常 48——D76 几何不动）。
	pillH := rowH
	if extra := u.inputExtraDp(); extra > 0 {
		pillH = rowH + extra
	}
	// 圆钮贴胶囊底缘：展开态下移 extra（常态 extra=0 = D49 原位、D76 不变量不动）。
	logo, pillEnd, sendEnd := inputRowRects(w, gtx.Dp(pillTopDp)+pillH-rowH, rowH,
		gtx.Dp(inputGapDp), gtx.Dp(sideMarginDp))
	// D54/D76 展开/收起几何：send 按 barP 从 logo 插值到终位（p=0 = 收起球、p=1 = D49 终位、
	// 过冲 p>1 越出终位再回落），胶囊按 send 分段导出（缩/长段双间隙恒 12、平移段成圆同步
	// 合球）；send 先收界（D57）、胶囊随夹后 send 导出 → 贴边夹掉后双间隙仍恒 12。
	pill, send := rowRectsFromSend(logo, pillEnd, sendEnd, u.barP(), w)
	if pillH != rowH {
		pill.Min.Y -= pillH - rowH // 胶囊顶缘上移（底缘与圆钮对齐）；圆角恒 24dp
	}
	clipRect := image.Rectangle{Max: u.frameSize}
	inAnim := u.expandAn.active

	// 输入胶囊（**先画**，D54 绘制顺序 = 胶囊 → 右钮 → logo）：内容**按终位整盒排版**
	// （原点 + 终宽都取 pillEnd → 文字图标不挤压、不位移），按当前胶囊矩形裁剪；
	// D77 显隐另走内容 alpha 时间线（动画期隐藏、bar 完成后淡入），裁剪只管几何揭示。
	// α 由 stepExpand 定帧（pillAlpha，两遍 layout 同帧同值）；仅 α<1 压组透明层
	//（胶囊底板不透明 → 位图命中/焦点/手势不变）。展开态（D106）内容盒与原点改取
	// 实际胶囊矩形（多行编辑从顶缘排）。
	contentBox, contentOrigin := pillEnd.Size(), pillEnd.Min
	if pillH != rowH {
		contentBox, contentOrigin = pill.Size(), pill.Min
	}
	paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(pill, rowH/2).Op(gtx.Ops))
	pst := clip.UniformRRect(pill, rowH/2).Push(gtx.Ops)
	inner := op.Offset(contentOrigin).Push(gtx.Ops)
	gtxC := gtx
	gtxC.Constraints = layout.Exact(contentBox)
	if al := u.pillAlpha; al >= 1 {
		u.pillContent(gtxC)
	} else {
		layer := paint.PushOpacity(gtx.Ops, float32(al))
		u.pillContent(gtxC)
		layer.Pop()
	}
	inner.Pop()
	pst.Pop()
	u.record(pill.Add(image.Pt(0, absY)), rowH/2, pillBg, clipRect)

	// 右圆钮（恒在，几何不随状态变、动画期只平移，D49）：idle = 发送、生成中 = 停止
	// ——确认态不特判（D86：工具确认发生在 Turn 内，停止键照常可点，取消整轮语义
	// 不变、迟到应答缓冲兜底；/rm 等非生成中确认遇 idle 为发送键，submitEditor 拦截）。
	fill, stop, cl := brandColor, false, &u.sendBtn
	if u.generating.Load() {
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
	u.logoDrag.Add(gtx.Ops)
	u.logoRight.add(gtx.Ops, logo) // D72：右键热区 = logo 钮矩形
	gst.Pop()
	u.record(logo.Add(image.Pt(0, absY)), rowH/2, brandColor, clipRect)

	// 悬浮 tips（§15.1 启动提示 / §15.2 发送·停止键）：独立底板元素随位图。
	// 显隐 = 事件态 × 光标直采（D53）：分层窗按像素 alpha 命中，光标移到透明像素/
	// 窗外后零 pointer 事件，Hover 收不到 Leave → 实测移开不消；直采离钮即熄，
	// tipShown 并入 heartbeatNeed 唤帧复评（D50 心跳底座复用）。动画期抑制（D54）。
	shown := false
	if !inAnim {
		cur := cursorPos()
		// D85：logo 门控 = 矩形直采 × WindowFromPoint 命中直证（事件态不可靠——
		// Enter 在「窗口出现于静止光标下/首次悬停」场景永不投递，仅 Press 会送）。
		if u.cursorHitsLogo(cur) {
			// D82/S1-1g：事实快照就绪则显多行事实卡，未就绪（零值）回退启动提示。
			if lines := u.factsCard(); len(lines) > 0 {
				u.hoverCard(gtx, absY, lines, false)
			} else {
				u.hoverTip(gtx, absY, startupHint, false)
			}
			shown = true
		}
		// D104：附件槽 tooltip（D85 直采；暂存态换 chip、无 tooltip——chip 自示意）。
		if !shown && u.m.confirm == nil && u.m.stagedFile == "" &&
			u.cursorHitsRect(attachSlotRect(pillEnd, gtx.Dp(inputPadDp), rowH,
				gtx.Dp(inputIconDp)).Add(image.Pt(0, absY)), cur) {
			u.hoverTip(gtx, absY, "附件", false)
			shown = true
		}
		if u.m.confirm != nil {
			// D86：确认态三钮 tips（图标无文字，tooltip 承担可发现性）——布局几何
			// 直采 × OS 命中直证（D85 口径；Clickable.Hovered 事件态在本窗不可靠）。
			if txt, ok := u.confirmTipAt(cur); ok {
				u.hoverTip(gtx, absY, txt, true)
				shown = true
			}
		} else if u.sendBtn.Hovered() && !u.generating.Load() &&
			u.overInputBtn(true, cur) {
			u.hoverTip(gtx, absY, "发送", true)
			shown = true
		}
		if u.generating.Load() && u.stopBtn.Hovered() &&
			u.overInputBtn(true, cur) {
			// D86：确认态恢复（右圆停止键不再置灰）。
			u.hoverTip(gtx, absY, "停止", true)
			shown = true
		}
	}
	u.tipShown = shown
	// D103 补全浮层（tips 之后画——盖住转写区下部，胶囊上方锚定）：chrome 形状整窗
	// 登记不受淡化带作用；行矩形随帧重登记（fade pass 同几何复登，bubbleRects 同构）。
	u.drawCompl(gtx, absY, pill)
}

// inputExtraDp 展开态输入带增高量（D106 单源，dp）：胶囊增高超出常排 48 的部分。
// 动画期恒 0（D76 几何不动）。
func (u *UI) inputExtraDp() int {
	if !u.expanded || u.expandAn.active {
		return 0
	}
	return int(inputPillExpandDp - inputRowDp)
}

// inputExtraPx 增高量换算（layout 与 inputBar 同帧同值——经同一 gtx.Metric）。
func (u *UI) inputExtraPx(gtx layout.Context) int {
	return gtx.Dp(unit.Dp(u.inputExtraDp()))
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
// （发送键），false=左对齐（logo）。单行 = hoverCard 的单行特例（D82）。
func (u *UI) hoverTip(gtx layout.Context, absY int, text string, rightAlign bool) {
	u.hoverCard(gtx, absY, []string{text}, rightAlign)
}

// hoverCard 多行悬浮提示卡（D82/S1-1g，泛化自单行 tips）：texts 逐行左对齐、宽度取
// 最宽行；画在胶囊上沿之上（输入栏段局部坐标，可为负 → 溢出到转写区之上，无遮挡
// 裁剪）；自带底板并登记形状（形状规则同单行）。rightAlign=右对齐到胶囊内边距
// （发送键），false=左对齐（logo）。
func (u *UI) hoverCard(gtx layout.Context, absY int, texts []string, rightAlign bool) {
	if len(texts) == 0 {
		return
	}
	type line struct {
		op op.CallOp
		w  int
		h  int
	}
	lines := make([]line, 0, len(texts))
	w, h := 0, 0
	for _, text := range texts {
		m := op.Record(gtx.Ops)
		cap := material.Caption(u.th, text)
		cap.Color = whiteText
		dims := cap.Layout(gtx)
		lines = append(lines, line{op: m.Stop(), w: dims.Size.X, h: dims.Size.Y})
		w = max(w, dims.Size.X)
		h += dims.Size.Y
	}
	padX, padY := gtx.Dp(tipsPadXDp), gtx.Dp(tipsPadYDp)
	radius := gtx.Dp(tipsRadiusDp)
	x := gtx.Dp(sideMarginDp)
	if rightAlign {
		x = gtx.Constraints.Max.X - gtx.Dp(sideMarginDp) - w - 2*padX
		if x < 0 {
			x = 0
		}
	}
	y := gtx.Dp(pillTopDp) - h - 2*padY - gtx.Dp(tipsUpGapDp)
	bgRect := image.Rectangle{
		Min: image.Pt(x, y),
		Max: image.Pt(x+w+2*padX, y+h+2*padY),
	}
	st := clip.UniformRRect(bgRect, radius).Push(gtx.Ops)
	paint.Fill(gtx.Ops, tipBg)
	ly := bgRect.Min.Y + padY
	for _, ln := range lines {
		inner := op.Offset(image.Pt(bgRect.Min.X+padX, ly)).Push(gtx.Ops)
		ln.op.Add(gtx.Ops)
		inner.Pop()
		ly += ln.h
	}
	st.Pop()
	u.record(bgRect.Add(image.Pt(0, absY)), radius, tipBg, image.Rectangle{Max: u.frameSize})
}

// factsCard logo 悬停事实卡内容（D82/S1-1g，§15.1）：profile · 会话（标题+ID 前缀）·
// 模型（权限档/effort）· 上下文占用（Q6：精确优先 est 兜底，分母 max_context_tokens）·
// 用量（Path 累计 + 有实测时上轮）。快照未就绪（零值）→ nil（调用方回退启动提示）。
// 纯逻辑（读 Status 回调后不触 GUI 状态），可测。
func (u *UI) factsCard() []string {
	var st Status
	if u.opts.Status != nil {
		st = u.opts.Status()
	}
	f := st.Facts
	if f.Title == "" && f.CtxMax == 0 { // 尚未发布（构造早期/计数一直失败）
		return nil
	}
	profile := st.Profile
	if profile == "" { // S4/Q1 前装配根留空 → 占位
		profile = "default"
	}
	title := f.Title
	if title == "" {
		title = "（未就绪）"
	}
	l1 := fmt.Sprintf("%s · %s（%s）", profile, title, shortID(f.ConvID))
	if st.Model != "" {
		l1 += " · " + st.Model
		if st.Level != "" {
			l1 += "（" + st.Level
			if st.Effort != "" {
				l1 += "，" + st.Effort
			}
			l1 += "）"
		}
	}
	var l2 string
	if f.CtxMax > 0 {
		mode := "估算"
		if f.CtxExact {
			mode = "精确"
		}
		l2 = fmt.Sprintf("上下文 %d/%d tokens（%.1f%%，%s）",
			f.CtxTokens, f.CtxMax, 100*float64(f.CtxTokens)/float64(f.CtxMax), mode)
	}
	l3 := fmt.Sprintf("累计 in %d / out %d tokens", f.SumIn, f.SumOut)
	out := []string{l1}
	if l2 != "" {
		out = append(out, l2)
	}
	out = append(out, l3)
	if f.LastIn > 0 || f.LastOut > 0 {
		out = append(out, fmt.Sprintf("上轮 in %d / out %d tokens", f.LastIn, f.LastOut))
	}
	return out
}

// shortID 会话 ID 展示前缀（D82）：前 8 位，短 ID 原样。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// pillContent 胶囊内横排（坐标原点 = 胶囊左上，约束 = 胶囊尺寸；D49/§15.2）：
// [pad16][附件槽20][12][文字 grow][12][展开槽20][12][动作区][pad16]。图标槽是 canvas 的
// 20dp 灰占位（附件/展开未实现、不可点，实现时启用）；动作区仅确认态有内容（[允许/拒绝]）——
// 生成中停止在右圆、胶囊内不占位，idle 时胶囊内只有占位文字。
func (u *UI) pillContent(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(inputPadDp), Right: unit.Dp(inputPadDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// D106 修订⑵：展开态 = 竖排 composer——附件左上/展开右上工具行 + 顶对齐多行
		// 编辑区；确认态仍走单行布局（原因编辑器，灰槽本就隐藏）。
		if u.expanded && u.m.confirm == nil {
			return u.pillContentExpanded(gtx)
		}
		return layout.Flex{
			Axis:      layout.Horizontal,
			Alignment: layout.Middle,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm != nil {
					return layout.Dimensions{} // D86：确认态灰槽隐藏（本就不可点占位）
				}
				// D104 附件槽实装：无暂存 = 20dp 可点图标槽；有暂存 = chip（点取消）。
				if u.m.stagedFile != "" {
					return u.attachChip(gtx)
				}
				return u.attachSlot(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				inset := layout.Inset{Left: unit.Dp(inputGapDp), Right: unit.Dp(inputGapDp)}
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// 约束 Min.Y 被 Exact 拉满会把编辑器/提示行顶到盒顶（Flex Middle 对满高盒
					// 无效 → 文本视觉偏上）：放开 Min 让其返回自然行高，由 Flex 垂直居中（§15.2）。
					gtx.Constraints.Min.Y = 0
					if u.m.confirm != nil {
						// D86：原因编辑器（拒绝原因，可留空；Enter = 拒绝附原因）。
						re := material.Editor(u.th, &u.reasonEd, "reason……")
						re.TextSize = unit.Sp(15)
						dims := re.Layout(gtx)
						if u.inFadePass && u.caretFocused {
							u.drawReasonCaret(gtx, dims) // 同 D62：fade pass caret 自绘
						}
						return dims
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
				if u.m.confirm != nil {
					return layout.Dimensions{} // D86：确认态灰槽隐藏（本就不可点占位）
				}
				return u.expandSlot(gtx) // D106：点击切换展开/收起，图标随态
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm == nil {
					return layout.Dimensions{}
				}
				// D86 确认动作区：左→右 [✗ 拒绝][✓ 允许][🔑 提升权限]（⌀36、间距 8；
				// 右内边距 12 = 胶囊 padding 16 − 4 圆形光学校正）。
				return layout.Inset{Left: unit.Dp(inputGapDp), Right: unit.Dp(-confirmBtnOptDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					kids := []layout.FlexChild{
						layout.Rigid(confirmBtn(gtx, &u.denyBtn, actionDeny, glyphDeny)),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(confirmBtnGapDp)}.Layout(gtx,
								confirmBtn(gtx, &u.allowBtn, actionAllow, glyphAllow))
						}),
					}
					if u.confirmElevatable() {
						kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: unit.Dp(confirmBtnGapDp)}.Layout(gtx,
								confirmBtn(gtx, &u.elevateBtn, actionElevate, glyphKey))
						}))
					}
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, kids...)
				})
			}),
		)
	})
}

// expandSlot 展开槽（D106）：点击切换展开/收起，图标随态（四角括号外扩 ↔ 内收）。
func (u *UI) expandSlot(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	draw := drawMaximize
	if u.expanded {
		draw = drawMinimize
	}
	return u.expandBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		draw(gtx, box)
		return layout.Dimensions{Size: box.Size()}
	})
}

// pillContentExpanded 展开态胶囊内竖排（D106 修订⑵⑸）：[工具行 20 + 顶距 10][编辑区
// 自然高]——附件左上、展开右上（顶距避开 24dp 圆角曲线），编辑器**顶对齐**（Vertical
// Flex 默认 Start，不居中）。
func (u *UI) pillContentExpanded(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(pillToolTopDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						// 附件左上（D104：无暂存 = 图标槽、有暂存 = chip）。
						if u.m.stagedFile != "" {
							return u.attachChip(gtx)
						}
						return u.attachSlot(gtx)
					}),
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(u.expandSlot), // 展开右上
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = 0 // 自然高顶对齐（原 Flex Middle 居中——首行悬半空）
			ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
			ed.TextSize = unit.Sp(15)
			dims := ed.Layout(gtx)
			if u.inFadePass && u.caretFocused {
				u.drawCaret(gtx, dims) // D62：fade pass 无 Focused 态，caret 自绘
			}
			return dims
		}),
	)
}

// confirmBtn 确认态动作圆钮（D86：⌀36 实色圆 + 白色字形，点击热区即整圆）。
func confirmBtn(gtx layout.Context, cl *widget.Clickable, fill color.NRGBA, glyph func(gtx layout.Context, d int, fill color.NRGBA)) func(gtx layout.Context) layout.Dimensions {
	return func(gtx layout.Context) layout.Dimensions {
		d := gtx.Dp(confirmBtnDp)
		draw := func(gtx layout.Context) layout.Dimensions {
			box := image.Rectangle{Max: image.Pt(d, d)}
			paint.FillShape(gtx.Ops, fill, clip.UniformRRect(box, d/2).Op(gtx.Ops))
			glyph(gtx, d, fill)
			return layout.Dimensions{Size: image.Pt(d, d)}
		}
		return cl.Layout(gtx, draw)
	}
}

// glyphDeny ✗（两对角白杆）。
func glyphDeny(gtx layout.Context, d int, _ color.NRGBA) {
	a := float32(d) * 0.16
	cx, cy := float32(d)/2, float32(d)/2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy-a))
	p.LineTo(f32.Pt(cx+a, cy+a))
	p.MoveTo(f32.Pt(cx+a, cy-a))
	p.LineTo(f32.Pt(cx-a, cy+a))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
}

// glyphAllow ✓（短杆下探 + 长臂上挑）。
func glyphAllow(gtx layout.Context, d int, _ color.NRGBA) {
	a := float32(d) * 0.20
	cx, cy := float32(d)/2, float32(d)/2
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(cx-a, cy))
	p.LineTo(f32.Pt(cx-a/3, cy+a*0.7))
	p.LineTo(f32.Pt(cx+a, cy-a*0.7))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(3))}.Op())
}

// glyphKey 🔑（环头 + 杆 + 两齿；环 = 白实心圆挖钮底色孔——实色钮上与描边等效，
// 本版 gio 无 clip.Circle，方盒 UniformRRect 即内切圆）。
func glyphKey(gtx layout.Context, d int, fill color.NRGBA) {
	a := float32(d) * 0.20
	cx, cy := float32(d)/2, float32(d)/2
	hx := cx - a*0.9
	headR := a * 0.62
	headBox := image.Rect(int(hx-headR), int(cy-headR), int(hx+headR), int(cy+headR))
	paint.FillShape(gtx.Ops, whiteText,
		clip.UniformRRect(headBox, headBox.Dx()/2).Op(gtx.Ops))
	if inner := headBox.Inset(2); !inner.Empty() {
		paint.FillShape(gtx.Ops, fill,
			clip.UniformRRect(inner, inner.Dx()/2).Op(gtx.Ops))
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(hx+headR, cy)) // 杆：环右缘 → 右端
	p.LineTo(f32.Pt(cx+a, cy))
	p.MoveTo(f32.Pt(cx+a*0.45, cy)) // 齿 1
	p.LineTo(f32.Pt(cx+a*0.45, cy+a*0.55))
	p.MoveTo(f32.Pt(cx+a, cy)) // 齿 2
	p.LineTo(f32.Pt(cx+a, cy+a*0.75))
	paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
}

// iconSlot 胶囊内 20dp 图标槽（D49/§15.2：canvas 20×20 灰占位、不可点——附件/展开实现时启用）。
func iconSlot(gtx layout.Context, draw func(gtx layout.Context, box image.Rectangle)) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	box := image.Rectangle{Max: image.Pt(d, d)}
	draw(gtx, box)
	return layout.Dimensions{Size: box.Size()}
}

// attachSlot 附件槽实装（D104⑤）：20dp 命中 + 回形针图标（canvas 1:1，D49 灰槽启用）。
func (u *UI) attachSlot(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(inputIconDp)
	return u.attachBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		box := image.Rectangle{Max: image.Pt(d, d)}
		drawPaperclip(gtx, box)
		return layout.Dimensions{Size: box.Size()}
	})
}

// attachChip 暂存附件 chip（D104①）：回形针 + 文件名（按剩余宽截断）+ ×，深底圆角条；
// 整 chip 点击 = 取消暂存（芯片无第二动作，命中面最大化）。
func (u *UI) attachChip(gtx layout.Context) layout.Dimensions {
	const (
		attachChipDp    = 32  // 芯片高（胶囊 48 内垂直居中）
		attachNameMaxDp = 200 // 文件名显示预算（超宽截断）
	)
	return u.attachClear.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(attachChipDp)
		padX, iconD, gap := gtx.Dp(10), gtx.Dp(12), gtx.Dp(6)
		budget := gtx.Dp(attachNameMaxDp)
		if max := gtx.Constraints.Max.X - 2*padX - iconD - 3*gap - gtx.Dp(14); budget > max {
			budget = max // 胶囊窄时让位（编辑器至少留一行宽）
		}
		name := u.complFitText(gtx, filepath.Base(u.m.stagedFile), budget)
		nameOp, nameDims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, name)
			s.TextSize = unit.Sp(13)
			return s.Layout(gtx)
		})
		w := 2*padX + iconD + gap + nameDims.Size.X + gap + gtx.Dp(14)
		box := image.Rectangle{Max: image.Pt(w, h)}
		paint.FillShape(gtx.Ops, tipBg, clip.UniformRRect(box, h/2).Op(gtx.Ops))
		// 回形针（品牌色小图）。
		off := op.Offset(image.Pt(padX, (h-iconD)/2)).Push(gtx.Ops)
		drawPaperclip(gtx, image.Rectangle{Max: image.Pt(iconD, iconD)})
		off.Pop()
		// 文件名。
		tr := op.Offset(image.Pt(padX+iconD+gap, (h-nameDims.Size.Y)/2)).Push(gtx.Ops)
		nameOp.Add(gtx.Ops)
		tr.Pop()
		// ×（两杆，白字色）。
		cx, cy := float32(w-padX-gtx.Dp(7)), float32(h)/2
		a := float32(gtx.Dp(5))
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(cx-a, cy-a))
		p.LineTo(f32.Pt(cx+a, cy+a))
		p.MoveTo(f32.Pt(cx+a, cy-a))
		p.LineTo(f32.Pt(cx-a, cy+a))
		paint.FillShape(gtx.Ops, whiteText, clip.Stroke{Path: p.End(), Width: float32(gtx.Dp(2))}.Op())
		return layout.Dimensions{Size: box.Size()}
	})
}

// attachSlotRect 附件槽窗口系矩形（D85 tooltip 直采；布局拓扑 [pad16][附件槽20]…，
// 内容按终位整盒排版 → 锚定 pillEnd 左缘）。
func attachSlotRect(pillEnd image.Rectangle, padX, rowH, iconPx int) image.Rectangle {
	y := pillEnd.Min.Y + (rowH-iconPx)/2
	x := pillEnd.Min.X + padX
	return image.Rect(x, y, x+iconPx, y+iconPx)
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

// drawMinimize 收起图标（D106）：四边中点向内的短杠（与外扩四角括号对偶——「收回」），
// 灰、臂长同 drawMaximize。
func drawMinimize(gtx layout.Context, box image.Rectangle) {
	if box.Dx() <= 0 || box.Dy() <= 0 {
		return
	}
	in, arm, th := gtx.Dp(2), gtx.Dp(6), gtx.Dp(2)
	x0, y0, x1, y1 := box.Min.X, box.Min.Y, box.Max.X, box.Max.Y
	cx, cy := (x0+x1)/2, (y0+y1)/2
	for _, r := range []image.Rectangle{
		image.Rect(cx-th/2, y0+in, cx+th/2, y0+in+arm), // 上·竖（向下指）
		image.Rect(cx-th/2, y1-in-arm, cx+th/2, y1-in), // 下·竖（向上指）
		image.Rect(x0+in, cy-th/2, x0+in+arm, cy+th/2), // 左·横（向右指）
		image.Rect(x1-in-arm, cy-th/2, x1-in, cy+th/2), // 右·横（向左指）
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

// featherWidth 元素边缘渐隐带的宽（px，D47/D48 响应式 + D70 全元素统一）：带宽恒取
// **输入胶囊的带宽** round(Dp(inputRowDp)×featherRatio)——同屏各元素边缘剖面一致（按
// 元素自身短边算会让气泡/chip/胶囊各得不同带宽）；仍夹到 [Dp(featherMinDp),
// Dp(featherMaxDp)] 且不超过该元素短边 1/3（极小元素防被整带吃掉）。Dp 换算保证
// DPI/缩放跟随、不写死 px。纯逻辑，可测。
func featherWidth(m unit.Metric, w, h int) int {
	short := w
	if h < short {
		short = h
	}
	f := int(float64(m.Dp(inputRowDp))*featherRatio + 0.5)
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
// md = 助手定稿块的 markdown 结构块（D66 复合行）；chip/chipIdx = 工具合并 chip
// 及其块序（D67），text 为空；branch = 分叉条（D81）；bi/id = 所源 model 块序与
// 节点 ID（D92 右键菜单数据键——与 block.id 同源，live 条目为空）；keyBase = 行选
// 键线性基址（transcript 量高期填，整条复制取渲染文本用））。
type blockView struct {
	kind    blockKind
	text    string
	secs    int
	live    bool
	md      []mdBlock
	chip    *toolChip
	chipIdx int
	branch  *branchStrip
	bi      int
	id      conversation.MessageID
	keyBase int
}

// menuable 该条目是否响应气泡右键（D92/D100）：user/assistant 需带节点 ID（编辑/
// 重生成目标）；thinking 定稿块需已盖章（D100②）；工具 chip 态自足（无节点 ID 亦可，
// 复制/查看原文不需要）。live 草稿/notice/分叉条不进菜单。
func (it blockView) menuable() bool {
	switch it.kind {
	case blockUser, blockAssistant, blockThinking:
		return it.id != ""
	case blockTool:
		return it.chip != nil
	}
	return false
}

// selCount 本条目占用的行选键数（D66 双键）：助手复合行 = 块数；工具 chip = 恒 2
// （参数/结果——开合切换不漂移后续行序号，D67）；**分叉条 = 0**（D81：无文本可选，且
// 不占键 → 分叉条的增删不漂移其它行的选择序号）；其余行恒 1。
func (it blockView) selCount() int {
	switch {
	case it.kind == blockBranch:
		return 0
	case it.kind == blockAssistant && len(it.md) > 0:
		return len(it.md)
	case it.kind == blockTool && it.chip != nil:
		return 2
	}
	return 1
}

// branchStrip 分叉条（D81）：气泡正下方的 `◀ i/n ▶`。数据面 = port.TreeView 快照（D80）。
type branchStrip struct {
	blockIdx int                      // 所属块序（m.blocks 下标；渲染期按此对齐插入）
	id       conversation.MessageID   // 所源节点
	ids      []conversation.MessageID // 同级全部节点（创建序，含自身）
	index    int                      // 自身在 ids 中的下标
	right    bool                     // 与气泡同向对齐（user 气泡右对齐 → 分叉条也右对齐）
	slot     int                      // 点击件缓存槽 = 分叉条序（与 ids 无关，只增不改）
}

// branchStrips 本次渲染的分叉条（D81）：按块序为「带节点 ID 且同级 ≥2」的 user/assistant
// 正文块各生成一条；同级只有 1 条（无分叉）不生成。仅事件循环 goroutine 调用。
func (u *UI) branchStrips() []branchStrip {
	if u.opts.Tree == nil {
		return nil
	}
	var out []branchStrip
	for bi, b := range u.m.blocks {
		if b.id == "" {
			continue
		}
		// D89：纯工具轮的锚点在 chip 块上（blockTool），分叉条随之渲染于 chip 下方。
		if b.kind != blockUser && b.kind != blockAssistant && b.kind != blockTool {
			continue
		}
		bi2, ok := u.opts.Tree.Branches(b.id)
		if !ok || len(bi2.IDs) < 2 {
			continue
		}
		out = append(out, branchStrip{
			blockIdx: bi, id: b.id, ids: bi2.IDs, index: bi2.Index,
			right: b.kind == blockUser, slot: len(out),
		})
	}
	return out
}

// branchClick 分叉条左右点击件 get-or-create（D81；照 chipClick 口径按序缓存）。
func (u *UI) branchClick(slot int, next bool) *widget.Clickable {
	if next {
		for len(u.branchNext) <= slot {
			u.branchNext = append(u.branchNext, new(widget.Clickable))
		}
		return u.branchNext[slot]
	}
	for len(u.branchPrev) <= slot {
		u.branchPrev = append(u.branchPrev, new(widget.Clickable))
	}
	return u.branchPrev[slot]
}

// branchArrow 分叉条上的方向键；enabled=false（已在边界）时暗色——真正的不响应在
// updateClicks 的边界夹取里，这里只表达"不可用"。
func (u *UI) branchArrow(gtx layout.Context, glyph string, enabled bool) layout.Dimensions {
	c := textMuted
	if !enabled {
		c = textDim
	}
	l := material.Caption(u.th, glyph)
	l.Color = c
	return l.Layout(gtx)
}

// branchRow 分叉条渲染（D81）：`◀ i/n ▶` 横排，左右各一个点击件。底板 cardTool
// **不透明** → 整条像素可命中（D62 逐像素命中：透明间隙会穿透到下层窗，箭头字形
// 之间的空隙吞点击）。
func (u *UI) branchRow(s *branchStrip, cardR int) (layout.Widget, color.NRGBA, int, bool, bool) {
	n := len(s.ids)
	prev, next := u.branchClick(s.slot, false), u.branchClick(s.slot, true)
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return prev.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.branchArrow(gtx, "◀", s.index > 0)
				})
			}),
			layout.Rigid(layout.Spacer{Width: branchGapDp}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Caption(u.th, fmt.Sprintf("%d/%d", s.index+1, n))
				l.Color = textMuted
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: branchGapDp}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return next.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.branchArrow(gtx, "▶", s.index < n-1)
				})
			}),
		)
	}, cardTool, cardR, s.right, false
}

// branchGapDp 分叉条内 `◀`/计数/`▶` 的横向间距。
const branchGapDp unit.Dp = 6

// frameItems 定稿块 + 实时思考/草稿（流式可见；对齐 D33"流式原样、定稿渲染"口径）。
// 助手定稿块 = 单条目携 markdown 结构块（D66 单回复单气泡），工具块 = 合并 chip
// 携块序（D67），live 草稿原样单行；带分叉的正文块后紧跟一条分叉条（D81）。
func (u *UI) frameItems() []blockView {
	strips := u.branchStrips()
	next := 0 // strips 按块序升序，指针单向前进即对齐
	items := make([]blockView, 0, len(u.m.blocks)+len(strips)+2)
	for bi, b := range u.m.blocks {
		switch {
		case b.kind == blockAssistant:
			items = append(items, blockView{kind: blockAssistant, md: u.mdBlocks(b.text), bi: bi, id: b.id})
		case b.kind == blockTool && b.chip != nil:
			// bi 同填（D100：chip 右键经 bubbleHit.bi 回查 chip 态；chipIdx = 点击件缓存槽）。
			items = append(items, blockView{kind: blockTool, chip: b.chip, chipIdx: bi, bi: bi})
		default:
			items = append(items, blockView{kind: b.kind, text: b.text, secs: b.secs, bi: bi, id: b.id})
		}
		if next < len(strips) && strips[next].blockIdx == bi {
			s := strips[next] // 拷贝：条目持值，避免共享切片元素
			items = append(items, blockView{kind: blockBranch, branch: &s})
			next++
		}
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
	if u.generating.Load() {
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
	// D92/D97 编辑态提示（无生成状态时占用状态行；生成优先——编辑可跨生成提交排队）；
	// 方式后缀让当前修订方式在提交前始终可见（D97④）。
	if u.m.editTarget != "" {
		switch u.m.editMode {
		case conversation.Carry:
			return "编辑中（转移历史）· Enter 提交 / Esc 取消"
		case conversation.Clone:
			return "编辑中（复制历史）· Enter 提交 / Esc 取消"
		}
		return "编辑中 · Enter 提交 / Esc 取消"
	}
	return ""
}

// updateEditor 消费编辑器事件（Enter → SubmitEvent → 提交）。material.Editor.Layout
// 内部也会 Update 但丢弃返回的 SubmitEvent——必须在渲染前自己循环取尽。
// D92：编辑态额外拉取 Esc = 取消（清目标与编辑框；与 D91 选区 Escape 清除同为广播
// 过滤器，同按时二者皆发生——语义相容）。
func (u *UI) updateEditor(gtx layout.Context) {
	if u.m.confirm != nil {
		u.updateReasonEditor(gtx) // D86：确认态 = 原因编辑器消费按键（主编辑器隐藏）
		return
	}
	if u.m.editTarget != "" {
		for {
			ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
			if !ok {
				break
			}
			if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
				if u.complEsc { // D103：浮层开启期 Esc 先关浮层（complUpdate 已处理），不取消编辑
					u.complEsc = false
					continue
				}
				u.cancelEdit()
				break
			}
		}
	}
	// D106：展开态 Esc = 收起（文本保留压平）；优先级让浮层（complEsc/complOpenNow）
	// 与编辑态（上方分支）——本过滤器 Focus 限编辑器，编辑器无 Esc 语义无冲突面。
	if u.expanded && u.m.editTarget == "" && !u.complOpenNow && !u.complEsc {
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &u.editor, Name: key.NameEscape})
			if !ok {
				break
			}
			if ke, isKey := ev.(key.Event); isKey && ke.State == key.Press {
				u.setExpanded(false)
				break
			}
		}
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
			if u.complOpenNow { // D103：浮层开启期 Enter = 补全判定（一致即提交、否则补全）
				u.complEnter()
			} else {
				u.submitEditor()
			}
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
	rect := caretRect(c, u.caretLineH(gtx))
	rect.Min.X = max(rect.Min.X, 0) // 空文本时 caret 贴原点，杆宽一半越出编辑器盒
	if rect.Empty() {
		return
	}
	cl := clip.Rect(rect).Push(gtx.Ops) // 只裁 caret 杆——paint.Fill 覆盖整个当前裁剪区
	paint.Fill(gtx.Ops, u.th.Palette.Fg)
	cl.Pop()
}

// caretRect 光标杆矩形（D106 修订⑴）：基线 c.Y、按行高 lineH 的 0.8/0.2 拆分——
// 高度只随**行高**、不随内容高（原实现用编辑器内容高，多行下光标正比于行数伸长）。
func caretRect(c f32.Point, lineH int) image.Rectangle {
	asc := int(float64(lineH) * 0.8)
	return image.Rect(int(c.X)-1, int(c.Y)-asc, int(c.X)+1, int(c.Y)-asc+lineH)
}

// caretLineH 编辑器单行行高（D106 修订⑴）：同字号单行量测——多行下内容高 = 整块高
// 不可用。字号与 pillContent 的编辑器设置（15sp）保持一致。
func (u *UI) caretLineH(gtx layout.Context) int {
	_, dims := complMeasure(gtx, func(gtx layout.Context) layout.Dimensions {
		s := material.Body2(u.th, "行")
		s.TextSize = unit.Sp(15)
		return s.Layout(gtx)
	})
	return dims.Size.Y
}

// updateReasonEditor 消费确认态原因编辑器事件（D86）：Enter → SubmitEvent → 拒绝
// （附原因；允许/提升必经按钮）。焦点一次性让入（reasonFocus，主编辑器确认态隐藏
// ——不转移则键入落空），沿 D63 口径不回投常驻焦点。
func (u *UI) updateReasonEditor(gtx layout.Context) {
	if !u.inFadePass {
		u.caretFocused = gtx.Focused(&u.reasonEd) // 真窗 pass 捕获（同主编辑器）
	}
	if u.reasonFocus {
		u.reasonFocus = false
		gtx.Execute(key.FocusCmd{Tag: &u.reasonEd})
	}
	for {
		evt, ok := u.reasonEd.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := evt.(widget.SubmitEvent); isSubmit {
			u.m.replyConfirm(port.ConfirmAnswer{Reason: u.confirmReason()})
		}
	}
}

// drawReasonCaret 原因编辑器 fade pass caret 自绘（drawCaret 的 reasonEd 版，D86）。
func (u *UI) drawReasonCaret(gtx layout.Context, dims layout.Dimensions) {
	c := u.reasonEd.CaretCoords()
	asc := int(float64(dims.Size.Y) * 0.8)
	rect := image.Rect(int(c.X)-1, int(c.Y)-asc, int(c.X)+1, int(c.Y)+dims.Size.Y-asc)
	rect.Min.X = max(rect.Min.X, 0)
	if rect.Empty() {
		return
	}
	cl := clip.Rect(rect).Push(gtx.Ops)
	paint.Fill(gtx.Ops, u.th.Palette.Fg)
	cl.Pop()
}

// confirmReason 取原因文本（TrimSpace + 控制序列消毒，同确认问句口径）。
func (u *UI) confirmReason() string {
	return sanitizeControl(strings.TrimSpace(u.reasonEd.Text()))
}

// nextPermLevel 权限档序列的下一档（D86）：无下一档（已 full-access / 档名未知）
// 返回 false。
func nextPermLevel(level string) (string, bool) {
	for i, lv := range permLevels {
		if lv == level && i+1 < len(permLevels) {
			return permLevels[i+1], true
		}
	}
	return "", false
}

// confirmElevatable 🔑 提升钮可见性（D86）：仅工具确认（问句并入 chip，confirmChip ≥ 0）
// 且当前档有下一档时显示；/rm 等非工具确认与 full-access 档隐藏。
func (u *UI) confirmElevatable() bool {
	if u.m.confirm == nil || u.m.confirmChip < 0 {
		return false
	}
	if u.opts.Status == nil {
		return false
	}
	_, ok := nextPermLevel(u.opts.Status().Level)
	return ok
}

// confirmElevate 一键提档放行（D86）：经输入通道投 /permission <下一档>（托盘 D73
// 同路径，PersistLevel 写回 config；转写回显由 /permission 报告承载）+ 放行本次——
// 放行是显式点击授权，档位只影响后续判定，二者无时序依赖。投递失败（缓冲满）只
// 降级为放行，不阻塞应答。
func (u *UI) confirmElevate() {
	if next, ok := nextPermLevel(u.opts.Status().Level); ok {
		u.m.submitCommand("/permission " + next)
	}
	u.m.replyConfirm(port.ConfirmAnswer{Allow: true})
}

// confirmTipAt 确认态三钮 tips 判定（D86）：窗口系矩形直采 × OS 命中直证（D85）。
// 返回 (tooltip 文本, 是否命中)；按钮在胶囊右段，tip 右对齐。
func (u *UI) confirmTipAt(cur point) (string, bool) {
	if u.frameSize.X <= 0 || u.frameMetric.PxPerDp <= 0 {
		return "", false
	}
	elevate := u.confirmElevatable()
	rects := confirmBtnRects(u.frameSize, u.frameMetric.Dp, elevate)
	texts := make([]string, len(rects))
	texts[0] = "拒绝"
	texts[1] = "允许"
	if elevate {
		next, _ := nextPermLevel(u.opts.Status().Level)
		texts[2] = "提升权限 → " + next
	}
	for i, r := range rects {
		if u.cursorHitsRect(r, cur) {
			return texts[i], true
		}
	}
	return "", false
}

// submitEditor 提交编辑器内容（发送键与 Enter 同路）；确认态无动作——D86：应答经
// 三钮/原因框 Enter，拦截以防主编辑器旧草稿被 submit 误当 y/N 应答。
func (u *UI) submitEditor() {
	if u.m.confirm != nil {
		return
	}
	text := strings.TrimSpace(u.editor.Text())
	if text == "" && u.m.stagedFile == "" {
		if u.m.editTarget != "" { // D92：编辑态空提交 = 取消
			u.cancelEdit()
		}
		return
	}
	u.editor.SetText("")
	u.m.submit(text)
}

// cancelEdit 退出编辑态（D92）：清目标节点并还原空编辑框（Esc / 空提交共用）。
func (u *UI) cancelEdit() {
	u.m.editTarget = ""
	u.editor.SetText("")
}

// setExpanded 切换展开态（D106；修订⑶ 收起保留换行）：编辑器 SingleLine 随动、焦点
// 保持；切换不触 buffer——已有 \n 原样保留（单行胶囊内显示溢出裁剪可接受，数据保真
// 优先：提交恒发真实文本，重新展开即完整多行）。
func (u *UI) setExpanded(on bool) {
	if u.expanded == on {
		return
	}
	u.expanded = on
	u.editor.SingleLine = !on
	u.focusPending = true
}

// flushCopy 处理挂起的复制请求（D92，frame 每帧调用）：clipboard.WriteCmd 须在 Gio
// 帧上下文执行——shell 线程分发的 copyMsg 经主循环记账（pendingCopy）到这里落盘。
func (u *UI) flushCopy(gtx layout.Context) {
	if u.pendingCopy == "" {
		return
	}
	t := u.pendingCopy
	u.pendingCopy = ""
	gtx.Execute(clipboard.WriteCmd{
		Type: "application/text", // Gio 自家 Selectable 复制同款 MIME（selCopy 同款）
		Data: io.NopCloser(strings.NewReader(t)),
	})
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
		u.m.replyConfirm(port.ConfirmAnswer{Allow: true})
	}
	if u.denyBtn.Clicked(gtx) {
		u.m.replyConfirm(port.ConfirmAnswer{Reason: u.confirmReason()})
	}
	if u.elevateBtn.Clicked(gtx) {
		u.confirmElevate() // D86：/permission 升一档 + 放行本次
	}
	if u.attachBtn.Clicked(gtx) {
		u.requestFileDlg() // D104：附件槽 → shell 线程文件选择框（模态泵不嵌 Gio 泵）
	}
	if u.attachClear.Clicked(gtx) {
		u.m.clearAttach() // D104：暂存 chip 点击 = 取消暂存
	}
	if u.expandBtn.Clicked(gtx) {
		u.setExpanded(!u.expanded) // D106：展开/收起切换
	}
	// 工具 chip 头部点击 → 折叠/展开（D67；m.blocks 只增，块序即 chipIdx）。
	for i, b := range u.m.blocks {
		if b.kind == blockTool && b.chip != nil && u.chipClick(i).Clicked(gtx) {
			if u.chipOpen == nil {
				u.chipOpen = map[int]bool{}
			}
			u.chipOpen[i] = !u.chipOpen[i]
		}
	}
	// 分叉条左右切换（D81）：投递 `/goto <兄弟id>` 经输入通道（与键入同路径，壳内不
	// 旁路）；已在边界则不环绕（按钮置暗且不投递）。
	for _, s := range u.branchStrips() {
		switch {
		case u.branchClick(s.slot, false).Clicked(gtx):
			u.gotoBranch(s, -1)
		case u.branchClick(s.slot, true).Clicked(gtx):
			u.gotoBranch(s, +1)
		}
	}
}

// gotoBranch 投递一次分叉切换（D81）：delta = -1 左（更旧版本）/ +1 右（更新版本）。
// 越界不环绕；输入缓冲满时给提示而不是静默丢弃（同 model.submit 口径）。
// D94：落点 = 目标版本的对话末端（port.TreeView.Tail）——/goto 停在消息节点上时其
// 回答不在 Head 路径上，切用户消息版本会只剩半截；端口不可用回退兄弟 id。
func (u *UI) gotoBranch(s branchStrip, delta int) {
	to := s.index + delta
	if to < 0 || to >= len(s.ids) {
		return
	}
	id := s.ids[to]
	if u.opts.Tree != nil {
		if t, ok := u.opts.Tree.Tail(id); ok {
			id = t
		}
	}
	if !u.m.submitCommand("/goto " + string(id)) {
		u.m.add(blockNotice, "[notice] 输入缓冲已满，分叉切换未执行")
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

// logoRight D72 logo 右键菜单手势状态：区域内右键按下（pointer.Event.Buttons 含
// ButtonSecondary）武装、同指针**原位**抬起（位置落 bounds 内）= 请求菜单；移出/
// Cancel = 放弃。Gio 的 Release 恒投给按下时记录的 handlers（不按位置重命中），故
// 「移出不弹」必须位置门控。gesture.Drag/Click 均跳过非主键按下（右键与其零冲突），
// 故需自挂 pointer.Filter。
type logoRight struct {
	armed  bool
	pid    pointer.ID
	bounds image.Rectangle // 注册区（logo 钮/球矩形；坐标空间 = event.Op 处的局部空间）
}

// add 注册右键手势并记录热区矩形（展开 = logo 钮 clip 矩形（输入栏局部空间）、
// 收起 = 球矩形（窗口空间）——与事件投递的 invTransform 局部空间一一对应）。
// 收起态注册整窗 clip、事件本只落球像素（D62 逐像素命中），热区仍按球矩形门控。
func (r *logoRight) add(ops *op.Ops, bounds image.Rectangle) {
	r.bounds = bounds
	event.Op(ops, r)
}

// update 消费手势事件：右键按下武装、同指针原位抬起返回 true（请求菜单）；位置在
// 热区外的抬起、Cancel、非右键按下均解除武装（左键照常零干扰——gesture.Drag 本就
// 跳过非主键）。
func (r *logoRight) update(q input.Source) bool {
	fire := false
	for {
		ev, ok := q.Event(pointer.Filter{
			Target: r,
			Kinds:  pointer.Press | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			r.armed = e.Buttons.Contain(pointer.ButtonSecondary)
			r.pid = e.PointerID
		case pointer.Release:
			if r.armed && e.PointerID == r.pid && e.Position.Round().In(r.bounds) {
				fire = true
			}
			r.armed = false
		case pointer.Cancel:
			r.armed = false
		}
	}
	return fire
}

// requestLogoMenu 请求弹出 logo 右键菜单（D72）：测试经 logoMenuHook 回执；生产投
// 托盘线程呈现（shell 线程已有独立消息泵，TrackPopupMenu 不嵌 Gio 泵）。
func (u *UI) requestLogoMenu() {
	if u.logoMenuHook != nil {
		u.logoMenuHook()
		return
	}
	postLogoMenu()
}

// bubbleHit 右键命中的气泡底板矩形（D92）：rect = 气泡底板（窗口系，与形状登记同一
// bgRect——padding 区也是气泡的一部分）；bi/id/kind 定位块，keyBase/keyN = 该块的
// 行选键区间（整条复制按键区间取渲染文本）。随帧复位、消费者阶段读上一帧（一帧陈旧）。
type bubbleHit struct {
	rect          image.Rectangle
	bi            int
	id            conversation.MessageID
	kind          blockKind
	keyBase, keyN int
}

// bubbleRight 气泡右键手势（D92，D72 logoRight 同款）：Secondary 按下武装、同指针
// 抬起 = fire（携按下/抬起两点——是否「原位」由消费方按气泡粒度判定）；Cancel/非
// 右键 = 放弃。gesture 系跳过非主键按下，与 D63/D91 主键选态零冲突。按下与抬起
// 分属两批事件，按下位置必须持久化在手势态里（Press/Release 事件各成一批送达）。
type bubbleRight struct {
	armed bool
	pid   pointer.ID
	press image.Point // 按下位置（窗口系；抬起时与抬起点比对定「原位」）
}

// add 注册右键手势（转写视口 clip 内、与 D91 观察者同组命中链；事件坐标 = 窗口系）。
func (r *bubbleRight) add(ops *op.Ops) { event.Op(ops, r) }

// update 消费手势事件：同指针按下后抬起返回 (按下点, 抬起点, true)；移出后抬起、
// Cancel、非右键按下均解除武装（位置门控的气泡级判定在消费方——hitBubble 比对）。
func (r *bubbleRight) update(q input.Source) (press, release image.Point, fire bool) {
	for {
		ev, ok := q.Event(pointer.Filter{
			Target: r,
			Kinds:  pointer.Press | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			r.armed = e.Buttons.Contain(pointer.ButtonSecondary)
			r.pid = e.PointerID
			r.press = e.Position.Round()
		case pointer.Release:
			if r.armed && e.PointerID == r.pid {
				release = e.Position.Round()
				press = r.press
				fire = true
			}
			r.armed = false
		case pointer.Cancel:
			r.armed = false
		}
	}
	return press, release, fire
}

// hitBubble 点落在哪个气泡底板（D92）；未命中返回 -1。
func (u *UI) hitBubble(p image.Point) int {
	for i := range u.bubbleRects {
		if p.In(u.bubbleRects[i].rect) {
			return i
		}
	}
	return -1
}

// updateBubbleRight 气泡右键消费者（D92）：命中解析 + 菜单请求。bubbleRects 一帧
// 陈旧（与 keyRects 同口径）——命中基于上一帧气泡矩形；「原位」= 按下与抬起落在
// 同一气泡（抬起位置定菜单位）。
func (u *UI) updateBubbleRight(gtx layout.Context) {
	press, release, fire := u.bubbleRight.update(gtx.Source)
	if !fire {
		return
	}
	pi := u.hitBubble(press)
	if pi < 0 || pi != u.hitBubble(release) {
		return
	}
	u.requestBubbleMenu(u.bubbleCtx(pi))
}

// bubbleMenuCtx 气泡右键菜单上下文（D92/D97/D99/D100）：Gio 线程命中时组好、
// atomic.Pointer 过线程到 shell；edit = 编辑预填文本（user/assistant 块原文，D97 开放
// 助手编辑），copy = 复制文本（选区优先，否则按块角色取），raw = 查看原文内容
// （D99/D100：标题 + 原始文本/JSON）。
type bubbleMenuCtx struct {
	id   conversation.MessageID
	kind blockKind
	edit string
	copy string
	raw  rawContent
}

// bubbleCtx 组装菜单上下文（D92/D97/D99/D100）：复制文本此刻定——选区激活取选区
// （D91 后果⑤：副键留菜单复用选态），否则按块角色（chip 用其态字段，见 D100④——
// 折叠态键未铺开、blockText 不可靠；其余逐键 Text 拼接与 selCopy 同口径）；编辑预填
// 仅 user/assistant；raw 按块角色组装。
func (u *UI) bubbleCtx(h int) *bubbleMenuCtx {
	it := u.bubbleRects[h]
	ctx := &bubbleMenuCtx{id: it.id, kind: it.kind}
	chip := u.chipOf(it) // D100：chip 态取自所源块（bubbleHit 不携指针）
	switch {
	case u.sel.active:
		ctx.copy = u.selText()
	case chip != nil:
		ctx.copy = chipCopyText(chip)
	default:
		ctx.copy = u.blockText(it.keyBase, it.keyN)
	}
	if (it.kind == blockUser || it.kind == blockAssistant) && it.bi < len(u.m.blocks) {
		ctx.edit = u.m.blocks[it.bi].text
	}
	switch {
	case chip != nil:
		ctx.raw = chipRaw(chip)
	case it.kind == blockThinking && it.bi < len(u.m.blocks):
		ctx.raw = rawContent{title: "思考原文 · " + shortID(string(it.id)), text: u.m.blocks[it.bi].text}
	case it.bi < len(u.m.blocks):
		ctx.raw = rawContent{title: "原文 · " + shortID(string(it.id)), text: u.m.blocks[it.bi].text}
	}
	return ctx
}

// chipOf 命中矩形对应的 chip（D100）：经块序回查 model 块（越界/非工具块 = nil）。
func (u *UI) chipOf(it bubbleHit) *toolChip {
	if it.kind != blockTool || it.bi >= len(u.m.blocks) {
		return nil
	}
	return u.m.blocks[it.bi].chip
}

// requestBubbleMenu 请求弹出气泡右键菜单（D92）：测试经 bubbleMenuHook 回执；生产
// 存上下文原子槽并投 shell 线程呈现（TrackPopupMenu 不嵌 Gio 泵，D72 同款）。
func (u *UI) requestBubbleMenu(ctx *bubbleMenuCtx) {
	if u.bubbleMenuHook != nil {
		u.bubbleMenuHook(ctx)
		return
	}
	u.bubbleMenu.Store(ctx)
	postBubbleMenu()
}

// updateLogo logo 圆钮手势（§15.1 把手含 logo）：拖动移窗；单击（位移小于
// dragClickSlackPx）= 收起回球。悬停 tips 不经手势事件（D85：事件态不可靠，改
// WindowFromPoint 直证命中，见 tips 块与 cursorHitsLogo）。收起态圆钮区不存在
// （事件归背景把手）——右键菜单例外，收起态照跑（球即 logo，D72）。
func (u *UI) updateLogo(gtx layout.Context) {
	// D72 右键菜单：原位抬起才请求（动画期不响应，D54；收起态照跑——球即 logo）。
	if !u.expandAn.active && u.logoRight.update(gtx.Source) {
		u.requestLogoMenu()
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

// beginDrag 记录拖动基准（窗口左上角 + 光标位置，铁律 2 绝对跟踪；按下时窗口未
// 就绪则忽略本次触发）。基点取逻辑位 u.x/u.y 而非 windowRectPx——停靠态按下先
// undockInstant 记账（D55，SetWindowPos 帧末才发），此刻 OS 矩形还是滑出位；按旧值
// 起基，点击期间 ≥1px 抖动的 moveDrag 会把贴齐位回退成滑出位（离边超 snapDp →
// 布防/停靠断链，§15.1 实测缺陷）。
func (u *UI) beginDrag() {
	if u.hwnd != 0 {
		u.dragWin0 = point{x: u.x, y: u.y}
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
