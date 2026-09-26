package uigui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"gioui.org/app"
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

// 窗口尺寸（展开态单窗骨架：转写浮层在上、输入栏在下；idle 悬浮球收缩形态
// 与"球→输入栏"展开/收起留待下一步，§15.1）。
const (
	winWidthDp   = 560
	winHeightDp  = 460
	winMinWidth  = 420
	winMinHeight = 240
	// regionRadiusDiv 形裁圆角半径 = 窗口短边 / div（物理 px 与窗口尺寸同比缩放，
	// 等效 dp 常量——免 GetDpiForWindow 换算）。
	regionRadiusDiv = 10
)

// 主题令牌（§15.4 MVP：品牌色 + 输入栏浅白/浅灰；深浅两版与多预设后补）。
var (
	brandColor = color.NRGBA{R: 0x00, G: 0xAE, B: 0xEF, A: 0xFF}
	pillBg     = color.NRGBA{R: 0xFA, G: 0xFA, B: 0xFC, A: 0xFF} // 输入栏浅白
	windowBg   = color.NRGBA{R: 0xEC, G: 0xEF, B: 0xF3, A: 0xFF} // 窗口浅灰
	textDim    = color.NRGBA{R: 0x8A, G: 0x8F, B: 0x98, A: 0xFF}
	textMuted  = color.NRGBA{R: 0x6B, G: 0x70, B: 0x78, A: 0xFF}
	textUser   = color.NRGBA{R: 0x1B, G: 0x6E, B: 0xA8, A: 0xFF}
	textError  = color.NRGBA{R: 0xD9, G: 0x3A, B: 0x3A, A: 0xFF}
	textNotice = color.NRGBA{R: 0xC0, G: 0x77, B: 0x00, A: 0xFF}
	textSystem = color.NRGBA{R: 0x8E, G: 0x6B, B: 0xC4, A: 0xFF}
)

// point/rect Win32 坐标对（中性定义：非 Windows 构建仅作占位类型）。
type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }

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

// 位置记忆（§15.1：拖拽 + 位置记忆，含多显示器工作区夹取）。
type posRec struct{ X, Y int32 }

func loadPos(path string) (int32, int32, bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	var p posRec
	if json.Unmarshal(src, &p) != nil {
		return 0, 0, false
	}
	return p.X, p.Y, true
}

func savePos(path string, x, y int32) {
	src, _ := json.Marshal(posRec{X: x, Y: y})
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = os.WriteFile(path, src, 0o644)
}

// newTheme 主题组装（§15.4 骨架：系统中文字体优先——gofont 无 CJK，spike 实证路径；
// 失败回落 gofont）。
func newTheme() *material.Theme {
	th := material.NewTheme()
	if faces := loadCJKFaces(); len(faces) > 0 {
		th.Shaper = text.NewShaper(text.WithCollection(faces))
	} else {
		th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	}
	return th
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
			u.layout(gtx)
			e.Frame(&ops)
		case app.DestroyEvent:
			// 用户关窗 = 输入流结束（Next → EOF → 装配根退出，退出码 0；
			// 创建失败 Err 非空 → Next 上抛，退出码 1）。
			// "关窗 = 隐藏、退出经菜单"的托盘常驻语义留托盘步接入（§15.1）。
			u.signalEOF(e.Err)
			return
		default:
			if h, ok := u.viewEvent(ev); ok {
				u.onHWND(h)
			}
		}
	}
}

// onHWND Win32ViewEvent 投递的窗口句柄：位置记忆恢复 + 形裁（§15.1/§15.6）。
func (u *UI) onHWND(h uintptr) {
	if u.hwnd != 0 {
		return
	}
	u.hwnd = h
	atomic.StoreUintptr(&mainHWND, h)
	rc, ok := windowRectPx()
	if !ok {
		return
	}
	w, ht := rc.right-rc.left, rc.bottom-rc.top
	u.x, u.y = rc.left, rc.top
	if u.opts.PosFile != "" {
		if sx, sy, found := loadPos(u.opts.PosFile); found {
			nx, ny := clampToWorkArea(sx, sy, w, ht)
			moveWindowTo(nx, ny)
			u.x, u.y = nx, ny
		}
	}
	// 形裁（§15.6 spike 实证）：窗口可见区 = 圆角矩形，区域外不可见 + 点击穿透；
	// 不依赖任何透明技术。改尺寸需重算（骨架固定尺寸，resize 后补）。
	r := min(w, ht) / regionRadiusDiv
	applyRegion(0, 0, w, ht, r*2)
}

