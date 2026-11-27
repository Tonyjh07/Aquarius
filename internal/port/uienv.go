package port

// uienv.go 界面环境的只读快照（D352）。
//
// 这份东西是给「插件自己拿 GUI 库开窗口」那条路用的：插件需要知道自己站在什么前端上、
// 现在是深色还是浅色、按什么比例缩放、宿主的语义色是哪几个，否则自建的窗跟宿主并排放
// 就是两个应用。宿主只交出**当前状态的描述**，不交句柄、不交事件、不交绘制能力。
//
// 与 D346 的面板面相反：那条路上插件交组件树、宿主画；这条路上插件自己画，宿主只回答
// 「现在长什么样」。同一份快照也能给面板面用（虽然它自己不需要）。

// 前端种类：只有 GUI 能自建窗口与自绘。
const (
	FrontendGUI  = "gui"  // 有 OS 窗口的前端（gio）
	FrontendTUI  = "tui"  // 终端全屏界面
	FrontendREPL = "repl" // 最小行式终端：无全屏、无面板
)

// UIEnvironment 一次环境快照。所有字段都是「当前值的拷贝」，不是访问器。
type UIEnvironment struct {
	Frontend string // FrontendGUI / FrontendTUI / FrontendREPL；空 = 宿主没说
	Dark     bool   // 当前主题是否深色
	Scale    float64
	Tokens   UIThemeTokens
	Fonts    []string // 正文候选字体路径（best-effort，可空）
	// SymbolFonts 符号回退面：折叠三角这类几何符号在正文 CJK 字体里普遍没有字形，
	// 缺字会渲染成方框（D348）。只给正文面会重演那一幕，所以单独列一份。
	SymbolFonts []string
}

// UIThemeTokens 宿主的语义色（RRGGBB，不含 alpha 与 #）。
//
// 只给六个：底、字、次要字、强调、描边、选中底。宿主 palette 里那些 card/pill/pop
// 是内部卡片语义，插件自建的窗拿不到也不该假装拿得到——给了就是把内部版式变成契约。
type UIThemeTokens struct {
	BG, FG, Muted, Accent, Border, Sel string
}
