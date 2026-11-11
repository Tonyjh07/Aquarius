package app

import (
	"context"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 参数相的共用声明（D250）：枚举值直接取自权威常量表，不在这里抄一份——
// 两处并存的枚举迟早会分叉，而分叉的表现是「提示里能点，点了报错」。
var (
	// onOffParams 开关类命令（/think、/unattended）：两档。文案放 key，见 D275。
	onOffParams = []port.CommandParam{{
		Name: "cmd.shared.onoff.p1.name", Kind: port.ParamChoice, Values: []string{"on", "off"},
		Desc: "cmd.shared.onoff.p1.desc",
	}}
	// levelParams /permission 的四档，取值来自 perm.Levels。
	levelParams = []port.CommandParam{{
		Name: "cmd.shared.level.p1.name", Kind: port.ParamChoice, Values: permLevelValues(),
		Desc: "cmd.shared.level.p1.desc",
	}}
)

// permLevelValues 权限等级的可填值（perm.Levels 的字符串形态）。
func permLevelValues() []string {
	out := make([]string, len(perm.Levels))
	for i, l := range perm.Levels {
		out[i] = l.String()
	}
	return out
}

// exportFormatValues 可写的导出格式名（跳过只读那档——列进去只会让人点到报错）。
func exportFormatValues() []string {
	var out []string
	for _, f := range port.ExchangeFormats() {
		if f.NoExport {
			continue
		}
		out = append(out, f.Name)
	}
	return out
}

// commandEntry 命令注册表的一条记录（D245）：元数据与处理函数同一行。
//
// 此前元数据在 commandCatalog、执行在 execCommand 的 switch 里，两处维护同一条事实，
// 靠一个从 switch 反推分支名的测试兜底。现在 /help、补全浮层与分派读同一张表：
// 加一条命令 = 这里加一行（§7.3）。
type commandEntry struct {
	// Names 命令名集合（不含斜杠；首名为主名，其余为别名——quit/exit 一条两名）。
	Names []string
	// Usage 参数用例（不含命令名本身；空 = 无参数）。
	Usage string
	// Desc 一句话描述（/help 行与补全浮层共用）。
	Desc string
	// Params 逐参数声明（D250）：参数相浮层的数据源，与 Usage 那句速记是两层事实。
	Params []port.CommandParam
	// Run 处理函数。收 *Session 而不闭包捕获：表因此保持包级不可变量，
	// CommandCatalog() 与补全浮层的无锁读法不必跟着改。
	Run func(s *Session, ctx context.Context, cmd port.Command) (string, error)
}

// commandTable 静态命令表（D245 单一事实源）：表序即 /help 的行序，也是补全浮层静态段
// 的顺序。动态命令（/mcp:<server>:<prompt>）不在这张表里——它们随插件启停增删，混进来
// 会让「表不可变」这个前提失效；两者在 Commands() 处合并。
//
// 在 init() 里填而不是声明处填：/help 的处理函数渲染的就是这张表，声明处填会构成
// 「表 → /help → 表」的初始化环（Go 拒绝编译）。表在 init 之后只读。
var (
	commandTable []commandEntry
	commandIndex map[string]*commandEntry // 名字 → 记录（别名与主名同一条记录，D245）
)

func init() {
	commandTable = buildCommandTable()
	commandIndex = buildCommandIndex(commandTable)
}

// buildCommandTable 构造静态命令表。抽成函数由 init 调用，而不是在声明处直接赋值：
// /help 的处理函数渲染的就是这张表，Go 的初始化依赖分析会穿透函数体，声明处赋值会构成
// 「表 → /help → 表」的环；init 在所有包级变量初始化之后运行，环到这里才断开。
// 每条记录的 Params（D250）逐参数说明「这个位置能填什么」，参数相浮层据此算候选；
// Usage 仍是给人一眼看全的那句速记。两者由 TestCommandParamsMatchUsage 对账。
// 文案只放 key（D275）：Desc / Usage / Params 的 Name·ValueName·Desc 都是 cmd.* 取词
// key，纯语法（命令名、--flag、枚举值、<id> 这类占位）仍是字面量——它们是命令的一部分，
// 不是文案，翻不得。翻译发生在 commandInfos 投影时，所以语言选定之后拿到的清单就是
// 选定语言的（/help 与补全浮层共用同一份投影）。
func buildCommandTable() []commandEntry {
	var table []commandEntry
	table = append(table, sessionCommands()...)
	table = append(table, runtimeCommands()...)
	table = append(table, configCommands()...)
	return table
}

// sessionCommands 收会话与消息树命令。分段只为把表字面量拆开，不改任何记录；
// 三段的拼接顺序就是 /help 与补全浮层的行序，调段序会改掉帮助输出。
func sessionCommands() []commandEntry {
	return []commandEntry{
		{Names: []string{"new"}, Usage: "cmd.new.usage", Desc: "cmd.new.desc",
			Params: []port.CommandParam{{Name: "cmd.new.p1.name", Kind: port.ParamText, Desc: "cmd.new.p1.desc"}},
			Run:    (*Session).execNew},
		{Names: []string{"list"}, Desc: "cmd.list.desc", Run: (*Session).execList},
		{Names: []string{"switch"}, Usage: "cmd.switch.usage", Desc: "cmd.switch.desc",
			Params: []port.CommandParam{{Name: "cmd.switch.p1.name", Kind: port.ParamText, Desc: "cmd.switch.p1.desc"}},
			Run:    (*Session).execSwitch},
		{Names: []string{"title"}, Usage: "cmd.title.usage", Desc: "cmd.title.desc",
			Params: []port.CommandParam{
				{Name: "cmd.title.p1.name", Kind: port.ParamFlag, ValueName: "cmd.title.p1.valuename", Desc: "cmd.title.p1.desc"},
				{Name: "cmd.title.p2.name", Kind: port.ParamText, Desc: "cmd.title.p2.desc"},
			},
			Run: (*Session).execTitle},
		{Names: []string{"goto"}, Usage: "cmd.goto.usage", Desc: "cmd.goto.desc",
			Params: []port.CommandParam{{Name: "cmd.goto.p1.name", Kind: port.ParamText, Desc: "cmd.goto.p1.desc"}},
			Run:    (*Session).execGoto},
		{Names: []string{"edit"}, Usage: "cmd.edit.usage", Desc: "cmd.edit.desc",
			Params: []port.CommandParam{
				{Name: "cmd.edit.p1.name", Kind: port.ParamText, Desc: "cmd.edit.p1.desc"},
				{Name: "cmd.edit.p2.name", Kind: port.ParamFlag, Desc: "cmd.edit.p2.desc"},
				{Name: "cmd.edit.p3.name", Kind: port.ParamFlag, Desc: "cmd.edit.p3.desc"},
				{Name: "cmd.edit.p4.name", Kind: port.ParamFlag, ValueName: "cmd.edit.p4.valuename", Desc: "cmd.edit.p4.desc"},
				{Name: "cmd.edit.p5.name", Kind: port.ParamFlag, ValueName: "cmd.edit.p5.valuename", Repeatable: true, Desc: "cmd.edit.p5.desc"},
				{Name: "cmd.edit.p6.name", Kind: port.ParamFlag, ValueName: "cmd.edit.p6.valuename", Repeatable: true, Desc: "cmd.edit.p6.desc"},
				{Name: "cmd.edit.p7.name", Kind: port.ParamFlag, Desc: "cmd.edit.p7.desc"},
				{Name: "cmd.edit.p8.name", Kind: port.ParamText, Desc: "cmd.edit.p8.desc"},
			},
			Run: (*Session).execEdit},
		{Names: []string{"regen"}, Usage: "cmd.regen.usage", Desc: "cmd.regen.desc",
			Params: []port.CommandParam{{Name: "cmd.regen.p1.name", Kind: port.ParamText, Desc: "cmd.regen.p1.desc"}},
			Run:    (*Session).execRegen},
		{Names: []string{"respond"}, Usage: "cmd.respond.usage", Desc: "cmd.respond.desc",
			Params: []port.CommandParam{{Name: "cmd.respond.p1.name", Kind: port.ParamText, Desc: "cmd.respond.p1.desc"}},
			Run:    (*Session).execRespond},
		{Names: []string{"inject"}, Usage: "cmd.inject.usage", Desc: "cmd.inject.desc",
			Params: []port.CommandParam{
				{Name: "cmd.inject.p1.name", Kind: port.ParamFlag, ValueName: "cmd.inject.p1.valuename", Desc: "cmd.inject.p1.desc"},
				{Name: "cmd.inject.p2.name", Kind: port.ParamFlag, Desc: "cmd.inject.p2.desc"},
				{Name: "cmd.inject.p3.name", Kind: port.ParamText, Desc: "cmd.inject.p3.desc"},
			},
			Run: (*Session).execInject},
		{Names: []string{"branch"}, Usage: "cmd.branch.usage", Desc: "cmd.branch.desc",
			Params: []port.CommandParam{{Name: "cmd.branch.p1.name", Kind: port.ParamText, Desc: "cmd.branch.p1.desc"}},
			Run:    (*Session).execBranch},
		{Names: []string{"tree-edit"}, Usage: "cmd.tree_edit.usage", Desc: "cmd.tree_edit.desc",
			Params: []port.CommandParam{
				{Name: "cmd.tree_edit.p1.name", Kind: port.ParamFlag, Desc: "cmd.tree_edit.p1.desc"},
				{Name: "cmd.tree_edit.p2.name", Kind: port.ParamFlag, Desc: "cmd.tree_edit.p2.desc"},
			},
			Run: (*Session).execTreeEdit},
		{Names: []string{"reparent"}, Usage: "cmd.reparent.usage", Desc: "cmd.reparent.desc",
			Params: []port.CommandParam{
				{Name: "cmd.reparent.p1.name", Kind: port.ParamText, Desc: "cmd.reparent.p1.desc"},
				{Name: "cmd.reparent.p2.name", Kind: port.ParamText, Desc: "cmd.reparent.p2.desc"},
				{Name: "cmd.reparent.p3.name", Kind: port.ParamFlag, Desc: "cmd.reparent.p3.desc"},
			},
			Run: (*Session).execReparent},
		{Names: []string{"rm"}, Usage: "cmd.rm.usage", Desc: "cmd.rm.desc",
			Params: []port.CommandParam{{Name: "cmd.rm.p1.name", Kind: port.ParamText, Desc: "cmd.rm.p1.desc"}},
			Run:    (*Session).execRm},
		{Names: []string{"rmconv"}, Usage: "cmd.rmconv.usage", Desc: "cmd.rmconv.desc",
			Params: []port.CommandParam{{Name: "cmd.rmconv.p1.name", Kind: port.ParamText, Repeatable: true, Desc: "cmd.rmconv.p1.desc"}},
			Run:    (*Session).execRmconv},
		{Names: []string{"compact"}, Desc: "cmd.compact.desc", Run: (*Session).execCompact},
		{Names: []string{"summary"}, Usage: "cmd.summary.usage", Desc: "cmd.summary.desc",
			Params: []port.CommandParam{
				{Name: "cmd.summary.p1.name", Kind: port.ParamText, Desc: "cmd.summary.p1.desc"},
				{Name: "cmd.summary.p2.name", Kind: port.ParamText, Desc: "cmd.summary.p2.desc"},
			},
			Run: (*Session).execSummary},
	}
}

// runtimeCommands 收运行期、任务与扩展命令。分段只为把表字面量拆开，不改任何记录；
// 三段的拼接顺序就是 /help 与补全浮层的行序，调段序会改掉帮助输出。
func runtimeCommands() []commandEntry {
	return []commandEntry{
		{Names: []string{"mic"}, Usage: "cmd.mic.usage", Desc: "cmd.mic.desc",
			Params: []port.CommandParam{{Name: "cmd.mic.p1.name", Kind: port.ParamText, Desc: "cmd.mic.p1.desc"}},
			Run:    (*Session).execMic},
		{Names: []string{"permission"}, Usage: "cmd.permission.usage",
			Desc:   "cmd.permission.desc",
			Params: levelParams,
			Run:    (*Session).execPermission},
		{Names: []string{"unattended"}, Usage: "cmd.unattended.usage",
			Desc:   "cmd.unattended.desc",
			Params: onOffParams,
			Run:    argsOnly((*Session).execUnattended)},
		{Names: []string{"memory"}, Usage: "cmd.memory.usage", Desc: "cmd.memory.desc",
			Params: []port.CommandParam{{Name: "cmd.memory.p1.name", Kind: port.ParamText, Desc: "cmd.memory.p1.desc"}},
			Run:    (*Session).execMemory},
		{Names: []string{"usage"}, Desc: "cmd.usage.desc", Run: (*Session).execUsage},
		{Names: []string{"lore"}, Desc: "cmd.lore.desc", Run: (*Session).execLore},
		{Names: []string{"watch"}, Usage: "cmd.watch.usage", Desc: "cmd.watch.desc",
			Params: []port.CommandParam{
				{Name: "cmd.watch.p1.name", Kind: port.ParamChoice, Values: []string{"list", "stop"}, Desc: "cmd.watch.p1.desc"},
				{Name: "cmd.watch.p2.name", Kind: port.ParamText, Desc: "cmd.watch.p2.desc"},
			},
			Run: argsOnly((*Session).watchReport)},
		{Names: []string{"jobs"}, Usage: "cmd.jobs.usage", Desc: "cmd.jobs.desc",
			Params: []port.CommandParam{
				{Name: "cmd.jobs.p1.name", Kind: port.ParamChoice, Values: []string{"list", "logs", "kill"}, Desc: "cmd.jobs.p1.desc"},
				{Name: "cmd.jobs.p2.name", Kind: port.ParamText, Desc: "cmd.jobs.p2.desc"},
				{Name: "cmd.jobs.p3.name", Kind: port.ParamText, Desc: "cmd.jobs.p3.desc"},
			},
			Run: argsOnly((*Session).jobsReport)},
		{Names: []string{"delegate"}, Usage: "cmd.delegate.usage", Desc: "cmd.delegate.desc",
			Params: []port.CommandParam{
				{Name: "cmd.delegate.p1.name", Kind: port.ParamChoice, Values: []string{"list", "kill"}, Desc: "cmd.delegate.p1.desc"},
				{Name: "cmd.delegate.p2.name", Kind: port.ParamText, Desc: "cmd.delegate.p2.desc"},
			},
			Run: argsOnly((*Session).delegateReport)},
		{Names: []string{"skill"}, Usage: "cmd.skill.usage", Desc: "cmd.skill.desc",
			Params: []port.CommandParam{
				{Name: "cmd.skill.p1.name", Kind: port.ParamChoice, Values: []string{"list", "install", "remove", "update"}, Desc: "cmd.skill.p1.desc"},
				{Name: "cmd.skill.p2.name", Kind: port.ParamText, Desc: "cmd.skill.p2.desc"},
				{Name: "cmd.skill.p3.name", Kind: port.ParamText, Desc: "cmd.skill.p3.desc"},
			},
			Run: argsOnly((*Session).execSkill)},
		{Names: []string{"export"}, Usage: "cmd.export.usage", Desc: "cmd.export.desc",
			Params: []port.CommandParam{
				{Name: "cmd.export.p1.name", Kind: port.ParamFlag, ValueName: "cmd.export.p1.valuename", Values: exportFormatValues(),
					Desc: "cmd.export.p1.desc"},
				{Name: "cmd.export.p2.name", Kind: port.ParamFlag, ValueName: "cmd.export.p2.valuename", Desc: "cmd.export.p2.desc"},
				{Name: "cmd.export.p3.name", Kind: port.ParamFlag, Desc: "cmd.export.p3.desc"},
				{Name: "cmd.export.p4.name", Kind: port.ParamFlag, ValueName: "cmd.export.p4.valuename", Repeatable: true,
					Desc: "cmd.export.p4.desc"},
			},
			Run: (*Session).execExport},
		{Names: []string{"import"}, Usage: "cmd.import.usage", Desc: "cmd.import.desc",
			Params: []port.CommandParam{
				{Name: "cmd.import.p1.name", Kind: port.ParamFlag, ValueName: "cmd.import.p1.valuename", Repeatable: true,
					Desc: "cmd.import.p1.desc"},
				{Name: "cmd.import.p2.name", Kind: port.ParamText, Desc: "cmd.import.p2.desc"},
			},
			Run: (*Session).execImport},
		{Names: []string{"character"}, Usage: "cmd.character.usage", Desc: "cmd.character.desc",
			Params: []port.CommandParam{
				{Name: "cmd.character.p1.name", Kind: port.ParamChoice, Values: []string{"list", "show", "import", "start", "remove"}, Desc: "cmd.character.p1.desc"},
				{Name: "cmd.character.p2.name", Kind: port.ParamText, Desc: "cmd.character.p2.desc"},
				{Name: "cmd.character.p3.name", Kind: port.ParamText, Desc: "cmd.character.p3.desc"},
				{Name: "cmd.character.p4.name", Kind: port.ParamText, Desc: "cmd.character.p4.desc"},
			},
			Run: argsOnly((*Session).execCharacter)},
	}
}

// configCommands 收配置、模型与帮助命令。分段只为把表字面量拆开，不改任何记录；
// 三段的拼接顺序就是 /help 与补全浮层的行序，调段序会改掉帮助输出。
func configCommands() []commandEntry {
	return []commandEntry{
		{Names: []string{"quit", "exit"}, Desc: "cmd.quit.desc", Run: quitCommand},
		{Names: []string{"plugin"}, Usage: "cmd.plugin.usage", Desc: "cmd.plugin.desc",
			Params: []port.CommandParam{
				{Name: "cmd.plugin.p1.name", Kind: port.ParamChoice, Values: []string{"list", "enable", "disable", "logs"}, Desc: "cmd.plugin.p1.desc"},
				{Name: "cmd.plugin.p2.name", Kind: port.ParamText, Desc: "cmd.plugin.p2.desc"},
			},
			Run: argsOnly((*Session).execPlugin)},
		{Names: []string{"model"}, Usage: "cmd.model.usage", Desc: "cmd.model.desc",
			Params: []port.CommandParam{
				{Name: "cmd.model.p1.name", Kind: port.ParamText, Desc: "cmd.model.p1.desc"},
				{Name: "cmd.model.p2.name", Kind: port.ParamChoice, Values: []string{"probe"},
					Desc: "cmd.model.p2.desc"},
				{Name: "cmd.model.p3.name", Kind: port.ParamText, Desc: "cmd.model.p3.desc"},
			},
			Run: argsOnly((*Session).execModel)},
		{Names: []string{"think"}, Usage: "cmd.think.usage", Desc: "cmd.think.desc",
			Params: onOffParams,
			Run:    argsOnly((*Session).execThink)},
		{Names: []string{"effort"}, Usage: "cmd.effort.usage",
			Desc: "cmd.effort.desc",
			Params: []port.CommandParam{{
				Name: "cmd.effort.p1.name", Kind: port.ParamChoice, Values: []string{"minimal", "low", "medium", "high", "off"},
				Desc: "cmd.effort.p1.desc",
			}},
			Run: argsOnly((*Session).execEffort)},
		{Names: []string{"help"}, Desc: "cmd.help.desc", Run: helpCommand}}
}

// buildCommandIndex 展开别名建索引；记录不完整或重名属于编码期错误，这里 panic——同一张表里
// 两条记录抢一个名字，静默让后一条覆盖前一条才是真正的危险。
func buildCommandIndex(table []commandEntry) map[string]*commandEntry {
	idx := make(map[string]*commandEntry, len(table))
	for i := range table {
		e := &table[i]
		if len(e.Names) == 0 || e.Desc == "" || e.Run == nil {
			panic("app: 命令注册表有不完整的记录（名字/描述/处理函数缺一项）")
		}
		for _, n := range e.Names {
			if _, dup := idx[n]; dup {
				panic("app: 命令注册表里名字重复: " + n)
			}
			idx[n] = e
		}
	}
	return idx
}

// argsOnly 适配器：只吃参数的处理函数（/skill /jobs 等）接进统一签名。
// 它们不关心 port.Command 的其余字段（如 /edit 才用的 Attachments）。
func argsOnly(f func(s *Session, ctx context.Context, args []string) (string, error)) func(*Session, context.Context, port.Command) (string, error) {
	return func(s *Session, ctx context.Context, cmd port.Command) (string, error) {
		return f(s, ctx, cmd.Args)
	}
}

// quitCommand /quit 与 /exit：返回退出请求，由装配根收尾（app 不依赖具体 UI）。
func quitCommand(*Session, context.Context, port.Command) (string, error) {
	return "", ErrQuit
}

// helpCommand /help：渲染这张表本身 + 动态命令段。
func helpCommand(s *Session, _ context.Context, _ port.Command) (string, error) {
	static, dyn := s.commandsSplit()
	return renderHelp(static, dyn), nil
}

// mcpFamilyLine MCP prompts 家族说明行（D103）：命名规则的说明性文案，非可执行命令、
// 不入清单（真实动态命令逐条入列）——保留在 help 动态段前。文案在 cmd.mcp.family。
func mcpFamilyLine() string { return i18n.T("cmd.mcp.family") }

// CommandCatalog 静态命令清单的只读视图（D245）：供装配根做「教程 / 文档文案 vs 真实命令
// 清单」的对照测试（D141），消费方仍照常用 port.CommandCatalog 逐帧拉清单。
func CommandCatalog() []port.CommandInfo {
	return commandInfos(commandTable)
}

// commandInfos 把注册表投影成端口形状（只取元数据，不暴露处理函数）。
//
// 投影时取词（D275）：表里存的是 key，这里换成当前语言的文案。放在投影而不是声明处，
// 是因为表的填充在 init、语言要到装配根才定；投影是每次 /help 与补全浮层拿清单的必经
// 之路，语言选定后自然生效。清单 ≤ 数十条，每帧翻一遍的开销与已有的深拷贝同级。
//
// Names 逐条拷贝：表是包级只读量，而端口给出去的是 UI 每帧都会拿的清单——把底层切片直接
// 交出去，任何一处（补全浮层、帮助渲染、第三方壳）顺手改动一个名字就会污染整张表，而且
// 下一次 /help 才会露出来。拷贝的成本是几十个短字符串，比排查这类问题的成本低。
func commandInfos(table []commandEntry) []port.CommandInfo {
	out := make([]port.CommandInfo, len(table))
	for i, e := range table {
		out[i] = port.CommandInfo{
			Names:  append([]string(nil), e.Names...),
			Usage:  i18n.T(e.Usage),
			Desc:   i18n.T(e.Desc),
			Params: paramsCopy(e.Params),
		}
	}
	return out
}

// paramsCopy 深拷贝参数声明（口径同 Names）：`Values` 底层数组若直接交出去，UI 侧
// 一次顺手改动就会污染包级只读表，而且下一次 /help 才露出来。
//
// 顺手取词：Name / ValueName / Desc 一律过 T——`--flag`、枚举值、`<id>` 这类纯语法不在表里，
// T 原样返回，所以表里只有真需要翻的那几项才有 key。
func paramsCopy(in []port.CommandParam) []port.CommandParam {
	if len(in) == 0 {
		return nil
	}
	out := make([]port.CommandParam, len(in))
	for i, p := range in {
		p.Name = i18n.T(p.Name)
		p.ValueName = i18n.T(p.ValueName)
		p.Desc = i18n.T(p.Desc)
		p.Values = append([]string(nil), p.Values...)
		out[i] = p
	}
	return out
}

// commandsSplit 当前清单两段：静态段（注册表投影）+ 动态表快照（dynMu 锁内、按名排序）。
//
// 动态命令与静态同名时**不出列**：分派是静态优先（execCommand 先查 commandIndex），
// 出两条同名项会让补全浮层列出两个看起来一样的东西，选哪个却只走静态那条。
func (s *Session) commandsSplit() (static, dyn []port.CommandInfo) {
	static = commandInfos(commandTable)
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	dyn = make([]port.CommandInfo, 0, len(s.dynamic))
	for name := range s.dynamic {
		if _, taken := commandIndex[name]; taken {
			continue // 静态优先：同名动态命令永远分派不到
		}
		if ci, ok := s.dynamicMeta[name]; ok {
			ci.Names = append([]string(nil), ci.Names...)
			dyn = append(dyn, ci)
			continue
		}
		dyn = append(dyn, port.CommandInfo{Names: []string{name}}) // 未带元数据按名生成（D103②）
	}
	sort.Slice(dyn, func(i, j int) bool { return dyn[i].Names[0] < dyn[j].Names[0] })
	return static, dyn
}

// Commands 实现 port.CommandCatalog（D103/S2b-1）：静态在前、动态缀尾的清单快照；
// UI 补全浮层每帧拉取（清单 ≤ 数十条，开销可忽略）。
func (s *Session) Commands() []port.CommandInfo {
	static, dyn := s.commandsSplit()
	out := make([]port.CommandInfo, 0, len(static)+len(dyn))
	out = append(out, static...)
	return append(out, dyn...)
}

// helpPrefix help 行前缀：`/` + names（`, /` 连接，别名一条一行）+ 空格 + usage。
func helpPrefix(ci port.CommandInfo) string {
	p := "/" + strings.Join(ci.Names, ", /")
	if ci.Usage != "" {
		p += " " + ci.Usage
	}
	return p
}

// renderHelp /help 文本（D103②）：清单表驱动渲染，行 = 前缀 + 对齐填充 + desc；
// 对齐列宽 = 静态清单前缀最大显示宽 + 2（动态命令缀尾、按自身前缀宽取 2 起步填充，
// 不参与列宽——避免插件启停抖动 help 布局）。
func renderHelp(static, dyn []port.CommandInfo) string {
	col := 0
	for _, ci := range static {
		if w := displayWidth(helpPrefix(ci)); w > col {
			col = w
		}
	}
	col += 2
	var b strings.Builder
	for _, ci := range static {
		writeHelpLine(&b, ci, col)
	}
	b.WriteString(mcpFamilyLine())
	b.WriteByte('\n')
	for _, ci := range dyn {
		writeHelpLine(&b, ci, col)
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeHelpLine 单行渲染：前缀 + 填充（不足列宽补空格，超出保底 2）+ desc。
func writeHelpLine(b *strings.Builder, ci port.CommandInfo, col int) {
	p := helpPrefix(ci)
	b.WriteString(p)
	if pad := col - displayWidth(p); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	} else {
		b.WriteString("  ")
	}
	b.WriteString(ci.Desc)
	b.WriteByte('\n')
}

// displayWidth 字符串显示宽（D103 help 对齐近似）：CJK/全角 = 2、其余 = 1。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth 单 rune 显示宽（常用 CJK/全角区段；对齐近似足够）。
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 0x1100:
		return 1
	case r <= 0x115F, // Hangul Jamo
		r == 0x2329 || r == 0x232A,
		r >= 0x2E80 && r <= 0x303E, // CJK 部首/符号（除 303F）
		r >= 0x3041 && r <= 0x33FF, // 平假名/片假名/兼容
		r >= 0x3400 && r <= 0x4DBF, // CJK 扩展 A
		r >= 0x4E00 && r <= 0x9FFF, // CJK 统一
		r >= 0xA000 && r <= 0xA4CF, // 彝文
		r >= 0xAC00 && r <= 0xD7A3, // Hangul 音节
		r >= 0xF900 && r <= 0xFAFF, // CJK 兼容
		r >= 0xFE30 && r <= 0xFE4F, // CJK 兼容形式
		r >= 0xFF00 && r <= 0xFF60, // 全角形式
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD: // CJK 扩展 B+
		return 2
	}
	return 1
}
