package i18n

// zhGuiCommonTexts 图形界面各处共用的短词：按钮、动作、通用后缀。设置窗、欢迎窗、历史窗、
// 菜单与浮层都取这一族，所以它是全 GUI 的**唯一**出处——同一个「取消」不要在各页各写一条，
// 否则改一处口径，另一处还留着旧说法。
//
// 只收「脱离语境也成立」的词。带语境的短句（「未接线（只读占位）」这类占位标记）留在使用它的
// 区域表里：那种句子换语言时整句都要重写，拆成词反而译不准。
var zhGuiCommonTexts = map[string]string{

	// 按钮与动作
	"ui.common.ok":        "确定",
	"ui.common.cancel":    "取消",
	"ui.common.save":      "保存",
	"ui.common.close":     "关闭",
	"ui.common.delete":    "删除",
	"ui.common.edit":      "编辑",
	"ui.common.add":       "添加",
	"ui.common.copy":      "复制",
	"ui.common.paste":     "粘贴",
	"ui.common.search":    "搜索",
	"ui.common.browse":    "浏览…",
	"ui.common.reset":     "恢复默认",
	"ui.common.enable":    "启用",
	"ui.common.disable":   "停用",
	"ui.common.allow":     "允许",
	"ui.common.deny":      "拒绝",
	"ui.common.back":      "上一步",
	"ui.common.confirm_q": "确认？",

	// 通用后缀与状态
	"ui.common.restart_effective": "（重启生效）",
	"ui.common.failed":            "操作失败：{err}",
	"ui.common.none":              "无",
	"ui.common.unset":             "未设",
	"ui.common.auto":              "自动",
	"ui.common.loading":           "加载中…",
}