// layout 悬浮窗骨架：背景（可拖动）| 转写浮层 + 状态行 + 输入栏。
func (u *UI) layout(gtx layout.Context) layout.Dimensions {
	// 事件消费顺序：编辑器按键 → 控件点击 → 拖动。指针命中规则（io/pointer doc）：
	// 最上层 area 优先、其内无 handler 才下穿到背景拖动区——按钮/编辑器/列表在
	// 内容层各自注册，其余空白处可拖窗。
	u.updateEditor(gtx)
	u.updateClicks(gtx)
	u.updateDrag(gtx)

	size := gtx.Constraints.Max
	return layout.Stack{Alignment: layout.N}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.Fill(gtx.Ops, windowBg)
			st := clip.Rect{Max: size}.Push(gtx.Ops)
			u.drag.Add(gtx.Ops)
			st.Pop()
			return layout.Dimensions{Size: size}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, u.transcript),
				layout.Rigid(u.statusBar),
				layout.Rigid(u.inputBar),
			)
		}),
	)
}

// updateEditor 消费编辑器事件（Enter → SubmitEvent → 提交）。material.Editor.Layout
// 内部也会 Update 但丢弃返回的 SubmitEvent——必须在渲染前自己循环取尽。
func (u *UI) updateEditor(gtx layout.Context) {
	if u.m.confirm != nil {
		return // 确认态：输入栏是按钮组，编辑器不消费按键（§15.2）
	}
	gtx.Execute(key.FocusCmd{Tag: &u.editor}) // 常驻焦点（窗口内唯一可聚焦控件）
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

// submitEditor 提交编辑器内容（发送键与 Enter 同路）；确认态由 model.submit 路由应答。
func (u *UI) submitEditor() {
	text := strings.TrimSpace(u.editor.Text())
	if text == "" {
		return
	}
	u.editor.SetText("")
	u.m.submit(text)
}

// updateClicks 控件行为：发送/停止/允许/拒绝。
func (u *UI) updateClicks(gtx layout.Context) {
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
func (u *UI) updateDrag(gtx layout.Context) {
	for {
		ev, ok := u.drag.Update(gtx.Metric, gtx.Source, gesture.Both)
		if !ok {
			break
		}
		switch ev.Kind {
		case pointer.Press:
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
			if u.dragging && u.opts.PosFile != "" {
				savePos(u.opts.PosFile, u.x, u.y)
			}
			u.dragging = false
		}
	}
}

// blockView 渲染期块视图（live = 本帧实时追加的思考/草稿，非定稿块）。
type blockView struct {
	kind blockKind
	text string
	secs int
	live bool
}

// frameItems 定稿块 + 实时思考/草稿（流式可见；对齐 D33"流式原样、定稿渲染"口径）。
func (u *UI) frameItems() []blockView {
	items := make([]blockView, 0, len(u.m.blocks)+2)
	for _, b := range u.m.blocks {
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

// transcript 转写浮层（§15.3 骨架：块列表 + 尾随跟随；markdown/折叠 chip/滚动
// 细化留后续步骤）。
func (u *UI) transcript(gtx layout.Context) layout.Dimensions {
	items := u.frameItems()
	if len(items) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, "输入 /help 查看命令")
			s.Color = textDim
			return s.Layout(gtx)
		})
	}
	return material.List(u.th, &u.list).Layout(gtx, len(items),
		func(gtx layout.Context, i int) layout.Dimensions {
			return layout.Inset{
				Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(4), Bottom: unit.Dp(4),
			}.Layout(gtx, u.blockWidget(items[i]))
		})
}

