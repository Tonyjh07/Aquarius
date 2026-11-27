package uigui

// 窗口管理（§15.7/D60）：设置/会话历史/欢迎三类功能窗 = 独立常规 OS 窗口，
// 各自 app.Window + 独立事件循环 goroutine。
//   - 形态：Decorated(true) 常规窗——不接主窗专属机制（形裁 SetWindowRgn、LWA_ALPHA、
//     淡出 overlay、位置记忆/停靠、置顶、WM_CLOSE→隐藏子类化、任务栏隐藏）；
//   - 并发：次窗自持状态，与主窗状态机无共享可变态——跨窗只经注册表（互斥锁）与
//     线程安全回调；win32Run 单槽 / mainHWND / ovl 是主窗专属，次窗一律不碰
//     （§15.6 铁律 1 的线程投递槽按窗各自持有）；
//   - 生命周期：单实例防重开（重开 = 尽力聚焦）、次窗关闭 = 真关闭（不走主窗
//     「Alt+F4 = 隐藏」语义）、退出经 UI.Close 收编全部次窗；
//   - 注册表 spawn 可注入——headless 测试用假开窗器（§15.5 不进 CI 图形路径）。

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"

	"image"
	"sync"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// winKind 功能窗种类（§15.7 窗口清单；设置窗数据面随设置步实现）。
type winKind int

const (
	winSettings winKind = iota
	winHistory
	winWelcome
	winRaw   // 查看原文（D99：气泡右键原始块只读次窗）
	winImage // 图片预览（D207：转写区缩略图点击 → 看原图）
	winView  // 插件面板（D346：/plugin view 打开，组件树次窗）
)

// winKey 窗口身份。内置窗只有 kind；插件面板按 (plugin, view) 各开一个窗——同一插件的
// 两个视图并存，同一视图重开 = 聚焦（D351）。
//
// 键不是封闭枚举，这是给「插件自持窗口」留的口：任何持有 app.Window 的实现都能被
// `winHost.spawn` 塞进来，收编/关窗/主题广播（winHandle）不用改。但那是**不承诺**的
// 扩展方向——插件侧没有 gio 类型，也不打算有。
type winKey struct {
	kind   winKind
	plugin string // 仅 winView
	view   string // 仅 winView
}

// keyOf 内置窗的身份。
func keyOf(k winKind) winKey { return winKey{kind: k} }

// winGeom 窗口尺寸（dp）。
type winGeom struct {
	width, height, minW, minH int
}

// title 窗标题（app.Title）。
func (k winKind) title() string {
	switch k {
	case winSettings:
		return i18n.T("ui.win.title.settings")
	case winHistory:
		return i18n.T("ui.win.title.history")
	case winRaw:
		return i18n.T("ui.win.title.raw")
	case winImage:
		return i18n.T("ui.win.title.image")
	case winView:
		// 窗标题固定：视图标题由插件给、会随动作变，而 app.Title 只在建窗时设一次。
		// 当前视图标题画在窗内标题行（pluginview.go 的 header）。
		return i18n.T("ui.win.title.view")
	default:
		return i18n.T("ui.win.title.welcome")
	}
}

// placeholder 占位窗正文（数据面留后续步；会话历史已由 historyFrame、设置窗经表单、
// 欢迎窗经 welcomeFrame（D141）渲染，此处仅作各自帧未构建时的回退显示）。
func (k winKind) placeholder() string {
	switch k {
	case winSettings:
		return i18n.T("ui.win.ph.settings")
	case winHistory:
		return "" // D112/S3：由 historyFrame 渲染，此处不应到达
	case winRaw:
		return "" // D99：专用帧（rawViewFrame），不走占位正文
	case winImage:
		return "" // D207：专用帧（imgViewFrame），不走占位正文
	case winView:
		return "" // D346：专用帧（viewFrame），不走占位正文
	case winWelcome:
		return "" // D141：专用帧（welcomeFrame），不走占位正文
	default:
		return i18n.T("ui.win.ph.welcome")
	}
}

