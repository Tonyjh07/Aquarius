package i18n

// enCmdTexts 命令面英文表：与 zh_cmd.go 同 key、同顺序、同占位符名（同构测试守着）。
//
// Usage 里的占位词跟着翻（`<文本>` → `<text>`），因为 Params 的 Name 也翻——两边必须继续
// 对得上，否则补全浮层会提示一个命令行里写不出来的词（TestCommandParamsMatchUsage 逐语言跑）。
var enCmdTexts = map[string]string{

	// 共享参数声明
	"cmd.shared.onoff.p1.name": "toggle",
	"cmd.shared.onoff.p1.desc": "no argument = flip the current state",
	"cmd.shared.level.p1.name": "level",
	"cmd.shared.level.p1.desc": "paths decide for file operations and risk tiers for tools; the two columns never stack; no argument = show the current one",

	// /new
	"cmd.new.desc":    "New conversation",
	"cmd.new.usage":   "[title]",
	"cmd.new.p1.name": "title",
	"cmd.new.p1.desc": "conversation title; omitted = named after the first message",

	// /list
	"cmd.list.desc":         "List conversations",
	"cmd.list.usage":        "[--tag <name>...]",
	"cmd.list.p1.name":      "--tag",
	"cmd.list.p1.valuename": "<name>",
	"cmd.list.p1.desc":      "list only conversations carrying all of these tags (repeatable; intersection)",

	// /tag
	"cmd.tag.desc":         "Tag or untag a conversation (--rm removes; no args shows current tags)",
	"cmd.tag.usage":        "<conversation> [name...] [--rm <name>...]",
	"cmd.tag.p1.name":      "<conversation>",
	"cmd.tag.p1.desc":      "target conversation id (unique prefix accepted)",
	"cmd.tag.p2.name":      "name",
	"cmd.tag.p2.desc":      "tags to add (several allowed; re-tagging the same tag is a no-op)",
	"cmd.tag.p3.name":      "--rm",
	"cmd.tag.p3.valuename": "<name>",
	"cmd.tag.p3.desc":      "tags to remove (repeatable)",

	// /pin
	"cmd.pin.desc":    "Pin or unpin a conversation (toggle)",
	"cmd.pin.usage":   "<conversation>",
	"cmd.pin.p1.name": "<conversation>",
	"cmd.pin.p1.desc": "target conversation id (unique prefix accepted)",

	// /switch
	"cmd.switch.desc":    "Switch to an existing conversation (clears the screen and replays history)",
	"cmd.switch.usage":   "<id prefix>",
	"cmd.switch.p1.name": "<id prefix>",
	"cmd.switch.p1.desc": "unique prefix of the conversation id (shown by /list)",

	// /title
	"cmd.title.desc":         "Show or rewrite a conversation title (--id targets another conversation)",
	"cmd.title.usage":        "[--id <id prefix>] [text]",
	"cmd.title.p1.name":      "--id",
	"cmd.title.p1.valuename": "<id prefix>",
	"cmd.title.p1.desc":      "which conversation; omitted = the current one",
	"cmd.title.p2.name":      "text",
	"cmd.title.p2.desc":      "new title; omitted = just show it",

	// /goto
	"cmd.goto.desc":    "Move Head to any node (branch navigation; id accepts a unique prefix)",
	"cmd.goto.usage":   "<id>",
	"cmd.goto.p1.name": "<id>",
	"cmd.goto.p1.desc": "target node id (a unique prefix is enough)",

	// /edit
	"cmd.edit.desc": "Edit a message: by default it forks a new branch (same as regenerating); " +
		"--keep moves the following history onto the new branch; --copy deep-copies it; " +
		"--part N rewrites only the Nth body segment and keeps the rest; " +
		"--attach appends an attachment, --drop N removes the Nth one (0-based), --drop-all removes them all; " +
		"with attachment edits the text is optional (user messages only)",
	"cmd.edit.usage":        "<id> [--keep|--copy] [--part N] [--attach <path>]... [--drop N]... [--drop-all] [<text>]",
	"cmd.edit.p1.name":      "<id>",
	"cmd.edit.p1.desc":      "id of the message to edit (a unique prefix is enough)",
	"cmd.edit.p2.name":      "--keep",
	"cmd.edit.p2.desc":      "move the following history onto the new branch (the only way to edit a persona)",
	"cmd.edit.p3.name":      "--copy",
	"cmd.edit.p3.desc":      "deep-copy the following history into an independent branch",
	"cmd.edit.p4.name":      "--part",
	"cmd.edit.p4.valuename": "N",
	"cmd.edit.p4.desc":      "rewrite only the Nth body segment (0-based) and keep the rest",
	"cmd.edit.p5.name":      "--attach",
	"cmd.edit.p5.valuename": "<path>",
	"cmd.edit.p5.desc":      "append one attachment; repeatable",
	"cmd.edit.p6.name":      "--drop",
	"cmd.edit.p6.valuename": "N",
	"cmd.edit.p6.desc":      "remove the Nth attachment (0-based); repeatable",
	"cmd.edit.p7.name":      "--drop-all",
	"cmd.edit.p7.desc":      "remove every attachment",
	"cmd.edit.p8.name":      "text",
	"cmd.edit.p8.desc":      "new body; optional when only the attachments change",

	// /regen
	"cmd.regen.desc":    "Regenerate: fork above the upstream user message and resend, or generate directly when there is no answer yet",
	"cmd.regen.usage":   "<id>",
	"cmd.regen.p1.name": "<id>",
	"cmd.regen.p1.desc": "user or assistant message id; its upstream user message is resent",

	// /respond
	"cmd.respond.desc":    "Keep generating from the chosen node: send the context above its watermark up to that node (forks when it already has children)",
	"cmd.respond.usage":   "<id>",
	"cmd.respond.p1.name": "<id>",
	"cmd.respond.p1.desc": "any non-root node id (persona, system and tool nodes work too; root does not)",

	// /inject
	"cmd.inject.desc":         "Inject a context entry by hand (stored as an injection node): starts a turn by default, --no-trigger only puts it in context; --source labels where it came from",
	"cmd.inject.usage":        "[--source <name>] [--no-trigger] <text>",
	"cmd.inject.p1.name":      "--source",
	"cmd.inject.p1.valuename": "<name>",
	"cmd.inject.p1.desc":      "label the source (shown on the node)",
	"cmd.inject.p2.name":      "--no-trigger",
	"cmd.inject.p2.desc":      "only put it in context, do not start a turn",
	"cmd.inject.p3.name":      "text",
	"cmd.inject.p3.desc":      "context text to inject",

	// /branch
	"cmd.branch.desc":    "Show sibling forks and children (Root shows top-level messages; the current Head by default)",
	"cmd.branch.usage":   "[id]",
	"cmd.branch.p1.name": "id",
	"cmd.branch.p1.desc": "which fork to look at; omitted = the current Head",

	// /tree-edit
	"cmd.tree_edit.desc":    "Tree edit mode: rewiring stays in memory until you leave the mode; --done saves and exits, --cancel discards every change",
	"cmd.tree_edit.usage":   "[--done|--cancel]",
	"cmd.tree_edit.p1.name": "--done",
	"cmd.tree_edit.p1.desc": "save and exit (the context count is compared only when something changed)",
	"cmd.tree_edit.p2.name": "--cancel",
	"cmd.tree_edit.p2.desc": "discard the changes and exit (the tree goes back to how it looked on entry)",

	// /reparent
	"cmd.reparent.desc":    "Reparent: hang the whole subtree under a new parent as its last child; --undo reverses the latest move (the new parent cannot be Root, itself or a descendant)",
	"cmd.reparent.usage":   "<id> <new parent> | --undo",
	"cmd.reparent.p1.name": "<id>",
	"cmd.reparent.p1.desc": "node to move (together with its whole subtree)",
	"cmd.reparent.p2.name": "<new parent>",
	"cmd.reparent.p2.desc": "where to hang it; cannot be Root, itself or one of its descendants",
	"cmd.reparent.p3.name": "--undo",
	"cmd.reparent.p3.desc": "reverse the latest move (transfer or reparent) without leaving a new record",

	// /rm
	"cmd.rm.desc":    "Delete a node and its whole subtree (asks for confirmation)",
	"cmd.rm.usage":   "<id>",
	"cmd.rm.p1.name": "<id>",
	"cmd.rm.p1.desc": "node to delete (together with its whole subtree)",

	// /rmconv
	"cmd.rmconv.desc":    "Delete whole conversations into the trash (asks for confirmation; deleting the current one switches to another)",
	"cmd.rmconv.usage":   "<id prefix> [<id prefix>…]",
	"cmd.rmconv.p1.name": "<id prefix>",
	"cmd.rmconv.p1.desc": "unique prefix of each conversation to delete; several are allowed and one confirmation covers them all",

	// /fork
	"cmd.fork.desc":    "Take a node and its whole subtree into a new conversation (Head lands on the fork node; the source stays untouched)",
	"cmd.fork.usage":   "<node id> [title]",
	"cmd.fork.p1.name": "<node id>",
	"cmd.fork.p1.desc": "unique prefix of the node to fork from (/branch shows them; the conversation root cannot be forked)",
	"cmd.fork.p2.name": "[title]",
	"cmd.fork.p2.desc": "title of the new conversation; omitted means the source title carries over",

	// /trash
	"cmd.trash.desc":    "Conversation trash: no args lists it; restore puts conversations back; empty clears it (asks for confirmation)",
	"cmd.trash.usage":   "[restore <id prefix>… | empty]",
	"cmd.trash.p1.name": "<action>",
	"cmd.trash.p1.desc": "restore puts conversations back (followed by ids, several allowed); empty clears the whole trash (asks for confirmation); no action just lists",
	"cmd.trash.p2.name": "<id prefix>",
	"cmd.trash.p2.desc": "unique prefix of each conversation to restore; several are allowed (restore only)",

	// /compact
	"cmd.compact.desc": "Compact the context (fold the earlier exchange into one summary)",

	// /summary
	"cmd.summary.desc":    "Insert a summary before a node (it becomes the new watermark): with text = written by you; without = the current model summarizes everything above that node",
	"cmd.summary.usage":   "<id> [text]",
	"cmd.summary.p1.name": "<id>",
	"cmd.summary.p1.desc": "the summary goes before this node",
	"cmd.summary.p2.name": "text",
	"cmd.summary.p2.desc": "hand-written summary; omitted = ask the model to summarize above that node",

	// /mic
	"cmd.mic.desc":    "Record a voice clip, transcribe it and send the text as input (60 seconds by default, 4 minutes at most)",
	"cmd.mic.usage":   "[seconds]",
	"cmd.mic.p1.name": "seconds",
	"cmd.mic.p1.desc": "recording length; 60 by default, the ceiling comes from the provider's audio size limit",

	// /permission
	"cmd.permission.desc":  "Show or switch the permission level (read-only/strict/permissive/full-access)",
	"cmd.permission.usage": "[level]",

	// /unattended
	"cmd.unattended.desc":  "Unattended: refuse out-of-bounds operations on the spot with a reason instead of waiting for approval (the permission level is unchanged; no argument = toggle)",
	"cmd.unattended.usage": "[on|off]",

	// /memory
	"cmd.memory.desc":    "Open the memory file in the system editor (the global memories.md by default)",
	"cmd.memory.usage":   "[conversation id]",
	"cmd.memory.p1.name": "conversation id",
	"cmd.memory.p1.desc": "open the per-conversation memory; omitted = global memories.md",

	// /usage
	"cmd.usage.desc": "Show token usage (context occupancy / last turn measured / conversation total)",
	"cmd.lore.desc":  "Show which lorebook entries this round activates (read-only, never written to the tree)",

	// /context
	"cmd.context.desc":    "Show the assembled context about to be sent to the model (per-section usage / one message in full / a full dump)",
	"cmd.context.usage":   "[last | full [last] | number]",
	"cmd.context.p1.name": "number",
	"cmd.context.p1.desc": "omitted = overview; last = what the previous round sent; full = write the whole thing to the export directory",

	// /raw
	"cmd.raw.desc":    "Show the raw response from the model API (metadata / one frame verbatim / a full dump)",
	"cmd.raw.usage":   "[-count | full [-count] | frame number]",
	"cmd.raw.p1.name": "frame number",
	"cmd.raw.p1.desc": "omitted = overview of the latest; -2 = the one before it; full = write it to the export directory",

	// /cost
	"cmd.cost.desc":    "Show usage and spend (current conversation / by model / by day)",
	"cmd.cost.usage":   "[session | model | day]",
	"cmd.cost.p1.name": "view",
	"cmd.cost.p1.desc": "omitted = this conversation; model = by model across conversations; day = by day across conversations",

	// /audit
	"cmd.audit.desc":    "Show the audit log (recent model and tool calls)",
	"cmd.audit.usage":   "[-n count] [--tool name]",
	"cmd.audit.p1.name": "count",
	"cmd.audit.p1.desc": "how many trailing lines to show, 20 by default",
	"cmd.audit.p2.name": "tool name",
	"cmd.audit.p2.desc": "show only that tool's entries",

	// /diag
	"cmd.diag.desc": "Bundle this request's context, audit lines and raw response into one diagnostic file",

	// /doctor: seven-point health check (§7.14/D306)
	"cmd.doctor.desc":            "Health check: config, keystore, conversation files, MCP state, directory usage and disk space (read-only, sends no requests)",
	"cmd.doctor.config_ok":       "Parsed: {providers} providers, {models} models",
	"cmd.doctor.config_bad":      "Cannot parse: {err}",
	"cmd.doctor.config_fix":      "Fix the spot named in the error in {path}; restart Aquarius to apply it",
	"cmd.doctor.secrets_ok":      "Unlocked: {n} keys ({path})",
	"cmd.doctor.secrets_bad":     "Cannot unlock: {err}",
	"cmd.doctor.secrets_unwired": "This build has no keystore",
	"cmd.doctor.secrets_unreadable_fix": "The file is there but the key is not: after moving machines or accounts, " +
		"copy the original secrets.key back, or refill the keys on the Secrets page (the old file is kept as .bak)",
	"cmd.doctor.secrets_fix":        "Check read permission on {path} and the disk under it",
	"cmd.doctor.secrets_no_profile": "The profile directory has no files yet, so there is no keystore",
	"cmd.doctor.sessions_ok":        "{n} conversation files, all readable",
	"cmd.doctor.sessions_broken":    "{total} conversation files, {n} unreadable: {name} ({err})",
	"cmd.doctor.sessions_more":      "; {n} more",
	"cmd.doctor.sessions_fix":       "Move the unreadable files aside; for a lone .bak, dropping the suffix restores it. None of them were listed in /list, and the other conversations are unaffected",
	"cmd.doctor.sessions_unwired":   "This build has no conversation store",
	"cmd.doctor.sessions_list_bad":  "Cannot list the conversation directory: {err}",
	"cmd.doctor.sessions_list_fix":  "Check that the conversations directory exists and is readable",
	"cmd.doctor.mcp_ok":             "{ready} connected, {disabled} disabled",
	"cmd.doctor.mcp_none":           "No MCP server declared",
	"cmd.doctor.mcp_failed":         "{name} is unusable: {err} ({ready} connected, {disabled} disabled)",
	"cmd.doctor.mcp_more":           "; {n} more with problems",
	"cmd.doctor.mcp_fix":            "See the error in /plugin list; after fixing the declaration, reconnect with /plugin enable <name>",
	"cmd.doctor.mcp_pending":        "{name} is connecting or restarting ({ready} connected, {disabled} disabled)",
	"cmd.doctor.mcp_pending_fix":    "Check again in a few seconds; if it stays there, see /plugin list",
	"cmd.doctor.mcp_unwired":        "This build has no plugin host",
	"cmd.doctor.mcp_cli_none":       "No MCP server declared",
	"cmd.doctor.mcp_cli_ok":         "All {n} declarations are valid (the command-line check connects to nothing)",
	"cmd.doctor.mcp_cli_bad":        "{bad} of {n} declarations are invalid: {err}",
	"cmd.doctor.mcp_cli_more":       "; {n} more",
	"cmd.doctor.mcp_cli_no_config":  "The config cannot be read, so declarations cannot be checked (see the Config item)",
	"cmd.doctor.dir_ok":             "{files} files, {size}",
	"cmd.doctor.dir_big":            "{files} files, {size} (over {limit})",
	"cmd.doctor.dir_capped":         "; counted {n} files only",
	"cmd.doctor.dir_bad":            "Cannot read {dir}: {err}",
	"cmd.doctor.dir_fix":            "Check read permission on {dir}; delete the old files yourself — the check will not do it",
	"cmd.doctor.disk_ok":            "{free} free of {total}",
	"cmd.doctor.disk_unknown":       "Cannot read free space: {err}",
	"cmd.doctor.disk_err":           "Cannot read free space of the volume holding {path}",
	"cmd.doctor.disk_fix":           "Free some space on the disk holding {dir} — a full disk makes saving conversations fail",

	// /watch
	"cmd.watch.desc":    "Watchers: declarative timed observations the model can declare (watch a command, file or page and report hits); list or stop them",
	"cmd.watch.usage":   "[list|stop <id>]",
	"cmd.watch.p1.name": "subcommand",
	"cmd.watch.p1.desc": "omitted = list",
	"cmd.watch.p2.name": "<id>",
	"cmd.watch.p2.desc": "the watcher id when stopping",

	// /jobs
	"cmd.jobs.desc":    "Background jobs (list, logs, kill)",
	"cmd.jobs.usage":   "[list|logs <id> [lines]|kill <id>]",
	"cmd.jobs.p1.name": "subcommand",
	"cmd.jobs.p1.desc": "omitted = list",
	"cmd.jobs.p2.name": "<id>",
	"cmd.jobs.p2.desc": "job id for logs / kill",
	"cmd.jobs.p3.name": "lines",
	"cmd.jobs.p3.desc": "how many trailing lines logs returns",

	// /delegate
	"cmd.delegate.desc":    "External agent delegation: list the configured agents and their availability, stop one delegation in progress (work is dispatched by the model through the delegate tool)",
	"cmd.delegate.usage":   "[list|kill <id>]",
	"cmd.delegate.p1.name": "subcommand",
	"cmd.delegate.p1.desc": "omitted = list",
	"cmd.delegate.p2.name": "<id>",
	"cmd.delegate.p2.desc": "delegation id for kill",

	// /skill
	"cmd.skill.desc":    "Skills: list installed skills / load one skill's instructions into the context (without answering) / install, remove or update",
	"cmd.skill.usage":   "[list|<name>|install <path or url>|remove <name>|update [name|all]]",
	"cmd.skill.p1.name": "subcommand",
	"cmd.skill.p1.desc": "omitted = list; a bare skill name also works",
	"cmd.skill.p2.name": "<name>",
	"cmd.skill.p2.desc": "skill name (load / remove / update)",
	"cmd.skill.p3.name": "<path or url>",
	"cmd.skill.p3.desc": "source for install: a single file, a folder, an archive or a URL",

	// /export
	"cmd.export.desc": "Export conversations (only the current one by default; aqx is lossless and imports back as-is; " +
		"chatgpt keeps every branch; openai/sillytavern keep just the active chain; " +
		"--all writes the whole library and --ids picks specific ones, both require aqx)",
	"cmd.export.usage":        "[--format <name>] [--path <file>] [--all | --ids <id>[,<id>…]]",
	"cmd.export.p1.name":      "--format",
	"cmd.export.p1.valuename": "<name>",
	"cmd.export.p1.desc":      "output format; aqx by default",
	"cmd.export.p2.name":      "--path",
	"cmd.export.p2.valuename": "<file>",
	"cmd.export.p2.desc":      "where to write; omitted = named after the title inside the export directory",
	"cmd.export.p3.name":      "--all",
	"cmd.export.p3.desc":      "write the whole library into one aqx backup (exclusive with --ids)",
	"cmd.export.p4.name":      "--ids",
	"cmd.export.p4.valuename": "<id>[,<id>…]",
	"cmd.export.p4.desc":      "export only these conversations (unique prefixes; comma separated or repeated)",

	// /import
	"cmd.import.desc":         "Import external conversations (the format comes from the extension and the file header; lands as new conversations and never overwrites existing ones; one aqx can yield just a few)",
	"cmd.import.usage":        "[--pick <index>]… <file>",
	"cmd.import.p1.name":      "--pick",
	"cmd.import.p1.valuename": "<index>",
	"cmd.import.p1.desc":      "import only the Nth entry of the listing (1-based; comma separated or repeated; omitted = all)",
	"cmd.import.p2.name":      "<file>",
	"cmd.import.p2.desc":      "file to import; quote the path when it contains spaces",

	// /character
	"cmd.character.desc":    "Character cards: list / show / import / start a conversation from a card / remove (import is read-only)",
	"cmd.character.usage":   "[list|show <slug>|import <path>|start <slug> [index]|remove <slug>]",
	"cmd.character.p1.name": "subcommand",
	"cmd.character.p1.desc": "omitted = list",
	"cmd.character.p2.name": "<slug>",
	"cmd.character.p2.desc": "character directory name (show / start / remove; /list shows it)",
	"cmd.character.p3.name": "<path>",
	"cmd.character.p3.desc": "card file for import: PNG / JSON / .charx",
	"cmd.character.p4.name": "index",
	"cmd.character.p4.desc": "which greeting start uses; omitted or 1 = the first one",

	// /quit
	"cmd.quit.desc": "Quit",

	// /plugin
	"cmd.plugin.desc":    "MCP plugin management (list, enable, disable)",
	"cmd.plugin.usage":   "[list|enable <name>|disable <name>]",
	"cmd.plugin.p1.name": "subcommand",
	"cmd.plugin.p1.desc": "omitted = list",
	"cmd.plugin.p2.name": "<name>",
	"cmd.plugin.p2.desc": "plugin name (enable / disable / logs)",

	// /model
	"cmd.model.desc":    "Show available models / switch and write back to config / probe capabilities",
	"cmd.model.usage":   "[name] | probe [name]",
	"cmd.model.p1.name": "name",
	"cmd.model.p1.desc": "model name; writing it plain switches to that model",
	"cmd.model.p2.name": "probe",
	"cmd.model.p2.desc": "probe capabilities: tool calling, image input and thinking once each; only clear verdicts are written back",
	"cmd.model.p3.name": "<name>",
	"cmd.model.p3.desc": "model to probe; omitted = the current one",

	// /think
	"cmd.think.desc":  "Master switch for native thinking (writes back to config; off wins over /effort)",
	"cmd.think.usage": "[on|off]",

	// /effort
	"cmd.effort.desc":    "Reasoning effort minimal|low|medium|high|off (writes back to config)",
	"cmd.effort.usage":   "[level]",
	"cmd.effort.p1.name": "level",
	"cmd.effort.p1.desc": "off clears the level; /think decides whether it is actually sent",

	// /help
	"cmd.help.desc": "List every command (the static catalog plus registered dynamic ones)",

	// /help 动态段前的家族说明
	"cmd.mcp.family": "/mcp:<server>:<prompt>  dynamic MCP prompts (registered as plugins start and stop, see /plugin)",
}
