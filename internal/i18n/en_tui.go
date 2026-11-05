package i18n

// enTuiTexts 与 zh_tui.go 同 key、同顺序。
var enTuiTexts = map[string]string{

	// 提问：逐题提示与回答说明
	"ask.counter":       "(question {n}/{total})",
	"ask.single_picked": "(single choice, taking only \"{picked}\")",

	// uitui 的输入说明：空行被输入行吞掉，不提「空行 = 不答」
	"ask.hint.direct": "(type the answer directly; /dismiss = give up on everything)",
	"ask.hint.multi":  "(multiple choice: separate numbers with commas or spaces; typing text instead = your own answer; /dismiss = give up on everything)",
	"ask.hint.number": "(type a number; typing text instead = your own answer; /dismiss = give up on everything)",

	// repl 的输入说明：命令行多一条「空行 = 不答」
	"ask.hint.direct_skip": "(type the answer directly; empty line = skip; /dismiss = give up on everything)",
	"ask.hint.multi_skip":  "(multiple choice: separate numbers with commas or spaces; typing text instead = your own answer; empty line = skip; /dismiss = give up on everything)",
	"ask.hint.number_skip": "(type a number; typing text instead = your own answer; empty line = skip; /dismiss = give up on everything)",

	// TUI 状态行与注入行
	"tui.state.idle":        "not triggered",
	"tui.state.fired":       "response triggered",
	"tui.status.inject":     "Inject · {source} · {state}",
	"tui.status.model":      "Model {model}",
	"tui.status.permission": "Permission {level}",
	"tui.status.effort":     "Effort {effort}",
	"tui.status.unattended": "Unattended",
	"tui.status.pending":    "{n} queued (Ctrl+U to undo)",
	"tui.hint.scroll":       "PgUp/PgDn to scroll",
	"tui.hint.cancel":       "Ctrl+C to cancel",

	// TUI 通知
	"tui.notice.pending_undone": "undid the queued input: {preview}",
	"tui.notice.buffer_full":    "the input buffer is full, this line was not executed",

	// 附件占位（终端、TUI、GUI 共用）
	"attach.transcript":  "[audio transcript]",
	"attach.image":       "[image]",
	"attach.image_named": "[image: {name}]",
}
