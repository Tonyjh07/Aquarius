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
}
