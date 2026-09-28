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
	"image"
	"sync"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// winKind 功能窗种类（§15.7 窗口清单；设置窗数据面随设置步落地）。
type winKind int

const (
	winSettings winKind = iota
	winHistory
	winWelcome
)

// title 窗标题（app.Title）。
func (k winKind) title() string {
	switch k {
	case winSettings:
		return "Aquarius 设置"
	case winHistory:
		return "Aquarius 会话历史"
	default:
		return "Aquarius 欢迎"
	}
}

// placeholder 占位窗正文（会话历史/欢迎数据面留后续步；设置窗经表单渲染，
// 此处仅作表单未构建时的回退显示）。
func (k winKind) placeholder() string {
	switch k {
	case winSettings:
		return "设置窗表单未就绪。"
	case winHistory:
		return "会话历史——即将提供（列表依赖未来切换会话命令）。"
	default:
		return "欢迎使用 Aquarius——首次运行引导即将提供。"
	}
}

// geometry 常规窗尺寸（dp；宽/高/最小宽/最小高——与悬浮球几何无关）。
func (k winKind) geometry() (width, height, minW, minH int) {
	switch k {
	case winSettings:
		return 560, 620, 480, 420
	case winHistory:
		return 520, 560, 420, 360
	default:
		return 560, 440, 460, 320
	}
}

