package app

import (
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// commandCatalog 静态命令清单（D103/S2b-1）：/help 渲染与 port.CommandCatalog 补全
// 数据的单一事实源；动态命令（/mcp:*）由装配根经 SetDynamicCommands 携带元数据合入，
// 不在本表。Usage = 参数用例（不含命令名）、Desc = 一句话描述（help 行与浮层共用）。
var commandCatalog = []port.CommandInfo{
	{Names: []string{"new"}, Usage: "[标题]", Desc: "新建会话"},
	{Names: []string{"list"}, Desc: "列出会话"},
	{Names: []string{"switch"}, Usage: "<id前缀>", Desc: "切换到既有会话（清屏并回放历史，D75）"},
	{Names: []string{"title"}, Usage: "[文本]", Desc: "查看/改写会话标题"},
	{Names: []string{"goto"}, Usage: "<id>", Desc: "Head 移到任意节点（分支导航；id 支持唯一前缀）"},
	{Names: []string{"edit"}, Usage: "<id> [--keep|--copy] [--part N] <文本>",
		Desc: "Revise：缺省 Fresh 开新分支（编辑用户消息即重新生成回答，D93）；--keep 边转移后续历史；--copy 深拷贝后续历史（D97）；--part N 只改第 N 个正文分片、其余分片原样保留（D107）"},
	{Names: []string{"regen"}, Usage: "<id>", Desc: "重新生成：上游用户消息分叉重发（已有回答）；无回答直接生成（D93）"},
	{Names: []string{"branch"}, Usage: "[id]", Desc: "展示同级分叉与下级（Root 显示顶层消息；缺省当前 Head）"},
	{Names: []string{"rm"}, Usage: "<id>", Desc: "删除节点及整棵子树（二次确认）"},
	{Names: []string{"compact"}, Desc: "触发上下文压缩（生成 system 摘要节点，D21）"},
	{Names: []string{"permission"}, Usage: "[等级]", Desc: "查看/切换权限等级（read-only/strict/permissive/full-access）"},
	{Names: []string{"memory"}, Usage: "[会话id]", Desc: "用系统编辑器打开记忆文件（缺省全局 memories.md，D24）"},
	{Names: []string{"usage"}, Desc: "查看 token 用量（上下文占用/上轮实测/会话累计，三级计数链 D26）"},
	{Names: []string{"jobs"}, Usage: "[list|logs <id> [行数]|kill <id>]", Desc: "后台任务管理（job_start 启动的任务，DESIGN §7.3）"},
	{Names: []string{"quit", "exit"}, Desc: "退出"},
	{Names: []string{"plugin"}, Usage: "[list|enable <name>|disable <name>]", Desc: "MCP 插件管理（D31，DESIGN §7.3）"},
	{Names: []string{"model"}, Usage: "[name]", Desc: "查看可用模型 / 切换并写回 config（D32）"},
	{Names: []string{"think"}, Usage: "[on|off]", Desc: "原生思考总开关（写回 config，D34；off 覆盖 /effort）"},
	{Names: []string{"effort"}, Usage: "[档位]", Desc: "推理档位 minimal|low|medium|high|off（写回 config，D34）"},
}

// mcpFamilyLine MCP prompts 家族说明行（D103）：命名规则的说明性文案，非可执行命令、
// 不入清单（真实动态命令逐条入列）——保留在 help 动态段前。
const mcpFamilyLine = "/mcp:<server>:<prompt>  MCP prompts 动态命令（随插件启停注册，见 /plugin）"

// commandsSplit 当前清单两段：静态表（包级不可变）+ 动态表快照（dynMu 锁内、按名排序）。
func (s *Session) commandsSplit() (static, dyn []port.CommandInfo) {
	static = commandCatalog
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	dyn = make([]port.CommandInfo, 0, len(s.dynamic))
	for name := range s.dynamic {
		if ci, ok := s.dynamicMeta[name]; ok {
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
