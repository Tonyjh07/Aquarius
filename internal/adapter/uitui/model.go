package uitui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uicommon"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 转写块种类：与 GUI 共用同一基枚举（uicommon.BlockKind）；渲染样式在入块时定稿，
// 宽度相关的 markdown 渲染在事件时做。
type blockKind = uicommon.BlockKind

const (
	blockPlain     = uicommon.BlockPlain
	blockUser      = uicommon.BlockUser
	blockAssistant = uicommon.BlockAssistant
	blockTool      = uicommon.BlockTool
	blockNotice    = uicommon.BlockNotice
	blockError     = uicommon.BlockError
	blockSystem    = uicommon.BlockSystem
	blockThinking  = uicommon.BlockThinking
	blockInject    = uicommon.BlockInject
)

// block 一段定稿转写（text 已含样式）。
type block struct {
	kind blockKind
	text string
}

// pendingConfirm 进行中的确认对话。
type pendingConfirm struct {
	prompt string
	reply  chan port.ConfirmAnswer
	answer []rune
}

// scrollStep PgUp/PgDn 一次滚动的行数。
const scrollStep = 8

// inputRuneCap 单行输入的 rune 上限（防超长粘贴撑爆渲染）。
const inputRuneCap = 64 << 10

// model tea.Model：转写区 + 输入区 + 状态行（状态由事件驱动更新）。
// 转写语义状态（草稿/思维链/待调和段/用量）由内嵌的共用状态机持有（uicommon），
// 本结构体只留 TUI 自己的块列表、输入与滚动。
type model struct {
	u *UI

	uicommon.Transcript               // 转写语义状态机：Draft/Drafting/Think/Usage/Flushed
	sink                uicommon.Sink // 共用事件分流的 TUI 出口

	blocks []block

	input     []rune
	history   []string
	histIdx   int      // -1 = 未浏览历史
	echoed    []string // 已本地回显、还没等到 CommittedEvent 的用户行（D272 核销用）
	confirm   *pendingConfirm
	width     int
	height    int
	scroll    int // 距尾部的行数（0 = 跟随输出）
	glam      *glamour.TermRenderer
	glamWidth int
}

// newModel 初始模型（宽度默认 80，等 WindowSizeMsg 校正）。
func newModel(u *UI) *model {
	m := &model{u: u, histIdx: -1, width: 80, height: 24}
	m.sink = m.newSink()
	if u.opts.HistoryStore != nil {
		m.history = u.opts.HistoryStore.Lines() // 重启后 ↑ 能翻到上次输入的句子
	}
	return m
}

// newSink 共用事件分流的 TUI 出口：块结构与样式留在 TUI，事件语义全在共用状态机。
func (m *model) newSink() uicommon.Sink {
	return uicommon.Sink{
		OnThink:      func(bool) { m.scroll = 0 },
		OnDraft:      func() { m.scroll = 0 },
		FlushThink:   func(text string) { m.add(blockThinking, "[thinking] "+text) },
		FlushSegment: m.addDraftSegment,
		ServerRead: func(text string) {
			// 服务端读链接（D310）：正文不进客户端，用户只能看到「读了哪一页」。
			m.add(blockTool, text)
		},
		ToolCall: func(call tool.Call) {
			m.add(blockTool, "[tool] "+call.Name+" "+preview(call.Args))
		},
		ToolResult: func(res tool.Result) {
			status, detail := "ok", res.Output
			if !res.OK {
				status, detail = "failed", res.Err
			}
			m.add(blockTool, "[tool "+status+"] "+preview([]byte(detail)))
		},
		Committed: m.commit,
		History:   m.replay,
		Error:     func(text string) { m.add(blockError, "error: "+text) },
		Notice: func(text string, attention bool) {
			// D267：改变了本轮上下文预期的提示占警示位；操作回执按普通行呈现，
			// 不冒充警示（信息不丢，样式让位）。
			if attention {
				m.add(blockNotice, "[notice] "+text)
				return
			}
			m.add(blockPlain, text)
		},
		Clear: m.clear,
		Unhandled: func(ev port.Event) {
			if ov, ok := ev.(port.OpenViewEvent); ok {
				m.showPluginView(ov)
				return
			}
			m.add(blockPlain, sanitizeControl(fmt.Sprintf("%v", ev)))
		},
	}
}

