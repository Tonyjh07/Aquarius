package uigui

import (
	"fmt"
	"sort"
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
	blockInject                     // 注入节点（D166）：暗卡片 + 来源/触发头部，正文可选
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
	// source/trigger 仅注入块使用（D166）：来源键（展示期经 port.InjectSourceLabel
	// 翻成用户面措辞）与落地时是否随附起了响应。只作展示，不改标题、不挂分叉条。
	source  string
	trigger bool
	// part 正文分片序号（D107②）：该块所源正文段内首个 PartText 在节点 Content 中的
	// 序号（0 起；编辑回传 --part 用）——回放/提交调和两路盖章；-1 = 未盖章（草稿期/
	// 非正文块）。
	part int
	// imgs 该块要显示的图片（D207）：附件分片（PartImage）与工具产出的图片都收在这里，
	// 渲染期按固定方图排（见 imgthumb.go）。空 = 纯文本块。
	imgs []imgRef
}

// toolChip 一次工具调用的合并视图（D67/§15.3）：调用 + 权限确认 + 结果同组，
// 默认折叠、点击展开。args/result 为不可信数据，只渲染（§9），全文不截断。
// images 本次调用产出的图片（D207：截屏、file_read 内联图）——折叠态只给张数记号，
// 展开后按方格出缩略图。
type toolChip struct {
	id       tool.CallID
	name     string
	args     string // 原始 JSON 参数
	confirmQ string // 权限问句（"" = 未触发确认）
	confirmA string // 应答（"允许"/"拒绝"；空 = 待应答/未触发）
	done     bool
	ok       bool
	result   string // 输出（ok）或错误（!ok）全文
	images   []tool.ImageRef
}

