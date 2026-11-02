package i18n

// zhTexts 界面文案母版（D275）：key 按区域分组，只增不改语义——意思变了就换 key，别在旧
// key 上改文本，否则两份表里总有一份留着旧意思。
//
// 这里是全仓唯一允许出现用户面中文字面量的地方（I4 的扫描锁盯的就是这条）。
var zhTexts = map[string]string{

	// 启动与首跑
	"boot.config_written": "已生成配置: {path}\n当前 profile: {profile}（指针: {pointer}）\n",
	"boot.first_run.hint": "请编辑 model.providers（base_url / models / api_key），并设置密钥环境变量后重新运行。\n" +
		"  api_key 推荐引用格式 secret:<环境变量名>（例如 secret:AQUARIUS_OPENAI_KEY）；也可直接填明文，但启动时会警告\n",
}
