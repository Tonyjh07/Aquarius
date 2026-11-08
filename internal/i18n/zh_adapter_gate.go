package i18n

// zhAdapterGateTexts 转交与插件闸门：acpgate（外部代理的确认句与台账）、mcpgate、plugin（信任与状态文案）、domain 的校验报错、toolbuiltin/toolrun/decorate。key 前缀 acp.* / mcp.* / plugin.* / tool.* / perm.*。
//
// 本片补遗的段落（I3 片 D）：密钥库、内置工具词表与确认句、外部代理转交、MCP 闸门、
// 插件宿主与配置、会话树不变量、权限等级解析。key 前缀 secret.* / tool.* / acp.* /
// mcp.* / plugin.* / conv.* / perm.*。
var zhAdapterGateTexts = map[string]string{
	// ---- secretbin：本机加密密钥库（D151/D161）。错误只带名字与路径，绝不回显密钥值。 ----
	"secret.err.key_missing":        "缺少密钥文件 {path}（本机换过 profile、或文件被单独拷走）",
	"secret.err.key_read":           "读取密钥文件 {path}",
	"secret.err.key_length":         "密钥文件 {path} 长度不对（{got} 字节，应为 {want}）",
	"secret.err.key_length_plain":   "密钥文件 {path} 长度不对",
	"secret.err.key_create":         "创建密钥文件 {path}",
	"secret.err.key_write":          "写入密钥文件 {path}",
	"secret.err.key_close":          "关闭密钥文件 {path}",
	"secret.err.cipher_short":       "密文长度不足（文件被截断或不是本档密文）",
	"secret.err.cipher_auth":        "密文校验失败（密钥不对或内容被改动）",
	"secret.err.build_aes":          "构造 AES",
	"secret.err.build_gcm":          "构造 GCM",
	"secret.err.dpapi_other":        "DPAPI 密文档仅 Windows 可用",
	"secret.err.dpapi_other_file":   "DPAPI 密文档仅 Windows 可用（文件可能来自 Windows 机器）",
	"secret.err.dpapi_empty_plain":  "DPAPI: 空明文不可加密",
	"secret.err.dpapi_empty_blob":   "DPAPI: 空密文不可还原",
	"secret.err.dpapi_protect":      "CryptProtectData 失败",
	"secret.err.dpapi_unprotect":    "CryptUnprotectData 失败（本机账户与写入时不是同一个，或文件已损坏）",
	"secret.err.dpapi_empty_result": "DPAPI 返回空结果",
	"secret.err.not_found":          "secretbin: 密钥不存在",
	"secret.err.unreadable":         "secretbin: 密钥库解不开",
	"secret.err.dir_empty":          "secretbin: 目录不能为空",
	"secret.err.dir_unusable":       "secretbin: 目录 {dir} 不可用",
	"secret.err.value_empty":        "secretbin: 「{name}」的值不可为空（要清掉这一条请用删除）",
	"secret.err.value_too_long":     "secretbin: 「{name}」的值过长（上限 {max} 字节）",
	"secret.err.read":               "secretbin: 读取 {path}",
	"secret.err.bad_structure":      "{path} 结构坏: {detail}",
	"secret.err.bad_version":        "{path} 版本 {version} 不被支持（本版认 {want}）",
	"secret.err.bad_base64":         "{path} 载荷不是合法 base64: {detail}",
	"secret.err.decrypt_failed":     "{path}: {detail}",
	"secret.err.bad_plain":          "{path} 明文结构坏: {detail}",
	"secret.err.marshal":            "secretbin: 序列化",
	"secret.err.marshal_envelope":   "secretbin: 序列化信封",
	"secret.err.backup":             "secretbin: 留备份 {path}",
	"secret.err.write":              "secretbin: 写入 {path}",
	"secret.err.unknown_cipher":     "secretbin: 未知密文档「{cipher}」（文件可能来自更新版本）",
	"secret.err.name_empty":         "secretbin: 密钥名不可为空",
	"secret.err.name_space":         "secretbin: 密钥名「{name}」首尾不可含空白",
	"secret.err.name_too_long":      "secretbin: 密钥名过长（上限 {max} 字节）",
	"secret.err.name_chars":         "secretbin: 密钥名「{name}」不可含空白或冒号（引用写作 secret:<名>）",
	"secret.err.name_control":       "secretbin: 密钥名「{name}」不可含控制字符",
	"secret.err.rand":               "secretbin: 取随机数",

	// ---- decorate/keypool：同 provider 内的密钥轮换（D173）。labelAt 的「第 N 把」也显示在这里。 ----
	"secret.keypool.all_cooling": "llm: {n} 把密钥都在冷却（最早 {until} 解冻）",
	"secret.keypool.token":       "密钥 {label}",
	"secret.keypool.label_nth":   "第 {n} 把",

	// ---- toolrun：工具确认句（用户要回答的那一句，带目标路径与参数预览）。 ----
	"tool.confirm.with_target":      "允许 {name} 访问 {target}？参数: {args}",
	"tool.confirm.plain":            "允许执行 {name}？参数: {args}",
	"tool.confirm.described":        "允许执行 {name}？{summary}",
	"tool.confirm.described_target": "允许 {name} 访问 {target}？{summary}",

	// ---- delegate 工具的确认摘要：范围全列、任务只留摘要（默认提示会把这两样挤出屏幕）。 ----
	"tool.delegate.describe.auto":          "缺省代理",
	"tool.delegate.describe.scope":         "工作目录 {dir}",
	"tool.delegate.describe.scope_default": "代理配置的工作目录",
	"tool.delegate.describe.scope_plus":    "{scope}，另可访问 {roots}",
	"tool.delegate.describe.line":          "代理 {agent}；{scope}；任务：{task}",

	// ---- decorate：审计文件与计数转发的内部哨兵（只有测试与诊断看，仍收进表以清干净字面量）。 ----
	"tool.err.audit_closed": "audit: 文件已关闭",
	"tool.err.no_counter":   "decorate: 内层未实现 TokenCounter",
	"tool.err.read_audit":   "读取审计日志",

	// ---- toolbuiltin：内置工具的中文词表（S6/D143 工具管理页展示；仅界面文案，非契约）。 ----
	"tool.label.memory_list":    "记忆索引",
	"tool.label.memory_read":    "读取记忆",
	"tool.label.memory_search":  "检索记忆",
	"tool.label.memory_write":   "写入记忆",
	"tool.label.file_read":      "读取文件",
	"tool.label.file_list":      "列出目录",
	"tool.label.file_search":    "检索文件",
	"tool.label.file_edit":      "编辑文件",
	"tool.label.file_write":     "写入文件",
	"tool.label.file_delete":    "删除文件",
	"tool.label.think":          "思考草稿",
	"tool.label.sleep":          "等待",
	"tool.label.term_exec":      "执行命令",
	"tool.label.job_start":      "启动任务",
	"tool.label.job_wait":       "等待任务",
	"tool.label.job_list":       "任务列表",
	"tool.label.job_status":     "任务状态",
	"tool.label.job_logs":       "任务日志",
	"tool.label.job_kill":       "终止任务",
	"tool.label.window_list":    "窗口列表",
	"tool.label.screen_capture": "屏幕截图",
	"tool.label.watch_start":    "开始观察",
	"tool.label.watch_list":     "观察列表",
	"tool.label.watch_stop":     "停止观察",
	"tool.label.ask_user":       "提问",
	"tool.label.web_search":     "联网搜索",
	"tool.label.web_fetch":      "读取网页",
	"tool.label.skill":          "载入技能",
	"tool.label.delegate":       "转交外部代理",
	"tool.label.delegate_wait":  "等待转交",
	"tool.label.delegate_kill":  "终止转交",

	// ---- acpgate：外部代理清单的不可用原因、无人应答的拒绝原因、权限确认对话框正文。 ----
	"acp.note.disabled":       "已停用（delegate.agents 里 enabled=false）",
	"acp.note.no_command":     "未配置 command",
	"acp.note.not_on_path":    "命令不在 PATH 上：{command}",
	"acp.deny.not_in_session": "你不在发起这次转交的会话",
	"acp.deny.no_confirmer":   "本构建没有配置确认器",
	"acp.deny.user_refused":   "用户拒绝",
	"acp.prompt.head":         "外部代理「{agent}」请求执行一个操作：",
	"acp.prompt.foot":         "该代理有自己的文件与命令访问权限；批准即表示同意代理按自己的判断动手。",
	// fs 越界与控制文件（A2）：三句都要说清「它要碰哪里」与「为什么问你」。
	"acp.confirm.read_outside":  "外部代理「{agent}」要读会话范围之外的文件：\n  {path}\n\n代理自己的 shell 本来也读得到，这里问一次是为了让这份访问留痕。",
	"acp.confirm.write_outside": "外部代理「{agent}」要写会话范围之外的文件：\n  {path}\n\n批准即允许代理按自己的判断改这个文件。",
	"acp.confirm.control_file":  "外部代理「{agent}」要写一个执行控制类文件（{rule}）：\n  {path}\n\n这类文件能改变「之后谁会跑起来」（版本控制钩子、编辑器或代理的设置、MCP 配置），所以单独问一次。",
	// 命令确认（A3）：命令、目录、环境变量三者都要出现——策略匹配的是三者，不是命令名。
	"acp.confirm.terminal":     "外部代理「{agent}」要执行一条命令：\n  {command}\n目录：{cwd}\n\n命令按你的身份运行，进程由 Aquarius 代起；批准即允许整条命令跑完。",
	"acp.confirm.terminal_env": "环境变量：{env}",
	// 连通性探测（A1「测试连接」）：三类失败要分开，用户能据此改对地方。
	"acp.probe.err.not_found": "命令不在 PATH 上",
	"acp.probe.err.start":     "起不了进程",
	"acp.probe.err.handshake": "握手失败",
	"acp.probe.err.session":   "建不了会话",

	// ---- mcpgate：MCP server 的连接、密钥解析与 prompt 参数用法。 ----
	"mcp.err.connect":            "连接插件 {name}",
	"mcp.err.env":                "插件 {name} 环境变量",
	"mcp.err.headers":            "插件 {name} 请求头",
	"mcp.err.unknown_transport":  "插件 {name}: 未知 transport「{transport}」",
	"mcp.err.secrets_missing":    "{name} 需要 Secrets 端口但未配置",
	"mcp.err.secret_read":        "取密钥 {name}",
	"mcp.err.prompt_empty":       "prompt {name} 未产出文本内容（插件 {plugin}）",
	"mcp.err.prompt_no_args":     "prompt {name} 不声明参数，但收到 {got} 个。用法: /mcp:{plugin}:{prompt} {usage}",
	"mcp.err.prompt_too_many":    "多余参数：prompt {name} 声明 {want} 个，收到 {got} 个。用法: /mcp:{plugin}:{prompt} {usage}",
	"mcp.err.prompt_missing_arg": "缺少参数 {arg}。用法: /mcp:{plugin}:{prompt} {usage}",

	// ---- plugin/config.go：启动声明的自检（/plugin 命令与启动日志共用）。stdio 与
	// streamable-http 是协议枚举、safe 与 confirm 是风险枚举，一律留在译文里不翻。 ----
	"plugin.cfg.parse":              "解析 plugin.json",
	"plugin.cfg.stdio_no_command":   "插件「{name}」: stdio 形态缺 command",
	"plugin.cfg.http_no_url":        "插件「{name}」: streamable-http 形态缺 url",
	"plugin.cfg.transport_required": "插件「{name}」: transport 必填（{stdio} | {http}）",
	"plugin.cfg.unknown_transport":  "插件「{name}」: 未知 transport「{transport}」（{stdio} | {http}）",
	"plugin.cfg.name_empty":         "插件名为空",
	"plugin.cfg.name_chars":         "插件名「{name}」含冒号或空白（工具/命令名按冒号切分，不允许）",
	"plugin.cfg.unknown_risk":       "插件「{name}」: 未知 risk「{risk}」（{safe} | {confirm}）",

	// ---- plugin/host.go：插件列表的状态文案、两道确认句、以及启用/停用/授权的报错。 ----
	"plugin.host.status_untrusted":     "未获信任或确认被拒绝（fail-closed）",
	"plugin.host.status_ungranted":     "授权被拒绝或缺少确认器（fail-closed）",
	"plugin.host.status_revoked_grant": "能力已撤销并停用（再启用即重走确认）",
	"plugin.host.status_revoked_trust": "信任已撤销（下次启用重新确认）",
	"plugin.host.trust_prompt":         "插件「{name}」来自插件目录（{source}），本次启动要运行这个插件？\n启动方式：{launch}",
	"plugin.host.grant_prompt":         "插件「{name}」请求能力「{cap}」，允许？",
	"plugin.host.crash_limit":          "崩溃重启超限（{n} 次）: {err}",
	"plugin.host.err_unknown":          "未声明的插件「{name}」（既不在 config mcpServers 也无 plugin.json）",
	"plugin.host.err_unknown_short":    "未声明的插件「{name}」",
	"plugin.host.err_untrusted":        "{name} 未获信任，本次不连接",
	"plugin.host.err_enable_persist":   "{name} 已在本次运行生效，但状态写回 config 失败",
	"plugin.host.err_disable_persist":  "{name} 已在本次运行停用，但状态写回 config 失败",
	"plugin.host.err_grant_persist":    "{name} 已在本次运行预授权 {cap}，但状态写回 config 失败",
	"plugin.host.err_revoke_persist":   "{name} 已在本次运行停用并撤销能力 {cap}，但状态写回 config 失败",
	"plugin.host.err_trust_config":     "{name} 来自 config（用户显式信任），不提供撤销信任",
	"plugin.host.err_trust_persist":    "{name} 已在本次运行断开并撤销信任，但状态写回 config 失败",

	// ---- domain/conversation：会话树的不变量报错（经 app 的 wrapSession 冒到界面）。
	// append / revise / prune / reparent / undo 是方法名，留在译文里不翻。 ----
	"conv.err.append_role":          "append: 仅 user|assistant 可走 Append，root/system/inject 用 AppendCommitted",
	"conv.err.append_root":          "append 「{id}」: root 由 New 创建，不可提交",
	"conv.err.revise_root":          "revise 「{id}」: 会话根节点就是会话本身，不可修订",
	"conv.err.prune_root":           "prune 「{id}」: 会话根节点就是会话本身，不可删除",
	"conv.err.undo_none":            "undo: 没有可撤销的搬运记录",
	"conv.err.undo_stale":           "undo: 最近一条记录已陈旧（「{home}」下已找不到被搬的节点），请照现状处理",
	"conv.err.insert_root":          "insert before 「{id}」: 会话根节点之前无处可插",
	"conv.err.insert_pad_role":      "insert before 「{id}」: 衬垫节点角色仅限 system|inject（实为「{role}」）",
	"conv.err.insert_pad_empty":     "insert before 「{id}」: 衬垫节点没有内容",
	"conv.err.insert_missing_edge":  "insert before 「{id}」: 目标节点在父节点「{parent}」下没有边",
	"conv.err.reparent_root":        "reparent 「{id}」: 会话根节点就是会话本身，不可挪动",
	"conv.err.reparent_to_root":     "reparent 「{id}」 -> 「{parent}」: 不能挂到根下（人格会退出装配）",
	"conv.err.reparent_descendant":  "reparent 「{id}」 -> 「{parent}」: 不能挂到自己的后代下",
	"conv.err.reparent_same_parent": "reparent 「{id}」 -> 「{parent}」: 已经挂在该父下",

	// ---- domain/perm：权限等级解析（config permissions.level 与 /permission 共用）。
	// 四个等级名是数据，留在译文里不翻。 ----
	"perm.err.unknown_level": "perm: 未知权限等级「{level}」（可用：read-only / strict / permissive / full-access）",
	"notify.err.no_sender":   "notify: 未配置发送器",
}
