package i18n

// zhBootTexts 启动与首跑：配置回执、首次运行的编辑指引。
var zhBootTexts = map[string]string{
	"boot.config_written": "已生成配置: {path}\n当前 profile: {profile}（指针: {pointer}）\n",
	"boot.first_run.hint": "请编辑 model.providers（base_url / models / api_key），并设置密钥环境变量后重新运行。\n" +
		"  api_key 推荐引用格式 secret:<环境变量名>（例如 secret:DEEPSEEK_API_KEY）；也可直接填明文，但启动时会警告\n",

	// 顶层 panic 写崩溃报告（D299）
	"cmd.crash.saved":        "程序异常退出，崩溃报告已写入：{path}",
	"cmd.crash.write_failed": "程序异常退出，崩溃报告写入失败：{error}",
	"cmd.crash.notice":       "上次运行异常退出，崩溃报告：{path}（发出去前先看一眼，里面可能有对话内容）",
}
