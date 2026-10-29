package uigui

// compl_args.go 补全浮层的「算什么」这一层（D250）：命令相与参数相共用一份候选形状，
// 纯函数、不碰 Gio 与 UI 状态，便于表驱动测试。事件的取用与落笔留在 compl.go。

import (
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// complItem 补全浮层的一行。
//
// 命令相与参数相共用一个形状：label 是左半（命令相自带斜杠，参数相是参数名或可选值），
// tail 是右半说明，insert 是 Tab / 点击真正填进编辑框的内容。insert 为空 = 这一行只提示
// 不可填（自由文本参数、以及吃完值就没有下文的开关，都只有这句话可给）。
type complItem struct {
	label  string
	tail   string
	insert string
}

// complPhase 浮层开启判据（D250）：文本以 "/" 开头即开——含空格。
//
// 这里是本次改动的关键：此前出现空格即关（参数级补全不做），于是用户敲完命令名就只剩
// 一句 Usage 可看；现在空格把浮层切到参数相，提示继续跟着走。
func complPhase(text string) bool {
	return strings.HasPrefix(text, "/")
}

// complToken 末尾 token 的切分：返回它在这段文本里的起点与内容（终点恒为 len(text)）。
//
// 文本以空白结尾时返回空 token、start = len(text)——那正是用户刚敲完分隔符、下一个参数
// 还没开始的位置，也是 Tab 落笔的位置。
func complToken(text string) (start int, tok string) {
	end := len(text)
	i := end
	for i > 0 && !isSpaceByte(text[i-1]) {
		i--
	}
	return i, text[i:end]
}

// isSpaceByte 单个字节是否是空白（命令行的分隔符只有 ASCII 空白）。
func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// complTarget 当前在补全什么：命令相（补命令名本身）还是参数相（补哪个命令的哪个参数）。
type complTarget struct {
	// cmdPhase 命令相（整段还没有空白）。命令相只补名字，不看参数。
	cmdPhase bool
	// prefix 命令相的前缀（斜杠之后那段）。
	prefix string
	// ci 参数相认得的命令记录；known = false 时参数相没有任何事实可提示。
	ci    port.CommandInfo
	known bool
	// args 已完成的参数（不含末尾仍在输入的那个）。
	args []string
	// tok 末尾还在输入的 token（空 = 刚敲完分隔符）。
	tok string
	// start tok 在文本里的起点，也是 Tab 替换范围的左端。
	start int
}

// complTargetOf 把编辑框文本解析成补全目标。
//
// 只按**末尾** token 算，不看光标位置：光标停在中间时补的仍是行尾那一段。这是刻意的
// 边界（docs/contracts.md §7.3）——追光标要复刻编辑器的换行与引号状态，收益只在「回头
// 改中间那个参数」这一种用法上，而那种用法本来就该直接改字。
func complTargetOf(text string, all []port.CommandInfo) complTarget {
	if !complPhase(text) {
		return complTarget{}
	}
	start, tok := complToken(text)
	if start == 0 && !strings.ContainsAny(text, " \t") {
		return complTarget{cmdPhase: true, prefix: text[1:], start: 0}
	}
	fields := strings.Fields(text)
	if len(fields) == 0 { // 只有空白，等不到命令名
		return complTarget{}
	}
	args := fields[1:]
	if tok != "" && len(args) > 0 {
		args = args[:len(args)-1] // 末尾那段还没写完，不算已完成
	}
	t := complTarget{args: args, tok: tok, start: start}
	name := strings.TrimPrefix(fields[0], "/")
	for _, ci := range all {
		for _, n := range ci.Names {
			if strings.EqualFold(n, name) {
				t.ci, t.known = ci, true
				break
			}
		}
		if t.known {
			break
		}
	}
	return t
}

// complFilter 命令相候选：对条目任一名称做大小写不敏感前缀匹配、保持清单序；命中名取
// 第一个匹配的名称（主名优先，输入 "/exit" 不改写成 "/quit"）。
func complFilter(prefix string, all []port.CommandInfo) []complItem {
	p := strings.ToLower(prefix)
	var out []complItem
	for _, ci := range all {
		for _, n := range ci.Names {
			if !strings.HasPrefix(strings.ToLower(n), p) {
				continue
			}
			tail := ci.Usage
			if ci.Desc != "" {
				if tail != "" {
					tail += " — "
				}
				tail += ci.Desc
			}
			out = append(out, complItem{label: "/" + n, tail: tail, insert: "/" + n})
			break
		}
	}
	return out
}

// complArgItems 参数相候选：末尾 token 是开关就列开关，否则按已消费的位置数取该位置的
// 参数声明（枚举给可选值、自由文本给一句提示）。
func complArgItems(ci port.CommandInfo, args []string, tok string) []complItem {
	// 1) 正在敲开关：只列还没用过（或可重复）的那些。
	if strings.HasPrefix(tok, "--") {
		return complFlagItems(ci, args, tok)
	}
	// 2) 上一个 token 是吃值的开关、这一位就是它的值。
	if f, ok := waitingFlag(ci, args); ok {
		if len(f.Values) > 0 {
			if out := complValueItems(f.Values, tok, f.Desc); len(out) > 0 {
				return out
			}
			return nil
		}
		if f.ValueName != "" {
			return []complItem{{label: f.ValueName, tail: f.Desc}}
		}
	}

	var out []complItem
	pos := positionalArgs(ci, args)
	params := positionalParams(ci)
	if len(params) == 0 {
		if len(ci.Params) == 0 {
			// 不吃参数的命令也进参数相：给一句「到此为止」，而不是让浮层凭空消失。
			return []complItem{{label: "（无参数）", tail: "这个命令不带参数，回车直接执行"}}
		}
		return complFlagItems(ci, args, tok)
	}
	var p port.CommandParam
	switch i := len(pos); {
	case i < len(params):
		p = params[i]
	case params[len(params)-1].Repeatable:
		p = params[len(params)-1] // 可重复的位置参数：超出的仍按最后那条算
	default:
		p = port.CommandParam{} // 位置参数已填满：只剩开关
	}
	switch p.Kind {
	case port.ParamChoice:
		out = append(out, complValueItems(p.Values, tok, p.Desc)...)
	case port.ParamText:
		out = append(out, complItem{label: p.Name, tail: p.Desc})
	}
	if tok == "" { // 新参数的开头：还能用的开关缀在后面（必填项排在前，Tab 先撞到的是它）
		out = append(out, complFlagItems(ci, args, "")...)
	}
	return out
}

// complFlagItems 开关候选：前缀匹配、跳过已用过的（可重复的永远列）。
func complFlagItems(ci port.CommandInfo, args []string, prefix string) []complItem {
	p := strings.ToLower(prefix)
	var out []complItem
	for _, cp := range ci.Params {
		if cp.Kind != port.ParamFlag || !strings.HasPrefix(strings.ToLower(cp.Name), p) {
			continue
		}
		if !cp.Repeatable && hasArg(args, cp.Name) {
			continue
		}
		label := cp.Name
		if cp.ValueName != "" {
			label += " " + cp.ValueName
		}
		tail := cp.Desc
		if cp.Repeatable {
			if tail != "" {
				tail += "；"
			}
			tail += "可给多次"
		}
		out = append(out, complItem{label: label, tail: tail, insert: cp.Name})
	}
	return out
}

// complValueItems 枚举候选：前缀过滤、保持声明序。
func complValueItems(values []string, prefix, tail string) []complItem {
	p := strings.ToLower(prefix)
	var out []complItem
	for _, v := range values {
		if !strings.HasPrefix(strings.ToLower(v), p) {
			continue
		}
		out = append(out, complItem{label: v, tail: tail, insert: v})
	}
	return out
}

// positionalParams 位置参数声明（按声明序，跳过开关）。
func positionalParams(ci port.CommandInfo) []port.CommandParam {
	var out []port.CommandParam
	for _, p := range ci.Params {
		if p.Kind != port.ParamFlag {
			out = append(out, p)
		}
	}
	return out
}

// positionalArgs 已消费的位置参数（跳过开关本身与开关的值）。
func positionalArgs(ci port.CommandInfo, args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if p, ok := flagParam(ci, args[i]); ok {
			if p.ValueName != "" {
				i++ // 吃值的开关：下一个 token 是值，不是位置参数
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// waitingFlag 末尾 token 是不是一个「还等着值」的开关（args 末位就是它自己）。
func waitingFlag(ci port.CommandInfo, args []string) (port.CommandParam, bool) {
	if len(args) == 0 {
		return port.CommandParam{}, false
	}
	p, ok := flagParam(ci, args[len(args)-1])
	if !ok || p.ValueName == "" {
		return port.CommandParam{}, false
	}
	return p, true
}

// flagParam 按名字查开关声明（大小写不敏感）。
func flagParam(ci port.CommandInfo, name string) (port.CommandParam, bool) {
	for _, p := range ci.Params {
		if p.Kind == port.ParamFlag && strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return port.CommandParam{}, false
}

// hasArg token 是否已出现过（开关去重判据）。
func hasArg(args []string, name string) bool {
	for _, a := range args {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

// complCycle Tab 轮换状态（readline 的 menu-complete 口径）。
//
// 首次 Tab 算一次候选并填第一项，候选表就此**锁定**；此后再按 Tab 依次替换同一段 token
// 并前移高亮。锁定是关键：不锁的话每替一次文本都变，候选会跟着缩成一项，轮换退化成
// 「一直填同一项」。用户敲了别的字符（文本变了）即退出轮换，下一次 Tab 重新起一轮。
//
// 只索引可填的行（insert 非空）：提示行轮进去也填不进去，混在轮换里只会让高亮停在
// 一行按 Tab 没反应的位置上。
type complCycle struct {
	on    bool
	start int         // 被替换 token 的起点
	items []complItem // 锁定的候选表（点选也要能查回内容）
	rows  []int       // 可填行在 items 里的下标（锁定）
	at    int         // 当前在 rows 里的位置
}

// reset 结束轮换（文本变更、浮层关闭、提交后都调）。
func (c *complCycle) reset() {
	*c = complCycle{}
}

// begin 开一轮：锁定候选表，返回第一项的下标。没有可填的行则不进入轮换。
func (c *complCycle) begin(items []complItem, start int) (int, bool) {
	c.reset()
	for i, it := range items {
		if it.insert != "" {
			c.rows = append(c.rows, i)
		}
	}
	if len(c.rows) == 0 {
		return 0, false
	}
	c.on, c.start, c.items, c.at = true, start, items, 0
	return c.rows[0], true
}

// advance 前移一行（环回），返回它在 complList 里的下标。
func (c *complCycle) advance() (int, bool) {
	if !c.on || len(c.rows) == 0 {
		return 0, false
	}
	c.at = (c.at + 1) % len(c.rows)
	return c.rows[c.at], true
}

// seek 把轮换位置移到某一行（点击落笔用）：不改变已锁定的候选表。
func (c *complCycle) seek(sel int) bool {
	if !c.on {
		return false
	}
	for i, r := range c.rows {
		if r == sel {
			c.at = i
			return true
		}
	}
	return false
}
