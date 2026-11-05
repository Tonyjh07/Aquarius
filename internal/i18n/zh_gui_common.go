package i18n

// zhGuiCommonTexts 图形界面各处共用的短词：按钮、动作、通用后缀。设置窗、欢迎窗、历史窗、
// 菜单与浮层都取这一族，所以它是全 GUI 的**唯一**出处——同一个「取消」不要在各页各写一条，
// 否则改一处口径，另一处还留着旧说法。
//
// 只收「脱离语境也成立」的词与短句。「未接线（只读占位）」是五个页签共用的空态行，五处各写一条
// 会变成五个说法；带专属前缀的（「密钥库未接线」）有额外语境，仍留在各自的区域表里。
var zhGuiCommonTexts = map[string]string{

	// 按钮与动作
	"ui.common.cancel":    "取消",
	"ui.common.save":      "保存",
	"ui.common.close":     "关闭",
	"ui.common.delete":    "删除",
	"ui.common.edit":      "编辑",
	"ui.common.add":       "添加",
	"ui.common.copy":      "复制",
	"ui.common.enable":    "启用",
	"ui.common.disable":   "停用",
	"ui.common.allow":     "允许",
	"ui.common.deny":      "拒绝",
	"ui.common.back":      "上一步",
	"ui.common.confirm_q": "确认？",

	// 通用后缀与状态
	"ui.common.failed": "操作失败：{err}",
	// 图内命令是「通道满即丢」的：投递失败时不能报成功，也不能装作没点过。
	"ui.common.post_dropped": "界面正忙，这条操作没投出去，请再点一次",
	"ui.common.none":         "无",
	"ui.common.unset":        "未设",
	"ui.common.unwired":      "未接线（只读占位）",
}
