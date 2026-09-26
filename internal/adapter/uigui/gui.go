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
//   - 修改性 Win32 调用一律经 Window.Run 送窗口线程（§15.6 铁律 1）。
//
// 平台边界：窗口壳仅 Windows 实测（形裁/定位见 win32_windows.go），其余平台由
// win32_other.go 桩接住构建、GUI 未适配。窗口循环不调 app.Main——Windows 的
// osMain 仅 select{}（Gio 自建窗口线程），库内调用会卡死装配根。
package uigui

import (
	"context"
	"image"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/gesture"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

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
)

// Status 状态行数据（浮层底栏生成时经回调现取，形状同 uitui.Status）。
type Status struct {
	Model  string
	Level  string
	Effort string // D34：推理档位（think off 时为空——effort 不显示）
}

// Options 装配选项（装配根注入）。
type Options struct {
	// Status 状态栏数据源；nil = 底栏只显示"思考中/生成中"。
	Status func() Status
	// PosFile 窗口位置记忆文件（§15.1）；空 = 不记忆。
	PosFile string
	// Interrupt 初始中断行为（装配根一般经 SetInterrupt 每轮注入，留 nil 即可）。
	Interrupt func()
}

// UI GUI 前端句柄（装配根按 uiFrontend 使用）。
type UI struct {
	opts Options

	// 桥接通道（装配根 goroutine ↔ 事件循环 goroutine）。
	inbox   chan uiMsg
	inCh    chan port.UserInput
	eofCh   chan struct{} // 关闭 = 输入流结束（广播：Next/Confirm 同时唤醒，天然粘滞）
	eofOnce sync.Once
	eofErr  error         // 写于 close 之前；close 提供 happens-before，读者安全
	done    chan struct{} // 事件循环退出（close）

	interrupt  atomic.Pointer[func()]
	generating atomic.Bool // 生成中（SetInterrupt 非空 = 一轮 Turn 进行中；驱动停止键）

	// w Gio 窗口句柄（构造后只读；headless = nil）。
	w *app.Window

	// 窗口侧状态（仅帧循环 goroutine 读写；headless 不触碰，构造成零值可用）。
	th       *material.Theme
	editor   widget.Editor
	sendBtn  widget.Clickable
	stopBtn  widget.Clickable
	allowBtn widget.Clickable
	denyBtn  widget.Clickable
	drag     gesture.Drag
	hwnd     uintptr
	x, y     int32 // 窗口屏幕坐标（拖动跟随 + 位置记忆）
	dragging bool
	dragWin0 point // 按下时窗口左上角（屏幕坐标，绝对跟踪修回弹，§15.6 铁律 2）
	dragCur0 point // 按下时光标位置（屏幕坐标）

	// 转写区手工滚动（D44：不用 widget.List——需要每行绝对矩形做逐元素形裁）。
	transcriptScroll gesture.Scroll
	scrollPx         int // 内容滚动偏移（物理 px，0 = 顶）
	contentH         int // 内容总高（上一帧测得，物理 px）

	// 形裁与淡出（D44/§15.1、§15.3）。
	shapes      []drawShape // 本帧可见元素矩形（窗口系、物理 px；layout 坐标即物理）
	physShapes  []shapePhys // 形裁转换缓冲
	lastShapes  []shapePhys // 已应用的形裁（变化才重建）
	frameMetric unit.Metric // 当前帧 Metric（headless 同源渲染用）
	frameSize   image.Point // 当前帧窗口尺寸（物理 px）
	fade        fadeState   // 淡出带 headless 渲染状态
	fadeBuf     []byte      // 淡出带预乘 BGRA 缓冲

	// m 渲染状态机：仅事件循环 goroutine 读写；测试经 drainSync 取 happens-before 后读。
	m *model
}

// New 启动 GUI 前端（Gio 窗口事件循环即刻在后台运行，退出经 Close 收尾）。
func New(opts Options) *UI {
	return newUI(opts, true)
}

// newUI 构造；window=false = headless（§15.5 无窗口逻辑测试：桥接层照常运转，
// 只是没有帧循环与窗口侧控件）。
func newUI(opts Options, window bool) *UI {
	u := &UI{
		opts:  opts,
		inbox: make(chan uiMsg, inboxCap),
		inCh:  make(chan port.UserInput, inputCap),
		eofCh: make(chan struct{}),
		done:  make(chan struct{}),
	}
	u.m = newModel(u)
	u.editor.Submit = true // Enter → SubmitEvent（Shift+Enter 仍换行，§15.2）
	u.editor.SingleLine = true
	if f := opts.Interrupt; f != nil {
		u.SetInterrupt(f)
	}
	if window {
		w := new(app.Window)
		u.w = w // go 前发布：post 侧读取无竞争
		go u.runWindow(w)
	} else {
		go u.runHeadless()
	}
	return u
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
func (u *UI) post(m uiMsg) bool {
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

// signalEOF 标记输入流结束（幂等；close 广播给 Next/Confirm）。
// err 非空 = 底层读取错误（Next 原样上抛，装配根以退出码 1 结束）。
func (u *UI) signalEOF(err error) {
	u.eofOnce.Do(func() {
		u.eofErr = err
		close(u.eofCh)
	})
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
func (u *UI) Confirm(ctx context.Context, prompt string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	reply := make(chan bool, 1) // 缓冲 1：取消后无人接收，事件循环侧非阻塞投递
	u.post(confirmMsg{prompt: prompt, reply: reply})
	for {
		select { // 模型侧应答优先（避免多取一行）
		case yes := <-reply:
			return yes, nil
		default:
		}
		select { // 排队输入优先于 EOF：eofCh 一旦关闭恒就绪，与 inCh 同时就绪时
		// select 随机选中会把 y 误拒——必须先非阻塞取（与 Next 同款结构）。
		case in := <-u.inCh:
			yes := isYes(in.Text)
			u.post(confirmResultMsg{yes: yes})
			return yes, nil
		default:
		}
		select {
		case yes := <-reply:
			return yes, nil
		case in := <-u.inCh:
			yes := isYes(in.Text)
			u.post(confirmResultMsg{yes: yes})
			return yes, nil
		case <-u.eofCh:
			u.post(confirmResultMsg{yes: false})
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
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
	// confirmMsg 确认请求（Confirm 投递）。
	confirmMsg struct {
		prompt string
		reply  chan bool
	}
	// confirmResultMsg Confirm 侧自行得出应答后的确认态收尾（关态 + 记转写）。
	confirmResultMsg struct{ yes bool }
	// drainMsg 排空标记（Close 投递：处理到它即代表此前 post 全部落进状态机）。
	drainMsg struct{ done chan struct{} }
	// eofMsg 输入流结束（关窗/外部泵投递；err 非空 = 读取错误，Next 原样上抛）。
	eofMsg struct{ err error }
	// quitMsg 结束事件循环（Close 在 drain 之后投递）。
	quitMsg struct{}
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
	case confirmMsg:
		u.m.startConfirm(m.prompt, m.reply)
	case confirmResultMsg:
		u.m.replyConfirm(m.yes)
	case eofMsg:
		u.signalEOF(m.err)
	case drainMsg:
		close(m.done)
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

// isYes y/yes（大小写不敏感）为同意——与 repl.Confirm 语义一致。
func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}
