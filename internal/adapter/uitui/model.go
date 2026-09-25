package uitui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 转写块种类（渲染样式在入块时定稿；宽度相关的 markdown 渲染在事件时做）。
type blockKind int

const (
	blockPlain     blockKind = iota // Say/命令输出/通用行
	blockUser                       // 提交的输入
	blockAssistant                  // committed 助手文本（glamour 渲染）
	blockTool                       // 工具调用/结果行
	blockNotice                     // notice 提示
	blockError                      // 错误行
	blockSystem                     // system 节点（/compact 摘要等）
)

// block 一段定稿转写（text 已含样式）。
type block struct {
	kind blockKind
	text string
}

// pendingConfirm 进行中的确认对话。
type pendingConfirm struct {
	prompt string
	reply  chan bool
	answer []rune
}

// scrollStep PgUp/PgDn 一次滚动的行数。
const scrollStep = 8

// inputRuneCap 单行输入的 rune 上限（防超长粘贴撑爆渲染）。
const inputRuneCap = 64 << 10

// model tea.Model：转写区 + 输入区 + 状态行（状态由事件驱动更新）。
type model struct {
	u *UI

	blocks   []block
	draft    strings.Builder // 流式草稿（committed 后以消息文本定稿替换）
	drafting bool

	input     []rune
	history   []string
	histIdx   int // -1 = 未浏览历史
	confirm   *pendingConfirm
	usage     conversation.Usage
	width     int
	height    int
	scroll    int // 距尾部的行数（0 = 跟随输出）
	glam      *glamour.TermRenderer
	glamWidth int
}

// newModel 初始模型（宽度默认 80，等 WindowSizeMsg 校正）。
func newModel(u *UI) *model {
	return &model{u: u, histIdx: -1, width: 80, height: 24}
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
		m.add(blockPlain, msg.text)
		return m, nil
	case inputMsg:
		m.submit(msg.text)
		return m, nil
	case confirmMsg:
		m.confirm = &pendingConfirm{prompt: msg.prompt, reply: msg.reply}
		m.add(blockPlain, msg.prompt+" [y/N]")
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
			m.replyConfirm(false)
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
		m.replyConfirm(isYes(text))
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
	default:
		m.add(blockUser, line)
		m.add(blockNotice, "[notice] 输入缓冲已满，此行未执行")
	}
}

// replyConfirm 应答进行中的确认（缓冲 1 + default：取消后迟到的应答不阻塞）。
func (m *model) replyConfirm(yes bool) {
	if m.confirm == nil {
		return
	}
	m.add(blockPlain, fmt.Sprintf("%s → %t", m.confirm.prompt, yes))
	select {
	case m.confirm.reply <- yes:
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

// pushHistory 记输入历史（去重相邻、上限取 Options.History）。
func (m *model) pushHistory(line string) {
	if n := len(m.history); n > 0 && m.history[n-1] == line {
		return
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

// handleEvent Turn 事件 → 转写块（与 repl.Emit 同呈现语义）。
func (m *model) handleEvent(ev port.Event) {
	switch e := ev.(type) {
	case port.DeltaEvent:
		if e.Delta.Text == "" {
			return // 工具调用分片、用量分片不进转写
		}
		m.drafting = true
		m.draft.WriteString(e.Delta.Text)
		m.scroll = 0
	case port.ToolCallEvent:
		m.add(blockTool, "[tool] "+e.Call.Name+" "+preview(e.Call.Args))
	case port.ToolResultEvent:
		status, detail := "ok", e.Result.Output
		if !e.Result.OK {
			status, detail = "failed", e.Result.Err
		}
		m.add(blockTool, "[tool "+status+"] "+preview([]byte(detail)))
	case port.CommittedEvent:
		m.commit(e.Message)
	case port.ErrorEvent:
		m.add(blockError, "error: "+e.Err.Error())
	case port.NoticeEvent:
		m.add(blockNotice, "[notice] "+e.Text)
	default:
		m.add(blockPlain, fmt.Sprintf("%v", ev))
	}
}

// commit 提交节点定稿：助手（done 经 glamour）以消息文本为准替换草稿；
// system 节点（/compact 摘要）入块；用量在节点上累计（不与 Delta 重复计）。
func (m *model) commit(msg conversation.Message) {
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleAssistant:
		final := text
		if final == "" {
			final = m.draft.String() // 极端：以流式草稿兜底
		}
		m.resetDraft()
		if strings.TrimSpace(final) == "" {
			return
		}
		if msg.Outcome == conversation.OutcomeDone {
			m.add(blockAssistant, m.renderMD(final))
		} else {
			m.add(blockAssistant, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(
				fmt.Sprintf("[%s]", msg.Outcome))+"\n"+final)
		}
		m.usage = addUsage(m.usage, msg.Usage)
	case conversation.RoleSystem:
		m.resetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	default:
		// user / tool 节点无事件面（输入与工具行已单独入块）。
		m.resetDraft()
	}
	m.scroll = 0
}

// partsText 节点文本：文本分片直连，其余留占位（§4.2 多模态呈现 MVP）。
func partsText(parts []conversation.Part) string {
	var b strings.Builder
	for _, p := range parts {
		switch p.Kind {
		case conversation.PartText:
			b.WriteString(p.Text)
		case conversation.PartImage:
			name := "图片"
			if p.Ref != nil {
				name = "图片：" + p.Ref.Name
			}
			fmt.Fprintf(&b, "〔%s〕", name)
		case conversation.PartAudio:
			b.WriteString("〔音频转写〕")
		case conversation.PartDoc:
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// addUsage 用量累加。
func addUsage(a, b conversation.Usage) conversation.Usage {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CostUSD += b.CostUSD
	return a
}

// resetDraft 清流式草稿。
func (m *model) resetDraft() {
	m.draft.Reset()
	m.drafting = false
}

// add 追加定稿块（跟随尾部）。
func (m *model) add(kind blockKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.blocks = append(m.blocks, block{kind: kind, text: m.style(kind, text)})
	m.scroll = 0
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
	if m.drafting || m.draft.Len() > 0 {
		lines = append(lines, strings.Split(strings.TrimRight(m.draft.String(), "\n"), "\n")...)
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
			parts = append(parts, "模型 "+st.Model)
		}
		if st.Level != "" {
			parts = append(parts, "权限 "+st.Level)
		}
	}
	if u := m.usage; u.InputTokens > 0 || u.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("↑%d ↓%d", u.InputTokens, u.OutputTokens))
	}
	parts = append(parts, "PgUp/PgDn 滚动", "Ctrl+C 取消")
	return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Join(parts, " · "))
}