// blockWidget 按块种类渲染（§15.3 骨架：分级配色的纯文本；完整 markdown、
// 思考折叠「已思考 · Ns」、工具折叠 chip 为后续增量——此处先完整展示）。
func (u *UI) blockWidget(it blockView) layout.Widget {
	switch it.kind {
	case blockThinking:
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
					return s.Layout(gtx)
				}),
			)
		}
	case blockUser:
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, "> "+it.text)
			s.Color = textUser
			return s.Layout(gtx)
		}
	case blockTool:
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Caption(u.th, it.text)
			s.Color = textDim
			return s.Layout(gtx)
		}
	case blockNotice:
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Caption(u.th, it.text)
			s.Color = textNotice
			return s.Layout(gtx)
		}
	case blockError:
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.Color = textError
			return s.Layout(gtx)
		}
	case blockSystem:
		return func(gtx layout.Context) layout.Dimensions {
			s := material.Body2(u.th, it.text)
			s.Color = textSystem
			return s.Layout(gtx)
		}
	default: // blockPlain / blockAssistant
		return material.Body2(u.th, it.text).Layout
	}
}

// statusBar 浮层底栏（§15.1：仅生成时显示"思考中/生成中 · 模型 · 权限档"）。
func (u *UI) statusBar(gtx layout.Context) layout.Dimensions {
	txt := u.statusText()
	if txt == "" {
		return layout.Dimensions{}
	}
	return layout.Inset{Left: unit.Dp(20), Right: unit.Dp(20)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			st := material.Caption(u.th, txt)
			st.Color = textMuted
			return st.Layout(gtx)
		})
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

// inputBar 输入栏（§15.2 骨架）：logo（拖拽把手）｜编辑器（确认态 = 提示行）｜
// 发送/停止；确认态整体切换为 [允许/拒绝] 按钮组（非模态）。
func (u *UI) inputBar(gtx layout.Context) layout.Dimensions {
	margin := layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(8), Bottom: unit.Dp(16)}
	return margin.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(72)
		w := gtx.Constraints.Max.X
		if w <= 0 {
			return layout.Dimensions{}
		}
		// 胶囊底：浅白叠浅灰窗口底 = 极简胶囊观感（真透明背景待后续形态步）。
		paint.FillShape(gtx.Ops, pillBg,
			clip.UniformRRect(image.Rectangle{Max: image.Pt(w, h)}, h/2).Op(gtx.Ops))
		gtx.Constraints = layout.Exact(image.Pt(w, h))
		return layout.Flex{
			Axis:      layout.Horizontal,
			Alignment: layout.Middle,
			Spacing:   layout.SpaceBetween,
		}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(12), Right: unit.Dp(8)}.Layout(gtx, u.logo)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if u.m.confirm != nil {
					return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(8)}.Layout(gtx,
						material.Body2(u.th, u.m.confirm.prompt).Layout)
				}
				ed := material.Editor(u.th, &u.editor, "Ask anything or type a command...")
				ed.TextSize = unit.Sp(15)
				return ed.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if u.m.confirm != nil {
						return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
							layout.Rigid(u.actionBtn(&u.allowBtn, "允许", brandColor)),
							layout.Rigid(u.actionBtn(&u.denyBtn, "拒绝", textMuted)),
						)
					}
					if u.generating.Load() {
						return u.actionBtn(&u.stopBtn, "停止", textError)(gtx)
					}
					return u.actionBtn(&u.sendBtn, "发送", brandColor)(gtx)
				})
			}),
		)
	})
}

// logo 品牌色圆钮（§15.1 单组件语义的骨架等价物；贴 assets/icon 与展开/收起
// 交互留形态步）。
func (u *UI) logo(gtx layout.Context) layout.Dimensions {
	d := gtx.Dp(44)
	paint.FillShape(gtx.Ops, brandColor,
		clip.UniformRRect(image.Rectangle{Max: image.Pt(d, d)}, d/2).Op(gtx.Ops))
	return layout.Dimensions{Size: image.Pt(d, d)}
}

// actionBtn 动作键（发送/停止/允许/拒绝；主题色底白字）。
func (u *UI) actionBtn(cl *widget.Clickable, label string, bg color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.Button(u.th, cl, label)
		b.Background = bg
		b.Color = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
		b.TextSize = unit.Sp(14)
		b.Inset = layout.Inset{
			Left: unit.Dp(14), Right: unit.Dp(14), Top: unit.Dp(8), Bottom: unit.Dp(8),
		}
		return b.Layout(gtx)
	}
}
