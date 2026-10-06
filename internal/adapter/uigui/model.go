package uigui

import (
	"fmt"
	"path/filepath"
	"strconv"
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
	// part 正文分片序号（D107②）：该块所源正文段内首个 PartText 在节点 Content 中的
	// 序号（0 起；编辑回传 --part 用）——回放/提交调和两路盖章；-1 = 未盖章（草稿期/
	// 非正文块）。
	part int
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
	// editTarget 编辑态目标节点（D92 气泡右键「编辑」）：非空时提交 = 结构化 /edit
	// 命令（不回显，修订结果由清屏回放呈现）；"" = 非编辑态。仅事件循环 goroutine 读写。
	editTarget conversation.MessageID
	// editMode 编辑态修订方式（D97 分叉三方式）：随 editMsg 进入时设定，zero = Fresh；
	// 提交时转为 --keep/--copy token（与 typed 路径同解析，D97①）。
	editMode conversation.KeepMode
	// editPart 编辑态正文分片序号（D107②）：随 editMsg 进入时设定（-1 = 缺省合段），
	// 提交时转为 --part token；编辑态生命周期与 editTarget 同步收尾。
	editPart int
	// stagedFile 已暂存附件路径（D104 暂存随文发）：非空时附件槽显示 chip、Enter
	// 提交 Raw{Kind:"file", File, Text:已输入文本}；编辑态不消费（修订不夹带新附件）。
	stagedFile string

	// flushedDrafts 待调和的草稿正文段块序（D95 Turn 粒度）：工具事件先于
	// CommittedEvent 到达——工具边界把流式草稿落为正文气泡（重构前观感：正文先于
	// chip，第 2 轮文本重新流式），块先不带节点 ID；提交（每 Turn 一次）时就地回填
	// 权威文本并盖章。chip 照旧直接入块，确认问答留痕不受提交调和影响。
	flushedDrafts []int
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
		m.flushDraftSegment() // D95：工具边界落正文段（正文气泡先于 chip，与重构前一致）
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
	m.flushedDrafts = nil
	m.usage = conversation.Usage{}
	m.u.scrollPx, m.u.contentH = 0, 0
	m.u.followTail = true
	m.u.selRows = m.u.selRows[:0]
	m.u.sel.clear() // D91 ④：清屏也清转写选区（键清零 → 指纹失配自愈）
	m.u.keyRects = m.u.keyRects[:0]
	m.u.keyFp = 0
}

// commit 提交节点定稿（D95 Turn 粒度：每 Turn 一次）：system 节点（/compact 摘要）
// 入块；用量累计；未完成 chip 一并收口（取消/异常收尾没有结果事件，D67）。
// assistant 节点做调和而非重渲——flushed 草稿段就地回填权威文本、chips 保留实时现场
// （确认问答留痕不丢），末段从 parts 追加渲染。
func (m *model) commit(msg conversation.Message) {
	m.closeOpenChips()
	m.usage = addUsage(m.usage, msg.Usage)
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleAssistant:
		start := len(m.blocks)
		// D100②：本轮思考块补盖节点 ID（未盖章 = 本轮所产——前轮已在各自 commit 盖过），
		// thinking 右键取"所源节点"语义；分叉条挂点口径不变（branchStrips 不取 thinking）。
		for i := range m.blocks {
			if b := &m.blocks[i]; b.kind == blockThinking && b.id == "" {
				b.id = msg.ID
			}
		}
		m.resetDraft()
		segs := textSegments(msg.Content)
		if len(segs) == 0 && len(m.flushedDrafts) == 0 {
			// 空内容的取消/错误也要有反馈（首个 token 前取消是最常见场景）；
			// 纯工具/思考轮（chip/思考卡已实时入块）走 D89 锚点回填节点 ID。
			if msg.Outcome != conversation.OutcomeDone {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), msg.ID)
			} else {
				m.stampAssistantAnchor(msg, start)
			}
			return
		}
		// flushed 草稿段就地回填（FIFO；权威文本以节点为准），并盖段序（D107④）。
		starts := textSegStarts(msg.Content)
		consumed := 0
		for _, idx := range m.flushedDrafts {
			if consumed >= len(segs) {
				break
			}
			m.blocks[idx].text = segs[consumed]
			if consumed < len(starts) {
				m.blocks[idx].part = starts[consumed]
			}
			consumed++
		}
		m.flushedDrafts = m.flushedDrafts[:0]
		// 余段（收场轮的正文，通常仍在草稿）追加渲染；最后一段盖节点 ID
		// ——分叉条（D81）每 Turn 一个挂点，挂在回答气泡下。
		stamped := false
		for i := consumed; i < len(segs); i++ {
			blk := block{kind: blockAssistant, text: segs[i], part: -1}
			if i < len(starts) {
				blk.part = starts[i]
			}
			if msg.Outcome != conversation.OutcomeDone && i == len(segs)-1 {
				blk.text = fmt.Sprintf("[%s]\n%s", msg.Outcome, segs[i])
			}
			if i == len(segs)-1 {
				blk.id = msg.ID
				stamped = true
			}
			m.blocks = append(m.blocks, blk)
		}
		// Turn 以工具收场（正文段全部经工具边界落块）：末个已落块盖 ID。
		if !stamped && len(m.flushedDrafts) > 0 {
			m.blocks[m.flushedDrafts[len(m.flushedDrafts)-1]].id = msg.ID
		}
		m.flushedDrafts = m.flushedDrafts[:0]
	case conversation.RoleSystem:
		m.resetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	default:
		// user 无独立事件面：输入行已在 submit 时回显入块——但回显时节点尚不存在、
		// 块无 ID（D87），此处按「最早未盖章且文本一致」回填节点 ID，分叉条（D81）
		// 在实时会话才能出现；工具结果随 assistant 节点分片呈现（D95）。
		if msg.Role == conversation.RoleUser && text != "" {
			m.stampUserBlock(text, msg.ID)
		}
		m.resetDraft()
	}
}

