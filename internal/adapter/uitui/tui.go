// Package uitui bubbletea TUI 前端（DESIGN D33，M4 MVP）：与 repl 同权实现
// port.Presenter + Prompter + Confirmer，装配根按 ui.kind 换壳——薄壳零业务逻辑，
// 未来 GUI 框架复用同一套 port 契约（§14）。
//
// MVP 范围（D33）：可滚动转写区（PgUp/PgDn、输出跟随尾部）+ 流式输出 + 输入框 +
// 斜杠命令 + 上下键历史 + Confirm 对话 + 状态行（模型/权限经 Status 回调现取、
// 用量从提交节点累计）；轻 markdown = committed 助手文本经 glamour 渲染，
// 流式阶段原样；图片/音频维持占位文本（与 repl 同形态）。
//
// 并发模型：事件循环独占 model 状态；Emit/Say/Confirm 从 REPL goroutine 经
// Program.Send 投递（线程安全）；Next/Confirm 对调用方呈阻塞语义（channel 桥接）。
package uitui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// inputLineLimit 单行输入上限（防超长粘贴撑爆扫描器；与 repl 同口径）。
const inputLineLimit = 1 << 20

var (
	_ port.Presenter = (*UI)(nil)
	_ port.Prompter  = (*UI)(nil)
	_ port.Confirmer = (*UI)(nil)
)

// inputCap 输入缓冲容量：打字/管道行快于 REPL 消费时暂存（审查修复：16 在
// 长 Turn 的脚本输入下偏小，放大到 256；仍溢出时提示"未执行"而非静默丢弃）。
const inputCap = 256

// Status 状态行数据（View 时经回调现取，反映 /model、/permission、/effort 热切换）。
type Status struct {
	Model  string
	Level  string
	Effort string // D34：推理档位（think off 时为空——effort 不发送）
}

// Options 装配选项（装配根注入）。
type Options struct {
	In  io.Reader
	Out io.Writer
	// Status 状态行数据源；nil = 状态行只显示用量与按键提示。
	Status func() Status
	// Interrupt Ctrl+C 行为：装配根注入"取消当前 Turn"。TUI 原始输入不产生
	// SIGINT（os.Interrupt 路径在此模式下不会触发），必须显式桥接；nil = 忽略。
	Interrupt func()
	// History 输入历史上限（<=0 = 50）。
	History int
}

// UI TUI 前端句柄（装配根按 uiFrontend 使用）。
type UI struct {
	prog      *tea.Program
	inCh      chan port.UserInput
	eofCh     chan struct{} // 关闭 = 输入流结束（广播：Next/Confirm 同时唤醒，天然粘滞）
	eofOnce   sync.Once
	eofErr    error // 写于 close 之前；close 提供 happens-before，读者安全
	done      chan struct{}
	interrupt atomic.Pointer[func()]
	opts      Options
}

// New 启动 TUI（事件循环即刻在后台运行，退出经 Close 收尾）。
//
// 输入路径按是否 TTY 分叉：真 TTY 交 bubbletea 原生读取（Windows 走 console API、
// UTF-16 直转 rune，中文安全）；**非 TTY（管道/测试）禁用 tea 输入**——tea 在非控制台
// reader 上会包一层 mattn/go-localereader（key_windows.go），把 UTF-8 字节当系统
// 代码页（GBK）解码造成乱码。此处自建按行 UTF-8 泵直投消息等价绕开。
func New(opts Options) *UI {
	if opts.History <= 0 {
		opts.History = 50
	}
	u := &UI{
		inCh:  make(chan port.UserInput, inputCap),
		eofCh: make(chan struct{}),
		done:  make(chan struct{}),
		opts:  opts,
	}
	if opts.Interrupt != nil {
		u.SetInterrupt(opts.Interrupt)
	}
	progOpts := []tea.ProgramOption{tea.WithOutput(opts.Out)}
	if isTerminal(opts.In) {
		progOpts = append(progOpts, tea.WithInput(opts.In))
	} else {
		progOpts = append(progOpts, tea.WithInput(nil)) // 禁用 tea 输入，走自建泵
	}
	u.prog = tea.NewProgram(newModel(u), progOpts...)
	go func() {
		defer close(u.done)
		if err := func() error { _, e := u.prog.Run(); return e }(); err != nil {
			fmt.Fprintf(os.Stderr, "tui: 事件循环退出: %v\n", err)
		}
	}()
	if !isTerminal(opts.In) {
		go u.pumpLines(opts.In)
	}
	return u
}