// geometry 常规窗尺寸（dp；宽/高/最小宽/最小高——与悬浮球几何无关）。
func (k winKind) geometry() winGeom {
	switch k {
	case winSettings:
		// S4d/D142：左侧 140dp 导航栏 + 右页内容 + 全局底栏（分页后需要更宽的内容区）。
		return winGeom{720, 560, 620, 420}
	case winHistory:
		// §5.4：左栏 200dp + 右栏关系图 + 状态栏；min 620×360。
		return winGeom{720, 560, 620, 360}
	case winRaw:
		return winGeom{560, 520, 420, 360}
	case winView:
		// 面板要放得下表格与表单：比只读原文宽一档，最小尺寸留出「标题 + 两行 + 按钮」。
		// 插件可以在 ViewSpec.Window 里改，见 u.windowShape。
		return winGeom{640, 560, 480, 380}
	case winImage:
		// 预览要够大才看得出细节；最小尺寸留出「图 + 标题 + 提示」三行。
		return winGeom{900, 700, 480, 360}
	case winWelcome:
		// D153：标题 + 副标题 + 编号圆徽步骤条 + 四行分组卡 + 候选 chip + 内联连通结果 +
		// 页脚三钮。模拟稿比例接近 1:1，560×520 会把卡片压到只剩两行，故放宽到 660×640。
		return winGeom{660, 640, 560, 520}
	default:
		return winGeom{560, 440, 460, 320}
	}
}

// winHandle 单个次窗实例（winHost 持有；字段原子存取，跨线程安全）。
type winHandle struct {
	// plat 平台实现（spawn 时从 UI 取；次窗平台操作经 plat——主窗机制不作用于次窗）。
	plat platformAPI
	// done 次窗事件循环退出时 close（host watcher 据此摘除登记；close 提供 happens-before）。
	done chan struct{}
	// runFn 次窗 Window.Run（次窗 goroutine 写；聚焦投递用——不碰主窗投递槽）。
	runFn atomic.Pointer[func(func())]
	// hwnd 次窗 HWND（Win32ViewEvent 写；独立于主窗句柄，主窗机制不作用于次窗）。
	hwnd atomic.Uintptr
	// closeFn/raiseFn Gio 侧关闭/抬升动作（次窗 goroutine 装配；无原生句柄平台用）。
	closeFn atomic.Pointer[func()]
	raiseFn atomic.Pointer[func()]
	// pal 主题原子快照（spawn 初存、applyTheme 广播；次窗帧内校正自身 material 主题）。
	pal atomic.Pointer[palette]
	// invalidate 次窗重绘请求（runSecondary 装配；Invalidate 并发安全，§15.5）。
	invalidate atomic.Pointer[func()]
	// closePending 关闭请求早于 HWND 就绪时置位，attach 到件补投（Dekker 两步：
	// 请求先置位后读句柄、挂接先存句柄后读置位——任一次序至少投出一次关闭）。
	closePending atomic.Bool
	// reload「再打开一次」的重取请求（D351）：面板窗每帧 Swap 一次，真 = 重新问插件
	// 要一帧。其他窗不看它——重开即聚焦，没有要刷新的东西。
	reload atomic.Bool
}

// doneClosed 次窗循环是否已退出。
func (h *winHandle) doneClosed() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// attach HWND 挂接（次窗 goroutine 的 Win32ViewEvent 路径）；关闭请求先到则补投。
func (h *winHandle) attach(hwnd platform.Handle) {
	h.hwnd.Store(uintptr(hwnd))
	if h.closePending.Load() {
		h.postClose()
	}
}

// requestClose 请求关闭次窗（幂等；host 收编调用）：置 pending，HWND 就绪即投。
func (h *winHandle) requestClose() {
	h.closePending.Store(true)
	h.postClose()
}

// postClose 请求关闭次窗：有原生句柄时走平台关闭消息（Windows：WM_CLOSE → Gio 默认
// 销毁 → DestroyEvent，与真关闭语义一致）；无句柄平台（非 Windows 降级）走 Gio 自身的
// 窗口关闭动作——两条路都落到次窗事件循环的退出。幂等：重复请求最终只销毁一次。
func (h *winHandle) postClose() {
	if hwnd := h.hwnd.Load(); hwnd != 0 && h.plat != nil {
		h.plat.CloseWindow(platform.Handle(hwnd))
		return
	}
	if f := h.closeFn.Load(); f != nil {
		(*f)()
	}
}

// focus 尽力聚焦（单实例重开）：有原生句柄走平台（经该窗自己的窗口线程投递）；否则走
// Gio 的抬升动作（非 Windows 降级）。
func (h *winHandle) focus() {
	if hwnd := h.hwnd.Load(); hwnd != 0 && h.plat != nil {
		if run := h.runFn.Load(); run != nil {
			h.plat.FocusWindow(platform.Handle(hwnd), *run)
			return
		}
	}
	if f := h.raiseFn.Load(); f != nil {
		(*f)()
	}
}

