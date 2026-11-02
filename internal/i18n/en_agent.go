package i18n

// enAgentTexts 模型与思考、Turn 循环提示、上下文压缩：与 zh_agent.go 同 key、同顺序、
// 同占位符名。
var enAgentTexts = map[string]string{

	// 用法
	"usage.model":       "Usage: /model [name] or /model probe [name]",
	"usage.model_probe": "Usage: /model probe [model name] (omit to probe the current model)",
	"usage.think":       "Usage: /think [on|off]",
	"usage.effort":      "Usage: /effort [minimal|low|medium|high|off]",
	"usage.summary":     "Usage: /summary <id> [summary text] (without text, the current model summarizes the history above that node)",

	// /model：当前口径与切换
	"model.unwired":          "no model service configured (SessionDeps.Models); /model is unavailable",
	"model.switch_unwired":   "no model write-back configured (SessionDeps.ModelSwitch); switching with /model is unavailable",
	"model.probe_unwired":    "no capability probe configured (SessionDeps.Prober); /model probe is unavailable",
	"model.switched":         "Model switched to {model} (written back to config, kept across restarts)",
	"model.current_provider": "Current provider: {provider}",
	"model.current":          "Current model: {model}",
	"model.caps":             "Effective capabilities: {caps}",
	"model.caps_off":         "not enabled: {caps} (declare them in model.providers[].models[].caps{cause})",
	"model.culprit":          "; narrowed by {names}",
	"model.chain":            "Degradation chain:",
	"model.no_list":          " (the model service returned no list; switch directly with /model <name>)",
	"model.list_head":        "Available models ({n}):",
	"model.cost":             "${in}/${out} per million tokens",
	"model.caps_none":        "(all three undeclared)",
	"model.list_sep":         ", ",

	// 能力名：完整写法（能力行/探测）与短写（清单标注/链路归因）
	"model.cap_tools":      "tool use",
	"model.cap_reasoning":  "reasoning",
	"model.cap_vision":     "image input",
	"model.mark_tools":     "tools",
	"model.mark_reasoning": "reasoning",
	"model.mark_vision":    "images",
	"model.link_narrowed":  "{provider}/{model} loses {lost}",

	// /model probe
	"model.probe_head":          "Capability probe {provider} / {model}",
	"model.probe_reason":        " ({reason})",
	"model.probe_usage":         "usage {in}->{out} tokens",
	"model.probe_written":       "Config written back for the conclusive items; effective after a restart (inconclusive items keep their declared values)",
	"model.probe_unchanged":     "Config unchanged (no conclusive item, or the results match the current declarations)",
	"model.verdict_supported":   "supported",
	"model.verdict_unsupported": "unsupported",
	"model.verdict_unknown":     "inconclusive",
	"model.elapsed_unknown":     "duration unknown",

	// 上下文预算行：生效值，以及它是被模型窗口还是全局上限压住的（D177）
	"model.budget_global_only": "Context budget: {effective} tokens (global limit; the model declares no window)",
	"model.budget_model":       "Context budget: {effective} tokens (the model window {window} is binding; global limit {limit})",
	"model.budget_global":      "Context budget: {effective} tokens (the global limit is binding; model window {window})",

	// 错误链短语（/model 走 session: 前缀，与 /permission 同口径）
	"model.err_list":  "list available models",
	"model.err_probe": "probe",

	// /think
	"think.state":             "Native thinking: {state} (/think is the master switch; when off, reasoning_effort and enable_thinking are not sent)",
	"think.no_reasoning":      "The current model declares no reasoning capability (no reasoning in the model service caps); the degradation chain ands to off, so the parameters are not sent",
	"think.effort_line":       "Reasoning effort: {effort} (/effort; sent along with the switch)",
	"think.effort_unset":      "Reasoning effort: not set (server default; set it with /effort)",
	"think.usage_state":       "Usage: /think [on|off] (currently {value})",
	"think.already":           "Native thinking is already {state}",
	"think.unwired":           "no thinking persistence configured (SessionDeps.Thinking); switching with /think is unavailable",
	"think.switched":          "Native thinking switched to {state} (written back to config, kept across restarts)",
	"think.warn_no_reasoning": "Note: the current model declares no reasoning capability, so the parameters are still not sent (add reasoning to the model under \"Model service\" in settings, or probe again)",

	// /effort
	"think.effort_only":         "Reasoning effort: {effort} (whether it is sent is up to /think)",
	"think.effort_unset_usage":  "Reasoning effort: not set (server default; /effort minimal|low|medium|high|off)",
	"think.effort_cleared":      "Reasoning effort cleared",
	"think.effort_cleared_note": "Reasoning effort cleared (written back to config; reasoning_effort is no longer sent while /think is on)",
	"think.effort_already":      "Reasoning effort is already {effort}",
	"think.effort_unwired":      "no effort persistence configured (SessionDeps.Thinking); switching with /effort is unavailable",
	"think.effort_warn":         "Note: the current model declares no reasoning capability, so the effort is not sent (add reasoning to the model under \"Model service\" in settings)",
	"think.effort_switched":     "Reasoning effort switched to {effort} (written back to config, kept across restarts; whether it is sent is up to /think){note}",
	"think.effort_invalid":      "effort must be minimal|low|medium|high|off, got {value}",

	// /summary（插入摘要）与自动压缩
	"compact.no_history":      "No history above that node to summarize (no summary is inserted before a top-level node)",
	"compact.canceled":        "Summary insertion canceled (the conversation is unchanged)",
	"compact.inserted_manual": "Inserted a summary before {id} (hand-written)",
	"compact.inserted_tokens": "Inserted a summary before {id} (in={in} out={out} tokens)",
	"compact.no_such_node":    "No such node in the conversation tree: {id}",
	"compact.root_target":     "Cannot insert a summary before the conversation root or its first node (no history above to summarize)",
	"compact.covered":         "That node is already covered by a summary: a new summary would sit above the old one, the waterline would stay there and it would never enter the context (to summarize again, insert the summary before a node that comes after the old summary)",
	"compact.root_no_place":   "there is no place to insert before the conversation root",
	"compact.err_insert":      "insert summary",
	"compact.retry_notice":    "The summary output did not match the template; retrying with a reminder",
	"compact.auto_done":       "Context is ≈{est} tokens, past the auto-compaction threshold {at}; a summary was generated",
	"compact.auto_failed":     "Auto-compaction failed ({err}); fell back to trimming the oldest messages",
	"compact.auto_omitted":    ", {n} older messages omitted",

	// Turn 循环的报错（装配/切段/提交），短语进表、%w 留在 Go 里
	"agent.err_segment":            "cut segment",
	"agent.err_assemble":           "assemble context",
	"agent.err_commit_cancel":      "commit the canceled node",
	"agent.err_start":              "start generation",
	"agent.err_generate":           "generation failed",
	"agent.err_commit":             "commit node",
	"agent.err_tools":              "read the tool list",
	"agent.err_commit_segment":     "commit the segment node",
	"agent.err_append_pending":     "append the pending message",
	"agent.err_persist":            "write config",
	"agent.err_empty_conversation": "the conversation is empty; there is nothing to generate from",
	"agent.err_no_llm":             "LLM dependency is nil",
	"agent.err_no_presenter":       "Presenter dependency is nil",
	"agent.err_no_idgen":           "IDGen dependency is nil",
	"agent.err_no_clock":           "Clock dependency is nil",
	"agent.err_no_model":           "model is empty",
	"agent.err.max_turns":          "agent: ran out of turns",
	"agent.err.max_turns_count":    "agent: ran out of turns ({n})",

	// 上下文装配
	"prompt.err_read_attachment": "read attachment {name}",
}
