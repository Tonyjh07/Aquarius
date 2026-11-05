package i18n

// zhLogTexts 运行日志文案（D276 / contracts §7.9）。
//
// 两条 key 一族：`log.<ctx>.<what>` 说发生了什么，`log.act.*` 说当场怎么处置。分开是因为
// 同一件事在不同处置下是两条日志（跳过 vs 回落），把处置写进事件句里，两行就成了两句手抄文案。
//
// 行动位只写**已做的处置**，不写「请检查 …」这类指令——隔天回看，指令既不知道当时怎么处置的，
// 也不知道后来自动恢复没有。动作要按类收敛复用，别一句一条。
var zhLogTexts = map[string]string{
	// 处置（跨上下文复用）
	"log.act.skip":                 "已跳过",
	"log.act.skip_missing":         "已跳过缺失引用，其余模块照常展开",
	"log.act.skip_key_this_run":    "本次启动跳过它",
	"log.act.start_without_key":    "没有密钥也照常启动，发消息时才报因",
	"log.act.fallback_skipped":     "已跳过这条降级链，主链照常启动",
	"log.act.stderr_only":          "本次只输出到 stderr",
	"log.act.unattended_disabled":  "本次不启用无人值守（配置文件未改动）",
	"log.act.recorded_unsupported": "已记入 {provider} 的 unsupported_params，后续不再重发",
	"log.act.retry_next_start":     "下次启动仍会先试发该字段",
	"log.act.continue":             "照常继续，不影响对话",
	"log.act.voice_off":            "本次不播报，文字照常",
	"log.act.voice_input_off":      "本次不识别语音，键入照常",
	"log.act.keep_raw":             "保留原文，照常识别",
	"log.act.dropped":              "已丢弃这条回执，对话照常",
	"log.act.deferred_next_start":  "留到下次启动再处理",
	"log.act.plaintext_used":       "明文照用，config 里留着它",
	"log.act.exit":                 "本次启动以失败退出",
	"log.act.deny":                 "按拒绝处理，本次不连接",
	"log.act.config_kept":          "以 config 条目为准",
	"log.act.reask_next_start":     "下次启动会再问一次",
	"log.act.plugin_off":           "本次不启用该插件",
	"log.act.gave_up":              "已停止重启，等下次启动再试",
	"log.act.restart":              "已安排重启",
	"log.act.start_empty":          "从空台账起步，安装记录重新累积",
	"log.act.report_next_start":    "下次启动会当作未收尾再报一次",
	"log.act.stop_watch":           "已停止观察这个任务，任务本身照常跑",
	"log.act.report_without_log":   "报告里不带日志尾部",
	"log.act.keep_previous":        "保留上一轮的登记",
	"log.act.env_fallback":         "改用环境变量继续读",

	// 启动
	"log.boot.file_unavailable": "日志文件打不开（{path}）：{err}",
	"log.boot.auto_yes_overrides_unattended": "配置里开着无人值守，但本次带 -yes，自动允许优先——" +
		"越界操作会被执行而不是拒绝",
	"log.boot.persona_missing": "人格模块树存在缺失引用 {missing}（检查 prompts.modules / " +
		"prompts.persona）",

	// 模型链路
	"log.llm.param_rejected":        "服务端不认参数 {field}，已从请求里剔除",
	"log.llm.param_record_failed":   "记录 unsupported_params 失败：{err}",
	"log.llm.key_unavailable":       "provider {provider} 第 {n} 把密钥不可用：{err}",
	"log.llm.provider_unconfigured": "provider {provider} 本轮没有可用密钥：{err}",
	// 整句由 narrowNote 给（与设置窗的降级说明同源），这条只作 JSON 侧的聚合标识。
	"log.llm.chain_narrowed": "降级链的能力比当前模型窄",

	// 语音
	"log.audio.tts_unconfigured":   "语音播报已开启，但{why}",
	"log.audio.asr_unavailable":    "语音识别不可用：{why}",
	"log.audio.tts_unavailable":    "语音合成不可用：{why}",
	"log.audio.zhconv_unavailable": "繁简转换不可用，识别结果保持原样：{err}",
	"log.audio.zhconv_failed":      "繁简转换失败：{err}",
	"log.audio.play_synth_failed":  "语音合成失败，这条不播报：{err}",
	"log.audio.play_read_failed":   "取合成音频失败，这条不播报：{err}",
	"log.audio.play_failed":        "语音播放失败：{err}",

	// 输出与回执
	"log.ui.tts_output_unavailable": "语音播报输出装不起来：{err}",
	"log.ui.output_failed":          "回执投递失败：{err}",

	// 后台任务与附件
	"log.job.settle_failed":            "任务 {id} 的台账没能销账：{err}",
	"log.job.ledger_unreadable":        "任务台账读不出：{err}",
	"log.job.unfinished_report_failed": "上次未收尾的任务没能补报进会话：{err}",
	"log.job.wait_failed":              "等待任务 {id} 结束失败：{err}",
	"log.job.log_unreadable":           "读任务 {id} 的日志失败：{err}",
	"log.job.report_no_injector":       "后台任务 {id} 的完成报告未送达：注入端口未就绪",
	"log.job.report_closed":            "后台任务 {id} 的完成报告未送达：会话已关闭",
	"log.job.report_failed":            "后台任务 {id} 的完成报告未送达：{err}",
	"log.job.watch_report_closed":      "观察 {id} 的报告未送达：会话已关闭",
	"log.job.watch_report_failed":      "观察 {id} 的报告未送达：{err}",
	"log.attach.gc_skipped":            "收集附件引用失败，本次不清理附件：{err}",
	"log.attach.gc_failed":             "附件清扫失败：{err}",

	// 技能、联网、密钥
	"log.skill.entry_skipped":      "技能条目被跳过：{text}",
	"log.skill.ledger_unreadable":  "技能台账读不出：{err}",
	"log.skill.ledger_corrupt":     "技能台账解析失败：{err}",
	"log.web.provider_unavailable": "联网{side}的第 {index} 家提供商（{kind}）不可用：{err}",
	// 整句由 warnPlaintext 给（沿用 cmd.secrets.warn.plaintext 的措辞），这条只作聚合标识。
	"log.perm.plaintext_key":      "启动时发现明文密钥",
	"log.perm.secrets_unreadable": "密钥库解不开",

	// 会话、转交与前端
	"log.session.replay_failed":             "历史回放失败",
	"log.session.target_gone":               "注入 {source} 的目标会话 {target} 已删除，内容丢弃",
	"log.delegate.ledger_unreadable":        "转交台账读不出：{err}",
	"log.delegate.ledger_write_failed":      "转交 {id} 写台账失败：{err}",
	"log.delegate.ledger_settle_failed":     "转交 {id} 销账失败：{err}",
	"log.delegate.settle_failed":            "转交 {id} 销账失败：{err}",
	"log.delegate.report_failed":            "转交 {id} 的报告注入失败：{err}",
	"log.delegate.unfinished_report_failed": "上次未收尾的转交没能补报进会话：{err}",
	"log.ui.terminal_suspend_failed":        "把终端交给编辑器时失败：{err}",
	"log.ui.terminal_resume_failed":         "从编辑器收回终端时失败：{err}",
	"log.ui.input_failed":                   "读输入失败：{err}",

	// 角色卡目录
	"log.character.dir_skipped":         "角色卡目录 {name} 被跳过：{err}",
	"log.character.meta_rebuild_failed": "重建 {name} 的 meta.json 失败：{err}",

	// 插件宿主与插件面
	"log.plugin.manifest_read_failed": "读 {name}/plugin.json 失败：{err}",
	"log.plugin.manifest_rejected":    "拒载插件 {name}：{err}",
	"log.plugin.config_wins":          "插件 {name}：以 config 条目为准，未声明的能力与风险等级继承 plugin.json",
	"log.plugin.trust_no_confirmer":   "插件 {name} 尚未信任，且无人可确认",
	"log.plugin.trust_confirm_failed": "插件 {name} 的信任确认失败：{err}",
	"log.plugin.trust_declined":       "用户拒绝了插件 {name} 的信任",
	"log.plugin.trust_persist_failed": "插件 {name} 的信任状态写回失败：{err}",
	"log.plugin.grant_no_confirmer":   "插件 {name} 请求能力 {caps}，但无人可确认",
	"log.plugin.grant_persist_failed": "插件 {name} 的授权状态写回失败：{err}",
	"log.plugin.grant_confirm_failed": "插件 {name} 的能力 {cap} 确认失败：{err}",
	"log.plugin.grant_declined":       "用户拒绝了插件 {name} 的能力 {cap}",
	"log.plugin.start_failed":         "插件 {name} 启动失败：{err}",
	"log.plugin.crash_limit":          "插件 {name} 连续崩溃 {n} 次：{err}",
	"log.plugin.restarting":           "插件 {name} 连接终止（{err}），{wait} 后重启（第 {n}/{max} 次）",
	"log.plugin.memory_index_failed":  "插件记忆投影索引失败：{err}",
	"log.plugin.memory_search_failed": "插件记忆投影检索失败：{err}",
	"log.plugin.tools_list_failed":    "插件 {name} 的工具清单拉取失败：{err}",
	"log.plugin.prompts_list_failed":  "插件 {name} 的提示词清单拉取失败：{err}",
}