// winHost 功能窗注册表（§15.7 生命周期）：单实例防重开 + 退出收编。
// spawn 可注入（headless 测试）；锁内只做登记，跨线程投递（聚焦/关闭）放锁外。
//
// 单实例按 key 算：内置窗每 kind 一个；插件面板每个 (plugin, view) 一个——两个视图
// 可以同时开着，同一个视图重开只是聚焦。
type winHost struct {
	mu    sync.Mutex
	live  map[winKey]*winHandle
	spawn func(winKey) *winHandle // nil = 无开窗能力（headless）：openWin no-op
}

// newWinHost 构造注册表（spawn 见字段注释）。
func newWinHost(spawn func(winKey) *winHandle) *winHost {
	return &winHost{live: make(map[winKey]*winHandle), spawn: spawn}
}

// openWin 打开内置功能窗。
func (h *winHost) openWin(k winKind) { h.open(keyOf(k)) }

// openView 打开一个插件面板窗（同一 (plugin, view) 已开 = 聚焦并重取一帧）。
func (h *winHost) openView(plugin, view string) {
	h.open(winKey{kind: winView, plugin: plugin, view: view})
}

// open 打开一个窗口：已开 = 尽力聚焦（不开第二实例）；上一实例已退出但
// watcher 未及摘除时直接补开。用户关窗（X/WM_CLOSE）经 watcher 摘除登记。
func (h *winHost) open(key winKey) {
	h.mu.Lock()
	if cur, ok := h.live[key]; ok {
		if !cur.doneClosed() {
			h.mu.Unlock()
			cur.reload.Store(true) // 再打开一次 = 重新问插件要一帧（面板窗用，其他窗忽略）
			cur.focus()
			return
		}
		delete(h.live, key) // 循环已退出、摘除未及：本条视同已关
	}
	if h.spawn == nil {
		h.mu.Unlock()
		return
	}
	hs := h.spawn(key)
	h.live[key] = hs
	h.mu.Unlock()
	go func() { // 实例退出 → 摘除登记（身份校验：不误删后开的同类实例）
		<-hs.done
		h.mu.Lock()
		if h.live[key] == hs {
			delete(h.live, key)
		}
		h.mu.Unlock()
	}()
}

// closeWin 关闭单窗；false = 本就未开。
func (h *winHost) closeWin(k winKind) bool { return h.close(keyOf(k)) }

// close 关闭指定 key 的窗；false = 本就未开。
func (h *winHost) close(key winKey) bool {
	h.mu.Lock()
	hs, ok := h.live[key]
	if ok {
		delete(h.live, key)
	}
	h.mu.Unlock()
	if ok {
		hs.requestClose()
	}
	return ok
}

// closeViewsOf 关掉某个插件的全部面板窗（停用插件时发），返回关掉的窗数。
// 按插件关而不是按 key 关：那个插件可能同时开着几个视图。
func (h *winHost) closeViewsOf(plugin string) int {
	h.mu.Lock()
	var hit []*winHandle
	for k, hs := range h.live {
		if k.kind == winView && k.plugin == plugin {
			delete(h.live, k)
			hit = append(hit, hs)
		}
	}
	h.mu.Unlock()
	for _, hs := range hit {
		hs.requestClose()
	}
	return len(hit)
}

// closeAll 退出收编全部次窗（返回收编数；幂等）。
func (h *winHost) closeAll() int {
	h.mu.Lock()
	live := h.live
	h.live = make(map[winKey]*winHandle)
	h.mu.Unlock()
	n := 0
	for _, hs := range live {
		hs.requestClose()
		n++
	}
	return n
}

// isOpen 查询内置窗的开窗状态（测试/菜单勾选规则）。
func (h *winHost) isOpen(k winKind) bool { return h.isOpenKey(keyOf(k)) }

// isOpenKey 查询指定 key 的开窗状态。
func (h *winHost) isOpenKey(key winKey) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur, ok := h.live[key]
	return ok && !cur.doneClosed()
}

// invalidateKind 对已开的 k 窗请求重绘（D99：rawView 原子槽更新后的重绘触发；
// 未开/已退出 = no-op）。线程安全（锁内取句柄、锁外投递）。
func (h *winHost) invalidateKind(k winKind) { h.invalidateKey(keyOf(k)) }

