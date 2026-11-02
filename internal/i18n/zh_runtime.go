package i18n

// zhRuntimeTexts 会话生命周期、插件、/watch 与 /delegate 的监视提示：回执与报错。
//
// 这里收的是「跑起来之后才说话」的那批文案：装配缺依赖时的启动报错、命令回执、以及
// 观察器/转交观察者送不出去报告时的那行运行日志（装配根接到 stderr，用户看得到）。
// 进模型上下文的报告正文（jobReportFmt 那一类）不在这里：那是给模型看的事实陈述，
// 与界面语言无关。
var zhRuntimeTexts = map[string]string{

	// 会话装配：缺依赖时启动即失败，这一行是用户看到的全部
	"session.deps.store_missing": "store 依赖为空",
	"session.deps.agent_missing": "agent 依赖为空",
	"session.deps.idgen_missing": "IDGen 依赖为空",
	"session.deps.clock_missing": "Clock 依赖为空",

	// 会话生命周期：错误链短语（`session: <短语>: <原因>`）
	"session.err.init":        "初始化会话",
	"session.err.persona":     "写入 persona 首节点",
	"session.err.append":      "追加消息",
	"session.err.restore":     "恢复会话 {id}",
	"session.err.ingest":      "摄取输入",
	"session.save.note":       "（且保存失败: {save}）",
	"session.ingest.empty":    "摄取未产出内容",
	"session.ingest.no_input": "文本输入为空",

	// 多模态输入：认不出输入类型时说明当前支持面
	"session.ingest.no_ingestor": "没有可用的摄取器处理 {kind} 输入（当前支持文件与剪贴板；语音输入尚未启用）",

	// 命令面
	"session.unknown_command":    "未知命令 /{name}（/help 查看可用命令）",
	"session.flag_missing_value": "{flag} 缺参数",

	// 待发清单（D272）：切走会话时不静默丢东西
	"session.pending.dropped": "（{n} 条待发消息未随会话带走）",

	// /plugin
	"plugin.unwired":         "未配置插件宿主（SessionDeps.Plugins），/plugin 不可用",
	"plugin.usage.list":      "用法: /plugin list",
	"plugin.usage.enable":    "用法: /plugin enable <name>",
	"plugin.usage.disable":   "用法: /plugin disable <name>",
	"plugin.usage.logs":      "用法: /plugin logs [name]",
	"plugin.usage":           "用法: /plugin [list|enable <name>|disable <name>|logs [name]]",
	"plugin.disabled":        "已停用 {name}",
	"plugin.enabled":         "已启用 {name}",
	"plugin.enabled_status":  "已启用 {name} → {status}",
	"plugin.enabled_note":    "（{error}）",
	"plugin.dynamic_head":    "\n动态命令（MCP prompts）:\n  /",
	"plugin.list.empty":      "没有已声明的插件（config mcpServers 或 ~/.aquarius/plugins/*/plugin.json）",
	"plugin.list.head":       "插件（{n} 个）:",
	"plugin.list.caps":       " 能力:{caps}",
	"plugin.list.calls":      " 调用{calls}",
	"plugin.list.errors":     ", 失败{errors}",
	"plugin.list.restarts":   " 重启{restarts}",
	"plugin.list.last_error": " 错误:{error}",
	"plugin.cap.granted":     "(已授权)",
	"plugin.cap.ungranted":   "(未授权)",

	// /watch：清单与判据
	"watch.unwired":           "未配置观察器（SessionDeps.Watches），/watch 不可用",
	"watch.usage":             "用法: /watch [list|stop <id>]",
	"watch.usage.stop":        "用法: /watch stop <id>",
	"watch.err_list":          "列出观察器",
	"watch.err_stop":          "停止观察",
	"watch.list.empty":        "（暂无观察器；模型可用 watch_start 声明一条）",
	"watch.list.item":         "    {criterion}  每 {interval} 一次，已查 {checks} 次，{ends} 结束",
	"watch.see_ids":           "（/watch 查看可用 ID）",
	"watch.stopped":           "已停止观察 {id}",
	"watch.criterion.changed": "判据：输出有变化",
	"watch.criterion.match":   "判据：输出匹配 /{pattern}/",

	// 任务收尾观察者（D189）：报告与日志送不出去时的运行日志
	"watch.job.wait_failed":         "等待任务 {id} 结束失败: {error}",
	"watch.job.read_log_failed":     "读取任务 {id} 日志失败: {error}",
	"watch.job.deliver_no_injector": "后台任务 {id} 的完成报告未送达: 注入端口未就绪",
	"watch.job.deliver_closed":      "后台任务 {id} 的完成报告未送达: 会话已关闭",
	"watch.job.deliver_failed":      "后台任务 {id} 的完成报告未送达: {error}",

	// 观察器（D190）：报告送不出去时的运行日志
	"watch.report.closed": "观察 {id} 的报告未送达: 会话已关闭",
	"watch.report.failed": "观察 {id} 的报告未送达: {error}",

	// /delegate
	"delegate.usage":             "用法: /delegate [list|kill <id>]",
	"delegate.usage.kill":        "用法: /delegate kill <id>",
	"delegate.unwired":           "未配置外部代理（SessionDeps.Delegate），/delegate 不可用",
	"delegate.err_list":          "列出外部代理",
	"delegate.err_kill":          "终止转交",
	"delegate.state.available":   "可用",
	"delegate.state.unavailable": "不可用",
	"delegate.name.titled":       "（{title}）",
	"delegate.name.versioned":    "（{title} {version}）",
	"delegate.killed":            "已叫停转交 {id}",
	"delegate.empty_hint": "未配置任何外部代理。在 config.json 的 delegate.agents 里写一条（command + 可选 args/env/workdir），" +
		"并确认该命令在 PATH 上；模型这时不会有 delegate 工具。",

	// 转交收尾观察者（D261）：销账与注入失败
	"delegate.settle_failed": "转交 {id} 销账失败: {error}",
	"delegate.report_failed": "转交 {id} 的报告注入失败: {error}",

	// 新会话的默认标题（首条消息后会被摘要改写）
	"session.default_title": "新会话",
}
