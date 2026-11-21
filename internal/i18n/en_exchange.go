package i18n

// enExchangeTexts /export 与 /import 的用法、回执与报错：与 zh_exchange.go 同 key、同顺序。
var enExchangeTexts = map[string]string{

	// 用法
	"usage.export":             "Usage: /export [--format <name>] [--path <file>] [--all | --ids <id>[,<id>…]] (only the current conversation by default; aqx is lossless and lands in the export directory)",
	"usage.import":             "Usage: /import [--all | --pick <index>]… [--filter <pattern>] [--no-files] <file> (the format is detected from the path and file contents; lands as a new conversation; --filter keeps the container entries whose title or source ID matches, indexes unchanged)",
	"exchange.filter_no_index": "this format offers no manifest, so --filter cannot apply (aqx multi-conversation archives and DeepSeek exports can)",
	"exchange.filter.empty":    "(no manifest entry matches)",
	"exchange.spaced_hint":     "Quote the path when it contains spaces, for example /import \"C:\\My Documents\\chat.aqx\"",

	// 导出回执
	"exchange.export.to":           "Exported to {path}",
	"exchange.format":              "Format: {name} ({label}) — {fidelity}",
	"exchange.export.multi":        "Content: {convs} conversations, {nodes} messages, {bytes}",
	"exchange.export.single":       "Content: {nodes} messages, {bytes}",
	"exchange.entry":               "- {title} · {nodes}",
	"exchange.entry.untitled":      "Conversation {index}",
	"exchange.fidelity.lossless":   "lossless, imports back as-is",
	"exchange.fidelity.lossy":      "lossy",
	"exchange.fidelity.lossy_note": "lossy: {note}",
	"exchange.dropped.head":        "Not carried over:",
	"exchange.dropped.item":        "- {item}",

	// 导入回执
	"exchange.import.to":             "Imported {path}",
	"exchange.import.multi":          "Content: imported {convs} conversations ({nodes} messages)",
	"exchange.import.one":            "Content: {nodes} messages (landed as a new conversation)",
	"exchange.import.listing.head":   "{count} conversations in the archive (newest first). Use --all to import everything, --pick <index> for selected ones",
	"exchange.import.listing.item":   "{index} · {date} · {title} ({count} messages)",
	"exchange.import.listing.more":   "…{count} more omitted",
	"exchange.import.progress":       "imported {done}/{total}…",
	"exchange.import.skipped":        "Also: skipped {count} conversations identical to the previous import",
	"exchange.import.all_skipped":    "all {count} conversations are identical to the previous import; nothing new was created",
	"exchange.import.no_ledger_note": "no import source dir wired: re-importing the same export creates all-new conversations",

	// 报错
	"exchange.exporter_missing": "this build has no export surface wired (SessionDeps.Exporter)",
	"exchange.importer_missing": "this build has no import surface wired (SessionDeps.Importer)",
	"exchange.all_and_ids":      "--all and --ids are exclusive: either the whole library or the listed conversations",
	"exchange.format_single":    "{format} writes a single conversation at a time: use aqx for a whole-library backup (drop --format)",
	"exchange.no_conversations": "there is no conversation to export",
	"exchange.no_formats":       "the export surface offers no format",
	"exchange.format_readonly":  "{format} is read-only ({note})",
	"exchange.unknown_format":   "unknown export format {name} (writable: {names})",
	"exchange.no_exports_dir":   "no export directory configured (SessionDeps.ExportsDir); pass a file with --path",
	"exchange.unknown_import":   "cannot tell what this file is (supported: {formats})",
	"exchange.empty_import":     "import failed: the file holds no importable conversation",
	"exchange.refresh_failed":   "the conversation was imported and switched, but the screen could not refresh",
	"exchange.replay_failed":    "the conversation was imported and switched, but its history could not be replayed",
	"exchange.bad_pick":         "--pick takes a 1-based index (got {value}); several indices look like --pick 1,3",
	"exchange.pick_missing":     "--pick needs an index (write --pick 1,3)",
	"exchange.bad_structure":    "the imported conversation is malformed",
	"exchange.too_many_names":   "too many files share that name in the export directory; pass a file name with --path",

	// 错误链短语
	"err.list_conversations":  "list conversations",
	"err.load_conversation":   "load conversation {id}",
	"err.mkdir_export":        "create export directory",
	"err.create_export":       "create export file",
	"err.export_failed":       "export failed",
	"err.open_file":           "open file",
	"err.read_file":           "read file",
	"err.read_head":           "read the file header",
	"err.import_failed":       "import failed",
	"err.assign_import_id":    "assign an id to the imported conversation",
	"err.save_import":         "save the imported conversation",
	"err.stat_target":         "check the target file",
	"err.import_ledger_write": "write import source ledger",
	"exchange.all_and_pick":   "--all and --pick cannot be combined",
}
