package i18n

// zhGuiPlatformTexts 平台层的用户面措辞：文件选择框的标题与过滤器名、打开链接的失败提示、
// 热键配置的校验报错、以及「本平台不支持 X」的降级提示。
//
// 这一族和 `ui.settings.*` 分开维护，因为它跨 Windows 与 Linux/macOS 两套实现：同一个概念
// 在两个文件里各写一句（zenity 与 Win32 的对话框标题），**必须共用同一个 key**，否则切语言时
// 会发现两个平台的对话框说着两种话。
//
// 过滤器名（`技能文件 | SKILL.md *.zip`）不是整句文案：zenity 的格式是「显示名 | 模式」，
// 代码用 `名 + " | " + 模式` 拼，所以表里只有显示名那半段。
var zhGuiPlatformTexts = map[string]string{

	// 文件对话框的降级提示（Linux 上没有 zenity / kdialog 时一次性提示）
	"ui.platform.dlg.unavailable_open": "文件选择框（Linux 需 zenity 或 kdialog）",
	"ui.platform.dlg.unavailable_save": "另存为对话框（Linux 需 zenity 或 kdialog）",

	// 对话框标题
	"ui.platform.dlg.pick_file":       "选择文件",
	"ui.platform.dlg.pick_attachment": "选择附件",
	"ui.platform.dlg.skill_file":      "选择技能文件",
	"ui.platform.dlg.skill_dir":       "选择技能文件夹",
	"ui.platform.dlg.character_card":  "选择角色卡文件",
	"ui.platform.dlg.untitled":        "未命名",
	"ui.platform.dlg.export_session":  "导出会话",
	"ui.platform.dlg.open_failed":     "文件对话框没能打开（错误码 {code}），请重试或换个路径",

	// 过滤器显示名（zenity 用「名 | 模式」，Win32 用「名 (模式)」，两处各自拼）
	"ui.platform.filter.all":        "所有文件",
	"ui.platform.filter.docs":       "文档",
	"ui.platform.filter.images":     "图片",
	"ui.platform.filter.common":     "常见文档与图片",
	"ui.platform.filter.skill":      "技能文件",
	"ui.platform.filter.zip":        "压缩包",
	"ui.platform.filter.character":  "角色卡",
	"ui.platform.filter.card_png":   "PNG 卡",
	"ui.platform.filter.card_json":  "JSON 卡",
	"ui.platform.filter.card_charx": "角色卡包",

	// 打开链接
	"ui.platform.url.empty":      "链接为空",
	"ui.platform.url.no_opener":  "本机没有 {cmd}，请手动打开链接",
	"ui.platform.url.not_opened": "系统未打开链接（错误码 {code}）",

	// 热键配置校验（设置窗填错时显示在那一行下面）
	"ui.platform.hotkey.need_combo":       "需要修饰键+键（如 Alt+A）:「{raw}」",
	"ui.platform.hotkey.unknown_modifier": "未知修饰键「{mod}」:「{raw}」",
	"ui.platform.hotkey.need_modifier":    "至少需要一个修饰键:「{raw}」",
	"ui.platform.hotkey.unsupported_key":  "不支持的键「{key}」",

	// 功能降级提示与功能名
	"ui.platform.downgrade":    "当前平台（{os}）暂不支持{feature}，功能已降级。",
	"ui.platform.feature.tray": "系统托盘与全局呼出快捷键",
	"ui.platform.feature.menu": "原生右键菜单",

	// 剪贴板
	"ui.platform.clip.open_failed": "打开剪贴板失败（可能被其他程序占用）：{err}",
}
