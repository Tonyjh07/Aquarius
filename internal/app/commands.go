package app

import (
	"context"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// commandEntry 命令注册表的一条记录（D245）：元数据与处理函数同一行。
//
// 此前元数据在 commandCatalog、执行在 execCommand 的 switch 里，两处维护同一条事实，
// 靠一个从 switch 反推分支名的测试兜底。现在 /help、补全浮层与分派读同一张表：
// 加一条命令 = 这里加一行（DESIGN §7.3）。
type commandEntry struct {
	// Names 命令名集合（不含斜杠；首名为主名，其余为别名——quit/exit 一条两名）。
	Names []string
	// Usage 参数用例（不含命令名本身；空 = 无参数）。
	Usage string
	// Desc 一句话描述（/help 行与补全浮层共用）。
	Desc string
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
	commandTable = []commandEntry{
		{Names: []string{"new"}, Usage: "[标题]", Desc: "新建会话", Run: (*Session).execNew},
		{Names: []string{"list"}, Desc: "列出会话", Run: (*Session).execList},
		{Names: []string{"switch"}, Usage: "<id前缀>", Desc: "切换到既有会话（清屏并回放历史）", Run: (*Session).execSwitch},
		{Names: []string{"title"}, Usage: "[--id <id前缀>] [文本]", Desc: "查看/改写会话标题（--id 可改非当前会话）", Run: (*Session).execTitle},
		{Names: []string{"goto"}, Usage: "<id>", Desc: "Head 移到任意节点（分支导航；id 支持唯一前缀）", Run: (*Session).execGoto},
		{Names: []string{"edit"}, Usage: "<id> [--keep|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]",
			Desc: "修改消息：缺省开新分支（等同重新回答）；--keep 把后续历史转到新分支；--copy 深拷贝后续历史；--part N 只改第 N 个正文分片、其余原样保留；--attach 追加附件、--drop N 删除第 N 个附件（0 起）、--drop-all 删除全部附件，带附件操作时文本可省（只对用户消息）",
			Run:  (*Session).execEdit},
		{Names: []string{"regen"}, Usage: "<id>", Desc: "重新生成：已有回答则在其上游分叉重发；无回答则直接生成", Run: (*Session).execRegen},
		{Names: []string{"respond"}, Usage: "<id>",
			Desc: "从所选节点继续生成一条响应：把其水位以上至该节点的上下文发给模型（已有子节点则开新分支）",
			Run:  (*Session).execRespond},
		{Names: []string{"inject"}, Usage: "[--source <名>] [--no-trigger] <文本>",
			Desc: "手动注入一条上下文（入树为注入节点）：默认起一轮响应，--no-trigger 只塞进上下文；--source 标注来源",
			Run:  (*Session).execInject},
		{Names: []string{"branch"}, Usage: "[id]", Desc: "展示同级分叉与下级（Root 显示顶层消息；缺省当前 Head）", Run: (*Session).execBranch},
		{Names: []string{"rm"}, Usage: "<id>", Desc: "删除节点及整棵子树（二次确认）", Run: (*Session).execRm},
		{Names: []string{"rmconv"}, Usage: "<id前缀>", Desc: "删除整个会话（二次确认；删当前会话会自动切换到另一个）", Run: (*Session).execRmconv},
		{Names: []string{"compact"}, Desc: "触发上下文压缩（把较早的往来压成一条摘要）", Run: (*Session).execCompact},
		{Names: []string{"summary"}, Usage: "<id> [文本]",
			Desc: "在节点之前插入摘要（成为新的水位）：带文本 = 手写；不带 = 让当前模型总结该节点之上的历史",
			Run:  (*Session).execSummary},
		{Names: []string{"mic"}, Usage: "[秒数]",
			Desc: "录一段语音，转成文字后作为输入发送（缺省 60 秒，最长 4 分钟）",
			Run:  (*Session).execMic},
		{Names: []string{"permission"}, Usage: "[等级]", Desc: "查看/切换权限等级（read-only/strict/permissive/full-access）", Run: (*Session).execPermission},
		{Names: []string{"unattended"}, Usage: "[on|off]",
			Desc: "无人值守：越界操作当场拒绝并说明、不再等人确认（权限等级不变；无参 = 切换）",
			Run:  argsOnly((*Session).execUnattended)},
		{Names: []string{"memory"}, Usage: "[会话id]", Desc: "用系统编辑器打开记忆文件（缺省为全局 memories.md）", Run: (*Session).execMemory},
		{Names: []string{"usage"}, Desc: "查看 token 用量（上下文占用 / 上轮实测 / 会话累计）", Run: (*Session).execUsage},
		{Names: []string{"watch"}, Usage: "[list|stop <id>]", Desc: "观察器：模型的声明式定时观察（看命令/文件/网页命中什么），查看或叫停",
			Run: argsOnly((*Session).watchReport)},
		{Names: []string{"jobs"}, Usage: "[list|logs <id> [行数]|kill <id>]", Desc: "后台任务管理（查看、日志、终止）",
			Run: argsOnly((*Session).jobsReport)},
		{Names: []string{"skill"}, Usage: "[list|<名字>|install <路径或链接>|remove <名字>|update [名字|all]]",
			Desc: "技能：列出已安装技能 / 把某个技能的指令载入上下文（不触发回答）/ 安装、卸载或更新",
			Run:  argsOnly((*Session).execSkill)},
		{Names: []string{"export"}, Usage: "[--format <名字>] [--path <文件>]",
			Desc: "导出当前会话（缺省 aqx 无损，可原样导回；chatgpt 带全部分支；openai/sillytavern 只导当前那条链）",
			Run:  (*Session).execExport},
		{Names: []string{"import"}, Usage: "<文件>",
			Desc: "导入外部会话（按扩展名与文件头认格式；落成一个新会话，不覆盖既有会话）",
			Run:  (*Session).execImport},
		{Names: []string{"quit", "exit"}, Desc: "退出", Run: quitCommand},
		{Names: []string{"plugin"}, Usage: "[list|enable <name>|disable <name>]", Desc: "MCP 插件管理（列表、启用、停用）",
			Run: argsOnly((*Session).execPlugin)},
		{Names: []string{"model"}, Usage: "[name] | probe [name]", Desc: "查看可用模型 / 切换并写回配置 / 实测能力（probe）",
			Run: argsOnly((*Session).execModel)},
		{Names: []string{"think"}, Usage: "[on|off]", Desc: "原生思考总开关（写回配置；off 优先于 /effort）",
			Run: argsOnly((*Session).execThink)},
		{Names: []string{"effort"}, Usage: "[档位]", Desc: "推理档位 minimal|low|medium|high|off（写回配置）",
			Run: argsOnly((*Session).execEffort)},
		{Names: []string{"help"}, Desc: "列出全部命令（静态清单 + 已注册的动态命令）", Run: helpCommand},
	}
	commandIndex = buildCommandIndex(commandTable)
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
// 不入清单（真实动态命令逐条入列）——保留在 help 动态段前。
const mcpFamilyLine = "/mcp:<server>:<prompt>  MCP prompts 动态命令（随插件启停注册，见 /plugin）"

// CommandCatalog 静态命令清单的只读视图（D245）：供装配根做「教程 / 文档文案 vs 真实命令
// 清单」的对照测试（D141），消费方仍照常用 port.CommandCatalog 逐帧拉清单。
func CommandCatalog() []port.CommandInfo {
	return commandInfos(commandTable)
}

// commandInfos 把注册表投影成端口形状（只取元数据，不暴露处理函数）。
//
// Names 逐条拷贝：表是包级只读量，而端口给出去的是 UI 每帧都会拿的清单——把底层切片直接
// 交出去，任何一处（补全浮层、帮助渲染、第三方壳）顺手改动一个名字就会污染整张表，而且
// 下一次 /help 才会露出来。拷贝的成本是几十个短字符串，比排查这类问题的成本低。
func commandInfos(table []commandEntry) []port.CommandInfo {
	out := make([]port.CommandInfo, len(table))
	for i, e := range table {
		out[i] = port.CommandInfo{Names: append([]string(nil), e.Names...), Usage: e.Usage, Desc: e.Desc}
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
	b.WriteString(mcpFamilyLine)
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
