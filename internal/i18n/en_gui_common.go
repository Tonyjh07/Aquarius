package i18n

// enGuiCommonTexts 与 zhGuiCommonTexts 同 key 同顺序（同构测试盯着）。
var enGuiCommonTexts = map[string]string{

	// 按钮与动作
	"ui.common.cancel":    "Cancel",
	"ui.common.save":      "Save",
	"ui.common.close":     "Close",
	"ui.common.delete":    "Delete",
	"ui.common.edit":      "Edit",
	"ui.common.add":       "Add",
	"ui.common.copy":      "Copy",
	"ui.common.enable":    "Enable",
	"ui.common.disable":   "Disable",
	"ui.common.allow":     "Allow",
	"ui.common.deny":      "Deny",
	"ui.common.back":      "Back",
	"ui.common.confirm_q": "Confirm?",

	// 通用后缀与状态
	"ui.common.failed":       "Failed: {err}",
	"ui.common.post_dropped": "The UI was busy; that action was not submitted, click again",
	"ui.common.none":         "None",
	"ui.common.unset":        "Not set",
	"ui.common.unwired":      "Not wired yet (read-only placeholder)",
	// List separator used when the UI joins "A, B, C" (D275: no literals in source).
	"ui.common.list_sep": ", ",
}
