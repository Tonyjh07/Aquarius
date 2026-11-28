package pluginapi

import "context"

// tool.go 工具供给面（plugin-guide.md §4）：插件声明工具、执行工具。
// 确认、超时与结果裁剪仍由内核的 ToolRunner 包在外面——插件拿到的永远是「已经裁决过」
// 的调用，因此不要在 Execute 里再问用户。

// ToolProvider 工具供给面。
//
// 工具面不是静态的：宿主在**每次装配请求时**（含同一 Turn 内工具循环的每一轮）调用
// Tools，所以清单可以随插件状态变化。这允许「标题即工具」这类形态——记忆点、目录节点
// 各自是一个工具，调用它表示下钻一层（D340）。
//
// 契约三条：
//  1. 名字文法 `[A-Za-z0-9_-]{1,64}`，在**当前会话可见集合内**唯一。不同会话可以同名——
//     名字按 ctx 里的会话解析，不需要全局唯一；与内置工具同名时以内置为准，插件的那一条
//     被丢弃。
//  2. 名字可以逐轮变化，但**同一轮内必须稳定**：模型这一轮看到的清单与本次 Execute 的
//     查找要一致，否则模型点开的东西和它看到的名字对不上。
//  3. 上一轮声明过、这一轮已经消失的名字不再是合法调用；宿主按「未知工具」回填，插件
//     不需要为它保留状态。
//
// Tools 每轮都会被调用（含工具循环的每一轮），实现要便宜：读自己的内存状态，别在这做
// 网络请求或全库扫描。
//
// 兼容承诺：改方法集或签名会破坏所有声明 provides: tool 的插件。
type ToolProvider interface {
	Tools(ctx context.Context) []ToolSpec
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}

// ToolNameMaxLen 工具名长度上限（与主流模型端点的限制一致；超长会在协议层被拒）。
// 兼容承诺：改值会破坏按 64 做名字规划的工具插件（窗口 slug 预算等）。
const ToolNameMaxLen = 64

// ValidToolName 工具名是否满足文法（`[A-Za-z0-9_-]{1,64}`）。宿主在装配时逐条校验，
// 不合格的清单整份拒掉并报因——模型端点不认识的名字会让整个请求失败，早点失败比在
// 协议层拿到 400 好读。
// 兼容承诺：改文法会破坏已按旧文法命名的工具插件与宿主侧逐条校验。
func ValidToolName(name string) bool {
	if name == "" || len(name) > ToolNameMaxLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_', c == '-':
		default:
			return false
		}
	}
	return true
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