func (m *model) Init() tea.Cmd { return nil }

// Update 事件循环主分发。
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refreshGlam()
		return m, nil
	case eventMsg:
		m.handleEvent(msg.ev)
		return m, nil
	case sayMsg:
		m.flushThink() // 时序：思维链先于外部输出
		m.add(blockPlain, sanitizeControl(msg.text))
		return m, nil
	case inputMsg:
		m.submit(msg.text)
		return m, nil
	case confirmMsg:
		m.confirm = &pendingConfirm{prompt: msg.prompt, reply: msg.reply}
		m.add(blockPlain, msg.prompt+" [y/N]")
		return m, nil
	case confirmResultMsg:
		// Confirm 自行应答（排队输入/EOF）后的收尾：关对话框、记转写；
		// 若模型侧已应答则为幂等 no-op。
		m.replyConfirm(msg.ans)
		return m, nil
	case drainMsg:
		close(msg.done)
		return m, nil
	case eofMsg:
		m.u.signalEOF(msg.err) // 输入流结束（或扫描错误）：广播给 Next/Confirm
		return m, nil
	case tea.KeyMsg:
		return m.key(msg), nil
	}
	return m, nil
}

// refreshGlam 按当前宽度重建 glamour 渲染器（resize 时）。
func (m *model) refreshGlam() {
	w := m.width - 4 // 留出边距
	if w < 20 {
		w = 20
	}
	if m.glam != nil && m.glamWidth == w {
		return
	}
	r, err := glamour.NewTermRenderer(glamour.WithWordWrap(w))
	if err != nil {
		m.glam = nil
		return
	}
	m.glam, m.glamWidth = r, w
}

// key 按键处理。
func (m *model) key(msg tea.KeyMsg) *model {
	// 回车族：Enter（\r）、Ctrl+J（\n，管道输入的行终止符）。
	if msg.Type == tea.KeyEnter || msg.Type == tea.KeyCtrlJ {
		text := string(m.input)
		if m.confirm != nil {
			text = string(m.confirm.answer) // 确认模式：提交的是应答缓冲
		} else {
			m.input = m.input[:0]
			m.histIdx = -1
		}
		// D272：生成中键入的一行进待发清单（Alt+Enter = 引导，其余 = 排队）——
		// 走输入通道要等这一轮结束才有人取，那正是待发清单要修的毛病。
		if m.confirm == nil && m.u.generating.Load() && strings.TrimSpace(text) != "" &&
			!strings.HasPrefix(strings.TrimSpace(text), "/") { // 命令不进清单（§5.19 边界）
			d := port.DeliveryQueue
			if msg.Alt && msg.Type == tea.KeyEnter {
				d = port.DeliverySteer
			}
			if ok, note := m.u.addPending(strings.TrimSpace(text), d); ok {
				m.pushHistory(strings.TrimSpace(text))
				return m
			} else if note != "" {
				m.add(blockNotice, note)
				return m
			}
		}
		m.submit(text)
		return m
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		// TUI 原始输入不产生 SIGINT：显式桥接取消当前 Turn + 清空输入/拒答。
		if f := m.u.interrupt.Load(); f != nil {
			(*f)()
		}
		if m.confirm != nil {
			m.replyConfirm(port.ConfirmAnswer{})
		}
		m.input = m.input[:0]
		return m
	case tea.KeyCtrlD:
		if m.confirm == nil && len(m.input) == 0 {
			m.u.signalEOF(nil) // 等价输入流结束（幂等）
		}
		return m
	case tea.KeyCtrlU:
		if m.confirm != nil {
			m.confirm.answer = m.confirm.answer[:0]
			return m
		}
		if len(m.input) == 0 { // 输入为空时 = 撤销最后一条待发（D272，TUI 没有待发条可点）
			if text, ok := m.u.undoPending(); ok {
				m.add(blockNotice, "[notice] "+i18n.Tf("tui.notice.pending_undone", "preview", preview([]byte(text))))
			}
			return m
		}
		m.input = m.input[:0]
		return m
	case tea.KeyBackspace:
		if m.confirm != nil {
			if len(m.confirm.answer) > 0 {
				m.confirm.answer = m.confirm.answer[:len(m.confirm.answer)-1]
			}
			return m
		}
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m
	case tea.KeyUp:
		if m.confirm == nil {
			m.historyPrev()
		}
		return m
	case tea.KeyDown:
		if m.confirm == nil {
			m.historyNext()
		}
		return m
	case tea.KeyPgUp:
		m.scroll += scrollStep
		return m
	case tea.KeyPgDown:
		m.scroll -= scrollStep
		if m.scroll < 0 {
			m.scroll = 0
		}
		return m
	}
	if len(msg.Runes) > 0 {
		if m.confirm != nil {
			// 确认应答缓冲同受上限约束（审查修复：无上限粘贴可撑爆渲染）。
			if len(m.confirm.answer)+len(msg.Runes) <= inputRuneCap {
				m.confirm.answer = append(m.confirm.answer, msg.Runes...)
			}
			return m
		}
		if len(m.input)+len(msg.Runes) <= inputRuneCap {
			m.input = append(m.input, msg.Runes...)
		}
	}
	return m
}

