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

// ToolRunner 工具执行门面：查找 + capability 校验 + Risk 确认 + 超时 + 结果裁剪。
type ToolRunner interface {
	Specs(ctx context.Context) ([]tool.Spec, error)
	Execute(ctx context.Context, call tool.Call) (tool.Result, error)
}

// Confirmer 逐次确认（Risk=Confirm 的工具、越界文件访问、插件授权等）。
type Confirmer interface {
	Confirm(ctx context.Context, prompt string) (bool, error)
}

// FileTarget 可选能力（D25）：文件类工具申报本次调用的目标路径与读写操作，
// ToolRunner 据此查权限矩阵路径格（D22 两列分工：文件类只看路径格）。
// 未申报（非文件类）的工具走执行类判定（只看工具列 Risk）。
type FileTarget interface {
	Target(ctx context.Context, call tool.Call) (path string, op perm.Op, ok bool)
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
