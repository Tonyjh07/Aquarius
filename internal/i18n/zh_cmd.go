package i18n

// zhCmdTexts 命令面文案母版：/help 的行、补全浮层的一段、参数说明，以及 /mcp 家族那行。
//
// key 形如 cmd.<主名>.desc / .usage / .p<n>.name / .p<n>.desc / .p<n>.valuename，
// n 是 Params 数组下标（1 起）。命令名、取值（on/off、read-only 这类）、纯语法占位
// （<id>、N）不进表——它们是命令语法的一部分，不是文案。
//
// 共享参数（开关类、权限等级）用 cmd.shared.*：一组只写一遍，改一处两个命令同时生效。
var zhCmdTexts = map[string]string{

	// 共享参数声明
	"cmd.shared.onoff.p1.name": "开关",
	"cmd.shared.onoff.p1.desc": "不带参数 = 切换当前状态",
	"cmd.shared.level.p1.name": "等级",
	"cmd.shared.level.p1.desc": "文件类看路径、工具类看风险档，两列互不叠加；不带参数 = 查看当前",

	// /new
	"cmd.new.desc":    "新建会话",
	"cmd.new.usage":   "[标题]",
	"cmd.new.p1.name": "标题",
	"cmd.new.p1.desc": "会话标题；省略则用首条消息自动起名",

	// /list
	"cmd.list.desc": "列出会话",

	// /switch
	"cmd.switch.desc":    "切换到既有会话（清屏并回放历史）",
	"cmd.switch.usage":   "<id 前缀>",
	"cmd.switch.p1.name": "<id 前缀>",
	"cmd.switch.p1.desc": "会话 id 的唯一前缀（/list 里能看到）",

	// /title
	"cmd.title.desc":         "查看/改写会话标题（--id 可改非当前会话）",
	"cmd.title.usage":        "[--id <id 前缀>] [文本]",
	"cmd.title.p1.name":      "--id",
	"cmd.title.p1.valuename": "<id 前缀>",
	"cmd.title.p1.desc":      "改哪个会话；省略 = 当前会话",
	"cmd.title.p2.name":      "文本",
	"cmd.title.p2.desc":      "新标题；省略 = 只查看",

	// /goto
	"cmd.goto.desc":    "Head 移到任意节点（分支导航；id 支持唯一前缀）",
	"cmd.goto.usage":   "<id>",
	"cmd.goto.p1.name": "<id>",
	"cmd.goto.p1.desc": "目标节点 id（支持唯一前缀）",

	// /edit
	"cmd.edit.desc": "修改消息：缺省开新分支（等同重新回答）；--keep 把后续历史转到新分支；" +
		"--copy 深拷贝后续历史；--part N 只改第 N 个正文分片、其余原样保留；" +
		"--attach 追加附件、--drop N 删除第 N 个附件（0 起）、--drop-all 删除全部附件，" +
		"带附件操作时文本可省（只对用户消息）",
	"cmd.edit.usage":        "<id> [--keep|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]",
	"cmd.edit.p1.name":      "<id>",
	"cmd.edit.p1.desc":      "要改的消息 id（支持唯一前缀）",
	"cmd.edit.p2.name":      "--keep",
	"cmd.edit.p2.desc":      "把后续历史转到新分支（人格只能这么改）",
	"cmd.edit.p3.name":      "--copy",
	"cmd.edit.p3.desc":      "把后续历史深拷贝成独立副本",
	"cmd.edit.p4.name":      "--part",
	"cmd.edit.p4.valuename": "N",
	"cmd.edit.p4.desc":      "只改第 N 个正文分片（0 起），其余原样保留",
	"cmd.edit.p5.name":      "--attach",
	"cmd.edit.p5.valuename": "<路径>",
	"cmd.edit.p5.desc":      "追加一个附件；可给多次",
	"cmd.edit.p6.name":      "--drop",
	"cmd.edit.p6.valuename": "N",
	"cmd.edit.p6.desc":      "删除第 N 个附件（0 起）；可给多次",
	"cmd.edit.p7.name":      "--drop-all",
	"cmd.edit.p7.desc":      "删掉全部附件",
	"cmd.edit.p8.name":      "文本",
	"cmd.edit.p8.desc":      "新正文；带附件操作时可省略 = 只改附件",

	// /regen
	"cmd.regen.desc":    "重新生成：已有回答则在其上游分叉重发；无回答则直接生成",
	"cmd.regen.usage":   "<id>",
	"cmd.regen.p1.name": "<id>",
	"cmd.regen.p1.desc": "用户或助手消息 id；取这条消息的上游用户消息重发",

	// /respond
	"cmd.respond.desc":    "从所选节点继续生成一条响应：把该节点水位以上的上下文发给模型；已有子节点时开新分支",
	"cmd.respond.usage":   "<id>",
	"cmd.respond.p1.name": "<id>",
	"cmd.respond.p1.desc": "任意非根节点 id（人格/系统/工具节点也行，根不行）",

	// /inject
	"cmd.inject.desc":         "手动注入一条上下文（入树为注入节点）：默认起一轮响应，--no-trigger 只塞进上下文；--source 标注来源",
	"cmd.inject.usage":        "[--source <名>] [--no-trigger] <文本>",
	"cmd.inject.p1.name":      "--source",
	"cmd.inject.p1.valuename": "<名>",
	"cmd.inject.p1.desc":      "标注来源（显示在节点上）",
	"cmd.inject.p2.name":      "--no-trigger",
	"cmd.inject.p2.desc":      "只塞进上下文，不起一轮响应",
	"cmd.inject.p3.name":      "文本",
	"cmd.inject.p3.desc":      "要注入的上下文文本",

	// /branch
	"cmd.branch.desc":    "展示同级分叉与下级（Root 显示顶层消息；缺省当前 Head）",
	"cmd.branch.usage":   "[id]",
	"cmd.branch.p1.name": "id",
	"cmd.branch.p1.desc": "看哪一处的分叉；省略 = 当前 Head",

	// /tree-edit
	"cmd.tree_edit.desc":    "树编辑模式：改挂只改内存、退出时统一保存；--done 保存退出，--cancel 放弃全部改动",
	"cmd.tree_edit.usage":   "[--done|--cancel]",
	"cmd.tree_edit.p1.name": "--done",
	"cmd.tree_edit.p1.desc": "保存并退出（有改动才比对上下文条数）",
	"cmd.tree_edit.p2.name": "--cancel",
	"cmd.tree_edit.p2.desc": "放弃改动并退出（树回到进入时的样子）",

	// /reparent
	"cmd.reparent.desc":    "改父：整棵子树挂到新父下、成为末子；--undo 反做最近一次搬运（新父不能是 Root、不能是自己或后代）",
	"cmd.reparent.usage":   "<id> <新父> | --undo",
	"cmd.reparent.p1.name": "<id>",
	"cmd.reparent.p1.desc": "要挪的节点 id（连同整棵子树）",
	"cmd.reparent.p2.name": "<新父>",
	"cmd.reparent.p2.desc": "挂到哪个节点下；不能是 Root、自己或自己的后代",
	"cmd.reparent.p3.name": "--undo",
	"cmd.reparent.p3.desc": "反做最近一次搬运（转移或改父），不留新记录",

	// /rm
	"cmd.rm.desc":    "删除节点及整棵子树（二次确认）",
	"cmd.rm.usage":   "<id>",
	"cmd.rm.p1.name": "<id>",
	"cmd.rm.p1.desc": "要删的节点 id（连同整棵子树）",

	// /rmconv
	"cmd.rmconv.desc":    "删除整个会话（二次确认；删当前会话会自动切换到另一个）",
	"cmd.rmconv.usage":   "<id 前缀> [<id 前缀>…]",
	"cmd.rmconv.p1.name": "<id 前缀>",
	"cmd.rmconv.p1.desc": "要删的会话 id 唯一前缀；可给多个，一次确认全部删除",

	// /compact
	"cmd.compact.desc": "触发上下文压缩（把较早的往来压成一条摘要）",

	// /summary
	"cmd.summary.desc":    "在节点之前插入摘要（成为新的水位）：带文本 = 手写；不带 = 让当前模型总结该节点之上的历史",
	"cmd.summary.usage":   "<id> [文本]",
	"cmd.summary.p1.name": "<id>",
	"cmd.summary.p1.desc": "摘要插在这个节点之前",
	"cmd.summary.p2.name": "文本",
	"cmd.summary.p2.desc": "手写摘要；省略 = 让模型总结该节点之上的历史",

	// /mic
	"cmd.mic.desc":    "录一段语音，转成文字后作为输入发送（缺省 60 秒，最长 4 分钟）",
	"cmd.mic.usage":   "[秒数]",
	"cmd.mic.p1.name": "秒数",
	"cmd.mic.p1.desc": "录音时长；缺省 60，上限由厂商音频体积决定",

	// /permission
	"cmd.permission.desc":  "查看/切换权限等级（read-only/strict/permissive/full-access）",
	"cmd.permission.usage": "[等级]",

	// /unattended
	"cmd.unattended.desc":  "无人值守：越界操作当场拒绝并说明、不再等人确认（权限等级不变；无参 = 切换）",
	"cmd.unattended.usage": "[on|off]",

	// /memory
	"cmd.memory.desc":    "用系统编辑器打开记忆文件（缺省为全局 memories.md）",
	"cmd.memory.usage":   "[会话 ID]",
	"cmd.memory.p1.name": "会话 ID",
	"cmd.memory.p1.desc": "开会话记忆；省略 = 全局 memories.md",

	// /usage
	"cmd.usage.desc": "查看 token 用量（上下文占用 / 上轮实测 / 会话累计）",

	// /watch
	"cmd.watch.desc":    "观察器：模型的声明式定时观察（看命令/文件/网页命中什么），查看或叫停",
	"cmd.watch.usage":   "[list|stop <id>]",
	"cmd.watch.p1.name": "子命令",
	"cmd.watch.p1.desc": "省略 = list",
	"cmd.watch.p2.name": "<id>",
	"cmd.watch.p2.desc": "stop 时指定观察器 id",

	// /jobs
	"cmd.jobs.desc":    "后台任务管理（查看、日志、终止）",
	"cmd.jobs.usage":   "[list|logs <id> [行数]|kill <id>]",
	"cmd.jobs.p1.name": "子命令",
	"cmd.jobs.p1.desc": "省略 = list",
	"cmd.jobs.p2.name": "<id>",
	"cmd.jobs.p2.desc": "logs / kill 时的任务 id",
	"cmd.jobs.p3.name": "行数",
	"cmd.jobs.p3.desc": "logs 时取末尾几行",

	// /delegate
	"cmd.delegate.desc":    "外部代理转交：查看配置里的代理与可用性、终止一次还没跑完的转交（派活由模型经 delegate 工具发起）",
	"cmd.delegate.usage":   "[list|kill <id>]",
	"cmd.delegate.p1.name": "子命令",
	"cmd.delegate.p1.desc": "省略 = list",
	"cmd.delegate.p2.name": "<id>",
	"cmd.delegate.p2.desc": "kill 时的转交 id",

	// /skill
	"cmd.skill.desc":    "技能：列出已安装技能 / 把某个技能的指令载入上下文（不触发回答）/ 安装、卸载或更新",
	"cmd.skill.usage":   "[list|<名字>|install <路径或链接>|remove <名字>|update [名字|all]]",
	"cmd.skill.p1.name": "子命令",
	"cmd.skill.p1.desc": "省略 = list；也可直接写技能名",
	"cmd.skill.p2.name": "<名字>",
	"cmd.skill.p2.desc": "技能名（载入 / 卸载 / 更新）",
	"cmd.skill.p3.name": "<路径或链接>",
	"cmd.skill.p3.desc": "install 的来源：单文件、文件夹、压缩包或链接",

	// /export
	"cmd.export.desc": "导出会话（缺省只导当前会话；aqx 无损可原样导回；chatgpt 带全部旁支；" +
		"openai/sillytavern 只导当前那条链；--all 整库、--ids 只导列出的几个，两者都要 aqx）",
	"cmd.export.usage":        "[--format <名字>] [--path <文件>] [--all | --ids <id>[,<id>…]]",
	"cmd.export.p1.name":      "--format",
	"cmd.export.p1.valuename": "<名字>",
	"cmd.export.p1.desc":      "导出格式；缺省 aqx",
	"cmd.export.p2.name":      "--path",
	"cmd.export.p2.valuename": "<文件>",
	"cmd.export.p2.desc":      "落点；省略 = 导出目录里按标题起名",
	"cmd.export.p3.name":      "--all",
	"cmd.export.p3.desc":      "把整库写进一份 aqx 备份（与 --ids 互斥）",
	"cmd.export.p4.name":      "--ids",
	"cmd.export.p4.valuename": "<id>[,<id>…]",
	"cmd.export.p4.desc":      "只导出这些会话（支持唯一前缀；逗号分隔或给多次）",

	// /import
	"cmd.import.desc":         "导入外部会话（按扩展名与文件头认格式；落成新会话，不覆盖既有会话；一份 aqx 可只取其中几个）",
	"cmd.import.usage":        "[--pick <序号>]… <文件>",
	"cmd.import.p1.name":      "--pick",
	"cmd.import.p1.valuename": "<序号>",
	"cmd.import.p1.desc":      "只导入清单里的第几项（1 起；逗号分隔或给多次；省略 = 全部）",
	"cmd.import.p2.name":      "<文件>",
	"cmd.import.p2.desc":      "要导入的文件；路径有空格时用引号括起来",

	// /character
	"cmd.character.desc":    "角色卡：列出 / 查看 / 导入 / 用卡开新会话 / 删除（只读导入）",
	"cmd.character.usage":   "[list|show <slug>|import <路径>|start <slug> [序号]|remove <slug>]",
	"cmd.character.p1.name": "子命令",
	"cmd.character.p1.desc": "省略 = list",
	"cmd.character.p2.name": "<slug>",
	"cmd.character.p2.desc": "角色目录名（show / start / remove；list 里能看到）",
	"cmd.character.p3.name": "<路径>",
	"cmd.character.p3.desc": "import 的卡文件：PNG / JSON / .charx",
	"cmd.character.p4.name": "序号",
	"cmd.character.p4.desc": "start 用第几条开场白；省略或 1 = 首条",

	// /quit
	"cmd.quit.desc": "退出",

	// /plugin
	"cmd.plugin.desc":    "MCP 插件管理（列表、启用、停用）",
	"cmd.plugin.usage":   "[list|enable <name>|disable <name>]",
	"cmd.plugin.p1.name": "子命令",
	"cmd.plugin.p1.desc": "省略 = list",
	"cmd.plugin.p2.name": "<name>",
	"cmd.plugin.p2.desc": "插件名（enable / disable / logs）",

	// /model
	"cmd.model.desc":    "查看可用模型 / 切换并写回配置 / 实测能力（probe）",
	"cmd.model.usage":   "[name] | probe [name]",
	"cmd.model.p1.name": "name",
	"cmd.model.p1.desc": "模型名；直接写 = 切换到这个模型",
	"cmd.model.p2.name": "probe",
	"cmd.model.p2.desc": "实测能力：工具调用 / 图片输入 / 思考各试一次，只写回判定明确的位",
	"cmd.model.p3.name": "<name>",
	"cmd.model.p3.desc": "probe 要试哪个模型；省略 = 当前模型",

	// /think
	"cmd.think.desc":  "原生思考总开关（写回配置；off 优先于 /effort）",
	"cmd.think.usage": "[on|off]",

	// /effort
	"cmd.effort.desc":    "推理档位 minimal|low|medium|high|off（写回配置）",
	"cmd.effort.usage":   "[档位]",
	"cmd.effort.p1.name": "档位",
	"cmd.effort.p1.desc": "off = 清除档位；是否真发由 /think 决定",

	// /help
	"cmd.help.desc": "列出全部命令（静态清单 + 已注册的动态命令）",

	// /help 动态段前的家族说明
	"cmd.mcp.family": "/mcp:<server>:<prompt>  MCP prompts 动态命令（随插件启停注册，见 /plugin）",
}
