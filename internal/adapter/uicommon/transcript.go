package uicommon

// 转写状态机（TUI/GUI 共用）：消费 port 的 UI 事件流，维护流式正文草稿、思维链草稿、
// 待调和的草稿段清单与用量累计，按 D95 规则切段回填。块结构与渲染不在共用层——前端经
// Sink 钩子把各出口接到自己的块列表与样式上；GUI 专有的呈现形态（分叉行、折叠条）由
// 前端在自己的块层扩展，不挤进共用枚举。

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// BlockKind 转写块种类（两前端共用的基枚举）。前端专有的块种另立区间扩展
// （uigui 的分叉行/折叠条等从 100 起），不在共用层为呈现差异加种类。
type BlockKind int

const (
	BlockPlain     BlockKind = iota // Say/命令输出/通用行
	BlockUser                       // 提交的输入
	BlockAssistant                  // committed 助手文本
	BlockTool                       // 工具调用/结果
	BlockNotice                     // notice 提示（attention 级）
	BlockError                      // 错误
	BlockSystem                     // system 节点（/compact 摘要等）
	BlockThinking                   // 思维链（D34 展示；节点侧 PartThinking 入树，D42）
	BlockInject                     // 注入节点（D166）
)

// Transcript 转写语义状态机：只管事件语义与 D95 调和规则，不管渲染。前端把本结构体
// 内嵌进自己的 model，View 直读草稿字段做实时呈现（流式增量只流经 UI，不进领域）。
// 仅事件循环 goroutine 读写。
type Transcript struct {
	Draft    strings.Builder    // 流式草稿（committed 后以消息文本定稿替换）
	Drafting bool               // 草稿是否在流式中（区分空草稿与流过又清空的草稿）
	Think    strings.Builder    // 进行中的思维链（D34：实时可见，落块只经 FlushThink 出口）
	Usage    conversation.Usage // 前端累计值（/usage 走节点汇总不受影响）
	// summaryNoticed 本段摘要流是否已发过「正在生成摘要」标注（每次流开始只发一条；
	// 任何非摘要增量把它复位，/compact 重试的第二段会再发一次）。
	summaryNoticed bool
	// Flushed 待调和的草稿正文段块序（D95 Turn 粒度）：工具事件先于 CommittedEvent
	// 到达——工具边界把流式草稿落为正文块（正文先于工具行/chip，第 2 轮文本重新
	// 流式）；提交（每 Turn 一次）时就地回填权威文本。元素是前端落块时回报的块句柄。
	Flushed []int
}

// ThinkDelta 追加一段思维链增量（消毒后入草稿）。
func (t *Transcript) ThinkDelta(text string) {
	t.Think.WriteString(SanitizeControl(text, KeepLayout))
}

// TakeThink 取走整段进行中的思维链（flushThink 时序出口；空返回 ""）。
func (t *Transcript) TakeThink() string {
	if t.Think.Len() == 0 {
		return ""
	}
	text := t.Think.String()
	t.Think.Reset()
	return text
}

// DraftDelta 追加一段正文流式增量（消毒后入草稿；模型流为不可信输入，§9）。
func (t *Transcript) DraftDelta(text string) {
	t.Drafting = true
	t.Draft.WriteString(SanitizeControl(text, KeepLayout))
}

// ResetDraft 清流式草稿。
func (t *Transcript) ResetDraft() {
	t.Draft.Reset()
	t.Drafting = false
}

