package i18n

// enErrTexts 错误链上的模块包装短语：与 zh_err.go 同 key、同顺序、同占位符名。
var enErrTexts = map[string]string{
	"err.save_conversation": "save conversation",
	"err.write_config":      "write back to config",
	"err.open_memory":       "open memory file",
	"err.estimate_usage":    "estimate usage",
	"err.list_jobs":         "list jobs",
	"err.read_log":          "read log of {id}",
	"err.kill_job":          "kill job",
	"err.replay_notice":     "emit the replay notice",
	"err.replay_history":    "replay history",
	"err.clear_screen":      "clear the screen",

	"err.read_input":         "read input",
	"err.tui_loop":           "event loop exited",
	"err.repl_ask_write":     "write the question",
	"err.repl_ask_read":      "read the answer",
	"err.repl_confirm_write": "write the confirmation prompt",
	"err.repl_confirm_read":  "read the confirmation",

	// 装配缺失：用户装到残缺构建时看到的就是这一行
	"err.memory_editor_unwired": "memory editor is not wired (SessionDeps.Memories)",
	"err.jobs_unwired":          "job manager is not wired (SessionDeps.Jobs), /jobs is unavailable",
	"err.permissions_unwired":   "permission persistence is not wired, cannot switch (config must be writable)",
	"err.unattended_unwired":    "unattended persistence is not wired, cannot toggle (config must be writable)",
}