// submit 提交一行：确认对话优先应答，空行忽略，其余先投递再入转写。
// 投递失败（缓冲满）时明确标注"未执行"——审查修复：旧行为先入块再丢弃，
// 转写区会把没执行的输入显示成已提交，制造"已执行"错觉。
func (m *model) submit(text string) {
	if m.confirm != nil {
		m.replyConfirm(port.ConfirmAnswer{Allow: isYes(text)})
		return
	}
	line := strings.TrimSpace(text)
	if line == "" {
		return
	}
	m.pushHistory(line)
	select {
	case m.u.inCh <- parseInput(line):
		m.add(blockUser, line)
		m.echoed = append(m.echoed, line)
	default:
		m.add(blockUser, line)
		m.add(blockNotice, "[notice] "+i18n.T("tui.notice.buffer_full"))
	}
}

// replyConfirm 应答进行中的确认（缓冲 1 + default：取消后迟到的应答不阻塞）。
func (m *model) replyConfirm(ans port.ConfirmAnswer) {
	if m.confirm == nil {
		return
	}
	m.add(blockPlain, fmt.Sprintf("%s → %t", m.confirm.prompt, ans.Allow))
	select {
	case m.confirm.reply <- ans:
	default:
	}
	m.confirm = nil
}

// isYes y/yes（大小写不敏感）为同意——与 repl.Confirm 语义一致。
func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}

// pushHistory 记输入历史（去重相邻、上限取 Options.History），同时追加到写入磁盘存储。
//
// 内存那份负责本次会话的 ↑↓，写入磁盘那份负责下次启动；文件规则与 GUI 相同（inhistory）。
func (m *model) pushHistory(line string) {
	if n := len(m.history); n > 0 && m.history[n-1] == line {
		return
	}
	if m.u.opts.HistoryStore != nil {
		m.u.opts.HistoryStore.Push(line)
	}
	m.history = append(m.history, line)
	if max := m.u.opts.History; len(m.history) > max {
		m.history = m.history[len(m.history)-max:]
	}
}

// historyPrev / historyNext 上下键翻历史。
func (m *model) historyPrev() {
	if len(m.history) == 0 {
		return
	}
	if m.histIdx < 0 {
		m.histIdx = len(m.history) - 1
	} else if m.histIdx > 0 {
		m.histIdx--
	}
	m.input = []rune(m.history[m.histIdx])
}

func (m *model) historyNext() {
	if m.histIdx < 0 {
		return
	}
	m.histIdx++
	if m.histIdx >= len(m.history) {
		m.histIdx = -1
		m.input = nil
		return
	}
	m.input = []rune(m.history[m.histIdx])
}

// handleEvent Turn 事件 → 转写块（与 repl.Emit 同呈现语义）：通用事件走共用状态机
// （uicommon.Transcript.Dispatch），TUI 出口经 sink 钩子接块列表。
func (m *model) handleEvent(ev port.Event) {
	m.Dispatch(ev, &m.sink)
}

// clear 清屏（D75：/switch、/new）：转写块与滚动归零；草稿、思维链、待调和段与
// 用量由共用状态机的 Reset 归零（前端累计值，/usage 走节点汇总不受影响）。
func (m *model) clear() {
	m.blocks = nil
	m.scroll = 0
}

