package port

// ParamKind 参数在补全浮层里的性质：能不能填入、按什么取候选。
type ParamKind string

const (
	// ParamText 自由文本参数：只给一句提示，选中不填（填进去的东西只能靠用户自己写）。
	ParamText ParamKind = "text"
	// ParamChoice 枚举参数：Values 是全部合法值，可选中填入。
	ParamChoice ParamKind = "choice"
	// ParamFlag 选项开关（`--keep` 这类字面量）：Name 即字面量，可选中填入。
	ParamFlag ParamKind = "flag"
)

// CommandParam 一个参数的声明（D250）：参数相浮层的唯一数据源。
//
// 与 Usage 是两个层次：Usage 是给人一眼看全的那句话（含 `[--keep|--copy]` 这类分组
// 语法），无法逐参数反解；这里逐条列出「这个位置能填什么」，两者由注册表自检对账。
type CommandParam struct {
	// Name text/choice 是占位说明（"<id>"、"等级"）；flag 是字面量（"--keep"）。
	Name string
	// Desc 一句话说明（参数相提示行；用户面，不带内部编号）。
	Desc string
	// Kind 性质，决定能不能选中填入。
	Kind ParamKind
	// Values choice 的合法值（按展示序）；flag 带枚举值时也放这里（如 --format）。
	Values []string
	// ValueName flag 后面还跟一个值时的占位名（"N"、"<路径>"）；空 = 纯开关。
	ValueName string
	// Repeatable 可重复出现（--attach、--drop 这类），提示里标注「可多次」。
	Repeatable bool
}

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
	// Params 逐参数声明（D250）；空 = 该命令不吃参数（浮层给一句通用提示）。
	Params []CommandParam
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
