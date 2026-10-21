package uigui

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"gioui.org/layout"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 提问面板（D184，§15.2）：模型调 ask_user 时输入栏**原地展开**成面板——不遮转写区、
// 不换确认态。1–4 题用顶部进度段表示（可点跳题），每题 0–9 个选项（单选 ○ / 多选 ☑），
// 末行「自己输入」点开就地变输入框。
//
// 取消与放弃是两件事（D184 口径三）：
//   - Esc / 停止键 = 中断本轮 → Ask 返回 ctx.Err，Runner 按装配级故障中止 Turn；
//   - 「忽略」= 放弃全部 → AskAnswer{Dismissed:true}，模型拿到「按你自己判断继续」。
const (
	askMaxQuestions = 4 // 题量上限（与 ask_user 声明同口径）
	askMaxOptions   = 9 // 每题选项上限

	askPadDp      = 16  // 面板内边距（与胶囊 padding 同源）
	askHeadDp     = 22  // 顶部进度行高
	askFootDp     = 32  // 底部按钮行高
	askGapDp      = 8   // 段间距
	askOptRowDp   = 28  // 选项行高（无说明）
	askOptDescDp  = 17  // 选项说明行追加高
	askFreeRowDp  = 30  // 「自己输入」行高
	askPanelFrac  = 0.7 // 面板高上限 = 窗高 × 该比例（D184）
	askPanelMinDp = 140 // 面板最小高（再小内容没法看）
)

// pendingAsk 进行中的提问（仅事件循环 goroutine 读写）。
type pendingAsk struct {
	req    port.AskRequest
	reply  chan port.AskAnswer
	cur    int      // 当前题下标
	sel    [][]int  // 每题已选选项下标（升序）
	free   []string // 每题自由输入（非空 = 与已选项一起回填）
	open   []bool   // 自由输入行是否已展开（点开就地变输入框）
	mini   bool     // 最小化（收起成一行摘要，进度不丢）
	cursor int      // 键盘游标：0..len(options)-1 = 选项，len(options) = 「自己输入」，-1 = 无
}

// Ask 提问（port.Asker）：投递面板请求并等应答。
//
// 三路收场：面板提交/忽略（reply）→ 正常返回；ctx 取消（Esc/停止键/中断）→ 上抛
// ctx.Err 并让面板收尾；输入流结束（关窗）→ 按「放弃全部」返回，与 repl/uitui 同口径
// （不替用户编答案）。
func (u *UI) Ask(ctx context.Context, req port.AskRequest) (port.AskAnswer, error) {
	if err := ctx.Err(); err != nil {
		return port.AskAnswer{}, err
	}
	if len(req.Questions) == 0 {
		return port.AskAnswer{}, nil
	}
	if len(req.Questions) > askMaxQuestions {
		req.Questions = req.Questions[:askMaxQuestions]
	}
	reply := make(chan port.AskAnswer, 1) // 缓冲 1：取消后无人接收，事件循环侧非阻塞投递
	if !u.post(askMsg{req: req, reply: reply}) {
		return port.AskAnswer{}, errors.New("uigui: the interface is closed")
	}
	select {
	case ans := <-reply:
		return ans, nil
	case <-ctx.Done():
		u.post(askDoneMsg{}) // 面板收尾：中断后不该留一张没人等的问答面板
		return port.AskAnswer{}, ctx.Err()
	case <-u.eofCh:
		u.post(askDoneMsg{})
		return port.AskAnswer{Dismissed: true}, nil
	}
}

// startAsk 打开提问面板（事件循环 goroutine）。
func (m *model) startAsk(req port.AskRequest, reply chan port.AskAnswer) {
	m.ask = &pendingAsk{
		req:    req,
		reply:  reply,
		sel:    make([][]int, len(req.Questions)),
		free:   make([]string, len(req.Questions)),
		open:   make([]bool, len(req.Questions)),
		cursor: -1,
	}
	m.u.askEd.SetText("")
	m.u.askList.Position = layout.Position{}
	if m.u.collapsed || m.u.expandAn.active {
		m.u.beginExpand() // 收起成球时来提问：先把窗口展开（否则面板无处显示、问题永远没人答）
	}
	m.u.askOpen.Store(true)
	m.u.setExpanded(true)    // 面板要占多行高度（与展开态同一套几何）
	m.u.focusPending = false // 焦点归面板：别让主编辑器在下一帧抢回
	m.u.askFocus = true      // 面板持键盘（下一帧 FocusCmd）
}

// endAsk 收尾提问面板（提交/忽略/中断共用）：清状态、焦点还主编辑器。
func (m *model) endAsk() {
	if m.ask == nil {
		return
	}
	m.ask = nil
	m.u.askOpen.Store(false)
	m.u.askFocus = false
	m.u.askFreeFocus = false
	m.u.focusPending = true // 焦点回主编辑器（D63 一次性口径）
}

// askSaveFree 把编辑器里的自由输入存回当前题（切题/提交前调用）。
func (m *model) askSaveFree() {
	if m.ask == nil {
		return
	}
	t := sanitizeControl(strings.TrimSpace(m.u.askEd.Text()))
	if t != "" {
		m.ask.open[m.ask.cur] = true
	}
	m.ask.free[m.ask.cur] = t
}

