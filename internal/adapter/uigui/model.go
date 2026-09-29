package uigui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// blockKind 转写块种类（样式由渲染期按 kind 决定——GUI 不做入块期着色，
// 与 uitui 的 lipgloss 入块样式不同：Gio 主题令牌在 window.go，§15.4）。
type blockKind int

const (
	blockPlain     blockKind = iota // Say/命令输出/通用行
	blockUser                       // 提交的输入
	blockAssistant                  // committed 助手文本（渲染期 markdown，§15.3 骨架先纯文本）
	blockTool                       // 工具调用/结果 chip（骨架先文本行）
	blockNotice                     // notice 提示
	blockError                      // 错误块
	blockSystem                     // system 节点（/compact 摘要等）
	blockThinking                   // 思考块（D42；流式实时 + 定稿折叠，§15.3）
)

// block 一段定稿转写（text 已剥控制序列，§9）。
// secs 仅思考块使用：>=0 定稿耗时秒数（折叠行"已思考 · Ns"），-1 = 回放无耗时。
type block struct {
	kind blockKind
	text string
	secs int
}

// pendingConfirm 进行中的确认（输入栏确认态，§15.2 非模态按钮组）。
type pendingConfirm struct {
	prompt string
	reply  chan bool
}

// model 渲染状态机：事件面口径对齐 uitui/model.go（§15.5）——
// Delta.Reasoning 分流进思考草稿、CommittedEvent 定稿落块、HistoryEvent 回放
// （思考暗块先行）、Say 纯文本块。仅事件循环 goroutine 读写；
// 输入编辑态在 Gio editor（window.go），不在此处。
type model struct {
	u *UI

	blocks   []block
	draft    strings.Builder // 流式草稿（committed 后以消息文本定稿替换）
	drafting bool
	think    strings.Builder // 进行中的思维链（流式实时可见，D42）
	thinkAt  time.Time       // 首个思考增量时刻（flush → secs 定稿耗时）

	confirm *pendingConfirm
	usage   conversation.Usage // 节点权威累计（用量详情后补进 logo 菜单，§15.1）
}

// newModel 初始状态机。
func newModel(u *UI) *model {
	return &model{u: u}
}

// say 纯文本行（命令输出、启动提示）。
func (m *model) say(text string) {
	m.flushThink() // 时序：思维链先于外部输出
	m.add(blockPlain, sanitizeControl(text))
}

// handleEvent Turn 事件 → 转写块（与 repl.Emit 同呈现语义）。
func (m *model) handleEvent(ev port.Event) {
	if d, ok := ev.(port.DeltaEvent); ok && d.Delta.Reasoning {
		// 思维链（D34 展示）：积累进 think 草稿实时可见；提交时节点已带
		// PartThinking（D42），故落块只由这里与 flushThink 负责，commit 不重复渲染。
		if m.think.Len() == 0 {
			m.thinkAt = time.Now()
		}
		m.think.WriteString(sanitizeControl(d.Delta.Text))
		return
	}
	m.flushThink() // 思维链阶段结束：落为暗块，后续正文/工具/提交照常
	switch e := ev.(type) {
	case port.DeltaEvent:
		if e.Delta.Text == "" {
			return // 工具调用分片、用量分片不进转写
		}
		m.drafting = true
		m.draft.WriteString(sanitizeControl(e.Delta.Text)) // 模型流为不可信输入（§9）
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
	case port.HistoryEvent:
		m.replay(e.Message) // 启动历史回放（D40/§7.4）：按节点角色定稿渲染
	case port.ErrorEvent:
		m.add(blockError, "error: "+sanitizeControl(e.Err.Error())) // 服务端错误片段可携带注入序列
	case port.NoticeEvent:
		m.add(blockNotice, "[notice] "+sanitizeControl(e.Text))
	default:
		m.add(blockPlain, sanitizeControl(fmt.Sprintf("%v", ev)))
	}
}

// commit 提交节点定稿：助手（done）以消息文本为准替换草稿；
// system 节点（/compact 摘要）入块；用量在所有节点上累计（含 system 的压缩摘要）。
func (m *model) commit(msg conversation.Message) {
	m.usage = addUsage(m.usage, msg.Usage)
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleAssistant:
		final := text
		if final == "" {
			final = m.draft.String() // 极端：以流式草稿兜底
		}
		m.resetDraft()
		if strings.TrimSpace(final) == "" {
			// 空内容的取消/错误也要有反馈（首个 token 前取消是最常见场景）。
			if msg.Outcome != conversation.OutcomeDone {
				m.add(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome))
			}
			return
		}
		if msg.Outcome == conversation.OutcomeDone {
			m.add(blockAssistant, final) // §15.3 完整 markdown 在渲染期处理（骨架先纯文本）
		} else {
			m.add(blockAssistant, fmt.Sprintf("[%s]\n%s", msg.Outcome, final))
		}
	case conversation.RoleSystem:
		m.resetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	default:
		// user / tool 节点无事件面（输入与工具行已单独入块）。
		m.resetDraft()
	}
}