// flushDraftSegment 工具边界把流式草稿落为正文气泡（D95 Turn 粒度）：块先不带节点
// ID（提交调和时回填权威文本并盖章），草稿清空后第 2 轮文本重新流式。
func (m *model) flushDraftSegment() {
	if !m.drafting && m.draft.Len() == 0 {
		return
	}
	text := m.draft.String()
	m.resetDraft()
	if strings.TrimSpace(text) == "" {
		return
	}
	m.flushedDrafts = append(m.flushedDrafts, len(m.blocks))
	m.blocks = append(m.blocks, block{kind: blockAssistant, text: text, part: -1}) // 段序提交调和时盖（D107④）
}

// textSegments 按工具分片把节点正文切段（D95 Turn 粒度）：段 = tool 分片之间的连续
// text/image/audio/doc 分片，渲染口径与 partsText 一致（§4.2 占位、§9 出口消毒）。
// thinking 分片不进正文（回放单独成块，D42）。
func textSegments(parts []conversation.Part) []string {
	var segs []string
	var b strings.Builder
	flush := func() {
		if s := sanitizeControl(b.String()); strings.TrimSpace(s) != "" {
			segs = append(segs, s)
		}
		b.Reset()
	}
	for _, p := range parts {
		if p.Kind == conversation.PartTool {
			flush()
			continue
		}
		appendPartText(&b, p)
	}
	flush()
	return segs
}

// textSegStarts 各正文段的首个正文分片序号（D107④）：与 textSegments 同分口径（仅工具
// 边界断段、空段不产出），段序 = 段内首个 PartText 在 Content 中的序号（0 起）——提交
// 调和时给正文块盖 part，编辑回传 --part 用。
func textSegStarts(parts []conversation.Part) []int {
	var starts []int
	var b strings.Builder
	ord, start := -1, -1
	flush := func() {
		if s := sanitizeControl(b.String()); strings.TrimSpace(s) != "" {
			starts = append(starts, start)
		}
		b.Reset()
		start = -1
	}
	for _, p := range parts {
		if p.Kind == conversation.PartTool {
			flush()
			continue
		}
		if p.Kind == conversation.PartText {
			ord++
			if start == -1 {
				start = ord
			}
		}
		appendPartText(&b, p)
	}
	flush()
	return starts
}

// stampAssistantAnchor 无正文 assistant 节点的分叉锚点回填（D89）：纯工具轮（模型
// 径直发起调用、无正文）在转写里没有任何带 ID 的**分叉挂点块** → 分叉条无处可挂
// （branchStrips 跳过空 ID 块），切到该分支后无法切回。优先按调用 ID 匹配本轮 chip
// （该节点的可见表示），无工具则锚到本轮最后一个新块。已有锚（挂点块直携 ID）时
// no-op——检查只看 user/assistant/tool（D100②：thinking 块现已盖章，但 branchStrips
// 不取 thinking，思考块带 ID 不构成锚）。
func (m *model) stampAssistantAnchor(msg conversation.Message, start int) {
	if start > len(m.blocks) {
		start = len(m.blocks)
	}
	for _, b := range m.blocks[start:] {
		if b.id == msg.ID && (b.kind == blockUser || b.kind == blockAssistant || b.kind == blockTool) {
			return
		}
	}
	for _, p := range msg.Content {
		if p.Kind == conversation.PartTool && p.Tool != nil {
			if i := m.findChipBlock(tool.CallID(p.Tool.CallID)); i >= 0 {
				m.blocks[i].id = msg.ID
				return
			}
		}
	}
	if len(m.blocks) > start {
		m.blocks[len(m.blocks)-1].id = msg.ID
	}
}

