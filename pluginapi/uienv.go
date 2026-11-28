package pluginapi

// uienv.go 界面环境的只读面（D352 / plugin-guide.md §6）。
//
// 这是给「插件自己 import GUI 库开窗口」那条路留的信息面：插件要知道自己站在什么前端、
// 深浅色、缩放比、以及宿主的语义色，才能画出一个跟宿主不打架的窗。宿主在这里只交出
// **状态描述**——没有句柄、没有事件、没有订阅、没有绘制回调。窗口归插件，出了事也归插件。
//
// 兼容承诺：本接口只读、只描述当前环境；新增字段可以，改字段与加方法会破坏实现方。
// 冻结的性质（不靠注释守）：
//   - 不出现任何 GUI 类型（independence 检查）；
//   - 不提供「等待变化」的通道或回调：深浅色变了不通知，插件自己轮询 Environ。
//     一旦给了订阅，撤销、缓冲、插件退出时的取消就全成了宿主的义务。
type UIEnviron interface {
	// Environ 当前环境快照。每次调用返回当时的真值（深浅色会随系统变），
	// 不是构造时冻结的常量。
	Environ() UIEnvironment
}

// 前端种类。只有 GUI 能自建窗口与自绘；其余两者没有 OS 窗口可开。
const (
	// FrontendGUI Gio 悬浮球 GUI。
	FrontendGUI = "gui"
	// FrontendTUI bubbletea 终端界面。
	FrontendTUI = "tui"
	// FrontendREPL 纯文本行。
	FrontendREPL = "repl"
)

// 前端形态词表冻结：新增前端追加常量，不改已有值——改值会破坏按形态分派的插件。

// UIEnvironment 一次环境快照。
//
// 零值是合法的「什么都不知道」：Frontend 空、Scale 0、Tokens 全空。插件必须能在这份
// 空快照下退化（按自己的缺省画，或干脆不开窗）。
// 兼容承诺：改字段会破坏读宿主环境的插件与环境面的实现方。
type UIEnvironment struct {
	Frontend string // FrontendGUI / FrontendTUI / FrontendREPL；空 = 未知
	Dark     bool   // 当前主题是否深色；只有 Frontend == FrontendGUI 时可信
	Scale    float64
	// Scale 逻辑像素到物理像素的倍率，1.0 = 100%；0 = 未知。按 1.0 画会在高分屏上糊。
	Tokens ThemeTokens
	// Fonts 正文候选字体**路径**（不是名字），按优先级排列，best-effort：
	// 可能为空、可能指向宿主的降级面、也可能在下一个宿主版本里变。插件缺字体时
	// 必须自己退到无衬线缺省，不要把「读到路径」当成「字体一定能用」。
	Fonts []string
	// SymbolFonts 符号回退面（可空）：几何符号（U+25B8 这类折叠三角）在正文 CJK
	// 字体里通常没有字形，缺了会变成方框（宿主 D348 踩过）。自建窗要画三角/箭头
	// 时按序追加这一组。
	SymbolFonts []string
}

// ThemeTokens 宿主语义色，格式 RRGGBB（六位十六进制，无 #、无 alpha）。
//
// 只给六个：其他颜色是宿主内部卡片版式的一部分，插件拿不到也不该假装拿得到。
// 兼容承诺：改字段集或取值格式会破坏按令牌配色的插件。
type ThemeTokens struct {
	BG     string // 窗口底
	FG     string // 正文
	Muted  string // 次要文字、状态行
	Accent string // 强调/品牌色
	Border string // 描边
	Sel    string // 选中/悬停底
}