// askGoto 跳到第 i 题（进度段/上一步/下一步）：先存自由输入再换题。
func (m *model) askGoto(i int) {
	a := m.ask
	if a == nil || i < 0 || i >= len(a.req.Questions) {
		return
	}
	m.askSaveFree()
	a.cur = i
	a.cursor = -1
	m.u.askEd.SetText(a.free[i])
	m.u.askList.Position = layout.Position{} // 新题从顶部看起
	// 焦点随「自己输入」行的开合走：开着就给编辑器，否则回面板（否则字符键还往旧输入框跑）。
	m.u.askFocus = true
	m.u.askFreeFocus = a.open[i]
}

// askToggle 切换当前题第 i 个选项（单选 = 只留一个；多选 = 取反）。
func (m *model) askToggle(i int) {
	a := m.ask
	if a == nil || a.cur < 0 || a.cur >= len(a.req.Questions) {
		return
	}
	q := a.req.Questions[a.cur]
	if i < 0 || i >= len(q.Options) {
		return
	}
	if !q.Multiple {
		a.sel[a.cur] = []int{i}
		return
	}
	sel := a.sel[a.cur]
	for k, v := range sel {
		if v == i {
			a.sel[a.cur] = append(sel[:k:k], sel[k+1:]...)
			return
		}
	}
	a.sel[a.cur] = append(sel, i)
}

// askSelected 第 i 个选项是否已选。
func (a *pendingAsk) askSelected(i int) bool {
	if a == nil || a.cur < 0 || a.cur >= len(a.sel) {
		return false
	}
	for _, v := range a.sel[a.cur] {
		if v == i {
			return true
		}
	}
	return false
}

// askNext 下一步：最后一题 = 提交，否则进下一题。
func (m *model) askNext() {
	a := m.ask
	if a == nil {
		return
	}
	if a.cur < len(a.req.Questions)-1 {
		m.askGoto(a.cur + 1)
		return
	}
	m.askSubmit()
}

// askSubmit 组答案并交回调用方（面板关闭）。
func (m *model) askSubmit() {
	a := m.ask
	if a == nil {
		return
	}
	m.askSaveFree()
	answers := make([][]string, len(a.req.Questions))
	for qi, q := range a.req.Questions {
		out := make([]string, 0, len(a.sel[qi])+1)
		for _, oi := range a.sel[qi] {
			if oi >= 0 && oi < len(q.Options) {
				out = append(out, q.Options[oi].Label)
			}
		}
		if f := a.free[qi]; f != "" {
			out = append(out, f)
		}
		answers[qi] = out
	}
	reply := a.reply
	m.endAsk()
	m.u.setExpanded(false) // 提交即收起（与发送后自动收起同口径）
	if reply != nil {
		select {
		case reply <- port.AskAnswer{Answers: answers}:
		default:
		}
	}
}

// askDismiss 「忽略」= 放弃全部（不是跳过本题）。
func (m *model) askDismiss() {
	a := m.ask
	if a == nil {
		return
	}
	reply := a.reply
	m.endAsk()
	m.u.setExpanded(false)
	if reply != nil {
		select {
		case reply <- port.AskAnswer{Dismissed: true}:
		default:
		}
	}
}

// askOpenFree 展开当前题的「自己输入」行并让入焦点。
func (m *model) askOpenFree() {
	a := m.ask
	if a == nil {
		return
	}
	a.open[a.cur] = true
	a.cursor = len(a.req.Questions[a.cur].Options)
	m.u.askFocus = true // 先把焦点稳定在面板，editor 的 FocusCmd 随后夺走（同帧顺序：面板 → 编辑器）
	m.u.askFreeFocus = true
}

// submitAsk 把一行文本按面板语义解释（外部输入泵/管道输入与 GUI 共用一条口径）：
// 纯编号 = 选选项（多选可逗号/空格分隔）、其他文本 = 自由答案、空行 = 不动、
// `/dismiss` = 放弃全部。
func (m *model) submitAsk(text string) {
	a := m.ask
	if a == nil {
		return
	}
	line := strings.TrimSpace(text)
	switch {
	case line == "":
		return
	case strings.EqualFold(line, "/dismiss"), strings.EqualFold(line, "dismiss"):
		m.askDismiss()
		return
	}
	q := a.req.Questions[a.cur]
	if picks, ok := parseAskPicks(line, len(q.Options)); ok {
		for _, i := range picks {
			if !q.Multiple {
				a.sel[a.cur] = []int{i}
				break
			}
			m.askToggle(i)
		}
		return
	}
	a.free[a.cur] = sanitizeControl(line)
	a.open[a.cur] = true
	m.u.askEd.SetText(a.free[a.cur])
}

// parseAskPicks 纯编号行 → 选项下标（0 起，去重保序）；含非编号内容则 ok=false
// （那是用户自己写的答案，不是选择）。
func parseAskPicks(line string, nOptions int) ([]int, bool) {
	if nOptions == 0 {
		return nil, false
	}
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return nil, false
	}
	out := make([]int, 0, len(fields))
	seen := map[int]bool{}
	for _, f := range fields {
		v, err := strconv.Atoi(strings.TrimSuffix(f, ")"))
		if err != nil || v < 1 || v > nOptions {
			return nil, false
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v-1)
	}
	return out, true
}
