package i18n

// enConfigTexts 与 zh_config.go 同 key、同顺序。
var enConfigTexts = map[string]string{

	// 采样参数表单（port.ParseSamplingText）
	"port.err.temperature_nan":   "sampling temperature {v} is not a number (leave it empty to use the server default)",
	"port.err.temperature_range": "sampling temperature must be between {min} and {max} (leave it empty to use the server default)",
	"port.err.top_p_nan":         "top_p {v} is not a number (leave it empty to use the server default)",
	"port.err.top_p_range":       "top_p must be between {min} and {max} (leave it empty to use the server default)",
	"port.err.max_tokens":        "output limit {v} must be a positive integer (leave it empty = not sent)",
	"port.err.stop_json":         "stop sequences must be written as a JSON array such as [\"\\n\", \"END\"] (leave it empty = not sent)",
	"port.err.stop_empty":        "a stop sequence cannot be empty (that is not a stop condition)",

	// 待发清单的哨兵：前端据 errors.Is 分档提示，文本只作错误原因
	"port.err.pending_full":     "pending: the pending list is full",
	"port.err.pending_empty":    "pending: the pending text is empty",
	"port.err.pending_too_long": "pending: the pending text is too long",

	// LLM 错误分类的哨兵（只作错误原因，判定走 errors.Is 的身份比较）
	"port.err.transient": "port: transient error (retryable)",
	"port.err.quota":     "port: key rate-limited or quota exhausted",

	// 输入栏三键（uicommon.ParseKeyBindings / Validate）
	"keys.err.empty":          "the key binding is empty",
	"keys.err.empty_segment":  "the key binding {v} has an empty segment",
	"keys.err.bad_modifier":   "{p} in the key binding {v} is not a modifier (available: Ctrl/Shift/Alt/Super)",
	"keys.err.bad_key":        "the key name {p} in the key binding {v} is not recognised (available: Enter/Tab/Escape/Space/Backspace or a single character)",
	"keys.err.only_modifiers": "the key binding {v} has modifiers only and no key name",
	"keys.err.missing":        "ui.keys.{name} is not configured",
	"keys.err.collision":      "ui.keys.{name} collides with ui.keys.{other} (both are {binding})",
}
