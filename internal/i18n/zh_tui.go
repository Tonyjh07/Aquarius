package i18n

// zhTuiTexts 终端两套前端（tui / repl）与它们共用的附件标记：提问提示、状态行、注入行。
//
// 提问是同一份问卷，两处用同一族 ask.* 前缀；差别只有一句：repl 的空行是一次「不答」，
// 所以那边多一条 `<...>_skip`（tui 的空行被输入行吞掉，提了反而误导）。
var zhTuiTexts = map[string]string{

	// 提问：逐题提示与回答说明
	"ask.counter":       "（第 {n}/{total} 题）",
	"ask.single_picked": "（本题为单选，只取「{picked}」）",

	// uitui 的输入说明：空行被输入行吞掉，不提「空行 = 不答」
	"ask.hint.direct": "（直接输入答案；/dismiss = 放弃全部）",
	"ask.hint.multi":  "（可多选：编号用逗号或空格分隔；直接输入文字 = 自己的答案；/dismiss = 放弃全部）",
	"ask.hint.number": "（输入编号；直接输入文字 = 自己的答案；/dismiss = 放弃全部）",

	// repl 的输入说明：命令行多一条「空行 = 不答」
	"ask.hint.direct_skip": "（直接输入答案；空行 = 不答；/dismiss = 放弃全部）",
	"ask.hint.multi_skip":  "（可多选：编号用逗号或空格分隔；直接输入文字 = 自己的答案；空行 = 不答；/dismiss = 放弃全部）",
	"ask.hint.number_skip": "（输入编号；直接输入文字 = 自己的答案；空行 = 不答；/dismiss = 放弃全部）",

	// TUI 状态行与注入行
	"tui.state.idle":        "未触发",
	"tui.state.fired":       "已触发响应",
	"tui.status.inject":     "注入 · {source} · {state}",
	"tui.status.model":      "模型 {model}",
	"tui.status.permission": "权限 {level}",
	"tui.status.effort":     "思考档 {effort}",
	"tui.status.unattended": "无人值守",
	"tui.status.pending":    "待发 {n} 条（Ctrl+U 撤销）",
	"tui.hint.scroll":       "PgUp/PgDn 滚动",
	"tui.hint.cancel":       "Ctrl+C 取消",

	// TUI 通知
	"tui.notice.pending_undone": "已撤销待发: {preview}",
	"tui.notice.buffer_full":    "输入缓冲已满，此行未执行",

	// 附件占位（终端、TUI、GUI 共用）
	"attach.transcript":  "〔音频转写〕",
	"attach.image":       "〔图片〕",
	"attach.image_named": "〔图片：{name}〕",
}
