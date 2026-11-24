package pluginapi

import "context"

// tool.go 工具供给面（plugin-design.md §4）：插件声明工具、执行工具。
// 确认、超时与结果裁剪仍由内核的 ToolRunner 包在外面——插件拿到的永远是「已经裁决过」
// 的调用，因此不要在 Execute 里再问用户。

// ToolProvider 工具供给面。Tools 在实例生命周期内应保持稳定（宿主按它建注册表）；
// 需要动态增删的实现请在 Shutdown 后重建实例，不要中途改表。
// 兼容承诺：改方法集或签名会破坏所有声明 provides: tool 的插件。
type ToolProvider interface {
	Tools() []ToolSpec
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}

// Describer 可选扩展：把调用参数提炼成用户能判断的一句话（内核按 type assertion 探测），
// 供需要确认的调用展示。返回空串 = 退回内核的默认提示。
//
// 没有 ctx：这个接口的契约就是「纯函数、无副作用、可能被反复调用」。你要读文件才能说清
// 该不该允许，那说明确认提示本身不该由工具来写。
//
// 兼容承诺：改签名会破坏实现了确认摘要的插件。
type Describer interface {
	Describe(call ToolCall) string
}
