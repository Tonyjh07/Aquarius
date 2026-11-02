package i18n

// enRuntimeTexts 会话生命周期、插件、/watch 与 /delegate 的监视提示：回执与报错：
// 与 zh_runtime.go 同 key、同顺序。
var enRuntimeTexts = map[string]string{

	// 会话装配：缺依赖时启动即失败，这一行是用户看到的全部
	"session.deps.store_missing": "the store dependency is nil",
	"session.deps.agent_missing": "the agent dependency is nil",
	"session.deps.idgen_missing": "the IDGen dependency is nil",
	"session.deps.clock_missing": "the Clock dependency is nil",

	// 会话生命周期：错误链短语（`session: <短语>: <原因>`）
	"session.err.init":        "initialize the conversation",
	"session.err.persona":     "write the persona root message",
	"session.err.append":      "append the message",
	"session.err.restore":     "restore conversation {id}",
	"session.err.ingest":      "ingest the input",
	"session.save.note":       " (and saving failed: {save})",
	"session.ingest.empty":    "the ingest produced no content",
	"session.ingest.no_input": "the text input is empty",

	// 多模态输入：认不出 kinds 时说明当前支持面
	"session.ingest.no_ingestor": "no ingestor can handle {kind} input (files and clipboard are supported; voice input is not enabled yet)",

	// 命令面
	"session.unknown_command":    "unknown command /{name} (see /help for the available commands)",
	"session.flag_missing_value": "{flag} needs a value",

	// 待发清单（D272）：切走会话时不静默丢东西
	"session.pending.dropped": " ({n} pending messages were not carried over with the conversation)",

	// /plugin
	"plugin.unwired":         "plugin host is not wired (SessionDeps.Plugins), /plugin is unavailable",
	"plugin.usage.list":      "Usage: /plugin list",
	"plugin.usage.enable":    "Usage: /plugin enable <name>",
	"plugin.usage.disable":   "Usage: /plugin disable <name>",
	"plugin.usage.logs":      "Usage: /plugin logs [name]",
	"plugin.usage":           "Usage: /plugin [list|enable <name>|disable <name>|logs [name]]",
	"plugin.disabled":        "Disabled {name}",
	"plugin.enabled":         "Enabled {name}",
	"plugin.enabled_status":  "Enabled {name} → {status}",
	"plugin.enabled_note":    " ({error})",
	"plugin.dynamic_head":    "\nDynamic commands (MCP prompts):\n  /",
	"plugin.list.empty":      "no plugins are declared (config mcpServers or ~/.aquarius/plugins/*/plugin.json)",
	"plugin.list.head":       "Plugins ({n}):",
	"plugin.list.caps":       " caps:{caps}",
	"plugin.list.calls":      " calls {calls}",
	"plugin.list.errors":     ", failed {errors}",
	"plugin.list.restarts":   " restarts {restarts}",
	"plugin.list.last_error": " error:{error}",
	"plugin.cap.granted":     "(granted)",
	"plugin.cap.ungranted":   "(not granted)",

	// /watch：清单与判据
	"watch.unwired":           "watcher is not wired (SessionDeps.Watches), /watch is unavailable",
	"watch.usage":             "Usage: /watch [list|stop <id>]",
	"watch.usage.stop":        "Usage: /watch stop <id>",
	"watch.err_list":          "list watchers",
	"watch.err_stop":          "stop the watcher",
	"watch.list.empty":        "(no watchers; the model can declare one with watch_start)",
	"watch.list.item":         "    {criterion}  every {interval}, checked {checks} times, ends at {ends}",
	"watch.see_ids":           " (see /watch for the available IDs)",
	"watch.stopped":           "Stopped watching {id}",
	"watch.criterion.changed": "Criterion: the output changed",
	"watch.criterion.match":   "Criterion: output matches /{pattern}/",

	// 任务收尾观察者（D189）：报告与日志送不出去时的运行日志
	"watch.job.wait_failed":         "failed to wait for job {id} to finish: {error}",
	"watch.job.read_log_failed":     "failed to read the log of job {id}: {error}",
	"watch.job.deliver_no_injector": "the completion report of job {id} was not delivered: the injector is not ready",
	"watch.job.deliver_closed":      "the completion report of job {id} was not delivered: the conversation is closed",
	"watch.job.deliver_failed":      "the completion report of job {id} was not delivered: {error}",

	// 观察器（D190）：报告送不出去时的运行日志
	"watch.report.closed": "the report of watch {id} was not delivered: the conversation is closed",
	"watch.report.failed": "the report of watch {id} was not delivered: {error}",

	// /delegate
	"delegate.usage":             "Usage: /delegate [list|kill <id>]",
	"delegate.usage.kill":        "Usage: /delegate kill <id>",
	"delegate.unwired":           "delegate surface is not wired (SessionDeps.Delegate), /delegate is unavailable",
	"delegate.err_list":          "list external agents",
	"delegate.err_kill":          "kill delegation",
	"delegate.state.available":   "available",
	"delegate.state.unavailable": "unavailable",
	"delegate.name.titled":       " ({title})",
	"delegate.name.versioned":    " ({title} {version})",
	"delegate.killed":            "Killed delegation {id}",
	"delegate.empty_hint": "No external agent is configured. Add one under delegate.agents in config.json (command plus optional args/env/workdir) " +
		"and make sure the command is on PATH; the model has no delegate tool until then.",

	// 转交收尾观察者（D261）：销账与注入失败
	"delegate.settle_failed": "failed to settle delegation {id}: {error}",
	"delegate.report_failed": "failed to inject the report of delegation {id}: {error}",

	// 新会话的默认标题（首条消息后会被摘要改写）
	"session.default_title": "New conversation",
}
