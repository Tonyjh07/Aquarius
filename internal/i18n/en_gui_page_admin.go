package i18n

// enGuiPageAdminTexts 与 zhGuiPageAdminTexts 同 key 同顺序（同构测试盯着）。
var enGuiPageAdminTexts = map[string]string{

	// 工具与 MCP 页
	"ui.page.tools.section_builtin": "Built-in tools (toggles apply immediately, no restart)",
	"ui.page.tools.section_mcp":     "MCP",
	"ui.page.tools.section_policy":  "Allow policy for external agents",
	"ui.page.tools.section_dir":     "Directory-wide allowances (from always-allow in the confirm bubble)",
	"ui.page.tools.dir_rule":        "{tool}: no longer asked in this directory",
	"ui.page.tools.dir_revoke":      "Revoke",
	"ui.page.tools.no_plugins":      "No plugins declared. Add one to mcpServers in config, or drop a plugin into the plugin directory.",

	"ui.page.tools.section_mode":           "Tool mode (experimental)",
	"ui.page.tools.mode_label":             "Mode",
	"ui.page.tools.mode_hint":              "Takes effect after restart; ptc is the experimental scripted tool calling",
	"ui.page.tools.mode_native":            "Native multi-tool",
	"ui.page.tools.mode_ptc":               "Scripted (run_code)",
	"ui.page.tools.group_fmt":              "{label} ({n})",
	"ui.page.tools.group_files":            "Files & editing",
	"ui.page.tools.group_memory":           "Memory",
	"ui.page.tools.group_jobs":             "Background jobs & watchers",
	"ui.page.tools.group_cmds":             "Commands & scripts",
	"ui.page.tools.group_net":              "Web & multimodal",
	"ui.page.tools.group_misc":             "Interaction & other",
	"ui.page.tools.section_decide":         "Model-judged decisions (experimental)",
	"ui.page.tools.decide_note":            "Confirmations ask a judge model first: clearly-fine ones run, clearly-bad ones (optional) get rejected, the rest still ask you. Failure hands back — timeouts and unreachable endpoints always fall to the original confirmation, never default-allow. Everything takes effect after restart; base URL and model are required when mode is not off, and the judge stays off until both are set.",
	"ui.page.tools.decide_mode_label":      "Mode",
	"ui.page.tools.decide_mode_hint":       "off / shadow (audit only) / on (route by thresholds)",
	"ui.page.tools.decide_off":             "Off",
	"ui.page.tools.decide_shadow":          "Shadow (audit only)",
	"ui.page.tools.decide_on":              "On",
	"ui.page.tools.decide_base_label":      "Endpoint",
	"ui.page.tools.decide_base_hint":       "e.g. http://127.0.0.1:11434; with or without /v1",
	"ui.page.tools.decide_key_label":       "API key",
	"ui.page.tools.decide_key_hint":        "secret: reference or plaintext; leave empty to keep",
	"ui.page.tools.decide_model_label":     "Model",
	"ui.page.tools.decide_model_hint":      "Model name as the server knows it",
	"ui.page.tools.decide_timeout_label":   "Judge timeout (ms)",
	"ui.page.tools.decide_timeout_hint":    "200–10000; empty = keep current",
	"ui.page.tools.decide_warmup_label":    "Warm up at startup",
	"ui.page.tools.decide_warmup_hint":     "Fire one minimal request after startup; local cold starts miss the first judgment otherwise",
	"ui.page.tools.decide_taskchars_label": "Task summary cutoff",
	"ui.page.tools.decide_taskchars_hint":  "0–400 chars; empty = keep current",
	"ui.page.tools.decide_risk_label":      "Allow risk below",
	"ui.page.tools.decide_risk_hint":       "(0, 2]; auto-allow requires both bounds",
	"ui.page.tools.decide_human_label":     "Allow needs_human below",
	"ui.page.tools.decide_human_hint":      "(0, 1]",
	"ui.page.tools.deny_enabled_label":     "Reject clearly-bad calls",
	"ui.page.tools.deny_enabled_hint":      "Off by default; when on, confirmations above the lower bound are rejected outright",
	"ui.page.tools.deny_above_label":       "Reject needs_human above",
	"ui.page.tools.deny_above_hint":        "(0, 1]; must exceed the allow bound or startup fails",
	// 外部代理页
	"ui.page.delegate.section_agents": "Agents (config delegate.agents)",
	"ui.page.delegate.empty": "No external agents yet. Add one with \"Add agent\" below (name + command line); " +
		"it shows up here after a restart. Without any agent the model gets no delegate* tools.",
	"ui.page.delegate.note": "Availability only checks that the command is on PATH; there is no handshake. " +
		"Use \"Test connection\" on an entry for a real handshake (it starts a process, capped at about " +
		"fifteen seconds). Everything changed here is config and takes effect after a restart: the " +
		"handoff manager assembles from the config read at startup. Saving rewrites the whole config.json " +
		"as indented JSON; hand-written comments and key order are not preserved.",
	"ui.page.delegate.section_add":  "Add an agent",
	"ui.page.delegate.refresh":      "Recheck",
	"ui.page.delegate.refresh_hint": "Rescan PATH after installing an agent elsewhere; no config is changed",
	"ui.page.delegate.rescanned":    "Rechecked (availability computed from the current PATH)",

	// 技能页
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
	"ui.page.secrets.section_entries": "Stored secrets (values are never shown, only the last 4 characters)",
	"ui.page.secrets.empty":           "The secret store is empty — fill in a name and a value below to store one",
	"ui.page.secrets.section_import":  "Bring in what is stored elsewhere",
	"ui.page.secrets.import_note":     "Move keys that are still plaintext in config (models, speech, decision, web) into the local secret store; config keeps only secret:<name> references. Reference values that exist only in a same-named environment variable are brought in too. Values do not change, and the plaintext warning stops appearing.",
	"ui.page.secrets.import_btn":      "Import plaintext keys",
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

	// Logs page (v0.9.0): per-day run logs, tail view, open folder.
	"ui.page.logs.section_files": "Log files",
	"ui.page.logs.note": "One file per day; a file rotates to .1 past 4 MiB and files are kept for 7 days. " +
		"Only the tail is shown here; use Open folder for the whole file.",
	"ui.page.logs.empty":       "No log files yet: nothing has been written under this profile.",
	"ui.page.logs.refresh":     "Refresh",
	"ui.page.logs.open_dir":    "Open folder",
	"ui.page.logs.view_title":  "Log {name}",
	"ui.page.logs.opened":      "Opened the tail of {name} (up to {lines} lines)",
	"ui.page.logs.empty_file":  "{name} is still empty",
	"ui.page.logs.read_failed": "Cannot read it: {err}",
	"ui.page.logs.list_failed": "Cannot list the log directory: {err}",
	"ui.page.logs.dir_failed":  "Cannot open the folder: {err}",
	"ui.page.logs.dir_opened":  "Opened in the file manager",
	"ui.page.logs.no_opener":   "Opening a folder is not supported on this platform",
	"ui.page.logs.file_meta":   "{size} · {at}",
	"ui.page.logs.size_bytes":  "{n} bytes",
	"ui.page.logs.size_kb":     "{n} KB",
	"ui.page.logs.size_mb":     "{n} MB",
	// Tool-level allow policy (D280/D282): rendered on each row of "Tools & MCP".
	"ui.page.policy.seg.default": "Default",
	"ui.page.policy.seg.ask":     "Ask",
	"ui.page.policy.seg.allow":   "Allow",
	"ui.page.policy.note": "\"Default\" follows the {level} level. A row you change here is judged by the " +
		"level you pick; \"Allow\" stops asking, and tightening to \"Ask\" works the same way.",
	"ui.page.policy.agent_label": "External agents · all requests",
	"ui.page.policy.hard_note": "Secret files, the config file, system directories and version-control " +
		"hooks are not affected. These paths still ask; auto-allow does not apply.",
	"ui.page.policy.mode.assist_short": "Assisted",
	"ui.page.policy.updated":           "Updated; takes effect now",
	"ui.page.policy.exec_confirm": "Auto-allow {tool}? The command text is generated by the model, " +
		"and the model will stop asking.",
	"ui.page.policy.confirm_ok":     "Allow it",
	"ui.page.policy.confirm_cancel": "Cancel",
}
