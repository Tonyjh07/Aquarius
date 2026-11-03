package i18n

// zhGuiSettingsTexts 设置窗骨架与表单：settings*.go（页面注册、表单字段、行渲染、底栏动作、子管理面）。各页签自己的行列在 zh_gui_page_*.go。
var zhGuiSettingsTexts = map[string]string{

	// 设置窗导航页名。页面模块的 Key() 返回英文标识（"audio" 这类），页名在这里取词：
	// 标识不随语言变，所以页序、日志与测试里的定位都靠它，只有显出来的字跟着语言走。
	"ui.settings.page.general":    "常规",
	"ui.settings.page.model":      "模型",
	"ui.settings.page.providers":  "模型服务",
	"ui.settings.page.secrets":    "密钥库",
	"ui.settings.page.chat":       "对话",
	"ui.settings.page.prompt":     "系统提示词",
	"ui.settings.page.tools":      "工具与 MCP",
	"ui.settings.page.delegate":   "外部代理",
	"ui.settings.page.skills":     "技能",
	"ui.settings.page.characters": "角色",
	"ui.settings.page.web":        "联网",
	"ui.settings.page.audio":      "语音",
	"ui.settings.page.adv":        "高级",
	"ui.settings.page.about":      "关于",
}
