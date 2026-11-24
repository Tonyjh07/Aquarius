package pluginapi

import "context"

// command.go 命令供给面（plugin-design.md §4）：插件注册斜杠命令。
// /help、分派与补全归内核；插件只声明「叫什么、吃什么参数、返回什么文本」。

// CommandProvider 命令供给面。命令名与内置命令、其他插件撞名时宿主在启动校验期拒载。
// 兼容承诺：改方法集或签名会破坏所有声明 provides: command 的插件。
type CommandProvider interface {
	// Commands 命令声明（含参数相）。清单在实例生命周期内应保持稳定。
	Commands() []CommandSpec
	// Run 执行一次命令，返回给用户看的文本（可为空串 = 无输出）。name 是 Commands
	// 里声明过的某个名字——别名与主名都可能传进来，插件按自己的表认。
	Run(ctx context.Context, name string, args []string) (string, error)
}

// CommandSpec 一条命令的声明。
// 兼容承诺：改字段会破坏宿主与命令插件的编译及 /help 的渲染。
type CommandSpec struct {
	// Names 命令名集合（不含斜杠）：首名为主名，其余是别名。
	Names []string
	// Usage 参数用例（不含命令名本身），如 "<id> [--keep|--copy]"；空 = 无参数。
	Usage string
	// Desc 一句话描述，/help 与补全浮层共用；用户面文本，不带内部编号。
	Desc string
	// Params 逐参数声明；空 = 该命令不吃参数（浮层只给一句通用提示）。
	Params []CommandParam
}

// ParamKind 参数在补全浮层里的性质。词表冻结。
// 兼容承诺：改取值会破坏宿主的补全浮层与命令插件的参数声明。
type ParamKind string

const (
	// ParamText 自由文本：只给一句提示，选中不填。
	ParamText ParamKind = "text"
	// ParamChoice 枚举参数：Values 是全部合法值，可选中填入。
	ParamChoice ParamKind = "choice"
	// ParamFlag 选项开关（`--keep` 这种字面量）：Name 即字面量。
	ParamFlag ParamKind = "flag"
)

// CommandParam 一个参数的声明（补全浮层的唯一数据源）。
// 兼容承诺：改字段会破坏宿主的参数相浮层。
type CommandParam struct {
	// Name text/choice 是占位说明（"<id>"）；flag 是字面量（"--keep"）。
	Name string
	Desc string
	Kind ParamKind
	// Values choice 的合法值（按展示序）；flag 带枚举值时也放这里。
	Values []string
	// ValueName flag 后面还跟值时的占位名（"N"、"<路径>"）；空 = 纯开关。
	ValueName string
	// Repeatable 可重复出现，提示里标注「可多次」。
	Repeatable bool
}