// invalidateKey 对指定 key 的已开窗请求重绘。
func (h *winHost) invalidateKey(key winKey) {
	h.mu.Lock()
	cur, ok := h.live[key]
	h.mu.Unlock()
	if ok && !cur.doneClosed() {
		if inv := cur.invalidate.Load(); inv != nil {
			(*inv)()
		}
	}
}

// propagatePalette 广播主题快照到全部次窗并请求重绘（§15.7/D61：跨窗只经原子
// 快照 + Invalidate，不共享裸字段）。
func (h *winHost) propagatePalette(p *palette) {
	h.mu.Lock()
	live := make([]*winHandle, 0, len(h.live))
	for _, hs := range h.live {
		live = append(live, hs)
	}
	h.mu.Unlock()
	for _, hs := range live {
		hs.pal.Store(p)
		if inv := hs.invalidate.Load(); inv != nil {
			(*inv)()
		}
	}
}

// spawnSecondary 起次窗（独立 goroutine + 独立 app.Window；不接主窗任何全局态，
// §15.7 形态与并发模型）。返回的句柄由注册表持有。
//
// 标题与尺寸在这里一次算定并交给窗口 goroutine：帧内不再查插件声明表（Views() 每次
// 调用都要问插件，帧内问一次就是每帧问一次）。
func (u *UI) spawnSecondary(key winKey) *winHandle {
	ctl := &winHandle{done: make(chan struct{}), plat: u.plat}
	ctl.pal.Store(u.pal.Load()) // 初始主题快照（spawn 可能在托盘线程，原子读）
	title, geom := u.windowShape(key)
	w := new(app.Window)
	go u.runSecondary(w, key, ctl, title, geom)
	return ctl
}

// windowShape 一个窗口的标题与尺寸。内置窗取常量；面板窗在常量之上叠加插件声明
// （ViewSpec.Window），夹取后兑现——插件只说想要多大，窗口是宿主的。
func (u *UI) windowShape(key winKey) (string, winGeom) {
	if key.kind != winView {
		return key.kind.title(), key.kind.geometry()
	}
	title, g := i18n.T("ui.win.title.view"), winView.geometry()
	if u.opts.Views == nil {
		return title, g
	}
	for _, v := range u.opts.Views.Views() {
		if v.Plugin != key.plugin || v.ID != key.view {
			continue
		}
		if v.Title != "" {
			title = v.Title // 一窗一视图，标题可以是插件文案（不必等帧内更新）
		}
		g = applyWinSpec(g, v.Window)
		break
	}
	return title, g
}

// applyWinSpec 把插件声明的尺寸叠到缺省值上。零值 = 不动；越界夹进 [320,4096]，
// 最小尺寸不超过最终尺寸——插件写错不该让窗口开成一个点或撑满整块屏。
func applyWinSpec(g winGeom, s port.UIWindow) winGeom {
	if s.Width > 0 {
		g.width = clampDp(s.Width, 320, 4096)
	}
	if s.Height > 0 {
		g.height = clampDp(s.Height, 320, 4096)
	}
	if s.MinWidth > 0 {
		g.minW = clampDp(s.MinWidth, 240, g.width)
	} else if g.minW > g.width {
		g.minW = g.width
	}
	if s.MinHeight > 0 {
		g.minH = clampDp(s.MinHeight, 200, g.height)
	} else if g.minH > g.height {
		g.minH = g.height
	}
	return g
}