// flushDraftSegment 工具边界把流式草稿落为正文块（D95 Turn 粒度）。
func (m *model) flushDraftSegment() {
	m.addDraftSegment(m.TakeDraftSegment())
}

// addDraftSegment 落一块正文草稿段并记入待调和清单（共用状态机的 FlushSegment 出口）：
// 块先不定稿样式，提交调和时回填权威渲染文本；raw = "" 时不落块。
func (m *model) addDraftSegment(raw string) {
	if raw == "" {
		return
	}
	m.Flushed = append(m.Flushed, len(m.blocks))
	m.blocks = append(m.blocks, block{kind: blockAssistant, text: m.style(blockAssistant, raw)})
	m.scroll = 0
}

// commit 提交节点定稿（D95 Turn 粒度：每 Turn 一次）：system 节点（/compact 摘要）
// 入块；用量累计。assistant 节点做调和而非重渲——flushed 草稿段就地回填权威渲染文本，
// 工具行保留实时位置，收场轮的正文段从 parts 追加。
func (m *model) commit(msg conversation.Message) {
	m.Usage = m.Usage.Add(msg.Usage)
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleAssistant:
		m.ResetDraft()
		handles, backfill, extra := m.ReconcileDrafts(msg.Content, uicommon.SegOpts{Mode: uicommon.ImageNamed})
		if len(handles) == 0 && len(backfill) == 0 && len(extra) == 0 {
			// 空内容的取消/错误也要有反馈（审查修复：首个 token 前 Ctrl+C 是最常见
			// 场景，旧实现什么都不显示；repl 会打 [cancelled]）。
			if msg.Outcome != conversation.OutcomeDone {
				m.add(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome))
			}
			return
		}
		// flushed 草稿段就地回填（FIFO；权威渲染文本以节点为准）。
		for i, seg := range backfill {
			m.blocks[handles[i]].text = m.style(blockAssistant, m.renderMD(seg.Text))
		}
		// 余段（收场轮的正文）追加渲染；非 done 终态标记并入最后一个正文块。
		for i, seg := range extra {
			if msg.Outcome != conversation.OutcomeDone && i == len(extra)-1 {
				m.add(blockAssistant, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(
					fmt.Sprintf("[%s]", msg.Outcome))+"\n"+seg.Text)
				continue
			}
			m.add(blockAssistant, m.renderMD(seg.Text))
		}
		m.scroll = 0
	case conversation.RoleSystem:
		m.ResetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	case conversation.RoleInject:
		// 注入节点（D166）：独立成档（头部 = 来源 + 触发状态），不落 user 提示符。
		m.ResetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockInject, injectHeader(msg.Source, msg.Trigger)+"\n"+text)
		}
	default:
		// user 节点无事件面（输入已单独入块）；工具结果随 assistant 节点分片呈现（D95）。
		// D272：轮内写成的 user 节点（引导/排队）没有本地回显——按「最早一条已回显且
		// 文本一致」核销，核销不到就补一块，否则那句话在转写区里消失。
		if msg.Role == conversation.RoleUser && strings.TrimSpace(text) != "" {
			if len(m.echoed) > 0 && m.echoed[0] == strings.TrimSpace(text) {
				m.echoed = m.echoed[1:]
			} else {
				m.add(blockUser, text)
			}
		}
		m.ResetDraft()
	}
	m.scroll = 0
}