// pendingConfirm 进行中的确认（输入栏确认态，§15.2 非模态按钮组）。
type pendingConfirm struct {
	prompt string
	reply  chan port.ConfirmAnswer
	// share 本次确认在窗间的投影身份（D157）：由 startConfirm 发布、replyConfirm 撤下；
	// 次窗应答据此校验「答的是不是这一次」（迟到应答不得落到下一次确认上）。
	share *confirmShare
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
	// ask 进行中的提问面板（D184）：非空时输入栏画面板、按键走面板语义。
	ask         *pendingAsk
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
	// stagedFiles 已暂存附件（D186 多附件）：非空时附件槽显示 chip、Enter 提交
	// Raw{Kind:"file", Attachments, Text:已输入文本}。D188 起编辑态也消费它——那是
	// **新增**的附件（提交时按 chip 序进 Command.Attachments），不再是"留到下一条"。
	stagedFiles []stagedAtt
	// editAtts 编辑态目标节点的已有附件（D188，进编辑态时从 port.TreeView 取一次快照）。
	// 序号口径与 /edit --drop N 一致（附件分片出现序）。生命周期与 editTarget 同步收尾。
	editAtts []port.AttachmentView
	// editDropped 编辑态已标记删除的已有附件序号（D188：提交时翻成 --drop N）。
	editDropped map[int]bool

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
	if m.ask != nil {
		m.askDismiss() // D184：清屏（/switch、/new）时提问面板一并收场（放弃全部）
	}
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
	// D182：思考块折叠态按块序缓存（块只增不减），清屏后块序从 0 重排——一并复位，
	// 新会话的思考块回到默认折叠，不继承上一会话某个序位的开合。
	m.u.thinkOpen = nil
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
		segs := nodeSegments(msg.Content)
		if len(segs) == 0 && len(m.flushedDrafts) == 0 {
			// 空内容的取消/错误也要有反馈（首个 token 前取消是最常见场景）；
			// 纯工具/思考轮（chip/思考卡已实时入块）走 D89 锚点回填节点 ID。
			// 空 Outcome 不打标记（D107 补：旧版 Revise 产的修订节点零值——标记会渲染成 `[]`）。
			if msg.Outcome != "" && msg.Outcome != conversation.OutcomeDone {
				m.addMsg(blockAssistant, fmt.Sprintf("[%s]", msg.Outcome), msg.ID)
			} else {
				m.stampAssistantAnchor(msg, start)
			}
			return
		}
		// flushed 草稿段就地回填（FIFO；权威文本以节点为准），并盖段序（D107④）。
		consumed := 0
		// D136：末个 flushed 段的索引在清空前捕获——「Turn 以工具收场（有助手正文、无
		// 追加余段）」时 stamped 恒 false，而下面那条回填分支原来判的是**已清空的**
		// flushedDrafts ⇒ 恒假 ⇒ 该 Turn 的挂点丢失、分叉条不出现（外部审查 2026-10-13）。
		lastFlushed := -1
		if n := len(m.flushedDrafts); n > 0 {
			lastFlushed = m.flushedDrafts[n-1]
		}
		for _, idx := range m.flushedDrafts {
			if consumed >= len(segs) {
				break
			}
			m.blocks[idx].text = segs[consumed].text
			m.blocks[idx].imgs = segs[consumed].imgs // D207：段内图片随段调和
			m.blocks[idx].part = segs[consumed].start
			consumed++
		}
		m.flushedDrafts = m.flushedDrafts[:0]
		// 余段（收场轮的正文，通常仍在草稿）追加渲染；最后一段盖节点 ID
		// ——分叉条（D81）每 Turn 一个挂点，挂在回答气泡下。
		stamped := false
		for i := consumed; i < len(segs); i++ {
			blk := block{kind: blockAssistant, text: segs[i].text, part: segs[i].start, imgs: segs[i].imgs}
			if msg.Outcome != "" && msg.Outcome != conversation.OutcomeDone && i == len(segs)-1 {
				mark := fmt.Sprintf("[%s]", msg.Outcome)
				if strings.TrimSpace(blk.text) == "" {
					blk.text = mark
				} else {
					blk.text = mark + "\n" + blk.text
				}
			}
			if i == len(segs)-1 {
				blk.id = msg.ID
				stamped = true
			}
			m.blocks = append(m.blocks, blk)
		}
		// Turn 以工具收场（正文段全部经工具边界落块）：末个已落块盖 ID。
		// D136：索引取**清空前捕获**的 lastFlushed——原实现判的是上面 [:0] 清空后的
		// flushedDrafts，恒假 ⇒ 这类 Turn 一个挂点都没有、分叉条不出现（外部审查 2026-10-13）。
		if !stamped && lastFlushed >= 0 && lastFlushed < len(m.blocks) {
			m.blocks[lastFlushed].id = msg.ID
		}
		m.flushedDrafts = m.flushedDrafts[:0]
	case conversation.RoleSystem:
		m.resetDraft()
		if strings.TrimSpace(text) != "" {
			m.add(blockSystem, text)
		}
	case conversation.RoleInject:
		// 注入节点（D166）：独立成档，不落 user 盖章、不改标题、不挂分叉条——
		// 头部由渲染期按 kind 生成（来源 + 触发状态），正文原样可选。
		m.resetDraft()
		m.addInject(text, msg.Source, msg.Trigger)
	default:
		// user 无独立事件面：输入行已在 submit 时回显入块——但回显时节点尚不存在、
		// 块无 ID（D87），此处按「最早未盖章且文本一致」回填节点 ID，分叉条（D81）
		// 在实时会话才能出现；工具结果随 assistant 节点分片呈现（D95）。
		// D207：纯图提交（无正文）也要盖章——回显块的正文同样为空，按空串配对。
		if msg.Role == conversation.RoleUser {
			if imgs := nodeImages(msg.Content); text != "" || len(imgs) > 0 {
				m.stampUserBlock(text, msg.ID, imgs)
			}
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

// nodeSeg 节点正文的一段（D95 Turn 粒度 + D207 图片）：text = 段内可读文本（§4.2 占位、
// §9 出口消毒），imgs = 段内图片（按出现序），start = 段内首个 PartText 的 Content 序号。
type nodeSeg struct {
	text  string
	imgs  []imgRef
	start int
}

// nodeSegments 按工具分片把节点正文切段（D95）：段 = tool 分片之间的连续
// text/image/audio/doc 分片；thinking 不进正文（回放单独成块，D42）。
//
// 判空口径（D207 修订）：**有图且无字也要成段**——纯图消息原来会被 TrimSpace 判空整段
// 丢掉，图就再也不出现。段序与 textSegments 原口径一致（仅工具边界断段）。
func nodeSegments(parts []conversation.Part) []nodeSeg {
	var (
		segs  []nodeSeg
		b     strings.Builder
		imgs  []imgRef
		ord   = -1
		start = -1
	)
	flush := func() {
		text := sanitizeControl(b.String())
		if strings.TrimSpace(text) != "" || len(imgs) > 0 {
			segs = append(segs, nodeSeg{text: text, imgs: imgs, start: start})
		}
		b.Reset()
		imgs = nil
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
		if p.Kind == conversation.PartImage {
			if r, ok := imageRefOf(p); ok {
				imgs = append(imgs, r)
				continue // 图片由真图承载，正文里不再留文字占位
			}
			// 缺 BlobRef 的分片没有可寻址的图：回落文字占位（见 appendPartText）。
		}
		appendPartText(&b, p)
	}
	flush()
	return segs
}

// imageRefOf 附件分片 → 转写区图片引用（D207）：BlobRef 齐备才算（Hash 是缓存键，
// 缺了没法定址）；缺引用的分片退回文字占位，不至于凭空消失。
func imageRefOf(p conversation.Part) (imgRef, bool) {
	if p.Ref == nil || p.Ref.Hash == "" {
		return imgRef{}, false
	}
	return imgRef{blob: p.Ref, name: p.Ref.Name}, true
}

// nodeImages 节点正文里的全部图片（按出现序）：用户消息整条归一在一个气泡里显示，
// 不需要按工具边界分段。
func nodeImages(parts []conversation.Part) []imgRef {
	var out []imgRef
	for _, p := range parts {
		if p.Kind != conversation.PartImage {
			continue
		}
		if r, ok := imageRefOf(p); ok {
			out = append(out, r)
		}
	}
	return out
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
//
// D207：顺手把图片换成节点里的权威引用（内容屏蔽路径项——暂存时按本地路径解的缩略图，
// 换成入库后的哈希，文件此后被移走/改名也不影响显示）。纯图消息的 want 为空串，
// 与「只有图片没有正文」的回显块配对，同样能盖上 ID（分叉条才有挂点）。
func (m *model) stampUserBlock(text string, id conversation.MessageID, imgs []imgRef) {
	want := sanitizeControl(text)
	for i := range m.blocks {
		b := &m.blocks[i]
		if b.kind != blockUser || b.id != "" || sanitizeControl(b.text) != want {
			continue
		}
		b.id = id
		b.part = 0 // D107④：user 节点单正文分片（标题/随文合并后恒一段）
		if len(imgs) > 0 {
			b.imgs = imgs
		}
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
		// D207：纯图消息（有附件无文字）也要成块——否则图整条消失。
		imgs := nodeImages(msg.Content)
		if strings.TrimSpace(text) != "" || len(imgs) > 0 {
			m.blocks = append(m.blocks, block{
				kind: blockUser, text: text, id: msg.ID, part: 0, imgs: imgs,
			})
		}
	case conversation.RoleAssistant:
		start := len(m.blocks)
		var seg strings.Builder
		var segImgs []imgRef
		// D107④：段序盖章——segStart = 本段首个 PartText 在 Content 中的序号（0 起），
		// 编辑回传 --part 用；flushSeg 产出块即盖。
		textOrd, segStart := -1, -1
		flushSeg := func() {
			s := sanitizeControl(seg.String())
			if strings.TrimSpace(s) != "" || len(segImgs) > 0 {
				// 段先不带 ID：末段统一盖。**不用 addMsg**——它按空文本丢弃，
				// 纯图段（有图无字）会整段消失（D207）。
				m.blocks = append(m.blocks, block{kind: blockAssistant, text: s, part: segStart, imgs: segImgs})
			}
			seg.Reset()
			segImgs = nil
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
				if p.Kind == conversation.PartImage {
					if r, ok := imageRefOf(p); ok {
						segImgs = append(segImgs, r)
						continue
					}
				}
				appendPartText(&seg, p)
			}
		}
		flushSeg()
		// 非 done 终态：标记行并入最后一个正文气泡（与实时 commit 同口径）；
		// 无正文段（空内容的取消/错误）单独给标记块。
		// 空 Outcome 不打标记（D107 补：旧版 Revise 产的修订节点零值——标记会渲染成 `[]`）。
		if msg.Outcome != "" && msg.Outcome != conversation.OutcomeDone {
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
	case conversation.RoleInject:
		m.addInject(text, msg.Source, msg.Trigger) // D166：暗色卡片，不参与 user 盖章/分叉
	default: // root 等不该出现在回放区间
	}
}

// submit 提交一行：确认态优先应答，提问态优先作答，编辑态走结构化 /edit，空行忽略，
// 其余先投递再入转写。
// 投递失败（缓冲满）时明确标注"未执行"——不制造"已执行"错觉（uitui 审查修复同款）。
func (m *model) submit(text string) {
	if m.ask != nil {
		// D184：提问面板持键盘时，键入行不参与会话——按面板语义解释（编号 / 文字答案）。
		m.submitAsk(text)
		return
	}
	if m.confirm != nil {
		m.replyConfirm(port.ConfirmAnswer{Allow: isYes(text)})
		return
	}
	line := strings.TrimSpace(text)
	// D186 暂存随文发：有暂存附件 → Raw 提交（文本可空 = 仅附件，暂存清空）；编辑态不
	// 走这条（D188：编辑态的新增附件要随 /edit 走 Command.Attachments）。
	if m.editTarget == "" && len(m.stagedFiles) > 0 {
		m.submitRaw(m.stagedFiles, line)
		return
	}
	// D188：编辑态有附件操作时文本可空（只改附件）——空提交不再等于取消。
	if line == "" && !m.editHasAttachOp() {
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
		// D188：删掉的已有附件 → --drop N（序号口径 = 节点内附件分片出现序，升序稳定）；
		// 新增附件一律走 Command.Attachments——**顺序即 chip 排顺序**（含路径项：路径也
		// 走结构化载荷，混合贴图与选文件时才不会因为"两条通道"把次序打乱；`--attach`
		// 仍是纯文本界面（repl/TUI）的入口）。
		for _, idx := range m.editDroppedIndexes() {
			args = append(args, "--drop", strconv.Itoa(idx))
		}
		added := make([]port.Attachment, 0, len(m.stagedFiles))
		for _, a := range m.stagedFiles {
			added = append(added, a.attachment())
		}
		if line != "" {
			args = append(args, line) // 空 = 只改附件（内核据此保留原正文）
		}
		cmd := port.Command{Name: "edit", Args: args, Attachments: added}
		staged := m.stagedFiles
		// 无论投递成败，编辑态都已收尾（失败只提示，不把用户留在半截状态里）。
		m.editTarget = ""
		m.editMode = conversation.Fresh
		m.editPart = -1
		m.editAtts = nil
		m.editDropped = nil
		m.stagedFiles = nil
		select {
		case m.u.inCh <- port.UserInput{Command: &cmd}:
		default:
			// 缓冲满：暂存的附件还回去（挑文件比打字贵），编辑态照常收尾——同 submitRaw
			// 「失败不吞附件」的口径。
			m.stagedFiles = staged
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

// submitRaw 投递 Raw 摄取输入（D186 多附件）：附件列表 + 已输入文本一并进摄取管线；
// 用户节点无 CommittedEvent（Agent 只广播 Turn/system 节点）→ 本地回显 blockUser
// （文本 + 附件名示意——与回放的摄取分片呈现存在口径差，D104③ 后果①）。
// 缓冲满：恢复暂存 + notice（编辑框已清、文本丢失，D104 后果③）。
func (m *model) submitRaw(atts []stagedAtt, text string) {
	if len(atts) == 0 {
		return
	}
	raw := port.RawInput{Kind: "file", Text: text}
	for _, a := range atts {
		raw.Attachments = append(raw.Attachments, a.attachment())
	}
	select {
	case m.u.inCh <- port.UserInput{Raw: &raw}:
		m.stagedFiles = nil // 提交即清暂存（D186）
	default:
		m.add(blockNotice, "[notice] 输入缓冲已满，附件未发送")
		return
	}
	echo := text
	if echo != "" {
		echo += " "
	}
	// D207：图片附件不再以文字占位回显——它们以缩略图入块（imgs），回显文本只列
	// 非图片附件（文档等仍需要名字，否则提交后到回放前看不出附了什么）。
	names := make([]string, 0, len(atts))
	var imgs []imgRef
	for _, a := range atts {
		if r, ok := imgRefOfAtt(a); ok {
			imgs = append(imgs, r)
			continue
		}
		names = append(names, a.displayName())
	}
	if len(names) == 1 {
		echo += "〔附件：" + names[0] + "〕"
	} else if len(names) > 1 {
		echo += fmt.Sprintf("〔附件 %d 个：%s〕", len(names), strings.Join(names, "、"))
	}
	m.blocks = append(m.blocks, block{kind: blockUser, text: strings.TrimSpace(echo), id: "", part: -1, imgs: imgs})
}

// stageAttach 暂存一项附件（D186）：已在列表里则不重复添加（同一文件附两遍只会白占
// 上下文）；超过单条上限后拒收并提示——报错文案由端口常量生成。
func (m *model) stageAttach(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	m.stageAtt(stagedAtt{path: path})
}

// stageBlob 暂存一项已入库附件（D187 粘贴图片，字节已在库里）。
func (m *model) stageBlob(ref conversation.BlobRef) {
	m.stageAtt(stagedAtt{blob: &ref})
}

// stageAtt 暂存展开的公共段：去重 + 上限。
func (m *model) stageAtt(att stagedAtt) {
	key := att.key()
	for _, a := range m.stagedFiles {
		if a.key() == key {
			return
		}
	}
	if len(m.stagedFiles) >= port.MaxAttachments {
		m.add(blockNotice, fmt.Sprintf("[notice] 一条消息最多 %d 个附件，已忽略 %s",
			port.MaxAttachments, att.displayName()))
		return
	}
	m.stagedFiles = append(m.stagedFiles, att)
}

// unstageAttach 从暂存列表移除第 i 项（chip 点击，D186）。
func (m *model) unstageAttach(i int) {
	if i < 0 || i >= len(m.stagedFiles) {
		return
	}
	m.stagedFiles = append(m.stagedFiles[:i], m.stagedFiles[i+1:]...)
}

// clearAttach 清空暂存（收起态汇总 chip 点击）。D188：编辑态顺带把已有附件全部标记删除
// ——收起态那个汇总 chip 的语义是「全部清掉」，编辑态下保持同一手感（Esc 可整体退出，
// 再点「+」逐项恢复）。
func (m *model) clearAttach() {
	m.stagedFiles = nil
	if m.editTarget != "" {
		m.dropAllEditAtts()
	}
}

// toggleEditAtt 编辑态已有附件的「删除/恢复」切换（D188）：标记只作用于本次修订，不动
// 附件库、也不改旧节点。
func (m *model) toggleEditAtt(index int) {
	if m.editDropped == nil {
		m.editDropped = map[int]bool{}
	}
	if m.editDropped[index] {
		delete(m.editDropped, index)
		return
	}
	m.editDropped[index] = true
}

// dropAllEditAtts 标记删除编辑态的全部已有附件（D188）。
func (m *model) dropAllEditAtts() {
	if len(m.editAtts) == 0 {
		return
	}
	if m.editDropped == nil {
		m.editDropped = map[int]bool{}
	}
	for _, att := range m.editAtts {
		m.editDropped[att.Index] = true
	}
}

// editDroppedIndexes 已标记删除的附件序号，升序（Args token 顺序稳定，便于测试断言）。
func (m *model) editDroppedIndexes() []int {
	out := make([]int, 0, len(m.editDropped))
	for idx := range m.editDropped {
		out = append(out, idx)
	}
	sort.Ints(out)
	return out
}

// editHasAttachOp 编辑态是否带附件操作（D188：有操作时文本可空 = 只改附件）。
func (m *model) editHasAttachOp() bool {
	return len(m.editDropped) > 0 || len(m.stagedFiles) > 0
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
		c.images = append(c.images, r.Images...) // D207：工具产出的图进 chip 展开体
		return
	}
	m.blocks = append(m.blocks, block{kind: blockTool, chip: &toolChip{
		done:   true,
		ok:     r.OK,
		result: sanitizeControl(resultText(r)),
		images: append([]tool.ImageRef(nil), r.Images...),
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
	// D157：发布跨窗投影（历史窗据此画同步确认弹窗；窗口未开则只存快照）。
	m.confirm.share = &confirmShare{prompt: m.confirm.prompt}
	m.u.publishConfirm(m.confirm.share)
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
	m.u.publishConfirm(nil) // D157：收尾即撤下跨窗投影（历史窗下一帧收起弹窗）
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

// addInject 追加注入块（D166）：来源/触发只作头部展示（D150 的落地事实）。
// 空正文的注入不落块——没有可读内容，头部单独成行只会制造噪声。
func (m *model) addInject(text, source string, trigger bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	m.blocks = append(m.blocks, block{
		kind: blockInject, text: text, source: source, trigger: trigger, part: -1,
	})
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
		// D207：图片由真缩略图承载（块里的 imgs），正文不再留文字占位——否则
		// 「图 + 名字」两行并排，纯图消息还会因为占位而变成一条假正文。
		// 没有可寻址引用的分片留在正文里当占位，至少不至于凭空消失。
		if p.Ref == nil || p.Ref.Hash == "" {
			b.WriteString("〔图片〕")
		}
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
