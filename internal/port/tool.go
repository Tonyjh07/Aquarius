package port

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// Tool 单个工具的执行契约；内置工具与三方插件同权接入（D13）。
type Tool interface {
	Spec() tool.Spec
	Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

// ToolSet 会话感知的工具源（D340）：清单可以随会话与插件状态逐轮变化，所以不在注册表
// 里物化成固定条目——装配时向源要声明，执行时向源找工具。
//
// 与 Tool 的区别是「名字的时效」：Tool 注册一次、名字长期有效；ToolSet 的名字只在
// 该会话的当前可见集合内有效，下一轮可能消失（模型拿到的工具面本来就按轮重建）。
type ToolSet interface {
	// Name 源的名字（插件名）：只用于报因与日志归组，不占用工具名空间。
	Name() string
	// Specs 本轮可见的工具声明（按 ctx 里的会话解析）。空切片是正常状态（空库、
	// 游标停在根）；返回错误会让本轮装配失败——插件不要用它表达「暂时不可用」。
	Specs(ctx context.Context) ([]tool.Spec, error)
	// Lookup 按名找本源的工具；不属于本源返回 (nil, nil)。名字须与同一轮 Specs 的
	// 返回一致，否则模型点开的东西和它看到的名字对不上。
	Lookup(ctx context.Context, name string) (Tool, error)
}

// MaxCallTimeoutSec 单次调用 timeout_sec 保留参数的硬顶（D38）：
// 模型可逐次覆盖 limits.tool_timeout_sec（允许高于 config），但不超过此上限。
const MaxCallTimeoutSec = 3600

// RunCodeTool 程序化工具调用的保留传输名（D319，实验性）：tools.mode=ptc 时模型面
// 只发这一个声明；toolrun 对它豁免 disabled 清单，starlarkrun 实现它。
const RunCodeTool = "run_code"

// ConfirmScope 一次脚本执行内的确认去重账本（D320）：同一次脚本里同参数（指纹）只问
// 一遍，distinct 确认数超过上限后需要确认的调用按超限回值。native 路径不携带 scope，
// 行为不变。实现方在 starlarkrun，toolrun 只读接口。
type ConfirmScope interface {
	// Recall 查重：命中返回上次结论（allow 与拒绝原因），未命中同时计入 distinct
	// 询问数（一个指纹只计一次，无论后续是否超限）。
	Recall(fingerprint string) (seen bool, allow bool, reason string)
	// Remember 记录一次确认的结论（放行或拒绝及其原因）。
	Remember(fingerprint string, allow bool, reason string)
	// AskAllowed 上限判定：已计数的 distinct 询问是否还在允许范围内（第 N 次询问
	// 放行、之后的 distinct 请求被上限拦下——上限语义在 scope，执行面只问这一句）。
	// 返回当前上限值供报因带数字。
	AskAllowed() (ok bool, limit int)
}

// ErrConfirmLimit 确认上限触达的报因前缀（D320）：toolrun 生成、starlarkrun 的
// 脚本摘要按它统计 capped，不用裸字符串两头对。
const ErrConfirmLimit = "confirmation limit reached"

// confirmScopeKey ConfirmScope 的 ctx 键（私有类型防碰撞）。
type confirmScopeKey struct{}

// WithConfirmScope 把去重账本挂进 ctx（starlarkrun 的脚本内调用专用）。
func WithConfirmScope(ctx context.Context, s ConfirmScope) context.Context {
	return context.WithValue(ctx, confirmScopeKey{}, s)
}

// ConfirmScopeFrom 取 ctx 里的去重账本；native 路径（无人注入）返回 nil。
func ConfirmScopeFrom(ctx context.Context) ConfirmScope {
	s, _ := ctx.Value(confirmScopeKey{}).(ConfirmScope)
	return s
}

// ToolRunner 工具执行门面：查找 + capability 校验 + Risk 确认 + 超时 + 结果裁剪。
type ToolRunner interface {
	Specs(ctx context.Context) ([]tool.Spec, error)
	Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

// ConfirmAnswer 确认应答（D86）：拒绝可带原因回传给模型（用户自填文本，原样
// 附上不翻译）；Allow=true 时 Reason 无意义，实现方应留空。
type ConfirmAnswer struct {
	Allow  bool
	Reason string
	// Level 用户在确认框里直选的权限档（D282）：非空 = 按这一档重判本次，此时
	// Allow 不参与判定。界面只上报「选了哪档」，结论由 perm 算（改档位 = 永久改全局，
	// 与 /permission 同一次授权）。只有工具确认会带回权限档——外部代理与命令行确认
	// 不读权限档，界面不给这个入口。
	Level string
	// Persist 用户点了「以后都允许」（D282）：范围由判定侧按工具性质算（执行类落工具、
	// 文件类收窄到目标所在目录），界面不参与——该位只表达「别再问这个」。没有策略表可写
	// 时退化为一次放行。
	//
	// 与 Allow 同义（点这个钮当然是放行这次），单独一位是为了让判定侧知道要不要写策略表。
	Persist bool
}

// ConfirmOrigin 一次确认的来源（D280 策略表的来源维）。
type ConfirmOrigin string

const (
	// OriginTool 本地模型发起的工具调用（toolrun）。
	OriginTool ConfirmOrigin = "toolrun"
	// OriginAgent 外部代理经 ACP 发起的反向请求（acpgate）。
	OriginAgent ConfirmOrigin = "acpgate"
	// OriginCommand 用户自己键入的破坏性命令确认（/rm 等）与插件、技能授权。
	// 这类请求不代表模型行为，策略表不参与。
	OriginCommand ConfirmOrigin = "command"
)

// ConfirmRequest 一次确认的完整请求（D280 第 7 条）。
//
// Prompt 是已经拼好的用户面文本，现有界面只读这段文本；策略表与决策模型读其余字段，
// 不再从一句中文里反解「要干什么」。拿不到的字段留零值：HasOp / HasRisk 区分
// 「读」与「不涉及文件」、Safe 与「不适用」。
type ConfirmRequest struct {
	Origin  ConfirmOrigin
	Tool    string // 工具名；外部代理的权限反问填对方给的工具标题
	Op      perm.Op
	HasOp   bool
	Target  string // 目标路径、命令或其他标识
	Risk    tool.Risk
	HasRisk bool
	Task    string // 外部代理这一趟的任务摘要（首 80 字）
	Prompt  string
	// CanPersist 这次确认允许提供「以后都允许」（D282）：判定侧在置位时已经算过
	// 「写了也不会生效」的情形（目标命中硬名单——那条规则永远轮不到），界面据此
	// 决定给不给这个钮。界面自己不做这个判断：界面拿不到敏感根。
	CanPersist bool
}

// Confirm 逐次确认（Risk=Confirm 的工具、越界文件访问、插件授权等）。
type Confirmer interface {
	Confirm(ctx context.Context, req ConfirmRequest) (ConfirmAnswer, error)
}

// FileTarget 可选能力（D25）：文件类工具申报本次调用的目标路径与读写操作，
// ToolRunner 据此查权限矩阵路径格（D22 两列分工：文件类只看路径格）。
// 未申报（非文件类）的工具走执行类判定（只看工具列 Risk）。
type FileTarget interface {
	Target(ctx context.Context, call tool.Call) (path string, op perm.Op, ok bool)
}

// Interactive 可选能力（D184/D189）：工具自己的等待时长由这个能力管，ToolRunner 不套默认
// 超时（limits.tool_timeout_sec）——等人应答（ask_user）、等外部事件（job_wait 等任务
// 收尾）都不该被当成「卡住」；显式 timeout_sec 与 ctx 取消仍然生效。
type Interactive interface {
	Interactive() bool
}

// sessionKey 会话上下文的私有键（ctx 值只在进程内传递，不进网络/树/日志）。
type sessionKey struct{}

// WithSessionID 注入当前会话 ID：Agent.Run 执行工具前调用，
// 供 memory_* 的 session 作用域等定位资源（值拷贝、只读；不暴露会话树对象）。
func WithSessionID(ctx context.Context, id conversation.ID) context.Context {
	return context.WithValue(ctx, sessionKey{}, id)
}

// SessionIDFrom 取出当前会话 ID；不在工具执行上下文中时 ok=false。
func SessionIDFrom(ctx context.Context) (conversation.ID, bool) {
	id, ok := ctx.Value(sessionKey{}).(conversation.ID)
	return id, ok
}
