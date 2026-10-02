package uigui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
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
	blockBranch                     // 分叉行（D81）：气泡下方的 `◀ i/n ▶` 切换条
)

// block 一段定稿转写（text 已剥控制序列，§9）。
// secs 仅思考块使用：>=0 定稿耗时秒数（折叠行"已思考 · Ns"），-1 = 回放无耗时。
// chip 仅工具块使用（D67）：一次调用合并调用/确认/结果。
// id 仅 user/assistant 正文块使用（D81）：所源节点——分叉编号与左右切换的数据键。
type block struct {
	kind blockKind
	text string
	secs int
	chip *toolChip
	id   conversation.MessageID
}

// toolChip 一次工具调用的合并视图（D67/§15.3）：调用 + 权限确认 + 结果同组，
// 默认折叠、点击展开。args/result 为不可信数据，只渲染（§9），全文不截断。
type toolChip struct {
	id       tool.CallID
	name     string
	args     string // 原始 JSON 参数
	confirmQ string // 权限问句（"" = 未触发确认）
	confirmA string // 应答（"允许"/"拒绝"；空 = 待应答/未触发）
	done     bool
	ok       bool
	result   string // 输出（ok）或错误（!ok）全文
}

// pendingConfirm 进行中的确认（输入栏确认态，§15.2 非模态按钮组）。
type pendingConfirm struct {
	prompt string
	reply  chan port.ConfirmAnswer
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

	confirm     *pendingConfirm
	confirmChip int                // 确认归属的 chip 块序（D67；-1 = 非 chip 确认，如 /rm）
	usage       conversation.Usage // 节点权威累计（用量详情后补进 logo 菜单，§15.1）
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
		m.addToolCall(e.Call)
	case port.ToolResultEvent:
		m.attachToolResult(e.Result)
	case port.CommittedEvent:
		m.commit(e.Message)
	case port.HistoryEvent:
		m.replay(e.Message) // 启动历史回放（D40/§7.4）：按节点角色定稿渲染
	case port.ErrorEvent:
		m.add(blockError, "error: "+sanitizeControl(e.Err.Error())) // 服务端错误片段可携带注入序列
	case port.NoticeEvent:
		m.add(blockNotice, "[notice] "+sanitizeControl(e.Text))
	case port.ClearEvent:
		m.clear() // 转写重开（D75：/switch、/new）
	default:
		m.add(blockPlain, sanitizeControl(fmt.Sprintf("%v", ev)))
	}
}

// clear 清屏（D75：/switch、/new）：丢弃转写块、未完成草稿与进行中的思维链，
// 用量归零（前端累计值，/usage 走节点汇总不受影响）；转写滚动与行选态在 UI 侧
// 一并归零——内容没了，偏移/高度失去意义，恢复跟随贴底。
func (m *model) clear() {
	m.blocks = nil
	m.draft.Reset()
	m.drafting = false
	m.think.Reset()
	m.usage = conversation.Usage{}
	m.u.scrollPx, m.u.contentH = 0, 0
	m.u.followTail = true
	m.u.selRows = m.u.selRows[:0]
	m.u.sel.clear() // D91 ④：清屏也清转写选区（键清零 → 指纹失配自愈）
	m.u.keyRects = m.u.keyRects[:0]
	m.u.keyFp = 0
}

// commit 提交节点定稿：助手（done）以消息文本为准替换草稿；
// system 节点（/compact 摘要）入块；用量在所有节点上累计（含 system 的压缩摘要）。
// 未完成 chip 一并收口（取消/异常收尾没有结果事件，D67）。
func (m *model) commit(msg conversation.Message) {
	m.closeOpenChips()
	m.usage = addUsage(m.usage, msg.Usage)
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleAssistant:
		start := len(m.blocks)
		final := text
		if final == "" {
			final = m.draft.String() // 极端：以流式草稿兜底
		}
		m.resetDraft()
		if strings.TrimSpace(final) == "" {
			// 空内容的取消/错误也要有反馈（首个 token 前取消是最常见场景）。
			if msg.Outcome != conversation.OutcomeDone {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), msg.ID)
			} else {
				// D89：纯工具/思考轮（chip 由 ToolCallEvent 先行入块）——锚回填节点
				// ID，分叉条在纯工具轮分支才有挂点。
				m.stampAssistantAnchor(msg, start)
			}
			return
		}
		if msg.Outcome == conversation.OutcomeDone {
			m.addMsg(blockAssistant, final, msg.ID) // §15.3 完整 markdown 在渲染期处理（骨架先纯文本）
		} else {
			m.addMsg(blockAssistant, fmt.Sprintf("[%s]\n%s", msg.Outcome, final), msg.ID)
		}
	case conversation.RoleSystem:
		m.resetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	default:
		// user / tool 节点无独立事件面：工具行由 chip 呈现；user 行已在 submit 时
		// 回显入块——但回显时节点尚不存在、块无 ID（D87），此处按「最早未盖章且
		// 文本一致」回填节点 ID，分叉条（D81）在实时会话才能出现。
		if msg.Role == conversation.RoleUser && text != "" {
			m.stampUserBlock(text, msg.ID)
		}
		m.resetDraft()
	}
}

