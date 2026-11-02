package i18n

// zhErrTexts 错误链上的模块包装短语（`session: <短语>: <原因>`）。
//
// 只放短语，不放整句：%w 要原样喂给 fmt，把动词写进表里等于让每份译文各自维护格式串，
// 一处写错就是错误链断掉。短语本身进表是因为这一行会显示给用户——命令失败时它就是界面
// 上那句话，不是只进调试日志的碎碎念。
var zhErrTexts = map[string]string{
	"err.save_conversation": "保存会话",
	"err.write_config":      "写回 config 失败",
	"err.open_memory":       "打开记忆文件",
	"err.estimate_usage":    "估算用量",
	"err.list_jobs":         "列出任务",
	"err.read_log":          "读取 {id} 日志",
	"err.kill_job":          "终止任务",
	"err.replay_notice":     "回放提示",
	"err.replay_history":    "回放历史",
	"err.clear_screen":      "清屏",

	// 装配缺失：用户装到残缺构建时看到的就是这一行
	"err.memory_editor_unwired": "未配置记忆编辑器（SessionDeps.Memories）",
	"err.jobs_unwired":          "未配置任务管理器（SessionDeps.Jobs），/jobs 不可用",
	"err.permissions_unwired":   "未配置权限持久化，无法切换（需能写回 config）",
	"err.unattended_unwired":    "未配置无人值守持久化，无法切换（需能写回 config）",
}