// replay 历史节点回放（D40/§7.4）：已提交节点按角色一次性定稿渲染，与实时
// 呈现同一套样式；无草稿/流式过程，也不累计用量（回放是展示，不是新一轮提交）。
// D95 Turn 粒度：逐分片序渲染——思考分片各自成块、正文按 tool 边界分段、工具行夹段间。
func (m *model) replay(msg conversation.Message) {
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleUser:
		if strings.TrimSpace(text) != "" {
			m.add(blockUser, text)
		}
	case conversation.RoleAssistant:
		start := len(m.blocks)
		var seg strings.Builder
		flushSeg := func() {
			if s := sanitizeControl(seg.String()); strings.TrimSpace(s) != "" {
				m.add(blockAssistant, m.renderMD(s))
			}
			seg.Reset()
		}
		for _, p := range msg.Content {
			switch p.Kind {
			case conversation.PartThinking:
				flushSeg()
				if strings.TrimSpace(p.Text) != "" {
					m.add(blockThinking, "[thinking] "+p.Text) // D42：展示规则恒含思考
				}
			case conversation.PartTool:
				flushSeg()
				tp := p.Tool
				if tp == nil {
					continue
				}
				if pr := preview(tp.Args); pr != "" {
					m.add(blockTool, "[tool] "+tp.Name+" "+pr)
				} else {
					m.add(blockTool, "[tool] "+tp.Name)
				}
				if res := tp.Result; res != nil {
					status, detail := "ok", res.Output
					if !res.OK {
						status, detail = "failed", res.Err
					}
					m.add(blockTool, "[tool "+status+"] "+preview([]byte(detail)))
				}
			default:
				appendPartText(&seg, p)
			}
		}
		flushSeg()
		// 非 done 终态：标记并入最后一个正文块（与实时 commit 同规则；repl 亦打 [cancelled]）；
		// 无正文段（空内容的取消/错误）单独给标记块。
		if msg.Outcome != conversation.OutcomeDone {
			marked := false
			for i := len(m.blocks) - 1; i >= start; i-- {
				if m.blocks[i].kind == blockAssistant {
					m.blocks[i].text = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(
						fmt.Sprintf("[%s]", msg.Outcome)) + "\n" + m.blocks[i].text
					marked = true
					break
				}
			}
			if !marked {
				m.add(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome))
			}
		}
	case conversation.RoleSystem:
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	case conversation.RoleInject:
		if strings.TrimSpace(text) != "" {
			m.add(blockInject, injectHeader(msg.Source, msg.Trigger)+"\n"+text)
		}
	default: // root 等不该出现在回放区间
	}
	m.scroll = 0
}

// injectHeader 注入块头部（D166）：来源经友好化表翻成用户面措辞，末尾记实际触发与否。
func injectHeader(source string, trigger bool) string {
	state := i18n.T("tui.state.idle")
	if trigger {
		state = i18n.T("tui.state.fired")
	}
	return i18n.Tf("tui.status.inject", "source", port.InjectSourceLabel(source), "state", state)
}

// partsText 节点文本：文本分片直连，其余留占位（§4.2 多模态呈现 MVP）；
// 出口统一剥控制序列（提交内容是不可信模型输出，§9/审查修复）。
// 思考分片不并入正文——回放路径单独渲染为暗块（D42）；
// 工具分片不并入正文——回放路径单独渲染为工具行（D95）。
// 实现与终端/GUI 共用（uicommon）；图片留占位并带上文件名。
func partsText(parts []conversation.Part) string {
	return uicommon.PartsText(parts, uicommon.ImageNamed)
}

// appendPartText 把一个分片的呈现文本写入 b（partsText/切段共用规则）。
func appendPartText(b *strings.Builder, p conversation.Part) {
	uicommon.AppendPartText(b, p, uicommon.ImageNamed)
}

// add 追加定稿块（跟随尾部）。
func (m *model) add(kind blockKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.blocks = append(m.blocks, block{kind: kind, text: m.style(kind, text)})
	m.scroll = 0
}

// flushThink 把进行中的思维链落为暗块（D34 展示规则）：不进正文草稿；
// 节点侧已由 PartThinking 入树并按 config 回传（D42），此处只管呈现。
func (m *model) flushThink() {
	if text := m.TakeThink(); text != "" {
		m.add(blockThinking, "[thinking] "+text)
	}
}

// style 按块种类着色（glamour 输出原样；用户行加提示符）。
func (m *model) style(kind blockKind, text string) string {
	switch kind {
	case blockUser:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render("> " + text)
	case blockTool:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(text)
	case blockNotice:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true).Render(text)
	case blockError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(text)
	case blockSystem:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Render(text)
	case blockThinking:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true).Render(text)
	case blockInject:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("109")).Render(text)
	default:
		return text
	}
}

// renderMD committed 助手文本经 glamour 轻 markdown 渲染（D33；失败回退原文）。
func (m *model) renderMD(text string) string {
	if m.glam == nil {
		return text
	}
	out, err := m.glam.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, "\n")
}

