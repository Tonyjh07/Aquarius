package port

import (
	"context"

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
