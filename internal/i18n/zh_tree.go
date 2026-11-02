package i18n

// zhTreeTexts 会话树与改父：/tree、/reparent 的用法、回执与报错。
//
// 一条文案一行 key：长回执拆成「一行一句事实」，拼接交给调用方——与 zh_exchange.go 同口径。
// 带标点的续行（`；另有…`）单独成 key，因为多一种情形就少印一行，而不是少半句译文。
var zhTreeTexts = map[string]string{

	// 用法
	"usage.switch":    "用法: /switch <id前缀>（/list 查看可用会话，支持唯一前缀）",
	"usage.title":     "用法: /title [--id <id前缀>] [<标题>]（--id 改非当前会话标题，/list 查看）",
	"usage.rmconv":    "用法: /rmconv <id前缀> [<id前缀>…]（删除整个会话，二次确认；/list 查看可用会话）",
	"usage.goto":      "用法: /goto <id>（id 可用 /branch 查看，支持唯一前缀）",
	"usage.branch":    "用法: /branch [id]（缺省取当前 Head）",
	"usage.rm":        "用法: /rm <id>（剪掉该节点及整棵子树，二次确认）",
	"usage.tree_edit": "用法: /tree-edit（进入）｜--exit（界面上关开关：有改动先问保存/放弃）｜--done（保存退出）｜--cancel（放弃并退出）",
	"usage.reparent":  "用法: /reparent <id> <新父>（都支持唯一前缀，id 与 /branch 里看到的一致）｜/reparent --undo",

	// /new、/list、/switch
	"tree.new.done":    "已新建会话 {id}",
	"tree.list.empty":  "（暂无会话）",
	"tree.list.count":  "{n}条",
	"tree.switch.done": "已切换到会话 {id}",

	// /title
	"tree.title.show":      "{id} 标题: {title}",
	"tree.title.set":       "标题已改为: {title}",
	"tree.title.set_other": "会话 {id} 标题已改为: {title}",

	// /rmconv
	"tree.rmconv.prompt":               "确认删除会话 {id}（{title}）？整会话连同其消息树一并删除，此操作不可恢复",
	"tree.rmconv.prompt_many":          "确认删除 {n} 个会话？整会话连同其消息树一并删除，此操作不可恢复：",
	"tree.rmconv.prompt_item":          "- {id}（{title}）",
	"tree.rmconv.no_confirmer":         "未配置确认器（SessionDeps.Confirmer），/rmconv 需要二次确认",
	"tree.rmconv.canceled":             "已取消删除会话 {id}",
	"tree.rmconv.canceled_many":        "已取消删除 {n} 个会话",
	"tree.rmconv.deleted":              "已删除会话 {id}（{title}）",
	"tree.rmconv.deleted_many":         "已删除 {n} 个会话：{titles}",
	"tree.rmconv.deleted_current":      "已删除当前会话 {id}（{title}），已切换到 {next}",
	"tree.rmconv.deleted_current_many": "已删除 {n} 个会话（含当前会话）：{titles}；已切换到 {next}",

	// 标题清单（批量删除回执用）
	"tree.titles.sep":  "、",
	"tree.titles.more": "{names} 等 {n} 个",

	// /goto、/branch
	"tree.goto.done":             "Head → {id}",
	"tree.branch.current":        "当前: {line}",
	"tree.branch.siblings":       "同级分叉",
	"tree.branch.top":            "顶层消息",
	"tree.branch.none":           "{label}: 无",
	"tree.branch.siblings_count": "{label}（{n} 条，不含自身）:",
	"tree.branch.children_none":  "下级: 无",
	"tree.branch.children_count": "下级（{n} 条）:",
	"tree.branch.head":           "← Head",
	"tree.branch.revised":        "（修订自 {id}）",

	// /rm
	"tree.rm.no_confirmer":    "未配置确认器（SessionDeps.Confirmer），/rm 需要二次确认",
	"tree.rm.root":            "会话根节点就是会话本身，不可删除",
	"tree.rm.prompt":          "确认删除 {id} 及其子树（至少 {n} 条节点）？此操作不可恢复",
	"tree.rm.prompt_editions": "；另有 {n} 个新版本会一并失去溯源记录（图上不再标出修订来源）",
	"tree.rm.canceled":        "已取消删除 {id}",
	"tree.rm.done":            "已删除 {id} 子树（{n} 条节点；Head → {head}）",
	"tree.rm.done_editions":   "；{n} 个新版本已失去溯源记录",

	// 节点预览
	"tree.node.tool_call":     "（调用 {names}）",
	"tree.node.error":         "错误: {text}",
	"tree.node.empty":         "（空）",
	"tree.attachment.unnamed": "(未命名附件)",

	// /tree-edit
	"tree.edit.not_active":      "当前不在树编辑模式（/tree-edit 进入）",
	"tree.edit.not_active_exit": "当前不在树编辑模式（无需退出）",
	"tree.edit.status_idle":     "树编辑模式：还没改过（拖动节点改挂；/tree-edit --done 保存退出，--cancel 放弃）",
	"tree.edit.status_edits":    "树编辑模式：已改 {n} 处（退出时统一保存；/tree-edit --cancel 放弃全部改动）",
	"tree.edit.entered":         "已进入树编辑模式：改挂只改内存，退出时统一保存（/tree-edit --done 保存，--cancel 放弃）",
	"tree.edit.shrink_prompt":   "树编辑的 {edits} 处改动会让进上下文的节点从 {before} 条减到 {after} 条（旧历史不再回传给模型），仍保存退出？",
	"tree.edit.save_canceled":   "已取消保存：改动仍在（{n} 处），可继续编辑或用 /tree-edit --cancel 放弃",
	"tree.edit.saved":           "已保存并退出树编辑：{n} 处改挂",
	"tree.edit.saved_context":   "；上下文 {before} → {after} 条",
	"tree.edit.aborted_clean":   "已退出树编辑（没有改动）",
	"tree.edit.aborted":         "已放弃 {n} 处改挂，树回到进入编辑时的样子",
	"tree.edit.aborted_saved":   "（会话期间落过盘，文件已一并回滚）",
	"tree.edit.confirm_save":    "树编辑有 {n} 处未保存改动，保存后退出？",
	"tree.edit.confirm_discard": "放弃这 {n} 处改动并退出树编辑？",
	"tree.edit.canceled":        "已取消：仍在树编辑模式（{n} 处改动未保存）",

	// /reparent
	"reparent.done":          "已改父：{n} 个节点挂到 {parent} 下（/reparent --undo 可撤销）",
	"reparent.done_pad":      "；目标是衬垫，它会降级为普通节点、版本组随之变化",
	"reparent.in_edit":       "（树编辑中，退出时统一保存）",
	"reparent.kind.move":     "改父",
	"reparent.kind.transfer": "转移",
	"reparent.undone":        "已撤销最近一次{what}：{n} 个节点挂回 {anchor} 下",

	// 错误链短语
	"err.new_conversation":            "新建会话",
	"err.save_current_conversation":   "保存当前会话",
	"err.save_title":                  "保存标题",
	"err.confirm_delete_conversation": "确认删除会话",
	"err.delete_conversation":         "删除会话",
	"err.save_successor":              "保存继任会话",
	"err.move_head":                   "移动 Head",
	"err.confirm_delete":              "确认删除",
	"err.delete":                      "删除",
	"err.rm_persist_rollback":         "落盘失败，删除已回滚（会话树未改动）",
	"err.rm_persist_stuck":            "落盘失败且无法回滚，删除仅存在于内存（下次成功保存会写盘，请谨慎继续）",
	"err.restore_tree_edit":           "回滚树编辑",
	"err.save_after_restore":          "回滚后保存",
	"err.confirm":                     "确认",
	"err.reparent":                    "改父",
	"err.reparent_persist_rollback":   "落盘失败，改父已回滚",
	"err.reparent_persist_stuck":      "落盘失败且无法回滚，改父仅存在于内存",
	"err.reparent_undo":               "撤销改父",
	"err.undo_persist_rollback":       "落盘失败，撤销已回滚",
	"err.undo_persist_stuck":          "落盘失败且无法回滚，撤销仅存在于内存",
}
