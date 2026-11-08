package i18n

// enTreeTexts 会话树与改父：与 zh_tree.go 同 key、同顺序、同占位符名。
//
// 续行的缩进（`; …`）随原文保留：调用方只做字符串相加，标点与空格都在词条里。
var enTreeTexts = map[string]string{

	// 用法
	"usage.switch":    "Usage: /switch <id prefix> (/list shows the available conversations; a unique prefix is enough)",
	"usage.title":     "Usage: /title [--id <id prefix>] [<text>] (--id retitles another conversation; /list shows them)",
	"usage.rmconv":    "Usage: /rmconv <id prefix> [<id prefix>…] (deletes whole conversations after a confirmation; /list shows the available ones)",
	"usage.goto":      "Usage: /goto <id> (/branch shows the ids; a unique prefix is enough)",
	"usage.branch":    "Usage: /branch [id] (the current Head by default)",
	"usage.rm":        "Usage: /rm <id> (cuts the node and its whole subtree off, after a confirmation)",
	"usage.tree_edit": "Usage: /tree-edit (enter) | --exit (the switch on screen: if anything changed it asks save/discard first) | --done (save and exit) | --cancel (discard and exit)",
	"usage.reparent":  "Usage: /reparent <id> <new parent> (both accept a unique prefix; ids read as in /branch) | /reparent --undo",

	// /new、/list、/switch
	"tree.new.done":    "Created conversation {id}",
	"tree.list.empty":  "(no conversations yet; /new starts one)",
	"tree.list.count":  "{n} msgs",
	"tree.switch.done": "Switched to conversation {id}",

	// /title
	"tree.title.show":      "{id} title: {title}",
	"tree.title.set":       "Title changed to: {title}",
	"tree.title.set_other": "Conversation {id} retitled to: {title}",

	// /rmconv
	"tree.rmconv.prompt":               "Delete conversation {id} ({title})? The whole conversation goes, message tree included, and this cannot be undone",
	"tree.rmconv.prompt_many":          "Delete {n} conversations? Each goes with its whole message tree, and this cannot be undone:",
	"tree.rmconv.prompt_item":          "- {id} ({title})",
	"tree.rmconv.no_confirmer":         "no confirmer configured (SessionDeps.Confirmer); /rmconv needs a confirmation",
	"tree.rmconv.canceled":             "Cancelled deleting conversation {id}",
	"tree.rmconv.canceled_many":        "Cancelled deleting {n} conversations",
	"tree.rmconv.deleted":              "Deleted conversation {id} ({title})",
	"tree.rmconv.deleted_many":         "Deleted {n} conversations: {titles}",
	"tree.rmconv.deleted_current":      "Deleted the current conversation {id} ({title}); switched to {next}",
	"tree.rmconv.deleted_current_many": "Deleted {n} conversations (the current one included): {titles}; switched to {next}",

	// 标题清单（批量删除回执用）
	"tree.titles.sep":  ", ",
	"tree.titles.more": "{names} and {n} in total",

	// /goto、/branch
	"tree.goto.done":             "Head → {id}",
	"tree.branch.current":        "Current: {line}",
	"tree.branch.siblings":       "Sibling forks",
	"tree.branch.top":            "Top-level messages",
	"tree.branch.none":           "{label}: none",
	"tree.branch.siblings_count": "{label} ({n}, excluding itself):",
	"tree.branch.children_none":  "Children: none",
	"tree.branch.children_count": "Children ({n}):",
	"tree.branch.head":           "← Head",
	"tree.branch.revised":        "(revised from {id})",

	// /rm
	"tree.rm.no_confirmer":    "no confirmer configured (SessionDeps.Confirmer); /rm needs a confirmation",
	"tree.rm.root":            "the conversation root is the conversation itself and cannot be deleted",
	"tree.rm.prompt":          "Delete {id} and its subtree (at least {n} nodes)? This cannot be undone",
	"tree.rm.prompt_editions": "; {n} newer versions will also lose their revision link (the map stops marking where they were revised from)",
	"tree.rm.canceled":        "Cancelled deleting {id}",
	"tree.rm.done":            "Deleted the subtree of {id} ({n} nodes; Head → {head})",
	"tree.rm.done_editions":   "; {n} newer versions lost their revision link",

	// 节点预览
	"tree.node.tool_call":     "(calling {names})",
	"tree.node.error":         "Error: {text}",
	"tree.node.empty":         "(empty)",
	"tree.attachment.unnamed": "(unnamed attachment)",

	// /tree-edit
	"tree.edit.not_active":      "not in tree edit mode (enter with /tree-edit)",
	"tree.edit.not_active_exit": "not in tree edit mode (nothing to exit)",
	"tree.edit.status_idle":     "Tree edit mode: nothing changed yet (drag a node to rewire it; /tree-edit --done saves and exits, --cancel discards)",
	"tree.edit.status_edits":    "Tree edit mode: {n} changes (saved together on exit; /tree-edit --cancel discards them all)",
	"tree.edit.entered":         "Entered tree edit mode: rewiring only touches memory until you leave (/tree-edit --done saves, --cancel discards)",
	"tree.edit.shrink_prompt":   "The {edits} changes in tree edit mode shrink the nodes entering the context from {before} to {after} (the old history is no longer sent to the model). Save and exit anyway?",
	"tree.edit.save_canceled":   "Save cancelled: the {n} changes are still here; keep editing or discard them with /tree-edit --cancel",
	"tree.edit.saved":           "Saved and left tree edit mode: {n} rewirings",
	"tree.edit.saved_context":   "; context {before} → {after}",
	"tree.edit.aborted_clean":   "Left tree edit mode (nothing changed)",
	"tree.edit.aborted":         "Discarded {n} rewirings; the tree is back to how it looked on entry",
	"tree.edit.aborted_saved":   " (a save happened during the session, so the file was rolled back too)",
	"tree.edit.confirm_save":    "{n} unsaved changes in tree edit mode; save and exit?",
	"tree.edit.confirm_discard": "Discard these {n} changes and leave tree edit mode?",
	"tree.edit.canceled":        "Cancelled: still in tree edit mode ({n} unsaved changes)",

	// /reparent
	"reparent.done":          "Reparented: {n} nodes now hang under {parent} (/reparent --undo reverses it)",
	"reparent.done_pad":      "; the new parent is an inserted summary node, so it becomes a regular node and its version group changes",
	"reparent.in_edit":       " (tree edit mode: saved together on exit)",
	"reparent.kind.move":     "reparent",
	"reparent.kind.transfer": "transfer",
	"reparent.undone":        "Reversed the latest {what}: {n} nodes hang under {anchor} again",

	// 错误链短语
	"err.new_conversation":            "create conversation",
	"err.save_current_conversation":   "save the current conversation",
	"err.save_title":                  "save the title",
	"err.confirm_delete_conversation": "confirm deleting the conversation",
	"err.delete_conversation":         "delete the conversation",
	"err.save_successor":              "save the successor conversation",
	"err.move_head":                   "move Head",
	"err.confirm_delete":              "confirm the deletion",
	"err.delete":                      "delete",
	"err.rm_persist_rollback":         "save failed, the deletion was rolled back (the tree is unchanged)",
	"err.rm_persist_stuck":            "save failed and could not be rolled back; the deletion lives in memory only (the next successful save writes it to disk, continue with care)",
	"err.restore_tree_edit":           "roll back the tree edit",
	"err.save_after_restore":          "save after the rollback",
	"err.confirm":                     "confirm",
	"err.reparent":                    "reparent",
	"err.reparent_persist_rollback":   "save failed, the reparent was rolled back",
	"err.reparent_persist_stuck":      "save failed and could not be rolled back; the reparent lives in memory only",
	"err.reparent_undo":               "reverse the reparent",
	"err.undo_persist_rollback":       "save failed, the reversal was rolled back",
	"err.undo_persist_stuck":          "save failed and could not be rolled back; the reversal lives in memory only",
}
