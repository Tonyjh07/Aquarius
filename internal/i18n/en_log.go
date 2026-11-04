package i18n

// enLogTexts 运行日志文案的英文版：与 zhLogTexts 同 key、同占位符名（D276 / contracts §7.9）。
//
// 译的是「发生了什么」和「当场怎么处置」，不是给人下指令；所以不用祈使句开头，动词用过去式——
// 这条日志写下来的时候，处置已经做完了。
var enLogTexts = map[string]string{
	// 处置（跨上下文复用）
	"log.act.skip":                 "skipped",
	"log.act.skip_missing":         "skipped the missing reference; the rest expanded as usual",
	"log.act.skip_key_this_run":    "skipped for this run",
	"log.act.stderr_only":          "stderr only this run",
	"log.act.unattended_disabled":  "unattended mode off for this run (config unchanged)",
	"log.act.recorded_unsupported": "recorded in {provider}'s unsupported_params; not resent later",
	"log.act.retry_next_start":     "the field is still sent first on the next start",
	"log.act.continue":             "carried on as usual; the conversation is unaffected",
	"log.act.voice_off":            "no playback this run; text is unaffected",
	"log.act.voice_input_off":      "no speech input this run; typing is unaffected",
	"log.act.keep_raw":             "kept the original text and transcribed as usual",
	"log.act.dropped":              "dropped this receipt; the conversation is unaffected",
	"log.act.deferred_next_start":  "left for the next start",
	"log.act.plaintext_used":       "the plaintext is used as is and stays in config",
	"log.act.verbatim":             "the adapter's own wording; the disposition is in the sentence",
	"log.act.exit":                 "this start exits with a failure",

	// 启动
	"log.boot.file_unavailable": "cannot open the log file ({path}): {err}",
	"log.boot.auto_yes_overrides_unattended": "-yes was given while unattended mode is on; " +
		"auto-allow wins, so out-of-bounds actions run instead of being denied",
	"log.boot.persona_missing": "persona module tree has missing references {missing} " +
		"(check prompts.modules / prompts.persona)",

	// 模型链路
	"log.llm.param_rejected":      "server does not accept parameter {field}; dropped from the request",
	"log.llm.param_record_failed": "failed to record unsupported_params: {err}",
	"log.llm.key_unavailable":     "provider {provider} key #{n} is unusable: {err}",
	"log.llm.chain_narrowed":      "the fallback chain is narrower than the current model",

	// 语音
	"log.audio.tts_unconfigured": "speech playback is on, but {why}",
	"log.audio.asr_unavailable":  "speech recognition is unavailable: {why}",
	"log.audio.tts_unavailable":  "speech synthesis is unavailable: {why}",
	"log.audio.zhconv_unavailable": "traditional-to-simplified conversion is unavailable; " +
		"transcripts stay as they are: {err}",
	"log.audio.zhconv_failed": "traditional-to-simplified conversion failed: {err}",

	// 输出与回执
	"log.ui.tts_output_unavailable": "speech playback output could not be built: {err}",
	"log.ui.output_failed":          "failed to deliver a receipt: {err}",

	// 后台任务与附件
	"log.job.settle_failed":            "the ledger entry for job {id} could not be settled: {err}",
	"log.job.ledger_unreadable":        "cannot read the job ledger: {err}",
	"log.job.unfinished_report_failed": "jobs left unfinished last time were not reported into the session: {err}",
	"log.attach.gc_skipped":            "could not collect attachment references; nothing cleaned this run: {err}",
	"log.attach.gc_failed":             "attachment cleanup failed: {err}",

	// 技能、联网、密钥
	"log.skill.entry_skipped":      "skill entry skipped: {text}",
	"log.web.provider_unavailable": "web {side} provider #{index} ({kind}) is unavailable: {err}",
	"log.perm.plaintext_key":       "a plaintext key was found at startup",

	// 会话、转交与前端
	"log.session.replay_failed":             "history replay failed",
	"log.delegate.ledger_unreadable":        "cannot read the delegate ledger: {err}",
	"log.delegate.unfinished_report_failed": "delegations left unfinished last time were not reported into the session: {err}",
	"log.ui.terminal_suspend_failed":        "failed to hand the terminal to the editor: {err}",
	"log.ui.terminal_resume_failed":         "failed to take the terminal back from the editor: {err}",
	"log.ui.input_failed":                   "failed to read input: {err}",

	// 适配器原话（过渡形状，D276 决策 5）
	"log.plugin.line":   "{text}",
	"log.skill.line":    "{text}",
	"log.session.line":  "{text}",
	"log.job.line":      "{text}",
	"log.delegate.line": "{text}",
	"log.ui.line":       "{text}",
	"log.perm.line":     "{text}",
}