// clampDp 夹取一个 dp 值。
func clampDp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// runSecondary 次窗事件循环（每窗一 goroutine、自持状态；不排空主窗 inbox——
// 次窗与渲染状态机无共享可变态，跨窗只经注册表与回调，§15.7 并发模型）。
// 关闭 = 真关闭：次窗不挂 WM_CLOSE 子类化，X/收编投递的 WM_CLOSE 走 Gio 默认
// 销毁 → DestroyEvent → 退出。
func (u *UI) runSecondary(w *app.Window, key winKey, ctl *winHandle, title string, geom winGeom) {
	defer close(ctl.done)
	k := key.kind
	runFn := w.Run
	ctl.runFn.Store(&runFn) // 次窗自己的线程投递槽——主窗投递槽归平台层，不碰
	inval := w.Invalidate
	ctl.invalidate.Store(&inval) // 主题广播后重绘请求（并发安全）
	// Gio 侧关闭/抬升动作（无原生句柄平台走这两条；Perform 并发安全，§15.5）。
	closeFn := func() { w.Perform(system.ActionClose) }
	raiseFn := func() { w.Perform(system.ActionRaise) }
	ctl.closeFn.Store(&closeFn)
	ctl.raiseFn.Store(&raiseFn)
	var form *settingsForm
	if k == winSettings {
		form = newSettingsForm(u) // 开窗现取快照（Options 回调；nil = 空表/只读占位）
		form.win = w              // 连通性测试异步结果的 Invalidate 回灌（D110②）
	}
	var raw *rawViewState
	if k == winRaw {
		// D99：查看原文帧状态（仅本窗 goroutine 读写）。D353 起带「复制 Markdown」，
		// 注入本窗重绘：次窗事件驱动，不请一次帧，「已复制」就永远挂在那儿。
		raw = newRawViewState()
		raw.inval = w.Invalidate
	}
	var vw *pluginViewState
	if k == winView {
		// D346/D351：面板帧状态（仅本窗 goroutine 读写）；一窗钉住一个 (plugin, view)，
		// 换视图 = 另一个窗。动作完成后请求本窗重绘。
		vw = newPluginViewState(u, key.plugin, key.view, w.Invalidate)
	}
	var iview *imageViewState
	if k == winImage {
		// D207：图片预览帧状态（Esc = 真关窗）；解码 goroutine 完成后请求本窗重绘。
		iview = newImageViewState(ctl.requestClose, w.Invalidate)
	}
	var hist *historyState
	if k == winHistory {
		hist = newHistoryState(u) // D112/S3：会话历史帧状态（仅本窗 goroutine 读写）
	}
	var welc *welcomeForm
	if k == winWelcome {
		// D141：欢迎 / 首次运行引导（in-app 宿主——设置窗入口 + 首跑自动开一次）。
		welc = newUISideWelcome(u.opts.Welcome, ctl.requestClose)
		welc.env.inval = w.Invalidate // 连通性测试异步结果回灌重绘
	}
	w.Option(
		app.Title(title),
		app.Size(unit.Dp(geom.width), unit.Dp(geom.height)),
		app.MinSize(unit.Dp(geom.minW), unit.Dp(geom.minH)),
		app.Decorated(true), // 常规装饰窗（D60：无边框形裁等主窗机制一律不接）
	)
	th := newTheme(u.plat.SystemFontCandidates(), u.plat.SymbolFontCandidates()) // 每窗独立主题实例（material：不同顶层窗应各自持有 Shaper）
	var applied *palette                                                         // 本窗已校正到的快照（帧内只读写本 goroutine）
	var ops op.Ops
	for {
		ev := w.Event()
		switch e := ev.(type) {
		case app.FrameEvent:
			if p := ctl.pal.Load(); p != nil && p != applied {
				// 次窗套 Paper：材料主题的默认文字/底与按钮对比色都取 Paper 令牌。
				// 主窗的 th 不经过这里，配色因此保持原样（D148）。
				applyPaperTheme(th, resolvePaper(p.mode(), p.isDark()))
				applied = p
			}
			gtx := app.NewContext(&ops, e)
			if form != nil {
				settingsFrame(gtx, th, u, form) // 设置窗 = 核心档表单（§15.7/D60）
			} else if raw != nil {
				rawViewFrame(gtx, th, u, raw) // 查看原文 = 只读编辑器（D99）
			} else if vw != nil {
				if ctl.reload.Swap(false) {
					vw.reload() // 再打开一次：重新问插件要一帧（D351）
				}
				viewFrame(gtx, th, u, vw) // D346：插件面板 = 组件树（拉模式）
			} else if iview != nil {
				imgViewFrame(gtx, th, u, iview) // 图片预览 = 适配 + 缩放平移（D207）
			} else if hist != nil {
				historyFrame(gtx, th, u, hist) // 会话历史 = 左栏+右图+状态栏（D112/S3）
			} else if welc != nil {
				welcomeFrame(gtx, th, welc) // 欢迎 / 首次运行引导（D141）
			} else {
				secondaryFrame(gtx, th, k)
			}
			e.Frame(&ops)
		case app.DestroyEvent:
			return
		default:
			if h, ok := u.viewEvent(ev); ok {
				ctl.attach(h)
			}
		}
	}
}

// secondaryFrame 占位窗单帧（会话历史/欢迎；设置窗走 settingsFrame）。
// 常规窗不做形裁/羽化/淡出带（§15.7 形态）：Paper 窗体面铺底 + 正文居中。
func secondaryFrame(gtx layout.Context, th *material.Theme, k winKind) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, paperOf().win,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(32)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return paperBody(th, k.placeholder())(gtx)
				})
			})
		}),
	)
}
