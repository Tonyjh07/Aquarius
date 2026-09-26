// spike/giospike —— Gio 悬浮球 GUI 能力实测（DESIGN §15.6 / D43 前置验证）。
//
// 验证清单：
//  1. 纯 Go 构建（CGO_ENABLED=0，构建期验证）+ Gio 事件循环 + Win32ViewEvent 取 HWND；
//  2. 色键透明（WS_EX_LAYERED+LWA_COLORKEY）/ 半透明（LWA_ALPHA）/ Win11 DWM 圆角；
//  3. 拖拽移动 + 位置记忆（SetWindowPos + JSON 持久化 + 工作区夹取）；
//  4. 托盘 + 全局快捷键（win32.go）；
//  5. 中文字形加载（系统 msyh.ttc → opentype.ParseCollection，gofont 无 CJK）。
//
// 目测项（人工确认）：色键模式下桌面是否透出/边缘毛刺、拖动跟随、Alt+Space 显隐、
// 托盘左右键、多显示器位置记忆。
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/gesture"
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

// 品牌色（DESIGN §15.4：logo/发送键），紫色仅为占位不用。
var (
	brandColor = color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF}
	pillBg     = color.NRGBA{R: 0xFA, G: 0xFA, B: 0xFC, A: 0xFF} // 输入栏浅白
	windowBg   = color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF3, A: 0xFF} // 窗口背景（形裁后仅胶囊可见）
)

// ui 悬浮球 UI 状态（渲染状态机极简：spike 只做 idle 胶囊形态）。
type ui struct {
	th   *material.Theme
	drag gesture.Drag
	// 拖动：以按下瞬间的光标屏幕坐标 + 窗口左上角为基准做绝对跟踪（修"回弹"：
	// 窗口移动会改变指针本地坐标，本地增量法与移动互为反馈）。
	dragging bool
	dragWin0 point // 按下时窗口左上角（屏幕坐标）
	dragCur0 point // 按下时光标位置（屏幕坐标）
	hwnd     uintptr
	x, y     int32 // 窗口屏幕坐标（拖动跟随 + 位置记忆）
	shapeOn  bool  // SetWindowRgn 形裁（胶囊裁切，替代透明）
	alphaOn  bool  // 半透明
	pillRect rect  // 胶囊在窗口内的矩形（形裁区域）
	btnShape widget.Clickable
	btnAlpha widget.Clickable
	btnExit  widget.Clickable
}

func main() {
	fmt.Println("=== Aquarius GUI spike（Gio v0.10.2，纯 Go，无 cgo） ===")
	fmt.Println("[build] 本二进制以 CGO_ENABLED=0 构建 → 见 go build 命令输出")
	fmt.Println("[font] 中文字形探测见 [font] 行；Win32 补位见 [hwnd]/[hotkey]/[tray]/[mon] 行")
	fmt.Println("目测项：①色键透明桌面透出与边缘 ②拖动跟随 ③Alt+Space 显隐 ④托盘左键显隐/右键菜单")

	go runShell() // 托盘 + 全局快捷键消息线程（win32.go）

	go func() {
		w := new(app.Window)
		runFn := w.Run // 修改性 Win32 调用统一走窗口线程（见 win32.onWindowThread）
		win32Run.Store(&runFn)
		u := newUI()
		onShow := func() { w.Invalidate() }
		onShowHook.Store(&onShow)
		w.Option(
			app.Title("Aquarius GUI spike"),
			app.Size(unit.Dp(760), unit.Dp(180)),
			app.MinSize(unit.Dp(520), unit.Dp(140)),
			app.Decorated(false), // 无边框 = 胶囊形态前提
			app.TopMost(true),    // 悬浮球常驻顶层（D43 形态）
		)
		var ops op.Ops
		frameN := 0
		for {
			switch e := w.Event().(type) {
			case app.FrameEvent:
				frameN++
				fmt.Printf("[frame] #%d\n", frameN) // 帧心跳：点击后帧停 = 事件循环断流
				gtx := app.NewContext(&ops, e)
				u.layout(gtx)
				e.Frame(&ops)
			case app.Win32ViewEvent:
				u.onHWND(e.HWND) // HWND 到手 → Win32 补位
			case app.DestroyEvent:
				os.Exit(0)
			}
		}
	}()
	app.Main()
}

