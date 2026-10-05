package port

// CommandInfo 命令清单条目（D103/S2b-1）：补全浮层与 /help 共用的最小事实。
// 值语义只读——UI 渲染补全与说明，拿不到命令处理器本体（执行一律走 Prompter →
// Session.Handle，§7.3）。
type CommandInfo struct {
	// Names 命令名集合（不含斜杠；首名为主名，其余为别名——quit/exit 一条两名）。
	Names []string
	// Usage 参数用例（不含命令名本身；空 = 无参数），如 "<id> [--keep|--copy] <文本>"。
	Usage string
	// Desc 一句话描述（help 行与浮层共用；浮层侧自行截断）。
	Desc string
}

// CommandCatalog 命令清单只读端口（D103/S2b-1，TreeView 同款反向端口）：UI 补全浮层
// 只读清单事实用，**不给变更入口**（命令执行一律经 Prompter → Session.Handle）。
//
// 并发（§15.5）：实现在 app 侧（静态表不可变 + 动态表锁内快照），UI 事件循环
// goroutine 可无锁调用（动态部分由实现内部持锁）。
type CommandCatalog interface {
	// Commands 返回当前可用命令清单：静态命令在前（表序）、动态命令（/mcp:*）按名
	// 排序缀尾；Session 未就绪或无动态命令时相应部分为空。
	Commands() []CommandInfo
}