// isTerminal 判定是否交互终端（*os.File 且 TTY）。
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// pumpLines 非 TTY 输入泵：按行读 UTF-8 直投（等价"输入行 + 回车"语义）。
// 绕开 tea 的 localereader 代码页误解码；EOF 经事件循环排队（与输入行同一队列，
// 先于它的提交必已入队）；扫描错误（如超长行）随 eofMsg 上抛，不再静默当 EOF。
func (u *UI) pumpLines(in io.Reader) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), inputLineLimit)
	for sc.Scan() {
		u.prog.Send(inputMsg{text: strings.TrimSuffix(sc.Text(), "\r")})
	}
	var err error
	if sc.Err() != nil {
		err = fmt.Errorf("tui: 读取输入: %w", sc.Err())
	}
	u.prog.Send(eofMsg{err: err})
}

// Close 排空队列后退出事件循环并等待收尾（幂等；repl 侧为 no-op，装配根按 io.Closer 调用）。
// 先投递 drain 标记并等其处理：Program.Send 是异步 FIFO，直接 Quit 可能在 select 里
// 抢先于排队中的渲染消息，把最后一轮的回答丢掉（偶发复现于 e2e）。
func (u *UI) Close() error {
	done := make(chan struct{})
	u.prog.Send(drainMsg{done: done})
	select {
	case <-done:
	case <-u.done: // 事件循环已退出（外部已 Quit）
		return nil
	}
	u.prog.Quit()
	<-u.done
	return nil
}

// Suspend 交出终端给外部全屏程序（/memory 系统编辑器，D24；审查修复：TUI 事件循环
// 仍在读同一 stdin、渲染器占用 stdout，不释放会与编辑器互相踩踏）。
// 非 TTY 模式下 tea 未持有终端，Release 为安全空操作。
func (u *UI) Suspend() error { return u.prog.ReleaseTerminal() }

// Resume 重新接管终端（Suspend 的逆操作；随后事件循环全量重绘）。
func (u *UI) Resume() error { return u.prog.RestoreTerminal() }

// SetInterrupt 注入 Ctrl+C 行为（装配根在每轮 Turn 前设为取消该轮；原子换，事件循环读）。
func (u *UI) SetInterrupt(fn func()) {
	if fn == nil {
		fn = func() {}
	}
	u.interrupt.Store(&fn)
}

// signalEOF 标记输入流结束（幂等；close 广播给 Next/Confirm——审查修复：旧的
// 一次性 channel 标记会被"EOF 与排队输入同时就绪"的随机 select 吞掉，后续 Next
// 永久阻塞）。err 非空 = 扫描错误（Next 原样上抛，装配根以退出码 1 结束）。
func (u *UI) signalEOF(err error) {
	u.eofOnce.Do(func() {
		u.eofErr = err
		close(u.eofCh)
	})
}

// Next 阻塞读取下一条输入（斜杠命令解析与 repl 同口径）；
// io.EOF = Ctrl+D（空输入）/ 输入流结束；扫描错误原样上抛；ctx 取消原样上抛。
// 广播到达时先取尽排队输入（eofMsg 经事件循环排队，先于它的提交必已入队），
// 取空才返回 EOF——任何 select 次序下都不丢输入、也不吞 EOF（审查修复）。
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

// Prompt 输入提示：TUI 输入行常驻，等价 no-op（保持前端接口同形）。
func (u *UI) Prompt() {}

// Say 输出一行会话文本（命令输出、启动提示）→ 转写区纯文本块。
func (u *UI) Say(text string) {
	if text == "" {
		return
	}
	u.prog.Send(sayMsg{text: text})
}

