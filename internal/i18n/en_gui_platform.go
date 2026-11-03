package i18n

// enGuiPlatformTexts 与 zhGuiPlatformTexts 同 key 同顺序（同构测试盯着）。
var enGuiPlatformTexts = map[string]string{

	"ui.platform.dlg.unavailable_open": "File picker (needs zenity or kdialog on Linux)",
	"ui.platform.dlg.unavailable_save": "Save dialog (needs zenity or kdialog on Linux)",

	"ui.platform.dlg.pick_file":       "Choose a file",
	"ui.platform.dlg.pick_attachment": "Choose an attachment",
	"ui.platform.dlg.skill_file":      "Choose a skill file",
	"ui.platform.dlg.skill_dir":       "Choose a skill folder",
	"ui.platform.dlg.character_card":  "Choose a character card",
	"ui.platform.dlg.untitled":        "Untitled",
	"ui.platform.dlg.export_session":  "Export conversation",
	"ui.platform.dlg.open_failed":     "The file dialog could not open (error {code}). Try again, or use another path.",

	"ui.platform.filter.all":        "All files",
	"ui.platform.filter.docs":       "Documents",
	"ui.platform.filter.images":     "Images",
	"ui.platform.filter.common":     "Common documents and images",
	"ui.platform.filter.skill":      "Skill files",
	"ui.platform.filter.zip":        "Archives",
	"ui.platform.filter.character":  "Character cards",
	"ui.platform.filter.card_png":   "PNG cards",
	"ui.platform.filter.card_json":  "JSON cards",
	"ui.platform.filter.card_charx": "Character card bundles",

	"ui.platform.url.empty":      "The link is empty",
	"ui.platform.url.no_opener":  "{cmd} is not available here; open the link manually",
	"ui.platform.url.not_opened": "The system did not open the link (error {code})",

	"ui.platform.hotkey.need_combo":       "Use a modifier plus a key (e.g. Alt+A): {raw}",
	"ui.platform.hotkey.unknown_modifier": "Unknown modifier {mod}: {raw}",
	"ui.platform.hotkey.need_modifier":    "At least one modifier is required: {raw}",
	"ui.platform.hotkey.unsupported_key":  "Unsupported key {key}",

	"ui.platform.downgrade":    "This platform ({os}) does not support {feature} yet; that feature is degraded.",
	"ui.platform.feature.tray": "the system tray and the global summon hotkey",
	"ui.platform.feature.menu": "the native context menu",

	"ui.platform.clip.open_failed": "Could not open the clipboard (another program may be using it): {err}",
}
