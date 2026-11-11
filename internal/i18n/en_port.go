package i18n

// enPortTexts 与 zh_port.go 同 key、同顺序。
var enPortTexts = map[string]string{

	// 导出格式的标签（回执、用法提示与文件框共用）
	"port.fmt.aqx.label":         "Aquarius archive",
	"port.fmt.chatgpt.label":     "ChatGPT conversation",
	"port.fmt.openai.label":      "OpenAI message list",
	"port.fmt.sillytavern.label": "SillyTavern chat",
	"port.fmt.openwebui.label":   "Open WebUI conversation",

	// 有损说明（无损的 aqx 没有这一条）
	"port.fmt.chatgpt.loss":     "attachments keep only a note; thinking and tool calls are dropped; injected nodes degrade to system; message metadata such as model/usage is dropped",
	"port.fmt.openai.loss":      "only the current chain is kept; side branches, thinking, tools and attachments are dropped; injected nodes degrade to system",
	"port.fmt.sillytavern.loss": "only the current chain is kept; side branches, thinking, tools and attachments are dropped; injected nodes degrade to system; the other side's world book / regex / author's note are not carried",
	"port.fmt.openwebui.loss":   "read-only: feed Open WebUI with the chatgpt format",

	// 注入来源的友好名
	"port.src.unknown":     "unknown source",
	"port.src.manual":      "manual",
	"port.src.manual_rest": "manual {rest}",
	"port.src.job":         "job {rest}",
	"port.src.poller":      "poller {rest}",
	"port.src.tool":        "tool {rest}",
	"port.src.plugin":      "plugin {rest}",
	"port.src.skill":       "skill {rest}",
	"port.src.clock":       "date change",
}
