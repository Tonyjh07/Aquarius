package plugin

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Session 一个已连接 MCP server 的能力与生命周期面——**消费方定义的小接口**（§6.4 宿主
// 只做编排、§6.3 mcpgate 负责协议映射，两者经本接口与 Dialer 解耦，宿主不 import 适配器）。
// 实现见 internal/adapter/mcpgate；装配根用闭包把 Secrets 等依赖注入 Dialer。
type Session interface {
	// Tools tools/list 快照 → port.Tool（命名为 mcp:<server>:<tool>）。
	Tools(ctx context.Context) ([]port.Tool, error)
	// Memory resources 只读投影（写/删返回错误，§6.3：写仍走自有记忆）。
	Memory() port.MemoryStore
	// Prompts prompts/list 快照（注册 /mcp:<server>:<prompt> 动态命令用）。
	Prompts(ctx context.Context) ([]PromptInfo, error)
	// RenderPrompt prompts/get 渲染为纯文本（参数按声明位次映射）。
	RenderPrompt(ctx context.Context, prompt string, args []string) (string, error)
	// Wait 阻塞至连接终止（崩溃检测入口）；返回终止原因。
	Wait() error
	// Close 优雅关闭（§6.4 #4）。
	Close() error
	// Stats 调用统计快照（§6.4 #5，/plugin list 展示）。
	Stats() Stats
}

// PromptInfo prompts/list 条目（动态命令注册用）。
type PromptInfo struct {
	Name        string
	Description string
}

// Stats 插件调用统计（§6.4 #5）。
type Stats struct {
	Calls  int   `json:"calls"`   // 已处理的工具调用
	Errors int   `json:"errors"`  // 其中失败的
	LastMS int64 `json:"last_ms"` // 最近一次耗时
}

// Dialer 连接一个声明的 server（装配根提供：闭包持有 Secrets 等适配器依赖）。
type Dialer func(ctx context.Context, d Decl) (Session, error)