// stampAssistantAnchor 无正文 assistant 节点的分叉锚点回填（D89）：thinking 卡与
// 工具 chip 都不携节点 ID，纯工具轮（模型径直发起调用、无正文）在转写里没有任何
// 带 ID 的块 → 分叉条无处可挂（branchStrips 跳过空 ID 块），切到该分支后无法切回。
// 优先按调用 ID 匹配本轮 chip（该节点的可见表示），无工具则锚到本轮最后一个新块
// （思考卡）。已有锚（带正文块直携 ID）时 no-op。
func (m *model) stampAssistantAnchor(msg conversation.Message, start int) {
	if start > len(m.blocks) {
		start = len(m.blocks)
	}
	for _, b := range m.blocks[start:] {
		if b.id == msg.ID {
			return
		}
	}
	for _, call := range msg.ToolCalls {
		for i := range m.blocks {
			if c := m.blocks[i].chip; c != nil && c.id == call.ID {
				m.blocks[i].id = msg.ID
				return
			}
		}
	}
	if len(m.blocks) > start {
		m.blocks[len(m.blocks)-1].id = msg.ID
	}
}

// stampUserBlock 给回显的用户行补盖节点 ID（D87）：按「最早未盖章且文本一致」匹配
// ——回显与提交同经输入通道串行 FIFO，重名按序对齐；命令行回显以 "/" 开头、
// parseInput 恒路由为命令、永不会成为节点文本，插队也不会被错盖。找不到匹配
// （如附件等无文本提交）静默跳过，维持无 ID 现状。
func (m *model) stampUserBlock(text string, id conversation.MessageID) {
	want := sanitizeControl(text)
	for i := range m.blocks {
		b := &m.blocks[i]
		if b.kind != blockUser || b.id != "" || sanitizeControl(b.text) != want {
			continue
		}
		b.id = id
		return
	}
}

// replay 历史节点回放（D40/§7.4）：已提交节点按角色一次性定稿渲染；无草稿/流式
// 过程，也不累计用量（回放是展示，不是新一轮提交）。思考暗块先行（D42）。
func (m *model) replay(msg conversation.Message) {
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleUser:
		if strings.TrimSpace(text) != "" {
			m.addMsg(blockUser, text, msg.ID)
		}
	case conversation.RoleAssistant:
		start := len(m.blocks)
		if tp := thinkingText(msg.Content); tp != "" {
			m.add(blockThinking, tp) // 回放无耗时 → secs 置 -1（add 内统一处理）
		}
		for _, call := range msg.ToolCalls {
			m.addToolCall(call) // D67：调用开 chip，结果由后续 tool 节点按 CallID 回填
		}
		if strings.TrimSpace(text) == "" {
			if msg.Outcome != conversation.OutcomeDone {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), msg.ID)
			}
		} else if msg.Outcome == conversation.OutcomeDone {
			m.addMsg(blockAssistant, text, msg.ID)
		} else {
			m.addMsg(blockAssistant, fmt.Sprintf("[%s]\n%s", msg.Outcome, text), msg.ID)
		}
		m.stampAssistantAnchor(msg, start) // D89：纯工具/思考轮锚点（见函数注释）
	case conversation.RoleTool:
		if msg.ToolResult == nil {
			return
		}
		m.attachToolResult(*msg.ToolResult) // D67：按 CallID 并入调用 chip
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
		m.replyConfirm(port.ConfirmAnswer{Allow: isYes(text)})
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

// submitCommand 投递一条命令且**不入转写块**（D81 分叉按钮）：与 model.submit 同走
// `inCh`（壳内不旁路内核，同 D72/D73 菜单口径），区别仅在不回显——按钮不是用户输入，
// 转写里写一行 `/goto <ulid>` 是噪音。返回 false = 输入缓冲满（点击丢弃，调用方给提示）。
func (m *model) submitCommand(line string) bool {
	select {
	case m.u.inCh <- parseInput(line):
		return true
	default:
		return false
	}
}

// addToolCall 新开一个工具调用 chip（D67：调用/确认/结果合并，默认折叠）。
func (m *model) addToolCall(call tool.Call) {
	m.blocks = append(m.blocks, block{kind: blockTool, chip: &toolChip{
		id:   call.ID,
		name: call.Name,
		args: sanitizeControl(string(call.Args)),
	}})
}

