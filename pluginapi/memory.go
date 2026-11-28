package pluginapi

import "context"

// memory.go 记忆供给面（plugin-guide.md §4）：完全替代内置文档记忆。
//
// 这里没有「文档增删查」的方法集——那是内置实现的形状，不是契约。插件接管记忆之后，
// 记忆存在哪（文件、SQLite、远端服务）、怎么组织（文档、图层、向量、图）、怎么管理
// （工具、命令、面板）全归插件，宿主一条都不假设。宿主只保留一个挂载点：每轮装配时
// 问一次「这一次要注入什么」。
//
// 代价写在明面上，不藏在注释里：
//   - 内置的 memory_* 工具与 /memory 在接管期间退出，记忆的读写面由插件自己提供
//     （ToolProvider / CommandProvider / ViewProvider）；
//   - 内置记忆转为只读，且只作为迁移素材（LegacyMemoryHost）；
//   - 同一时刻至多一个插件拥有记忆，两个同时启用要被宿主拒绝——否则两套记忆同时注入。

// Memory 记忆供给面。实现方即「记忆的所有者」，负责存、取、组织、巩固与预算裁剪。
//
// 兼容承诺：改本接口的方法集会破坏所有声明 provides: memory 的插件与每轮装配的转换。
type Memory interface {
	// Recall 返回本轮要注入上下文的记忆片段：markdown 原文，宿主原样放进去，不解读、
	// 不重排、不加标题（段标题由宿主加）。
	//
	// 返回空 Text 表示本轮不注入——这是正常状态，不是错误（例如库里还没有任何记忆）。
	// 可能每轮调用一次，实现方自行缓存索引，不要在热路径上扫全库。
	Recall(ctx context.Context, req MemoryRequest) (MemoryContext, error)
}

// MemoryRequest 一次注入请求。
//
// 会话身份不在这里：它由宿主经 ctx 注入，插件用 SessionFrom(ctx) 读（见 session.go）——
// 与工具、命令、面板同一个来源，避免两处会话字段。
//
// 兼容承诺：改字段会破坏记忆类插件与每轮装配的转换。
type MemoryRequest struct {
	// BudgetTokens 本轮记忆段的建议 token 上限（软预算），由模型窗口与其余段占用算出。
	// 0 = 宿主不给建议。超出由宿主硬截断并记日志——插件应按这个数自己裁剪，别指望截断
	// 截得好看。
	BudgetTokens int
}

// MemoryContext 注入结果。Text 原样进上下文；宿主只负责把它放进记忆段的位置，
// 超预算时按 BudgetTokens 截断（截断点由宿主挑，插件无法控制，所以要自己裁）。
//
// 兼容承诺：改字段会破坏记忆类插件与每轮装配的转换。
type MemoryContext struct {
	Text string
}

// ---------- 迁移素材（内置记忆的交接） ----------

// LegacyMemoryHost 宿主可选扩展：内置文档记忆的一次性只读快照，供插件迁移。
// 用法是对 Host 句柄做 type assertion；只有声明 provides: memory 的插件才拿得到。
//
// 它是交接素材，不是常驻读取面：接管期间内核不再写内置记忆，快照之后不会有新内容。
// 插件应在首次启用时取一次、落进自己的存储，之后照自己的模型组织。
//
// 兼容承诺：改方法签名会破坏做迁移的插件与实现它的宿主。
type LegacyMemoryHost interface {
	LegacyMemory(ctx context.Context) (LegacyMemorySnapshot, error)
}

// LegacyMemorySnapshot 内置记忆的只读快照：全局一份 + 若干会话各一份。
// 空快照（全局与会话都为空）表示用户没有可迁移的内容，不是错误。
//
// 兼容承诺：改字段会破坏做迁移的插件。
type LegacyMemorySnapshot struct {
	Global   string
	Sessions []LegacySessionMemory
}

// LegacySessionMemory 一个会话的内置记忆。Session 是会话 ID，与 SessionFrom(ctx) 同源。
//
// 兼容承诺：改字段会破坏做迁移的插件。
type LegacySessionMemory struct {
	Session string
	Content string
}