// winHandle 单个次窗实例（winHost 持有；字段原子存取，跨线程安全）。
type winHandle struct {
	// done 次窗事件循环退出时 close（host watcher 据此摘除登记；close 提供 happens-before）。
	done chan struct{}
	// runFn 次窗 Window.Run（次窗 goroutine 写；聚焦投递用——不碰 win32Run 主窗单槽）。
	runFn atomic.Pointer[func(func())]
	// hwnd 次窗 HWND（Win32ViewEvent 写；独立于 mainHWND，主窗机制不作用于次窗）。
	hwnd atomic.Uintptr
	// pal 主题原子快照（spawn 初存、applyTheme 广播；次窗帧内校正自身 material 主题）。
	pal atomic.Pointer[palette]
	// invalidate 次窗重绘请求（runSecondary 装配；Invalidate 并发安全，§15.5）。
	invalidate atomic.Pointer[func()]
	// closePending 关闭请求早于 HWND 就绪时置位，attach 到件补投（Dekker 两步：
	// 请求先置位后读句柄、挂接先存句柄后读置位——任一次序至少投出一次关闭）。
	closePending atomic.Bool
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
func (h *winHandle) attach(hwnd uintptr) {
	h.hwnd.Store(hwnd)
	if h.closePending.Load() {
		h.postClose()
	}
}

// requestClose 请求关闭次窗（幂等；host 收编调用）：置 pending，HWND 就绪即投。
func (h *winHandle) requestClose() {
	h.closePending.Store(true)
	if h.hwnd.Load() != 0 {
		h.postClose()
	}
}

// winHost 功能窗注册表（§15.7 生命周期）：单实例防重开 + 退出收编。
// spawn 可注入（headless 测试）；锁内只做登记，跨线程投递（聚焦/关闭）放锁外。
type winHost struct {
	mu    sync.Mutex
	live  map[winKind]*winHandle
	spawn func(winKind) *winHandle // nil = 无开窗能力（headless）：openWin no-op
}

// newWinHost 构造注册表（spawn 见字段注释）。
func newWinHost(spawn func(winKind) *winHandle) *winHost {
	return &winHost{live: make(map[winKind]*winHandle), spawn: spawn}
}

// openWin 打开功能窗：已开 = 尽力聚焦（不开第二实例）；上一实例已退出但
// watcher 未及摘除时直接补开。用户关窗（X/WM_CLOSE）经 watcher 摘除登记。
func (h *winHost) openWin(k winKind) {
	h.mu.Lock()
	if cur, ok := h.live[k]; ok {
		if !cur.doneClosed() {
			h.mu.Unlock()
			cur.focus()
			return
		}
		delete(h.live, k) // 循环已退出、摘除未及：本条视同已关
	}
	if h.spawn == nil {
		h.mu.Unlock()
		return
	}
	hs := h.spawn(k)
	h.live[k] = hs
	h.mu.Unlock()
	go func() { // 实例退出 → 摘除登记（身份校验：不误删后开的同类实例）
		<-hs.done
		h.mu.Lock()
		if h.live[k] == hs {
			delete(h.live, k)
		}
		h.mu.Unlock()
	}()
}

// closeWin 关闭单窗；false = 本就未开。
func (h *winHost) closeWin(k winKind) bool {
	h.mu.Lock()
	hs, ok := h.live[k]
	if ok {
		delete(h.live, k)
	}
	h.mu.Unlock()
	if ok {
		hs.requestClose()
	}
	return ok
}

// closeAll 退出收编全部次窗（返回收编数；幂等）。
func (h *winHost) closeAll() int {
	h.mu.Lock()
	live := h.live
	h.live = make(map[winKind]*winHandle)
	h.mu.Unlock()
	n := 0
	for _, hs := range live {
		hs.requestClose()
		n++
	}
	return n
}

// isOpen 查询开窗状态（测试/菜单勾选口径）。
func (h *winHost) isOpen(k winKind) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur, ok := h.live[k]
	return ok && !cur.doneClosed()
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
func (u *UI) spawnSecondary(k winKind) *winHandle {
	ctl := &winHandle{done: make(chan struct{})}
	ctl.pal.Store(u.pal.Load()) // 初始主题快照（spawn 可能在托盘线程，原子读）
	w := new(app.Window)
	go u.runSecondary(w, k, ctl)
	return ctl
}

// runSecondary 次窗事件循环（每窗一 goroutine、自持状态；不排空主窗 inbox——
// 次窗与渲染状态机无共享可变态，跨窗只经注册表与回调，§15.7 并发模型）。
// 关闭 = 真关闭：次窗不挂 WM_CLOSE 子类化，X/收编投递的 WM_CLOSE 走 Gio 默认
// 销毁 → DestroyEvent → 退出。
func (u *UI) runSecondary(w *app.Window, k winKind, ctl *winHandle) {
	defer close(ctl.done)
	runFn := w.Run
	ctl.runFn.Store(&runFn) // 次窗自己的线程投递槽——win32Run 单槽归主窗，不碰
	inval := w.Invalidate
	ctl.invalidate.Store(&inval) // 主题广播后重绘请求（并发安全）
	var form *settingsForm
	if k == winSettings {
		form = newSettingsForm(u) // 开窗现取快照（Options 回调；nil = 空表/只读占位）
	}
	width, height, minW, minH := k.geometry()
	w.Option(
		app.Title(k.title()),
		app.Size(unit.Dp(width), unit.Dp(height)),
		app.MinSize(unit.Dp(minW), unit.Dp(minH)),
		app.Decorated(true), // 常规装饰窗（D60：无边框形裁等主窗机制一律不接）
	)
	th := newTheme()     // 每窗独立主题实例（material：不同顶层窗应各自持有 Shaper）
	var applied *palette // 本窗已校正到的快照（帧内只读写本 goroutine）
	var ops op.Ops
	for {
		ev := w.Event()
		switch e := ev.(type) {
		case app.FrameEvent:
			if p := ctl.pal.Load(); p != nil && p != applied {
				th.Palette.Fg, th.Palette.Bg = p.fg, p.bg
				applied = p
			}
			gtx := app.NewContext(&ops, e)
			if form != nil {
				settingsFrame(gtx, th, u, form) // 设置窗 = 核心档表单（§15.7/D60）
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
// 常规窗不做形裁/羽化/淡出带（§15.7 形态）：主题 Bg 铺底 + 正文居中。
func secondaryFrame(gtx layout.Context, th *material.Theme, k winKind) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, th.Bg,
				clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Op())
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(32)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.Body1(th, k.placeholder()).Layout(gtx)
				})
			})
		}),
	)
}