// attachToolResult 按 CallID 回填最新匹配 chip（D67；并行调用交错不错配）；
// 找不到（历史截断等）则新开结果 chip 兜底。
func (m *model) attachToolResult(r tool.Result) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		c := m.blocks[i].chip
		if c == nil || c.id != r.CallID {
			continue
		}
		c.done, c.ok = true, r.OK
		if r.OK {
			c.result = sanitizeControl(r.Output)
		} else {
			c.result = sanitizeControl(r.Err)
		}
		return
	}
	m.blocks = append(m.blocks, block{kind: blockTool, chip: &toolChip{
		done:   true,
		ok:     r.OK,
		result: sanitizeControl(resultText(r)),
	}})
}

// resultText 取结果文本（ok = 输出，!ok = 错误）。
func resultText(r tool.Result) string {
	if r.OK {
		return r.Output
	}
	return r.Err
}

// closeOpenChips 收口所有未完成 chip（commit 时：取消/异常收尾没有结果事件，D67）。
func (m *model) closeOpenChips() {
	for i := range m.blocks {
		if c := m.blocks[i].chip; c != nil && !c.done {
			c.done = true
			c.ok = false
			c.result = "（未返回——本轮已结束）"
		}
	}
}

// startConfirm 打开确认态（Confirm 投递；输入栏切按钮组，§15.2）。
// 工具权限确认（问句含工具名且最新 chip 未完成）并入该 chip（D67）；
// 其余（/rm 二次确认等）保持独立文本行。
func (m *model) startConfirm(prompt string, reply chan port.ConfirmAnswer) {
	m.confirm = &pendingConfirm{prompt: sanitizeControl(prompt), reply: reply}
	m.u.reasonEd.SetText("") // D86：原因框清空待填
	m.u.reasonFocus = true   // 焦点让入原因框（主编辑器确认态隐藏，D63 一次性口径）
	if i := m.openChipFor(m.confirm.prompt); i >= 0 {
		m.blocks[i].chip.confirmQ = m.confirm.prompt
		m.confirmChip = i
		return
	}
	m.confirmChip = -1
	m.add(blockPlain, m.confirm.prompt)
}

// openChipFor 确认问句归属的最新未完成 chip（D67）：问句含其工具名才算（confirmPrompt
// 格式含 call.Name；/rm 等命令确认不含 → 不并入）。无 chip 或最新 chip 不匹配返回 -1。
func (m *model) openChipFor(prompt string) int {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		c := m.blocks[i].chip
		if c == nil {
			continue
		}
		if !c.done && c.confirmQ == "" && c.name != "" && strings.Contains(prompt, c.name) {
			return i
		}
		return -1
	}
	return -1
}

// replyConfirm 应答进行中的确认（缓冲 1 + default：取消后迟到的应答不阻塞）。
// chip 确认把问答留痕在 chip 内（D67）；其余保持文本行回显。两条路径都必须把应答
// 送回 runner（否则权限等待永不解除）。
func (m *model) replyConfirm(ans port.ConfirmAnswer) {
	if m.confirm == nil {
		return
	}
	reply, prompt := m.confirm.reply, m.confirm.prompt
	if m.confirmChip >= 0 && m.confirmChip < len(m.blocks) {
		if c := m.blocks[m.confirmChip].chip; c != nil && c.confirmQ != "" {
			c.confirmA = confirmEcho(ans) // D86：允许 / 拒绝 / 拒绝：<原因>
			prompt = ""                   // chip 路径留痕在展开体，不再加文本行
		}
	}
	m.confirm = nil
	m.confirmChip = -1
	if prompt != "" {
		m.add(blockPlain, prompt+" → "+confirmEcho(ans))
	}
	select {
	case reply <- ans:
	default:
	}
}

// confirmEcho 应答回显文案（D86）：允许 / 拒绝 / 拒绝：<原因>（原因消毒同问句；
// Allow 时忽略原因）。chip confirmA 与 plain 行回显共用。
func confirmEcho(ans port.ConfirmAnswer) string {
	if ans.Allow {
		return "允许"
	}
	if r := sanitizeControl(strings.TrimSpace(ans.Reason)); r != "" {
		return "拒绝：" + r
	}
	return "拒绝"
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
	m.addMsg(kind, text, "")
}

// addMsg 追加定稿块并记下其所源节点（D81：UI 据此在气泡下方画分叉编号；
// 空 id = 不参与分叉呈现，如命令输出/notice/思考块）。
func (m *model) addMsg(kind blockKind, text string, id conversation.MessageID) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b := block{kind: kind, text: text, id: id}
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
