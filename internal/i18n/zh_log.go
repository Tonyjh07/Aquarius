package i18n

// zhLogTexts 运行日志文案（D276 / contracts §7.9）。
//
// 两条 key 一族：`log.<ctx>.<what>` 说发生了什么，`log.act.*` 说当场怎么处置。分开是因为
// 同一件事在不同处置下是两条日志（跳过 vs 回落），把处置写进事件句里，两行就成了两句手抄文案。
//
// 行动位只写**已做的处置**，不写「请检查 …」这类指令——隔天回看，指令既不知道当时怎么处置的，
// 也不知道后来自动恢复没有。动作要按类收敛复用，别一句一条。
var zhLogTexts = map[string]string{
	// 处置（跨上下文复用）
	"log.act.skip":                 "已跳过",
	"log.act.skip_missing":         "已跳过缺失引用，其余模块照常展开",
	"log.act.skip_key_this_run":    "本次启动跳过它",
	"log.act.stderr_only":          "本次只输出到 stderr",
	"log.act.unattended_disabled":  "本次不启用无人值守（配置文件未改动）",
	"log.act.recorded_unsupported": "已记入 {provider} 的 unsupported_params，后续不再重发",
	"log.act.retry_next_start":     "下次启动仍会先试发该字段",

	// 启动
	"log.boot.file_unavailable": "日志文件打不开（{path}）：{err}",
	"log.boot.auto_yes_overrides_unattended": "配置里开着无人值守，但本次带 -yes，自动允许优先——" +
		"越界操作会被执行而不是拒绝",
	"log.boot.persona_missing": "人格模块树存在缺失引用 {missing}（检查 prompts.modules / " +
		"prompts.persona）",

	// 模型链路
	"log.llm.param_rejected":      "服务端不认参数 {field}，已从请求里剔除",
	"log.llm.param_record_failed": "记录 unsupported_params 失败：{err}",
	"log.llm.key_unavailable":     "provider {provider} 第 {n} 把密钥不可用：{err}",
}