// dumpStacks 抓全量 goroutine 堆栈（死锁现场）。
func dumpStacks() {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	_ = os.WriteFile("spike-stacks.log", buf[:n], 0o644)
	fmt.Println("[dump] goroutine 堆栈已写 spike-stacks.log（若 [frame] 心跳停止，此即死锁现场）")
}

// newUI 组装主题：优先系统中文字体（msyh.ttc 等），失败回落 gofont（无 CJK）。
func newUI() *ui {
	th := material.NewTheme()
	if faces := loadCJKFaces(); len(faces) > 0 {
		th.Shaper = text.NewShaper(text.WithCollection(faces))
		fmt.Printf("[font] 系统中文字体加载成功：%d 个字形面（界面中文可渲染）\n", len(faces))
	} else {
		th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
		fmt.Println("[font] 系统中文字体加载失败 → 回落 gofont（中文将显示为占位框，M5 需解决）")
	}
	return &ui{th: th}
}

// loadCJKFaces 加载 Windows 系统中文字体（M5 GUI 中文渲染的前置验证）。
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
		if err != nil {
			fmt.Printf("[font] ParseCollection(%s) 失败: %v\n", filepath.Base(p), err)
			continue
		}
		return faces
	}
	return nil
}

// onHWND Win32ViewEvent 投递的窗口句柄：位置记忆恢复 + 补位能力挂钩点。
func (u *ui) onHWND(h uintptr) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	atomic.StoreUintptr(&mainHWND, h)
	fmt.Printf("[hwnd] Win32ViewEvent.HWND = %#x → Win32 补位（定位/透明/圆角）可行\n", h)
	rc, ok := windowRectPx()
	if !ok {
		fmt.Println("[pos] GetWindowRect 失败")
		return
	}
	w, hgt := rc.right-rc.left, rc.bottom-rc.top
	if sx, sy, found := loadPos(); found {
		nx, ny := clampToWorkArea(sx, sy, w, hgt)
		moveWindowTo(nx, ny)
		u.x, u.y = nx, ny
		fmt.Printf("[pos] 位置记忆恢复: (%d,%d)%s\n", nx, ny, clampMark(sx, sy, nx, ny))
	} else {
		u.x, u.y = rc.left, rc.top
		fmt.Printf("[pos] 首次运行，初始位置 (%d,%d)；拖动后自动记忆\n", u.x, u.y)
	}
}

func clampMark(sx, sy, nx, ny int32) string {
	if sx != nx || sy != ny {
		return "（已夹进显示器工作区）"
	}
	return ""
}

// layout 悬浮球 idle 形态：透明/纯色背景 + 胶囊输入栏（logo + 提示文字 + 按钮行）。
func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	// 拖动增量 → SetWindowPos（拖拽移动验证；绝对跟踪法修回弹）。
	for {
		ev, ok := u.drag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
			fmt.Println("[input] Press")
			time.AfterFunc(3*time.Second, dumpStacks) // 点击后 3s 无恢复则抓全量堆栈定位死锁
			if rc, ok := windowRectPx(); ok {
				u.dragWin0 = point{x: rc.left, y: rc.top}
				u.dragCur0 = cursorPos()
				u.dragging = true
			}
		case pointer.Drag:
			if !u.dragging {
				break
			}
			cur := cursorPos()
			u.x = u.dragWin0.x + (cur.x - u.dragCur0.x)
			u.y = u.dragWin0.y + (cur.y - u.dragCur0.y)
			moveWindowTo(u.x, u.y)
		case pointer.Release, pointer.Cancel:
			fmt.Println("[input] Release/Cancel")
			if u.dragging {
				savePos(u.x, u.y)
			}
			u.dragging = false
		}
	}

	// 按钮行为（Win32 补位开关）。
	if u.btnShape.Clicked(gtx) {
		fmt.Println("[input] 形裁 clicked")
		u.shapeOn = !u.shapeOn
		if u.shapeOn {
			r := u.pillRect
			applyPillRegion(r.left, r.top, r.right-r.left, r.bottom-r.top)
		} else {
			clearPillRegion()
		}
	}
	if u.btnAlpha.Clicked(gtx) {
		fmt.Println("[input] 半透明 clicked")
		u.alphaOn = !u.alphaOn
		applyAlpha(u.alphaOn)
	}
	if u.btnExit.Clicked(gtx) {
		fmt.Println("[input] 退出 clicked")
		os.Exit(0)
	}

	size := gtx.Constraints.Max
	return layout.Stack{Alignment: layout.N}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.Fill(gtx.Ops, windowBg)
			// 全窗注册拖动区（按钮在上层，优先接收点击）。
			st := clip.Rect{Max: size}.Push(gtx.Ops)
			u.drag.Add(gtx.Ops)
			st.Pop()
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			pillH := gtx.Dp(76)
			px := gtx.Dp(20)
			py := (size.Y - pillH) / 2
			u.pillRect = rect{left: int32(px), top: int32(py),
				right: int32(size.X - px), bottom: int32(py + pillH)} // 形裁区域（窗口坐标）
			dp := func(p int) unit.Dp { return unit.Dp(float32(p) / gtx.Metric.PxPerDp) }
			return layout.Inset{Top: dp(py), Left: dp(px), Right: dp(px), Bottom: dp(py)}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(size.X-gtx.Dp(40), pillH))
					return u.pill(gtx)
				})
		}),
	)
}

