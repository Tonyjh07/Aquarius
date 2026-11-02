package i18n

// zhBootTexts 启动与首跑：配置回执、首次运行的编辑指引。
var zhBootTexts = map[string]string{
	"boot.config_written": "已生成配置: {path}\n当前 profile: {profile}（指针: {pointer}）\n",
	"boot.first_run.hint": "请编辑 model.providers（base_url / models / api_key），并设置密钥环境变量后重新运行。\n" +
		"  api_key 推荐引用格式 secret:<环境变量名>（例如 secret:AQUARIUS_OPENAI_KEY）；也可直接填明文，但启动时会警告\n",
}