// findChipBlock 按调用 ID 找 chip 块序（D89 锚点匹配；D95 后调用声明在节点分片里）。
func (m *model) findChipBlock(id tool.CallID) int {
	for i := range m.blocks {
		if c := m.blocks[i].chip; c != nil && c.id == id {
			return i
		}
	}
	return -1
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
		b.part = 0 // D107④：user 节点单正文分片（标题/随文合并后恒一段）
		return
	}
}

// replay 历史节点回放（D40/§7.4）：已提交节点按角色一次性定稿渲染；无草稿/流式
// 过程，也不累计用量（回放是展示，不是新一轮提交）。D95 Turn 粒度：逐分片序渲染
// ——思考分片各自成块、正文按 tool 边界分段成气泡、chip 夹段间，与实时一致。
func (m *model) replay(msg conversation.Message) {
	text := partsText(msg.Content)
	switch msg.Role {
	case conversation.RoleUser:
		if strings.TrimSpace(text) != "" {
			m.addMsg(blockUser, text, msg.ID)
		}
	case conversation.RoleAssistant:
		start := len(m.blocks)
		var seg strings.Builder
		// D107④：段序盖章——segStart = 本段首个 PartText 在 Content 中的序号（0 起），
		// 编辑回传 --part 用；flushSeg 产出块即盖。
		textOrd, segStart := -1, -1
		flushSeg := func() {
			if s := sanitizeControl(seg.String()); strings.TrimSpace(s) != "" {
				m.addMsg(blockAssistant, s, "") // 段先不带 ID：末段统一盖
				m.blocks[len(m.blocks)-1].part = segStart
			}
			seg.Reset()
			segStart = -1
		}
		for _, p := range msg.Content {
			switch p.Kind {
			case conversation.PartThinking:
				flushSeg()
				if strings.TrimSpace(p.Text) != "" {
					m.addMsg(blockThinking, p.Text, msg.ID) // 回放无耗时 → secs 置 -1；D100② 直盖节点 ID
				}
			case conversation.PartTool:
				flushSeg()
				if p.Tool == nil {
					continue
				}
				m.addToolCall(tool.Call{ID: tool.CallID(p.Tool.CallID), Name: p.Tool.Name, Args: p.Tool.Args})
				if p.Tool.Result != nil {
					m.attachToolResult(*p.Tool.Result) // D67：按 CallID 并入调用 chip
				}
			default:
				if p.Kind == conversation.PartText {
					textOrd++
					if segStart == -1 {
						segStart = textOrd
					}
				}
				appendPartText(&seg, p)
			}
		}
		flushSeg()
		// 非 done 终态：标记行并入最后一个正文气泡（与实时 commit 同口径）；
		// 无正文段（空内容的取消/错误）单独给标记块。
		if msg.Outcome != conversation.OutcomeDone {
			marked := false
			for i := len(m.blocks) - 1; i >= start; i-- {
				if m.blocks[i].kind == blockAssistant {
					m.blocks[i].text = fmt.Sprintf("[%s]\n%s", msg.Outcome, m.blocks[i].text)
					marked = true
					break
				}
			}
			if !marked {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), "")
			}
		}
		// 最后一个正文气泡盖节点 ID（D81 分叉条每 Turn 一个挂点）；
		// 无正文段（纯工具/思考轮）走 D89 锚点回填。
		stamped := false
		for i := len(m.blocks) - 1; i >= start; i-- {
			if m.blocks[i].kind == blockAssistant {
				m.blocks[i].id = msg.ID
				stamped = true
				break
			}
		}
		if !stamped {
			m.stampAssistantAnchor(msg, start) // D89：纯工具/思考轮锚点（见函数注释）
		}
	case conversation.RoleSystem:
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	default: // root 等不该出现在回放区间
	}
}

