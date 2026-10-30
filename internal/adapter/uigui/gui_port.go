package uigui

// UI 对 port 六面的实现：Present 侧（Emit/Say/Prompt）、中断、关闭、Confirm 与 Next 的阻塞桥接。

import (
	"context"
	"io"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

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