// TakeDraftSegment 工具边界取段（D95 Turn 粒度）：草稿非空时取走整段原始文本；
// 空草稿或整段只有空白时返回 ""（不落块，草稿照样清空）。
func (t *Transcript) TakeDraftSegment() string {
	if !t.Drafting && t.Draft.Len() == 0 {
		return ""
	}
	text := t.Draft.String()
	t.ResetDraft()
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

// Reset 转写重开（D75：/switch、/new）：丢弃草稿、思维链与待调和段，用量归零。
// 前端的块列表、滚动与折叠态由 Clear 出口自理。
func (t *Transcript) Reset() {
	t.summaryNoticed = false
	t.ResetDraft()
	t.Think.Reset()
	t.Flushed = t.Flushed[:0]
	t.Usage = conversation.Usage{}
}

// Segment 节点正文的一段（D95 Turn 粒度）：段 = tool 分片之间的连续 text/image/
// audio/doc 分片。Start 是段内首个 PartText 在 Content 中的序号（0 起，编辑回传
// --part 用；无文本分片的段为 -1）。Image 是段内前端图片引用的按序收列（每个元素
// 是 SegOpts.ImageOf 的一次产出；行式界面不收图，恒 nil）。
type Segment struct {
	Text  string
	Start int
	Image any
}

// SegOpts 切段参数：图片怎么呈现由前端声明。
type SegOpts struct {
	// Mode 图片分片的文字占位策略（ImageNamed / ImageWhenUnaddressable）。
	Mode ImageMode
	// ImageOf 附件分片 → 前端图片引用（D207）；返回 nil 的分片回落文字占位。
	// nil = 不收图（行式界面），图片一律走占位文本。
	ImageOf func(p conversation.Part) any
}

// Segments 按工具分片把节点正文切段（D95）：thinking 不进正文（回放单独成块，D42），
// tool 单独成 chip/工具行。出口统一消毒（§9）。判空：有图且无字也成段（D207 修订
// ——纯图消息原来会被 TrimSpace 判空整段丢掉）。
func Segments(parts []conversation.Part, o SegOpts) []Segment {
	var segs []Segment
	var b strings.Builder
	var imgs []any
	ord, start := -1, -1
	flush := func() {
		text := SanitizeControl(b.String(), KeepLayout)
		if strings.TrimSpace(text) != "" || len(imgs) > 0 {
			var image any
			if len(imgs) > 0 {
				image = imgs
			}
			segs = append(segs, Segment{Text: text, Start: start, Image: image})
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
		if p.Kind == conversation.PartImage && o.ImageOf != nil {
			if r := o.ImageOf(p); r != nil {
				imgs = append(imgs, r)
				continue // 图片由真图承载，正文里不再留文字占位
			}
			// 缺 BlobRef 的分片没有可寻址的图：回落文字占位（见 AppendPartText）。
		}
		AppendPartText(&b, p, o.Mode)
	}
	flush()
	return segs
}

// ReconcileDrafts 提交调和的段配对（D95 Turn 粒度）：按节点权威内容切段，与工具边界
// 已落块的草稿段 FIFO 配对——前 n 段回填已落块（n = min(草稿段数, 段数)），余段尚未
// 落块、由前端追加渲染。返回 handles（回填目标的块句柄，与 backfill 逐位对应）、
// backfill（回填段）、extra（待追加段）；待调和清单随即清空。
func (t *Transcript) ReconcileDrafts(parts []conversation.Part, o SegOpts) (handles []int, backfill []Segment, extra []Segment) {
	segs := Segments(parts, o)
	n := len(t.Flushed)
	if n > len(segs) {
		n = len(segs)
	}
	handles = append(handles, t.Flushed[:n]...)
	backfill = append(backfill, segs[:n]...)
	extra = append(extra, segs[n:]...)
	t.Flushed = t.Flushed[:0]
	return handles, backfill, extra
}

// Sink 共用分流的前端出口：每个钩子接前端自己的块列表与样式，留 nil = 该出口由前端
// 自行忽略。钩子收到的文本均已消毒（§9）。
type Sink struct {
	// OnThink 思维链增量已入草稿（first = 本轮首个增量，前端可借此记起止时刻）。
	OnThink func(first bool)
	// OnDraft 正文增量已入草稿。
	OnDraft func()
	// FlushThink 思维链阶段结束：落为暗块/思考块（D34）。
	FlushThink func(text string)
	// FlushSegment 工具边界把流式草稿落为正文段（D95）：raw 为 "" 时不落；落块后把
	// 块句柄记入 Transcript.Flushed（共用状态机经 TakeDraftSegment 已清草稿）。
	FlushSegment func(raw string)
	// ServerRead 服务端读链接（D310）：正文不进客户端，用户只能看到「读了哪一页」。
	ServerRead func(text string)
	// ToolCall / ToolResult 工具调用与结果（TUI 落工具行，GUI 并入 chip，D67）。
	ToolCall   func(call tool.Call)
	ToolResult func(res tool.Result)
	// Committed / History 提交节点与历史回放节点（渲染在前端：盖章/回填规则各异）。
	Committed func(msg conversation.Message)
	History   func(msg conversation.Message)
	// Error 错误行（已消毒）。
	Error func(text string)
	// Notice 提示（D267）：attention = 改变了本轮上下文预期，必须立刻看得见、留在
	// 转写里；false = 操作回执/状态，前端自行收纳（GUI 进折叠条，TUI 落普通行）。
	Notice func(text string, attention bool)
	// Clear 清屏（D75）：共用状态已 Reset，前端的块列表与界面态在此自理。
	Clear func()
	// Unhandled 未识别事件的兜底呈现（前端各自决定展示或忽略）。
	Unhandled func(ev port.Event)
}

// Dispatch Turn 事件 → 转写语义状态（与 repl.Emit 同呈现语义）。通用事件在共用状态
// 机上消化，前端经 Sink 出口渲染。思维链阶段结束先于一切后续出口（flushThink 时序，
// 两前端同规则）。
func (t *Transcript) Dispatch(ev port.Event, s *Sink) {
	if d, ok := ev.(port.DeltaEvent); ok && d.Delta.Reasoning {
		// 思维链（D34 展示）：积累进 think 草稿实时可见；提交时节点已带
		// PartThinking（D42），故落块只由 FlushThink 出口负责，commit 不重复渲染。
		first := t.Think.Len() == 0
		t.ThinkDelta(d.Delta.Text)
		if s.OnThink != nil {
			s.OnThink(first)
		}
		return
	}
	t.flushThink(s) // 思维链阶段结束：落为暗块，后续正文/工具/提交照常
	switch e := ev.(type) {
	case port.DeltaEvent:
		if e.Summary && e.Delta.Text != "" {
			// 摘要生成流（/compact 与 /summary 共用）：首条增量发标注，用户看得见
			// 这段流不是回答。摘要落盘是 system 节点，提交后草稿被权威文本替换。
			if !t.summaryNoticed {
				t.summaryNoticed = true
				if s.Notice != nil {
					s.Notice(i18n.T("compact.generating"), true)
				}
			}
		} else {
			t.summaryNoticed = false
		}
		if e.Delta.Server != nil {
			// 服务端读链接（D310）：正文不进客户端，用户只能看到「读了哪一页」。
			t.flushSegment(s)
			if s.ServerRead != nil {
				s.ServerRead(i18n.Tf("ui.notice.server_read", "url", e.Delta.Server.URL))
			}
			return
		}
		if e.Delta.Text == "" {
			return // 工具调用分片、用量分片不进转写
		}
		t.DraftDelta(e.Delta.Text)
		if s.OnDraft != nil {
			s.OnDraft()
		}
	case port.ToolCallEvent:
		t.flushSegment(s) // D95：工具边界落正文段（正文先于工具行/chip，与重构前一致）
		if s.ToolCall != nil {
			s.ToolCall(e.Call)
		}
	case port.ToolResultEvent:
		t.flushSegment(s)
		if s.ToolResult != nil {
			s.ToolResult(e.Result)
		}
	case port.CommittedEvent:
		if s.Committed != nil {
			s.Committed(e.Message)
		}
	case port.HistoryEvent:
		if s.History != nil {
			s.History(e.Message) // 启动历史回放（D40/§7.4）：按节点角色定稿渲染
		}
	case port.ErrorEvent:
		if s.Error != nil {
			s.Error(SanitizeControl(e.Err.Error(), KeepLayout)) // 服务端错误片段可携带注入序列
		}
	case port.NoticeEvent:
		if s.Notice != nil {
			s.Notice(SanitizeControl(e.Text, KeepLayout), e.Attention)
		}
	case port.ClearEvent:
		t.Reset() // 转写重开（D75：/switch、/new）
		if s.Clear != nil {
			s.Clear()
		}
	case port.TextViewEvent:
		// 上下文全文只投给有次窗的前端（§7.1）。行式界面没有次窗，而这份内容已经写进
		// 导出目录了，读文件即可——这里刻意什么都不做。
	default:
		if s.Unhandled != nil {
			s.Unhandled(ev)
		}
	}
}

// flushThink 把进行中的思维链经 FlushThink 出口落块（草稿取走即清，不残留）。
func (t *Transcript) flushThink(s *Sink) {
	if t.Think.Len() == 0 {
		return
	}
	text := t.TakeThink()
	if s.FlushThink != nil {
		s.FlushThink(text)
	}
}

// flushSegment 把流式草稿经 FlushSegment 出口落为正文段（D95）；前端落块后自行把
// 块句柄记入 Flushed。
func (t *Transcript) flushSegment(s *Sink) {
	if s.FlushSegment == nil {
		return
	}
	s.FlushSegment(t.TakeDraftSegment())
}