// Confirm 逐次确认：转写区记提示行，输入行切换为 [y/N] 对话；
// 应答三路，优先级：模型侧已应答（reply）→ **排队输入**（审查修复：管道输入在
// 对话框打开前就已全部入队，只等 reply/EOF 会先撞上已关闭的 EOF 而把 y 误拒）→
// 输入流结束（拒，对齐 repl"不替用户做破坏性决定"）。取排队输入或走 EOF 时同步发
// confirmResultMsg 关闭模型侧对话框，避免残留对话框吞掉后续输入。
func (u *UI) Confirm(ctx context.Context, prompt string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	reply := make(chan bool, 1) // 缓冲 1：取消后无人接收，事件循环侧非阻塞投递
	u.prog.Send(confirmMsg{prompt: prompt, reply: reply})
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
			u.prog.Send(confirmResultMsg{yes: yes}) // 关闭模型侧对话框并记转写
			return yes, nil
		default:
		}
		select {
		case yes := <-reply:
			return yes, nil
		case in := <-u.inCh:
			yes := isYes(in.Text)
			u.prog.Send(confirmResultMsg{yes: yes})
			return yes, nil
		case <-u.eofCh:
			u.prog.Send(confirmResultMsg{yes: false})
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// Emit 呈现 Turn 事件（投递给事件循环；线程安全）。
func (u *UI) Emit(_ context.Context, ev port.Event) error {
	u.prog.Send(eventMsg{ev: ev})
	return nil
}

// 事件循环与外部世界的桥接消息。
type (
	// eventMsg Turn 事件（Emit 投递）。
	eventMsg struct{ ev port.Event }
	// sayMsg 纯文本行（Say 投递）。
	sayMsg struct{ text string }
	// inputMsg 用户提交的一行（事件循环 → Next）。
	inputMsg struct{ text string }
	// confirmMsg 确认请求（Confirm 投递）。
	confirmMsg struct {
		prompt string
		reply  chan bool
	}
	// confirmResultMsg Confirm 侧自行得出应答后的模型状态收尾（关闭对话框 + 记转写）。
	confirmResultMsg struct{ yes bool }
	// drainMsg 排空标记（Close 投递：处理到它即代表此前 Send 全部落帧）。
	drainMsg struct{ done chan struct{} }
	// eofMsg 输入流结束（pump 投递：与输入行同一队列，杜绝 EOF 抢跑于排队输入）；
	// err 非空 = 扫描错误（Next 原样上抛）。
	eofMsg struct{ err error }
)

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

// preview 截断工具参数/结果为单行预览（不可信数据只渲染，DESIGN §9）。
// 先剥控制序列（ANSI/OSC/C0/C1——与 notify.sanitizeControl 同算法、终端注入面一致，
// 审查修复：工具参数与服务端错误片段可携带注入序列）。
func preview(b []byte) string {
	const toolPreviewLen = 120
	s := strings.TrimSpace(sanitizeControl(string(b)))
	r := []rune(s)
	if len(r) > toolPreviewLen {
		return string(r[:toolPreviewLen]) + "…"
	}
	return s
}

// sanitizeControl 剥除转义序列与控制字符（保留 \n\t；DESIGN §9 不可信数据只渲染）。
// 算法与 internal/adapter/notify 的同名函数一致——两处分别守护通知面与终端转写面。
func sanitizeControl(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == 0x1b && i+1 < len(rs) {
			switch rs[i+1] {
			case '[': // CSI：吞至终结字节（0x40–0x7E）
				j := i + 2
				for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
					j++
				}
				if j < len(rs) {
					i = j
				} else {
					i = len(rs) - 1
				}
				continue
			case ']': // OSC：吞至 BEL 或 ESC\
				j := i + 2
				for j < len(rs) && rs[j] != 0x07 && !(rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\') {
					j++
				}
				if j < len(rs) {
					if rs[j] == 0x1b {
						i = j + 1 // ST 的 ESC\ 两字符都要跳过
					} else {
						i = j
					}
				} else {
					i = len(rs) - 1
				}
				continue
			default: // 其余两字符转义
				i++
				continue
			}
		}
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