// View 组装画面：转写窗口（尾随/PgUp 滚动）+ 状态行 + 确认对话或输入行。
func (m *model) View() string {
	// 转写行（含流式草稿）。
	var lines []string
	for _, b := range m.blocks {
		lines = append(lines, strings.Split(b.text, "\n")...)
	}
	if m.Think.Len() > 0 {
		// 进行中的思维链实时可见（落块后由 blocks 承载）。
		lines = append(lines, strings.Split(m.style(blockThinking, "[thinking] "+m.Think.String()), "\n")...)
	}
	if m.Drafting || m.Draft.Len() > 0 {
		lines = append(lines, strings.Split(strings.TrimRight(m.Draft.String(), "\n"), "\n")...)
	}

	bottom := 2 // 状态行 + 输入/确认行
	if m.height <= 0 {
		m.height = 24
	}
	avail := m.height - bottom - 1
	if avail < 3 {
		avail = 3
	}
	maxScroll := len(lines) - avail
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	end := len(lines) - m.scroll
	start := end - avail
	if start < 0 {
		start = 0
	}

	var b strings.Builder
	for _, ln := range lines[start:end] {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	b.WriteString(m.statusLine())
	b.WriteByte('\n')
	if m.confirm != nil {
		b.WriteString(m.confirm.prompt + " " + string(m.confirm.answer) + " [y/N]")
	} else {
		b.WriteString("> " + string(m.input) + "▌")
	}
	return b.String()
}

// statusLine 状态行：模型/权限（Status 回调现取）+ 用量累计 + 按键提示。
func (m *model) statusLine() string {
	parts := []string{}
	if m.u.opts.Status != nil {
		st := m.u.opts.Status()
		if st.Model != "" {
			parts = append(parts, i18n.Tf("tui.status.model", "model", st.Model))
		}
		if st.Level != "" {
			parts = append(parts, i18n.Tf("tui.status.permission", "level", st.Level))
		}
		if st.Unattended {
			parts = append(parts, i18n.T("tui.status.unattended"))
		}
		if st.Effort != "" {
			parts = append(parts, i18n.Tf("tui.status.effort", "effort", st.Effort))
		}
	}
	if n := m.u.pendingCount(); n > 0 {
		parts = append(parts, i18n.Tf("tui.status.pending", "n", strconv.Itoa(n)))
	}
	if u := m.Usage; u.InputTokens > 0 || u.OutputTokens > 0 {
		line := fmt.Sprintf("↑%d ↓%d", u.InputTokens, u.OutputTokens)
		// D308：命中缓存的占比只在端点报过命中时补——没报时这半个括号只是噪音。
		if u.CacheRead > 0 {
			line += i18n.Tf("tui.status.cache", "pct", strconv.Itoa(int(u.CacheHitRatio()*100+0.5)))
		}
		parts = append(parts, line)
	}
	parts = append(parts, i18n.T("tui.hint.scroll"), i18n.T("tui.hint.cancel"))
	return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Join(parts, " · "))
}

// showPluginView 把插件面板就地渲染成文本（D346）。
//
// 行式界面没有次窗，也没有逐字段问答的输入循环——面板在这里是**只读的**：形状照渲染，
// 操作仍走命令（`/mem` 之类）。这条口径与 §6 的降级表差一处（form / buttons 不接交互），
// 已在 plugin-guide.md §6 写明，不让它悄悄成为「TUI 支持面板」的印象。
//
// Render 是同步调用：插件实现要求便宜（读自己的内存），这里也就没有必要为它铺一条异步
// 通道——真慢了会卡住输入循环，但那是插件违反契约，与界面层无关。
func (m *model) showPluginView(ev port.OpenViewEvent) {
	if m.u.opts.Views == nil {
		return
	}
	n, err := m.u.opts.Views.Render(context.Background(), ev.Plugin, ev.ViewID)
	if err != nil {
		m.add(blockError, sanitizeControl(err.Error()))
		return
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	text := uicommon.PlainNode(n, width-2)
	if text == "" {
		text = i18n.Tf("tui.view.empty", "plugin", ev.Plugin)
	}
	m.add(blockPlain, text)
	m.add(blockNotice, i18n.T("tui.view.readonly"))
}
