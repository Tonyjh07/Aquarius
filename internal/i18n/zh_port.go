package i18n

// zhPortTexts 端口层的导出/注入措辞：导出格式的标签与有损说明、注入来源的友好名。
//
// 这几句话必须在端口而不是在适配器：命令面拼用法提示、识别失败要列「支持这几种」、
// 主窗与遮罩共用同一张来源表——那时可能连适配器都没装配。
//
// 注入来源那几条是**整句模板**：原来是「词 + 空格 + 余下」两段拼，英文语序未必相同，
// 所以整句交给 Tf 填，不取一个词再自己接空格。
var zhPortTexts = map[string]string{

	// 导出格式的标签（回执、用法提示与文件框共用）
	"port.fmt.aqx.label":         "Aquarius 归档",
	"port.fmt.chatgpt.label":     "ChatGPT 会话",
	"port.fmt.openai.label":      "OpenAI 消息列表",
	"port.fmt.sillytavern.label": "SillyTavern 对话",
	"port.fmt.openwebui.label":   "Open WebUI 会话",

	// 有损说明（无损的 aqx 没有这一条）
	"port.fmt.chatgpt.loss":     "附件只留一条说明、思考与工具调用不带、注入节点降级为 system、消息的模型/用量等元数据不带",
	"port.fmt.openai.loss":      "只取当前那条链，旁支、思考、工具与附件不带；注入节点降级为 system",
	"port.fmt.sillytavern.loss": "只取当前那条链，旁支、思考、工具与附件不带；注入节点降级为 system；对方的世界书、正则与作者注不会带过来",
	"port.fmt.openwebui.loss":   "只读不写：要供给 Open WebUI，请用 chatgpt 那份",

	// 注入来源的友好名
	"port.src.unknown":     "未知来源",
	"port.src.manual":      "手动",
	"port.src.manual_rest": "手动 {rest}",
	"port.src.job":         "任务 {rest}",
	"port.src.poller":      "观察 {rest}",
	"port.src.tool":        "工具 {rest}",
	"port.src.plugin":      "插件 {rest}",
	"port.src.skill":       "技能 {rest}",
	"port.src.clock":       "日期变更",
}
