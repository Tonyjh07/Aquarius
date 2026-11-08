package i18n

// enSessionTexts 会话命令的回执、用法行与报告：与 zh_session.go 同 key、同顺序、同占位符名。
var enSessionTexts = map[string]string{

	// 通用短写
	"common.on":  "on",
	"common.off": "off",

	// /compact
	"session.compact.nothing":       "Nothing to compact: no new history above the summary",
	"session.compact.canceled":      "Compaction canceled (the conversation is unchanged)",
	"session.compact.done":          "Compacted {absorbed} entries into 1 summary (in={in} out={out} tokens)",
	"session.compact.replay_suffix": ";",

	// /unattended
	"usage.unattended":               "Usage: /unattended [on|off]",
	"session.unattended.usage_state": "Usage: /unattended [on|off] (currently {state})",
	"session.unattended.already":     "Unattended is already {state}",
	"session.unattended.off":         "Unattended is off (out-of-bounds operations ask again one by one; written back to config)",
	"session.unattended.on": "Unattended is on (written back to config; takes effect immediately for tool calls)\n" +
		"The permission boundary is unchanged: out-of-bounds operations the model starts are refused on the spot " +
		"with a reason instead of waiting for approval; commands you type yourself (deleting a conversation, " +
		"for instance) and plugin grants still ask. The opposite of -yes, which approves everything.",

	// /permission
	"usage.permission":              "Usage: /permission [read-only|strict|permissive|full-access]",
	"session.permission.already":    "Permission level is already {level}",
	"session.permission.switched":   "Permission level switched to {level} (written back to config; takes effect immediately for tool calls)",
	"session.permission.head":       "Current permission level: {level} (strict by default)",
	"session.permission.sandbox":    "Privileged directory: {path}",
	"session.permission.matrix":     "Pre-approved matrix (anything outside it is confirmed case by case):",
	"session.permission.privileged": "privileged",
	"session.permission.other":      "other",
	"session.permission.tools_ask":  "tools: confirm each time",
	"session.permission.tools_free": "tools: all pre-approved",

	// /memory
	"usage.memory":          "Usage: /memory [conversation id prefix] (the global memory file by default)",
	"session.memory.opened": "Opened {path} in the system editor (takes effect on the next read after you save)",

	// /usage
	"session.usage.mode_estimate": "estimated (calibrated against the usage the server reports, ×{ratio})",
	"session.usage.mode_exact":    "exact (local tokenizer count)",
	"session.usage.context":       "Context now: ≈{est} / {max} tokens ({pct}%, auto-compaction threshold {at} = {atpct}%)",
	"session.usage.counter":       "Counting: {mode}",
	"session.usage.last":          "Last turn measured: in={in} out={out} tokens (reported by the server)",
	"session.usage.total":         "Conversation total (Path): in={in} out={out} tokens ({gen} generations)",
	"session.usage.cost":          ", about ${usd}",

	// /jobs
	"usage.jobs":              "Usage: /jobs [list|logs <id> [lines]|kill <id>]",
	"session.jobs.empty":      "(no background jobs; the model can start one with job_start)",
	"usage.jobs_logs":         "Usage: /jobs logs <id> [lines] (last 50 by default)",
	"session.jobs.bad_tail":   "/jobs logs needs a positive integer line count",
	"session.jobs.logs_empty": "Job {id} has an empty log",
	"session.jobs.logs":       "Job {id} log (last {n} lines):\n{text}",
	"usage.jobs_kill":         "Usage: /jobs kill <id>",
	"session.jobs.killed":     "Killed {id}",
	"session.jobs.hint":       "(/jobs lists the available ids)",

	// /mic
	"usage.mic":             "Usage: /mic [seconds]",
	"session.mic.range":     "Seconds must be between 1 and {max}",
	"session.mic.recording": "Recording, up to {secs} seconds…",

	// 切换会话后的回放提示
	"session.replay.notice":       "{verb} conversation {title} ({id}), replayed {n} entries",
	"replay.verb.restored":        "Restored",
	"replay.verb.started":         "Started",
	"replay.verb.revised":         "Revised",
	"replay.verb.regenerated":     "Regenerated",
	"replay.verb.injected":        "Injected",
	"replay.verb.reparented":      "Reparented",
	"replay.verb.reparent_undone": "Undid the reparent of",
	"replay.verb.moved":           "Moved to",
	"replay.verb.skill_loaded":    "Loaded skill in",
	"replay.verb.switched":        "Switched to",
	"replay.verb.branch_switched": "Switched branch to",
	"replay.verb.imported":        "Imported",
}
