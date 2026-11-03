package i18n

// enGuiPageAdminTexts 与 zhGuiPageAdminTexts 同 key 同顺序（同构测试盯着）。
var enGuiPageAdminTexts = map[string]string{

	// 工具与 MCP 页
	"ui.page.tools.unwired":         "Not wired (read-only placeholder)",
	"ui.page.tools.section_builtin": "Built-in tools (toggles apply immediately, no restart)",
	"ui.page.tools.section_mcp":     "MCP and plugins (from config / plugin directory)",
	"ui.page.tools.no_plugins":      "No plugins declared (config mcpServers or plugin directory)",

	// 外部代理页
	"ui.page.delegate.unwired":        "Not wired (read-only placeholder)",
	"ui.page.delegate.section_agents": "Agents (config delegate.agents)",
	"ui.page.delegate.empty": "No external agents yet. Add an entry under delegate.agents in config.json " +
		"(at least name and command); it shows up here after a restart. Without any agent the model gets no delegate* tools.",
	"ui.page.delegate.note": "Availability only checks that the command is on PATH; there is no handshake — " +
		"starting every agent at launch would trade the user's time for a list, so the title and version " +
		"only appear once the agent has actually run. Everything changed here is config and " +
		"**takes effect after restart**: the handoff manager assembles from the config read at startup. " +
		"To delete an agent, edit config.json (this page only edits, never deletes).",
	"ui.page.delegate.refresh":      "Recheck",
	"ui.page.delegate.refresh_hint": "Rescan PATH after installing an agent elsewhere; no config is changed",
	"ui.page.delegate.rescanned":    "Rechecked (availability computed from the current PATH)",

	// 技能页
	"ui.page.skills.unwired":           "Not wired (read-only placeholder)",
	"ui.page.skills.section_installed": "Installed skills",
	"ui.page.skills.empty":             "No skills yet — give a SKILL.md, a folder, an archive or a GitHub link below to install one",
	"ui.page.skills.section_install":   "Install / import",
	"ui.page.skills.note": "Accepts a single SKILL.md file, a skill folder, a .zip archive or a GitHub link " +
		"(append #branch if needed). What gets installed is a copy: updating overwrites local edits " +
		"in that copy, and uninstalling deletes only the copy. A skill with the same name is never " +
		"overwritten — uninstall it first.",
	"ui.page.skills.source_label":       "Source",
	"ui.page.skills.source_hint":        "Absolute path or GitHub link (e.g. https://github.com/o/r or …#branch)",
	"ui.page.skills.install":            "Install",
	"ui.page.skills.pick_file":          "Choose file…",
	"ui.page.skills.pick_dir":           "Choose folder…",
	"ui.page.skills.pick_hint":          "The picked path only fills the source box above; nothing is written until you click Install",
	"ui.page.skills.section_dirs":       "Skill directories (searched in order; the first match wins)",
	"ui.page.skills.dir_add_label":      "Add directory",
	"ui.page.skills.dir_add_hint":       "Extra skill root (absolute path)",
	"ui.page.skills.rescan":             "Rescan",
	"ui.page.skills.section_warnings":   "Entries not loaded",
	"ui.page.skills.confirm_uninstall":  "Confirm uninstall",
	"ui.page.skills.update":             "Update",
	"ui.page.skills.uninstall":          "Uninstall",
	"ui.page.skills.remove_dir":         "Remove",
	"ui.page.skills.status_dir_added":   "Skill directory added (effective immediately)",
	"ui.page.skills.status_rescanned":   "Skill directories rescanned",
	"ui.page.skills.status_uninstalled": "Uninstalled skill {name}",
	"ui.page.skills.status_dir_removed": "Removed skill directory {path}",

	// 角色页
	"ui.page.characters.unwired":          "Not wired (read-only placeholder)",
	"ui.page.characters.section_imported": "Imported character cards",
	"ui.page.characters.read_failed":      "Failed to read the character card directory: {err}",
	"ui.page.characters.empty":            "No character cards yet — pick a card file (.png / .json / .charx) below to import",
	"ui.page.characters.section_import":   "Import character card",
	"ui.page.characters.note": "Supports TavernCard character cards: PNG image cards, card.json and .charx bundles. " +
		"What you import is a copy, read-only against the original card; a card with the same name is " +
		"never overwritten — delete it first.",
	"ui.page.characters.source_label":   "Card file",
	"ui.page.characters.source_hint":    "Absolute path of the character card file (.png / .json / .charx)",
	"ui.page.characters.import":         "Import",
	"ui.page.characters.pick_file":      "Choose file…",
	"ui.page.characters.pick_hint":      "The picked path only fills the source box above; nothing is written until you click Import",
	"ui.page.characters.start_chat":     "Start chat",
	"ui.page.characters.confirm_delete": "Confirm delete",
	"ui.page.characters.need_path":      "Fill in the card file path first (or use Choose file…)",
	"ui.page.characters.imported":       "Import submitted: {path} (see the main window for the result)",
	"ui.page.characters.started":        "Chat started: {slug}",
	"ui.page.characters.deleted":        "Character card deleted: {slug}",

	// 密钥库页
	"ui.page.secrets.unwired":         "Not wired (read-only placeholder)",
	"ui.page.secrets.section_entries": "Stored secrets (values are never shown, only the last 4 characters)",
	"ui.page.secrets.empty":           "The secret store is empty — fill in a name and a value below to store one",
	"ui.page.secrets.section_add":     "Add / overwrite",
	"ui.page.secrets.name_label":      "Secret name",
	"ui.page.secrets.name_hint":       "e.g. AQUARIUS_OPENAI_API_KEY; reference it as secret:<name>",
	"ui.page.secrets.value_label":     "Secret value",
	"ui.page.secrets.value_hint":      "Never shown again once stored; leave empty to skip",
	"ui.page.secrets.add":             "Store in secret store",
	"ui.page.secrets.confirm_delete":  "Confirm delete",

	// 对话页（限额五项）
	"ui.page.chat.section_limits":    "Limits",
	"ui.page.chat.max_turns":         "Max turns",
	"ui.page.chat.max_turns_hint":    "Tool-call rounds per request (empty = keep current)",
	"ui.page.chat.max_ctx":           "Context limit",
	"ui.page.chat.max_ctx_hint":      "Tokens; the budget behind auto-compaction and truncation (empty = keep current)",
	"ui.page.chat.compact":           "Compact threshold",
	"ui.page.chat.compact_hint":      "{min}–{max}; auto-compaction fires above this usage (empty = keep current)",
	"ui.page.chat.tool_chars":        "Tool output limit",
	"ui.page.chat.tool_chars_hint":   "Characters; longer output is truncated (empty = keep current)",
	"ui.page.chat.tool_timeout":      "Tool timeout",
	"ui.page.chat.tool_timeout_hint": "Seconds; the default timeout per call (empty = keep current)",

	// 高级页
	"ui.page.adv.section_raw": "Raw config.json",
	"ui.page.adv.raw_hint":    "Whole-file editing; JSON is validated before saving and the file is left untouched if parsing fails",

	// 关于页
	"ui.page.about.tagline": "Your minimal AI assistant",
	"ui.page.about.privacy": "No usage data is collected and nothing is reported online: conversations, attachments " +
		"and config all stay on this machine. Apart from the model service, speech service and MCP servers " +
		"you configure yourself, this program talks to no other service.",
	"ui.page.about.section_build":   "Build",
	"ui.page.about.version_label":   "Build version",
	"ui.page.about.runtime_label":   "Runtime",
	"ui.page.about.section_project": "Project",
	"ui.page.about.project_label":   "Project home",
	"ui.page.about.section_legal":   "Copyright and license",
	"ui.page.about.copyright_label": "Copyright",
	"ui.page.about.license_label":   "License",
	"ui.page.about.legal_note":      "The icon and product name belong to the author; the full license text is in the program directory and on the project home page.",
	"ui.page.about.open_project":    "Open project home",
	"ui.page.about.copy_link":       "Copy link",
	"ui.page.about.dev_build":       "Development build (no version injected)",
	"ui.page.about.no_browser":      "Cannot open the browser: platform not wired; copy the link and open it manually",
	"ui.page.about.open_failed":     "Cannot open the browser: {err}; copy the link and open it manually",
	"ui.page.about.opened":          "Opened in your browser",
	"ui.page.about.copied":          "Link copied",
}
