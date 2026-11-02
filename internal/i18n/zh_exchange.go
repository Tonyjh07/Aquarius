package i18n

// zhExchangeTexts /export 与 /import 的用法、回执与报错。
//
// 回执是分行的：一行一句事实，拼接交给调用方（每行一条 key），别把整份回执写进一条
// 模板——那样加一行字段就要动两份译文，而漏改只会少印一行。
var zhExchangeTexts = map[string]string{

	// 用法
	"usage.export":         "用法: /export [--format <名字>] [--path <文件>] [--all | --ids <id>[,<id>…]]（缺省只导当前会话；aqx 无损、落在导出目录）",
	"usage.import":         "用法: /import [--pick <序号>]… <文件>（按扩展名与文件头自动认格式；落成新会话）",
	"exchange.spaced_hint": "路径里有空格时用引号括起来，例如 /import \"C:\\我的文档\\会话.aqx\"",

	// 导出回执
	"exchange.export.to":           "已导出到 {path}",
	"exchange.format":              "格式: {name}（{label}）— {fidelity}",
	"exchange.export.multi":        "内容: {convs} 个会话，{nodes} 条消息，{bytes}",
	"exchange.export.single":       "内容: {nodes} 条消息，{bytes}",
	"exchange.entry":               "- {title} · {nodes} 条",
	"exchange.entry.untitled":      "第 {index} 个会话",
	"exchange.fidelity.lossless":   "无损，可原样导回",
	"exchange.fidelity.lossy":      "有损",
	"exchange.fidelity.lossy_note": "有损：{note}",
	"exchange.dropped.head":        "没有带过去的内容:",
	"exchange.dropped.item":        "- {item}",

	// 导入回执
	"exchange.import.to":    "已导入 {path}",
	"exchange.import.multi": "内容: 已导入 {convs} 个会话（{nodes} 条消息）",
	"exchange.import.one":   "内容: {nodes} 条消息（已落成新会话）",

	// 报错
	"exchange.exporter_missing": "本构建未接入导出面（SessionDeps.Exporter）",
	"exchange.importer_missing": "本构建未接入导入面（SessionDeps.Importer）",
	"exchange.all_and_ids":      "--all 与 --ids 只能给一个：要么整库，要么列出来的那几个会话",
	"exchange.format_single":    "{format} 这种格式一次只能写一个会话：整库备份请用 aqx（去掉 --format）",
	"exchange.no_conversations": "库里没有会话可导出",
	"exchange.no_formats":       "导出面没有提供任何格式",
	"exchange.format_readonly":  "{format} 只认不写（{note}）",
	"exchange.unknown_format":   "不认识的导出格式 {name}（可写：{names}）",
	"exchange.no_exports_dir":   "未配置导出目录（SessionDeps.ExportsDir），请用 --path 指定文件",
	"exchange.unknown_import":   "认不出这份文件是什么格式（支持：{formats}）",
	"exchange.empty_import":     "导入失败: 文件里没有可导入的会话",
	"exchange.refresh_failed":   "会话已导入并切换，但界面没能刷新",
	"exchange.replay_failed":    "会话已导入并切换，但历史没能回放",
	"exchange.bad_pick":         "--pick 只接受 1 起的序号（收到 {value}）；多个序号写成 --pick 1,3",
	"exchange.pick_missing":     "--pick 后面没给序号（写成 --pick 1,3）",
	"exchange.bad_structure":    "导入的会话结构不合法",
	"exchange.too_many_names":   "导出目录里同名文件太多，请用 --path 指定文件名",

	// 错误链短语
	"err.list_conversations": "列出会话",
	"err.load_conversation":  "载入会话 {id}",
	"err.mkdir_export":       "创建导出目录",
	"err.create_export":      "创建导出文件",
	"err.export_failed":      "导出失败",
	"err.open_file":          "打开文件",
	"err.read_file":          "读取文件",
	"err.read_head":          "读取文件头",
	"err.import_failed":      "导入失败",
	"err.assign_import_id":   "分配导入会话的身份",
	"err.save_import":        "保存导入的会话",
	"err.stat_target":        "检查目标文件",
}
