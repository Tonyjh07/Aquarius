package i18n

// zhConfigTexts 填写校验与哨兵报错：采样参数表单、待发清单、按键绑定、LLM 错误分类。
//
// 这些串的共同点是「用户填错了」或「环境不允许」，都以错误文本的形式回到界面上；
// 只供 errors.Is 判定的哨兵也用这里的 key，文本随语言变而身份不变。
var zhConfigTexts = map[string]string{

	// 采样参数表单（port.ParseSamplingText）；数字由调用方预格式化，表里不留 fmt 动词
	"port.err.temperature_nan":   "采样温度 {v} 不是数字（留空 = 交服务端默认）",
	"port.err.temperature_range": "采样温度须在 {min}–{max} 之间（留空 = 交服务端默认）",
	"port.err.top_p_nan":         "top_p {v} 不是数字（留空 = 交服务端默认）",
	"port.err.top_p_range":       "top_p 须在 {min}–{max} 之间（留空 = 交服务端默认）",
	"port.err.max_tokens":        "输出上限 {v} 须为正整数（留空 = 不发送）",
	"port.err.stop_json":         "停止串须写成 JSON 数组，如 [\"\\n\", \"END\"]（留空 = 不发送）",
	"port.err.stop_empty":        "停止串里不能有空串（那不是停止条件）",

	// 待发清单的哨兵：前端据 errors.Is 分档提示，文本只作错误原因
	"port.err.pending_full":     "pending: 待发清单已满",
	"port.err.pending_empty":    "pending: 待发内容为空",
	"port.err.pending_too_long": "pending: 待发内容过长",

	// LLM 错误分类的哨兵（重试层与密钥池按身份判定，文本只作错误原因）
	"port.err.transient": "port: 瞬时错误（可重试）",
	"port.err.quota":     "port: 密钥被限流或额度耗尽",

	// 输入栏三键（uicommon.ParseKeyBindings / Validate）
	"keys.err.empty":          "按键绑定为空",
	"keys.err.empty_segment":  "按键绑定 {v} 有空段",
	"keys.err.bad_modifier":   "按键绑定 {v} 里的 {p} 不是修饰键（可用 Ctrl/Shift/Alt/Super）",
	"keys.err.bad_key":        "按键绑定 {v} 里的键名 {p} 不认识（可用 Enter/Tab/Escape/Space/Backspace 或单个字符）",
	"keys.err.only_modifiers": "按键绑定 {v} 只有修饰键、没有键名",
	"keys.err.missing":        "ui.keys.{name} 未配置",
	"keys.err.collision":      "ui.keys.{name} 与 ui.keys.{other} 撞键（都是 {binding}）",
}
