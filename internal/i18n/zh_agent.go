package i18n

// zhAgentTexts 模型与思考、Turn 循环提示、上下文压缩：/model、/think、/compact 的用法、
// 回执与报错。
//
// 回执是分行的：一行一句事实，拼接交给调用方（每行一条 key）。发给模型的那批字符串
// （system 提示、工具声明、工具回填）不在这里：它们一律英文且与界面语言无关（§7.8）。
var zhAgentTexts = map[string]string{

	// 用法
	"usage.model":       "用法: /model [name] 或 /model probe [name]",
	"usage.model_probe": "用法: /model probe [模型名]（省略 = 当前模型）",
	"usage.think":       "用法: /think [on|off]",
	"usage.effort":      "用法: /effort [minimal|low|medium|high|off]",
	"usage.summary":     "用法: /summary <id> [摘要文本]（不带文本 = 让当前模型总结该节点之上的历史）",

	// /model：当前口径与切换
	"model.unwired":          "未配置模型服务（SessionDeps.Models），/model 不可用",
	"model.switch_unwired":   "未配置模型写回（SessionDeps.ModelSwitch），/model 切换不可用",
	"model.probe_unwired":    "未配置能力探测（SessionDeps.Prober），/model probe 不可用",
	"model.switched":         "已切换模型 → {model}（已写回 config，重启沿用）",
	"model.current_provider": "当前 provider: {provider}",
	"model.current":          "当前模型: {model}",
	"model.caps":             "生效能力: {caps}",
	"model.caps_off":         "未启用: {caps}（在 model.providers[].models[].caps 里声明{cause}）",
	"model.culprit":          "；拉低整链的是 {names}",
	"model.chain":            "降级链:",
	"model.no_list":          "（模型服务未返回可用列表，可直接 /model <name> 切换）",
	"model.list_head":        "可用模型（{n}）:",
	"model.cost":             "${in}/${out} 每百万 tokens",
	"model.caps_none":        "（三项均未声明）",
	"model.list_sep":         "、",

	// 能力名：完整写法（能力行/探测）与短写（清单标注/链路归因）
	"model.cap_tools":      "工具调用",
	"model.cap_reasoning":  "思考",
	"model.cap_vision":     "图片输入",
	"model.mark_tools":     "工具",
	"model.mark_reasoning": "思考",
	"model.mark_vision":    "图片",
	"model.link_narrowed":  "{provider}/{model} 失去了 {lost}",

	// /model probe
	"model.probe_head":          "能力探测 {provider} / {model}",
	"model.probe_reason":        "（{reason}）",
	"model.probe_usage":         "用量 {in}→{out} tokens",
	"model.probe_written":       "已按判定明确的项写回配置，重启后生效（无结论的项保持原声明）",
	"model.probe_unchanged":     "未改动配置（没有判定明确的项，或结果与现有声明一致）",
	"model.verdict_supported":   "支持",
	"model.verdict_unsupported": "不支持",
	"model.verdict_unknown":     "无结论",
	"model.elapsed_unknown":     "耗时未知",

	// 上下文预算行：生效值，以及它是被模型窗口还是全局上限压住的（D177）
	"model.budget_global_only": "上下文预算: {effective} tokens（全局上限；模型未声明窗口）",
	"model.budget_model":       "上下文预算: {effective} tokens（模型窗口 {window} 在压；全局上限 {limit}）",
	"model.budget_global":      "上下文预算: {effective} tokens（全局上限在压；模型窗口 {window}）",

	// 错误链短语（/model 走 session: 前缀，与 /permission 同口径）
	"model.err_list":  "列出可用模型",
	"model.err_probe": "探测",

	// /think
	"think.state":             "原生思考: {state}（/think 总开关，off 时不发 reasoning_effort 与 enable_thinking）",
	"think.no_reasoning":      "当前模型未声明思考能力（模型服务的 caps 里没有 reasoning），整条降级链取与后为关，参数不会发送",
	"think.effort_line":       "推理档位: {effort}（/effort；随开关发送）",
	"think.effort_unset":      "推理档位: 未设（交服务端默认；/effort 设置）",
	"think.usage_state":       "用法: /think [on|off]（当前 {value}）",
	"think.already":           "原生思考已是 {state}",
	"think.unwired":           "未配置思考持久化（SessionDeps.Thinking），/think 切换不可用",
	"think.switched":          "原生思考已切到 {state}（已写回 config，重启沿用）",
	"think.warn_no_reasoning": "注意：当前模型未声明思考能力，参数仍不会发送。到设置窗「模型服务」里给模型加上 reasoning，或重新探测一次",

	// /effort
	"think.effort_only":         "推理档位: {effort}（是否发送由 /think 决定）",
	"think.effort_unset_usage":  "推理档位: 未设（交服务端默认；/effort minimal|low|medium|high|off）",
	"think.effort_cleared":      "推理档位已清除",
	"think.effort_cleared_note": "推理档位已清除（已写回 config；/think on 时不再发送 reasoning_effort）",
	"think.effort_already":      "推理档位已是 {effort}",
	"think.effort_unwired":      "未配置档位持久化（SessionDeps.Thinking），/effort 切换不可用",
	"think.effort_warn":         "注意：当前模型未声明思考能力，档位不会发送（到设置窗「模型服务」里给模型加上 reasoning）",
	"think.effort_switched":     "推理档位已切换为 {effort}（已写回 config，重启沿用；是否发送由 /think 决定）{note}",
	"think.effort_invalid":      "effort 须为 minimal|low|medium|high|off，当前 {value}",

	// /summary（插入摘要）与自动压缩
	"compact.no_history":      "该节点之上没有可总结的历史（顶层节点之前不插摘要）",
	"compact.canceled":        "已取消插入摘要（会话未改动）",
	"compact.inserted_manual": "已在 {id} 之前插入摘要（手写）",
	"compact.inserted_tokens": "已在 {id} 之前插入摘要（in={in} out={out} tokens）",
	"compact.no_such_node":    "会话树中没有节点 {id}",
	"compact.root_target":     "不能在会话根或会话首节点之前插入摘要（其上没有可总结的历史）",
	"compact.covered":         "该节点之下已被摘要覆盖：新摘要会落在旧摘要之上，水位仍停在旧摘要，不会进入上下文。要重新总结，请把摘要插到旧摘要之后的节点之前",
	"compact.root_no_place":   "会话根节点之前无处可插",
	"compact.err_insert":      "插入摘要",
	"compact.retry_notice":    "摘要输出未匹配模板，正在带提醒重试",
	"compact.auto_done":       "上下文 ≈{est} tokens 达自动压缩阈值 {at}，已生成摘要",
	"compact.auto_failed":     "自动压缩失败（{err}），已回退最旧裁剪",
	"compact.auto_omitted":    "，已省略 {n} 条较早消息",

	// Turn 循环的报错（装配/切段/提交），短语进表、%w 留在 Go 里
	"agent.err_segment":            "切段",
	"agent.err_assemble":           "装配上下文",
	"agent.err_commit_cancel":      "提交取消节点",
	"agent.err_start":              "发起生成",
	"agent.err_generate":           "生成失败",
	"agent.err_commit":             "提交节点",
	"agent.err_tools":              "工具清单",
	"agent.err_commit_segment":     "提交段节点",
	"agent.err_append_pending":     "追加待发消息",
	"agent.err_persist":            "写回 config",
	"agent.err_empty_conversation": "会话为空，无可生成的上下文",
	"agent.err_no_llm":             "LLM 依赖为空",
	"agent.err_no_presenter":       "Presenter 依赖为空",
	"agent.err_no_idgen":           "IDGen 依赖为空",
	"agent.err_no_clock":           "Clock 依赖为空",
	"agent.err.max_turns":          "agent: 超过最大轮次数",
	"agent.err.max_turns_count":    "agent: 超过最大轮次数（{n}）",

	// 上下文装配
	"prompt.err_read_attachment": "读取附件 {name}",
}