// pill 胶囊输入栏（视觉稿 idle 形态的 spike 等价物）：
// logo 圆钮（品牌色）｜提示文字｜色键/半透明/圆角/退出 按钮。
func (u *ui) pill(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, pillBg, clip.UniformRRect(image.Rectangle{Max: size}, size.Y/2).Op(gtx.Ops))

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(10), Right: unit.Dp(12)}.Layout(gtx, u.logo)
		}),
		layout.Rigid(material.Body1(u.th, "Aquarius 悬浮球 spike").Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(8)}.Layout(gtx,
				material.Caption(u.th, "拖动移动 · Alt+Space 显隐 · 右键托盘菜单").Layout)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }),
		layout.Rigid(u.modeBtn(&u.btnShape, "形裁", u.shapeOn)),
		layout.Rigid(u.modeBtn(&u.btnAlpha, "半透", u.alphaOn)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(10)}.Layout(gtx,
				material.Button(u.th, &u.btnExit, "退出").Layout)
		}),
	)
}

// logo 品牌色圆钮（= 悬浮球本体的 spike 等价物，§15.1 单组件语义）。
func (u *ui) logo(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(48)
	r := image.Rectangle{Max: image.Pt(d, d)}
	paint.FillShape(gtx.Ops, brandColor, clip.UniformRRect(r, d/2).Op(gtx.Ops))
	return layout.Dimensions{Size: image.Pt(d, d)}
}

// modeBtn 开关样式按钮（激活态用品牌色描边示意）。
func (u *ui) modeBtn(cl *widget.Clickable, label string, on bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.Button(u.th, cl, label)
		b.TextSize = unit.Sp(12)
		b.Inset = layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(6), Bottom: unit.Dp(6)}
		if on {
			b.Background = brandColor
			b.Color = color.NRGBA{A: 0xFF}
		} else {
			b.Background = color.NRGBA{R: 0xE4, G: 0xE7, B: 0xEC, A: 0xFF}
			b.Color = color.NRGBA{R: 0x33, G: 0x36, B: 0x3B, A: 0xFF}
		}
		return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, b.Layout)
	}
}

// 位置记忆（§15.1：拖拽 + 位置记忆，含多显示器工作区夹取）。
const posFileName = "aquarius-giospike-pos.json"

type posRec struct{ X, Y int32 }

func posPath() string { return filepath.Join(os.TempDir(), posFileName) }

func loadPos() (int32, int32, bool) {
	src, err := os.ReadFile(posPath())
	if err != nil {
		return 0, 0, false
	}
	var p posRec
	if json.Unmarshal(src, &p) != nil {
		return 0, 0, false
	}
	return p.X, p.Y, true
}

func savePos(x, y int32) {
	src, _ := json.Marshal(posRec{X: x, Y: y})
	_ = os.WriteFile(posPath(), src, 0o644)
}
