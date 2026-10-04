package conversation

import (
	"encoding/json"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// PartKind 内容分片种类。
type PartKind string

const (
	PartText     PartKind = "text"
	PartImage    PartKind = "image"
	PartAudio    PartKind = "audio"
	PartDoc      PartKind = "doc"
	PartThinking PartKind = "thinking" // 思考过程（D42）：仅 assistant 节点可携带
	PartTool     PartKind = "tool"     // 工具调用+结果同片（D95）：仅 assistant 节点可携带
)

// ApprovalState 工具确认态（D95 预留字段，M5 工具确认接线；"" = 未进入确认流程）。
type ApprovalState string

const (
	ApprovalNone     ApprovalState = ""
	ApprovalPending  ApprovalState = "pending"
	ApprovalApproved ApprovalState = "approved"
	ApprovalDenied   ApprovalState = "denied"
)

// BlobRef 附件引用（sha256 内容寻址），由 AttachmentStore 解析（DESIGN §4.2 / D17）。
type BlobRef struct {
	Hash string `json:"hash"`
	MIME string `json:"mime"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ToolPart 工具分片载荷（D95）：调用与结果同片——跨节点引用不复存在。
// Result=nil 表示未执行/被取消（正常路径在提交前填齐失败结果，树恒可装配）。
type ToolPart struct {
	CallID   string          `json:"call_id"` // 节点内唯一（不再全树唯一，D95）
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args,omitempty"` // 与 tool.Call.Args 同口径：落盘为 JSON 对象而非 base64
	Result   *tool.Result    `json:"result,omitempty"`
	Approval ApprovalState   `json:"approval,omitempty"` // 预留：M5 工具确认
}

// Part 消息内容的多态片段。
type Part struct {
	Kind       PartKind  `json:"kind"`
	Text       string    `json:"text,omitempty"`       // Kind=text；Kind=doc 为提取文本（截断）；Kind=thinking 为思考文本（D42）
	Ref        *BlobRef  `json:"ref,omitempty"`        // Kind=image|audio|doc：附件引用
	Transcript string    `json:"transcript,omitempty"` // Kind=audio：ASR 转写文本（模型只见文本）
	Tool       *ToolPart `json:"tool,omitempty"`       // Kind=tool：调用与结果同片（D95）
}

// Role 消息角色（D95：RoleTool 删除——工具调用/结果内嵌为 assistant 的 PartTool 分片）。
type Role string

const (
	// RoleRoot 会话唯一根：实节点空消息，ID = 会话 ID（D19，Root 即会话）。
	RoleRoot Role = "root"
	RoleUser Role = "user"
	// RoleAssistant 助手消息，可携带思考分片（D42）与工具分片（D95）。
	RoleAssistant Role = "assistant"
	// RoleSystem 系统节点：会话首节点 persona（D20）与上下文压缩摘要（D21）。
	RoleSystem Role = "system"
)

// Outcome 节点终态，提交时确定（DESIGN §4.1）。
type Outcome string

const (
	OutcomeDone      Outcome = "done"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeError     Outcome = "error"
)

// Usage 一次生成的用量与成本；port 的流式 Delta 直接引用本类型（D17）。
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// Message 树节点：创建后内容只读，值语义。
//
// 结构边指针 Parent 可随 Revise Carry 边转移改写（DESIGN §4.1 / D16）；
// 其余字段（Content/Outcome/Model/Usage/CreatedAt）一经入树永不改写。
// 角色约束：user/system 纯内容节点；assistant 可携带思考分片（D42）与工具分片（D95）。
type Message struct {
	ID        MessageID `json:"id"`
	Parent    MessageID `json:"parent"` // "" = Root 自身；其余节点恒非空（D19）
	Role      Role      `json:"role"`
	Content   []Part    `json:"content,omitempty"`
	Outcome   Outcome   `json:"outcome,omitempty"`
	Model     string    `json:"model,omitempty"`
	Usage     Usage     `json:"usage"`
	CreatedAt time.Time `json:"created_at"`
}

// Clone 返回 m 的深拷贝（含切片与指针），用于入树防御拷贝与快照比对。
func (m Message) Clone() Message {
	out := m
	out.Content = cloneParts(m.Content)
	return out
}

func cloneParts(in []Part) []Part {
	if in == nil {
		return nil
	}
	out := make([]Part, len(in))
	copy(out, in)
	for i := range out {
		if in[i].Ref != nil {
			ref := *in[i].Ref
			out[i].Ref = &ref
		}
		if in[i].Tool != nil {
			out[i].Tool = cloneToolPart(in[i].Tool)
		}
	}
	return out
}

func cloneToolPart(in *ToolPart) *ToolPart {
	out := *in
	if len(in.Args) > 0 {
		args := make(json.RawMessage, len(in.Args))
		copy(args, in.Args)
		out.Args = args
	}
	if in.Result != nil {
		res := *in.Result
		out.Result = &res
	}
	return &out
}