// submit 提交一行：确认态优先应答，编辑态走结构化 /edit，空行忽略，其余先投递再入转写。
// 投递失败（缓冲满）时明确标注"未执行"——不制造"已执行"错觉（uitui 审查修复同款）。
func (m *model) submit(text string) {
	if m.confirm != nil {
		m.replyConfirm(port.ConfirmAnswer{Allow: isYes(text)})
		return
	}
	line := strings.TrimSpace(text)
	// D104 暂存随文发：有暂存附件 → Raw 提交（文本可空 = 仅附件，暂存清空）；
	// 编辑态不消费（修订不夹带新附件，暂存保留待正常输入）。不回显常规 blockUser
	// 以外的附加块——用户节点无 CommittedEvent（D104③），本地回显见 submitRaw。
	if m.editTarget == "" && m.stagedFile != "" {
		m.submitRaw(m.stagedFile, line)
		return
	}
	if line == "" {
		return
	}
	// D92/D97 编辑态：提交 = 结构化 /edit 命令——Args 直达不经斜杠解析（parseEditArgs
	// 的 Join 对单元素是恒等，多行文本保真）；修订方式转为 flag token（--keep=Carry/
	// --copy=Clone，与 typed 路径同解析，D97①）；不回显 blockUser，修订结果由 /edit 的
	// 清屏回放呈现（D81 口径：转写区恒与 Head 一致）。
	if m.editTarget != "" {
		id := m.editTarget
		mode := m.editMode
		part := m.editPart
		m.editTarget = ""
		m.editMode = conversation.Fresh
		m.editPart = -1
		args := []string{string(id)}
		switch mode {
		case conversation.Carry:
			args = append(args, "--keep")
		case conversation.Clone:
			args = append(args, "--copy")
		}
		if part >= 0 {
			args = append(args, "--part", strconv.Itoa(part)) // D107②：精确替换该正文分片
		}
		args = append(args, line)
		select {
		case m.u.inCh <- port.UserInput{Command: &port.Command{
			Name: "edit",
			Args: args,
		}}:
		default:
			m.add(blockNotice, "[notice] 输入缓冲已满，此行未执行")
		}
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

// submitRaw 投递 Raw 摄取输入（D104 暂存随文发）：文件 + 已输入文本一并进摄取管线；
// 用户节点无 CommittedEvent（Agent 只广播 Turn/system 节点）→ 本地回显 blockUser
// （文本 + 附件名示意——与回放的摄取分片呈现存在口径差，D104③ 后果①）。
// 缓冲满：恢复暂存 + notice（编辑框已清、文本丢失，D104 后果③）。
func (m *model) submitRaw(file, text string) {
	select {
	case m.u.inCh <- port.UserInput{Raw: &port.RawInput{Kind: "file", File: file, Text: text}}:
		m.stagedFile = "" // 提交即清暂存（D104①）
	default:
		m.stagedFile = file
		m.add(blockNotice, "[notice] 输入缓冲已满，附件未发送")
		return
	}
	echo := text
	if echo != "" {
		echo += " "
	}
	m.add(blockUser, echo+"〔附件："+filepath.Base(file)+"〕")
}

// stageAttach 暂存/替换附件（D104：单槽——再选即替换，点 chip 取消）。
func (m *model) stageAttach(path string) {
	m.stagedFile = path
}

// clearAttach 取消暂存（chip 点击）。
func (m *model) clearAttach() {
	m.stagedFile = ""
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
	b := block{kind: kind, text: text, id: id, part: -1}
	if kind == blockThinking {
		b.secs = -1
	}
	m.blocks = append(m.blocks, b)
}

// partsText 节点文本：文本分片直连，其余留占位（§4.2 多模态呈现 MVP）；
// 出口统一剥控制序列（提交内容是不可信模型输出，§9）。
// 思考分片不并入正文——回放路径单独渲染为思考块（D42）；
// 工具分片不并入正文——回放路径单独渲染为 chip（D95）。
func partsText(parts []conversation.Part) string {
	var b strings.Builder
	for _, p := range parts {
		appendPartText(&b, p)
	}
	return sanitizeControl(b.String())
}

// appendPartText 把一个分片的呈现文本写入 b（partsText/textSegments 共用口径）；
// 思考/工具分片不并入正文（各自单独成块，D42/D95）。
func appendPartText(b *strings.Builder, p conversation.Part) {
	switch p.Kind {
	case conversation.PartText:
		b.WriteString(p.Text)
	case conversation.PartImage:
		name := "图片"
		if p.Ref != nil {
			name = "图片：" + p.Ref.Name
		}
		fmt.Fprintf(b, "〔%s〕", name)
	case conversation.PartAudio:
		b.WriteString("〔音频转写〕")
	case conversation.PartDoc:
		b.WriteString(p.Text)
	case conversation.PartThinking, conversation.PartTool:
		// 单独成块（思考卡 / chip），不混正文。
	}
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
