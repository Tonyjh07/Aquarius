package i18n

// zhEditTexts 会话编辑、人格（character）、技能与注入：/edit、/character、/skill、/inject
// 的用法、回执与报错。
//
// 回执是分行的：一行一句事实，拼接交给调用方（每行一条 key）。错误链短语也只放短语，
// `%w` 留在 Go 里——把动词写进表等于让每份译文各自维护格式串，一处写错就是错误链断掉。
var zhEditTexts = map[string]string{

	// 用法
	"usage.edit":      "用法: /edit <id> [--keep|--copy] [--part N] [--attach <路径>]... [--drop N]... [--drop-all] [<文本>]（缺省 Fresh 开新分支；--keep 转移后续历史；--copy 复制后续历史；--part N 只改第 N 个正文分片，0 起；--attach 追加附件；--drop N 删除第 N 个附件，0 起；带附件操作时文本可省 = 只改附件）",
	"usage.regen":     "用法: /regen <id>（id 可为用户或助手消息，支持唯一前缀）",
	"usage.character": "用法: /character list | /character show <角色> | /character import <卡文件> | /character start <角色> [开场白序号] | /character remove <角色>",
	"usage.skill":     "用法: /skill list | /skill <名字> | /skill install <路径或链接> | /skill remove <名字> | /skill update [名字|all]",
	"usage.inject":    "用法: /inject [--source <名>] [--no-trigger] <文本>",
	"usage.respond":   "用法: /respond <id>（id 支持唯一前缀）",

	// /edit 与 /regen 的回执
	"edit.branch.kept":    "旧分支保留",
	"edit.branch.moved":   "后续历史已转移",
	"edit.branch.copied":  "后续历史已复制",
	"edit.note.attach":    "，{note}",
	"edit.note.regen":     "，已重新生成回答",
	"edit.revised":        "已修订 {id} → {new}（{mode}，{desc}；Head → {head}）",
	"edit.regen.forked":   "已重新生成（旧回答保留为分支；Head → {head}）",
	"edit.regen.done":     "已重新生成（Head → {head}）",
	"edit.attach.added":   "新增 {n} 个附件",
	"edit.attach.removed": "移除 {n} 个附件",
	"edit.attach.join":    "、",

	// /edit 的报错
	"edit.attach_user_only":   "附件增删只适用于用户消息",
	"edit.persona_keep":       "编辑人格要用 --keep（转移历史）：另起新分支或复制历史会把现有对话留在旧分支，看起来像被清空",
	"edit.revise":             "修订",
	"edit.move_head":          "移动 Head",
	"edit.resend_user":        "重发用户消息",
	"edit.missing_node_id":    "缺少节点 id",
	"edit.node_missing":       "会话树中没有节点 {id}（/branch 可查看同级 id）",
	"edit.node_ambiguous":     "节点标识 {id} 有歧义（命中 {n} 个: {hits}），请加长前缀",
	"edit.node_not_in_tree":   "节点 {id} 不在会话树中",
	"edit.no_upstream_user":   "该消息上游没有用户消息，无法重新生成",
	"edit.drop_range":         "附件序号 {n} 越界（该节点共 {total} 个附件）",
	"edit.attach_abs_path":    "附件路径必须是绝对路径（本产品无工作区概念）：{path}",
	"edit.attach_limit_added": "一条消息最多 {max} 个附件（本次新增 {added} 个）",
	"edit.attach_limit_total": "一条消息最多 {max} 个附件（修订后会有 {total} 个）",
	"edit.ingest_attach":      "摄取新附件",
	"edit.empty_after_edit":   "修订后至少要有正文或一个附件（本次把内容删空了）",
	"edit.part_range":         "正文分片 {n} 越界（该节点共 {total} 个正文分片）",
	"edit.conv_missing":       "没有会话 {id}（/list 查看可用会话）",
	"edit.conv_ambiguous":     "会话标识 {id} 有歧义（命中 {n} 个: {hits}），请加长前缀",
	"edit.text_empty":         "（修订文本不可为空）",

	// /character 的清单与详情
	"char.list.empty_head":  "还没有导入任何角色卡。\n",
	"char.list.empty_hint":  "用 /character import <卡文件> 导入一张（PNG / JSON / .charx）。",
	"char.list.head":        "已导入 {n} 张角色卡：\n",
	"char.list.item":        "- {display}（{slug}） · {source}/{spec} · 世界书 {book} 条 · 备用开场白 {greetings} 条\n",
	"char.list.hint":        "用 /character show <角色> 看详情，/character start <角色> 开新会话。",
	"char.show.head":        "角色卡：{display}（{slug}）\n",
	"char.show.spec_source": "规范：{spec} ｜ 来源：{source}",
	"char.show.imported":    " ｜ 导入：{at}",
	"char.start_hint":       "\n用 /character start {slug} 开新会话。",
	"char.meta.author":      "作者：{name}",
	"char.meta.version":     "版本：{version}",
	"char.meta.tags":        "标签：{tags}",
	"char.meta.join":        " ｜ ",
	"char.list_join":        "、",
	"char.warnings.head":    "\n注意（{n} 条）：\n",
	"char.warnings.item":    "- {text}\n",

	// /character 的导入、删除与报错
	"char.unavailable":         "未配置角色卡目录，/character 不可用",
	"char.not_found":           "没有名为 {slug} 的角色卡（/character list 查看清单）",
	"char.admin_missing":       "本构建未接入角色卡导入面",
	"char.imported":            "已导入角色卡「{display}」（{slug}）\n",
	"char.bad_opening_index":   "（开场白序号是整数）",
	"char.confirm_remove":      "确认删除角色卡 {slug}？已用它开过的会话不受影响（卡文本在会话里是快照）",
	"char.confirm_remove_fail": "确认删除角色卡",
	"char.remove_canceled":     "已取消删除角色卡 {slug}",
	"char.removed":             "已删除角色卡 {slug}",
	"char.load":                "载入角色卡",
	"char.new_conversation":    "新建角色会话",
	"char.write_persona":       "写入角色 persona 节点",
	"char.write_examples":      "写入角色示例节点",
	"char.write_greeting":      "写入开场白",
	"char.greeting_range":      "开场白序号 {n} 越界：这张卡共有 {total} 条可选",

	// 角色会话的回执与人格模式
	"char.mode.append":  "用户人格在最前，卡的角色设定接在后面（{{original}} 按空处理）",
	"char.mode.replace": "卡的 system_prompt 顶掉用户人格（{{original}} 展开为用户人格正文，环境信息保留）",
	"char.started":      "已用角色「{display}」开始新会话 {id}\n人格模式：{mode}",

	// 角色卡回执正文（四类 + 两类提示）
	"char.report.book_injected":  "世界书：共 {total} 条，其中 {constant} 条常驻条目已注入人格（每轮都付 token）",
	"char.report.book_kept":      "世界书：共 {total} 条全部保留未注入，其中 {constant} 条是常驻条目（当前开关下不注入）",
	"char.report.assets_head":    "资产：",
	"char.report.assets_icon":    "图标已用作头像",
	"char.report.assets_no_icon": "没有图标",
	"char.report.assets_extra":   "；其余 {n} 个资产随原包保留，本版不内联、不下载",
	"char.report.unsupported":    "不执行的机制：{list} —— 已随原卡保留，本版一律不运行",
	"char.report.display_only":   "只展示字段：作者备注、标签、作者、版本、导入日期只作展示，不进模型上下文。",
	"char.report.html":           "界面提示：卡的文本里有 HTML 标签，界面会按纯文本显示，标签会原样出现。",
	"char.report.opening_long":   "开场白偏长：第 {index} 条约 {tokens} tokens（压缩阈值四分之一约 {budget}），会占掉首轮可用上下文的一大块；卡文本原样使用，不截断。",

	// /skill 的清单与载入
	"skill.unavailable":        "未配置技能目录，/skill 不可用",
	"skill.no_space":           "（技能名里不含空格）",
	"skill.not_found":          "没有名为 {name} 的技能（/skill 查看已安装的技能）",
	"skill.queued":             "技能已入队（未被排空）",
	"skill.loaded":             "已载入技能 {name}（正文已进上下文，未起响应；下一步直接说你要做什么）\n目录：{dir}",
	"skill.list.empty_head":    "还没有安装任何技能。\n",
	"skill.list.empty_hint":    "把技能目录放到 <profile>/skills/<名字>/SKILL.md 就生效（或在 config 的 skills.dirs 里加目录）。",
	"skill.list.head":          "已安装 {n} 个技能：\n",
	"skill.list.item":          "- {name} — {desc}\n",
	"skill.list.source":        "  来源：{source}\n",
	"skill.list.hint":          "用 /skill <名字> 把某个技能载入上下文；模型也会在需要时自己载入。",
	"skill.list.warnings_head": "\n未加载的条目（{n} 条）：\n",
	"skill.list.warning_item":  "- {text}\n",

	// /skill 的安装面
	"skill.admin_missing":       "本构建未接入技能安装面",
	"skill.verb.installed":      "已安装",
	"skill.verb.updated":        "已更新",
	"skill.confirm_remove":      "确认卸载技能 {name}？只删除安装目录里的这份副本，外部技能目录不受影响",
	"skill.confirm_remove_fail": "确认卸载技能",
	"skill.remove_canceled":     "已取消卸载技能 {name}",
	"skill.removed":             "已卸载技能 {name}（安装目录里的副本已删除）",
	"skill.report.zero":         "{verb} 0 个技能",
	"skill.report.head":         "{verb} {n} 个技能：\n",
	"skill.report.item":         "- {name}\n",
	"skill.report.skipped_head": "\n未安装的条目（{n} 条）：\n",
	"skill.report.footer":       "\n用 /skill list 查看清单；技能里的 scripts/ 仍要经工具确认才会执行。",

	// /inject 的回执
	"inject.queued":           "注入已入队（未被排空）",
	"inject.state.no_trigger": "未触发响应",
	"inject.state.triggered":  "已触发响应",
	"inject.done":             "已注入一条 {source} 上下文（{state}；Head → {head}）",
	"inject.usage_error":      "{usage}（{reason}）",

	// /inject 与注入管线的报错
	"inject.queue_full":       "注入队列已满（稍后重试）",
	"inject.empty":            "注入内容为空",
	"inject.invalid":          "注入内容非法",
	"inject.announce":         "注入落地呈现",
	"inject.land":             "注入落地",
	"inject.elsewhere_load":   "异地落地读取会话 {id}",
	"inject.elsewhere_append": "异地落地会话 {id}",
	"inject.elsewhere_save":   "异地落地保存会话 {id}",

	// /respond
	"respond.root":      "不能在会话根节点生成响应（根即会话本身，没有可续写的上下文）",
	"respond.move_head": "移动 Head",
	"respond.done":      "已从该节点生成响应（Head → {head}）",
}