// replay 历史节点回放（D40/§7.4）：已提交节点按角色一次性定稿渲染；无草稿/流式
// 过程，也不累计用量（回放是展示，不是新一轮提交）。思考暗块先行（D42）。
func (m *model) replay(msg conversation.Message) {
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleUser:
		if strings.TrimSpace(text) != "" {
			m.add(blockUser, text)
		}
	case conversation.RoleAssistant:
		if tp := thinkingText(msg.Content); tp != "" {
			m.add(blockThinking, tp) // 回放无耗时 → secs 置 -1（add 内统一处理）
		}
		for _, call := range msg.ToolCalls {
			m.add(blockTool, "[tool] "+call.Name+" "+preview(call.Args))
		}
		if strings.TrimSpace(text) == "" {
			if msg.Outcome != conversation.OutcomeDone {
				m.add(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome))
			}
			break
		}
		if msg.Outcome == conversation.OutcomeDone {
			m.add(blockAssistant, text)
		} else {
			m.add(blockAssistant, fmt.Sprintf("[%s]\n%s", msg.Outcome, text))
		}
	case conversation.RoleTool:
		if msg.ToolResult == nil {
			return
		}
		status, detail := "ok", msg.ToolResult.Output
		if !msg.ToolResult.OK {
			status, detail = "failed", msg.ToolResult.Err
		}
		m.add(blockTool, "[tool "+status+"] "+preview([]byte(detail)))
	case conversation.RoleSystem:
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	default: // root 等不该出现在回放区间
	}
}

// submit 提交一行：确认态优先应答，空行忽略，其余先投递再入转写。
// 投递失败（缓冲满）时明确标注"未执行"——不制造"已执行"错觉（uitui 审查修复同款）。
func (m *model) submit(text string) {
	if m.confirm != nil {
		m.replyConfirm(isYes(text))
		return
	}
	line := strings.TrimSpace(text)
	if line == "" {
		return
	}
	select {
	case m.u.inCh <- parseInput(line):
		m.add(blockUser, line)
	default:
		m.add(blockUser, line)
		m.add(blockNotice, "[notice] 输入缓冲已满，此行未执行")
	}
}

// startConfirm 打开确认态（Confirm 投递；输入栏切按钮组，§15.2）。
func (m *model) startConfirm(prompt string, reply chan bool) {
	m.confirm = &pendingConfirm{prompt: sanitizeControl(prompt), reply: reply}
	m.add(blockPlain, m.confirm.prompt+" [y/N]")
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

// flushThink 把进行中的思维链落为定稿思考块（D34/§15.3：定稿带耗时秒数）。
func (m *model) flushThink() {
	if m.think.Len() == 0 {
		return
	}
	secs := 0
	if !m.thinkAt.IsZero() {
		secs = int(time.Since(m.thinkAt).Round(time.Second).Seconds())
	}
	b := block{kind: blockThinking, text: m.think.String(), secs: secs}
	m.blocks = append(m.blocks, b)
	m.think.Reset()
	m.thinkAt = time.Time{}
}

// resetDraft 清流式草稿。
func (m *model) resetDraft() {
	m.draft.Reset()
	m.drafting = false
}

// add 追加定稿块。思考块缺省 secs=-1（回放无耗时；flushThink 自行填）。
func (m *model) add(kind blockKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b := block{kind: kind, text: text}
	if kind == blockThinking {
		b.secs = -1
	}
	m.blocks = append(m.blocks, b)
}

// partsText 节点文本：文本分片直连，其余留占位（§4.2 多模态呈现 MVP）；
// 出口统一剥控制序列（提交内容是不可信模型输出，§9）。
// 思考分片不并入正文——回放路径单独渲染为思考块（D42）。
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
		case conversation.PartThinking:
			// 单独成块（thinkingText），不混正文。
		}
	}
	return sanitizeControl(b.String())
}

// thinkingText 提取首个非空思考分片（D42：回放口径恒含思考）；无思考返回空串。
func thinkingText(parts []conversation.Part) string {
	for _, p := range parts {
		if p.Kind == conversation.PartThinking && strings.TrimSpace(p.Text) != "" {
			return sanitizeControl(p.Text)
		}
	}
	return ""
}

// addUsage 用量累加。
func addUsage(a, b conversation.Usage) conversation.Usage {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CostUSD += b.CostUSD
	return a
}

// preview 截断工具参数/结果为单行预览（不可信数据只渲染，DESIGN §9）。
// 先剥控制序列（与 notify.sanitizeControl 同算法、GUI 出口面一致）。
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
// 算法与 internal/adapter/notify、uitui 的同名函数一致——分别守护通知面/终端转写面/
// GUI 出口面。
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
