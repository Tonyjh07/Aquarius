package uigui

// 本文件只留转写模型的状态与入口（块与 chip 类型、model 结构、事件分流、清屏）；
// 提交与回放见 model_commit.go，发送与附件见 model_submit.go，工具与确认见 model_tool.go。

import (
	"fmt"
	"strings"
	"time"

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
