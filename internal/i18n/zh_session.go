package i18n

// zhSessionTexts 会话命令的回执、用法行与报告（/compact、/unattended、/permission、
// /memory、/usage、/jobs、/mic）。
//
// `usage.*` 是敲错参数时那句话——用户会照着它重敲，所以它算文案不是诊断。错误链上的
// 包装前缀（`session: save conversation: %w`）不在这里：那是给改代码的人定位用的。
var zhSessionTexts = map[string]string{

	// 通用短写
	"common.on":  "开",
	"common.off": "关",

	// /compact
	"session.compact.nothing":  "无需压缩：摘要之上没有新的历史",
	"session.compact.canceled": "已取消压缩（会话未改动）",
	"session.compact.done":     "已压缩 {absorbed} 条历史 → 1 条摘要（in={in} out={out} tokens）",
	// 回放时给摘要行收尾的标点：与界面语言一致的逗号/句读，不写死在代码里。
	"session.compact.replay_suffix": "；",

	// /unattended
	"usage.unattended":               "用法: /unattended [on|off]",
	"session.unattended.usage_state": "用法: /unattended [on|off]（当前 {state}）",
	"session.unattended.already":     "无人值守已是 {state}",
	"session.unattended.off":         "无人值守已关闭（越界操作恢复逐次确认；已写回 config）",
	"session.unattended.on": "无人值守已开启（已写回 config，工具调用立即生效）\n" +
		"权限边界不变：模型发起的越界操作会被当场拒绝并说明，不再弹确认等人；" +
		"你自己输入的命令（删除会话等）与插件授权照常询问。与 -yes 相反——那个是自动放行。",

	// /permission
	"usage.permission":              "用法: /permission [read-only|strict|permissive|full-access]",
	"session.permission.already":    "权限等级已是 {level}",
	"session.permission.switched":   "权限等级已切换为 {level}（已写回 config；工具调用立即生效）",
	"session.permission.head":       "当前权限等级: {level}（默认 strict）",
	"session.permission.sandbox":    "特权目录: {path}",
	"session.permission.matrix":     "免确认矩阵（矩阵之外的操作一律逐次确认）:",
	"session.permission.privileged": "特权",
	"session.permission.other":      "其他",
	"session.permission.tools_ask":  "工具 Confirm 逐次",
	"session.permission.tools_free": "工具全免确认",

	// /memory
	"usage.memory":          "用法: /memory [会话 ID 前缀]（缺省打开全局记忆文件）",
	"session.memory.opened": "已用系统编辑器打开 {path}（保存后下次读取即生效）",

	// /usage
	"session.usage.mode_estimate": "估算（按服务端返回的 usage 校准 ×{ratio}）",
	"session.usage.mode_exact":    "精确（本地 tokenizer 计数）",
	"session.usage.context":       "当前上下文:≈{est} / {max} tokens（{pct}%，自动压缩阈值 {at} = {atpct}%）",
	"session.usage.counter":       "计数方式: {mode}",
	"session.usage.last":          "上轮实测: in={in} out={out} tokens（服务端返回）",
	"session.usage.total":         "本会话累计(Path): in={in} out={out} tokens（{gen} 次生成）",
	"session.usage.cost":          "，约 ${usd}",
	"session.usage.lore":          "世界书: 激活 {on} 条 · {tok} tokens",
	"session.usage.lore_dropped":  "世界书: 激活 {on} 条 · {tok} tokens（另有 {dropped} 条命中但装不下）",

	// /lore：世界书激活（只读，与装配同一份判定）
	"session.lore.none":     "这个会话没有绑定角色卡，没有世界书。",
	"session.lore.missing":  "这张卡已经不在目录里，世界书取不到（人格快照仍在）。",
	"session.lore.head":     "世界书: 卡「{name}」共 {total} 条，扫描最近 {depth} 条消息，预算 {budget} tokens（占上下文 {pct}%）",
	"session.lore.untitled": "（无标题）",
	"session.lore.item":     "- {title} · order {order} · 约 {tok} tokens",
	"session.lore.foot":     "已激活 {on} 条 · {tok} tokens",
	"session.lore.empty":    "这一轮没有条目命中（共 {total} 条参与检索）。",
	"session.lore.dropped":  "另有 {dropped} 条命中但装不下（预算用完）；要让这些条目生效，调大 characters.lore.budget_ratio。",

	// /jobs
	"usage.jobs":              "用法: /jobs [list|logs <id> [行数]|kill <id>]",
	"session.jobs.empty":      "（暂无后台任务；模型可用 job_start 启动）",
	"usage.jobs_logs":         "用法: /jobs logs <id> [行数]（缺省最后 50 行）",
	"session.jobs.bad_tail":   "/jobs logs 行数须为正整数",
	"session.jobs.logs_empty": "任务 {id} 日志为空",
	"session.jobs.logs":       "任务 {id} 日志（末尾 {n} 行）:\n{text}",
	"usage.jobs_kill":         "用法: /jobs kill <id>",
	"session.jobs.killed":     "已终止 {id}",
	"session.jobs.hint":       "（/jobs 查看可用 ID）",

	// /mic
	"usage.mic":             "用法：/mic [秒数]",
	"session.mic.range":     "秒数需在 1–{max} 之间",
	"session.mic.recording": "正在录音，最长 {secs} 秒…",

	// /context（§7.1）：装配结果的概览、单条全文与整份落盘
	"usage.context":                          "用法：/context [last | full [last] | 消息编号]",
	"session.ctx.head":                       "模型 {model} · 上下文 {est} / {max}（{pct}%）· 压缩线 {at}",
	"session.ctx.params":                     "采样：{params}",
	"session.ctx.src_live":                   "来源：下一轮将发送（本回合尚未提交的缓冲不在其中）",
	"session.ctx.src_last":                   "来源：上一轮实际发送（含自动压缩后的形态）",
	"session.ctx.msg_head":                   "[{n}] {role} · {tok} tok",
	"session.ctx.msg_one":                    "[{n}] {role} · {title} · {tok} tok",
	"session.ctx.part_tok":                   " · {tok} tok（{pct}%）",
	"session.ctx.tools":                      "工具声明 {n} 个 · {tok} tok",
	"session.ctx.role.system":                "system",
	"session.ctx.role.user":                  "user",
	"session.ctx.role.assistant":             "assistant",
	"session.ctx.role.tool":                  "tool",
	"session.ctx.kind.persona":               "人格",
	"session.ctx.kind.summary":               "压缩摘要",
	"session.ctx.kind.lore":                  "世界书（{n} 条）",
	"session.ctx.kind.memory":                "记忆（{parts}）",
	"session.ctx.kind.note":                  "常驻声明",
	"session.ctx.kind.tail":                  "尾部指令",
	"session.ctx.kind.user":                  "用户消息",
	"session.ctx.kind.assistant":             "助手消息",
	"session.ctx.kind.tool":                  "工具结果",
	"session.ctx.kind.tool_image":            "工具产图",
	"session.ctx.kind.inject_host":           "注入（宿主 · {source}）",
	"session.ctx.kind.inject_host.plain":     "注入（宿主）",
	"session.ctx.kind.inject_external":       "注入（外部 · {source}）",
	"session.ctx.kind.inject_external.plain": "注入（外部）",
	"session.ctx.mem.index":                  "索引",
	"session.ctx.mem.session":                "本会话",
	"session.ctx.mem.both":                   "索引 + 本会话",
	"session.ctx.param_none":                 "无（全部交服务端默认）",
	"session.ctx.param_temp":                 "温度 {v}",
	"session.ctx.param_topp":                 "top_p {v}",
	"session.ctx.param_maxtok":               "输出上限 {v}",
	"session.ctx.param_think_on":             "思考开",
	"session.ctx.param_think_off":            "思考关",
	"session.ctx.param_effort":               "档位 {v}",
	"session.ctx.image_placeholder":          "[图片 {mime} · {kb} KB]",
	"session.ctx.no_last":                    "还没有发出过请求，没有可回的实发上下文",
	"session.ctx.no_message":                 "没有编号为 {n} 的消息（当前共 {max} 段）",
	"session.ctx.dumped":                     "已写入 {path}（合计 {tok} tok，含明文对话与工具输出）",
	"session.ctx.view_title":                 "上下文全文 · {model} · {tok} tok",
	"session.ctx.dump_head":                  "# 上下文 {model}",
	"session.ctx.dump_total":                 "合计 {est} / {max} tok · 压缩线 {at}",
	"session.ctx.dump_tools":                 "工具声明 {n} 个 · {tok} tok",
	"session.ctx.dump_section":               "## [{n}] {role} · {title} · {tok} tok",
	"session.ctx.dump_tool_head":             "## 工具声明",

	// /raw（§7.1）：原始返回的概览、单帧与整份落盘
	"usage.raw":                 "用法：/raw [-k | full [-k] | 帧号]",
	"session.raw.when_now":      "最近一次请求",
	"session.raw.when_back":     "倒数第 {k} 次请求",
	"session.raw.head":          "{when} · {proto} · 状态 {status}",
	"session.raw.where":         "{url} · {model}",
	"session.raw.timing":        "收流 {elapsed} · {size}",
	"session.raw.timing_first":  "收流 {elapsed} · 首字节 {first} · {size}",
	"session.raw.truncated":     "（留存已截断）",
	"session.raw.end":           "结束：{end} · 帧 {n}（data {data} · 注释 {comment}）",
	"session.raw.end_plain":     "结束：{end}",
	"session.raw.end.done":      "收到结束标记",
	"session.raw.end.eof":       "连接关闭（没有结束标记）",
	"session.raw.end.cancelled": "已中断",
	"session.raw.end.error":     "收流出错",
	"session.raw.end.status":    "被状态码拒绝",
	"session.raw.note":          "· {text}",
	"session.raw.frame":         "{idx}  {size}  {head}",
	"session.raw.hint":          "「/raw 帧号」看整帧原文，「/raw full」写进导出目录",
	"session.raw.more_frames":   "…另有 {n} 帧未列出（用帧号可直接看）",
	"session.raw.older":         "还有 {n} 次更早的请求（-2 起往回数）",
	"session.raw.no_trace":      "还没有收到过模型的返回",
	"session.raw.no_back":       "只留最近 {keep} 次请求（-1 是最新的）",
	"session.raw.no_frame":      "没有第 {n} 帧（本次共 {max} 帧）",
	"session.raw.dumped":        "已写入 {path}",
	"session.raw.view_title":    "原始返回 · {proto} · 状态 {status}",
	"session.raw.dump_head":     "# 原始返回 · {proto} · 状态 {status} · {size}",
	"session.raw.dump_url":      "# 端点：{url}",
	"session.raw.dump_end":      "# 结束：{end}",
	"session.raw.dump_line":     "# {text}",
	"session.raw.dump_note":     "# 以下为服务端返回原文；去掉 # 开头的行就是原件",

	// /audit：审计日志的查看口（模型与工具实际做了什么）
	"usage.audit":             "用法：/audit [-n 行数] [--tool 工具名]",
	"session.audit.head":      "审计日志 · 最近 {n} 行（文件共 {all} 行）",
	"session.audit.head_tool": "审计日志 · {tool} 的最近 {n} 行（文件共 {all} 行）",
	"session.audit.empty":     "还没有审计记录",
	"session.audit.no_match":  "没有工具名为 {tool} 的记录",
	"session.audit.no_path":   "本构建未接入审计日志",
	"session.audit.err_read":  "读取审计日志",
	"session.audit.ok":        "ok",
	"session.audit.fail":      "失败",
	"session.audit.usage":     "  in {in} · out {out}",
	"session.audit.hint":      "「--tool 工具名」只看某个工具，「-n 行数」看更多",

	// 切换会话后的回放提示（会话切换、改挂、改父、载入技能等都会发这一行）
	"session.replay.notice":       "{verb}会话 {title} ({id})，回放 {n} 条历史",
	"replay.verb.restored":        "已恢复",
	"replay.verb.started":         "已开始",
	"replay.verb.revised":         "已修订",
	"replay.verb.regenerated":     "已重新生成",
	"replay.verb.injected":        "已注入",
	"replay.verb.reparented":      "已改父",
	"replay.verb.reparent_undone": "已撤销改父",
	"replay.verb.moved":           "已定位至",
	"replay.verb.skill_loaded":    "已载入技能",
	"replay.verb.switched":        "已切换到",
	"replay.verb.branch_switched": "已切换分支至",
	"replay.verb.imported":        "已导入",
}
