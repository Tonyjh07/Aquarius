package i18n

// zhGuiChatTexts 对话面：输入区、气泡与右键项、附件与图片、会话历史窗、确认与提问、状态行与提示条。
var zhGuiChatTexts = map[string]string{

	// ── 状态行与提示条（interact.go / pendingbar.go / pending.go / transcript.go /
	// branch.go / model_submit.go / shell.go）。带计数的句子一律用 {name} 占位符。
	"ui.state.generating":      "生成中",
	"ui.state.thinking":        "思考中",
	"ui.state.unattended":      "无人值守",
	"ui.state.edit_hint":       "编辑中 · Enter 提交 / Esc 取消",
	"ui.state.edit_hint_carry": "编辑中（转移历史）· Enter 提交 / Esc 取消",
	"ui.state.edit_hint_clone": "编辑中（复制历史）· Enter 提交 / Esc 取消",
	"ui.state.unattended_hint": "无人值守 · {hint}",

	"ui.state.unattended_deny": "无人值守 · 越界操作会被拒绝",
	"ui.state.inject":          "注入 · {source} · {state}",
	"ui.state.fired":           "已触发响应",
	"ui.state.idle":            "未触发",

	"ui.state.pending_full":     "待发已满 8 条，先撤销一条再发",
	"ui.state.pending_empty":    "待发内容为空，这一行没有发出去",
	"ui.state.pending_too_long": "这一行太长了（上限 8000 字符），没有进待发",
	"ui.state.pending_failed":   "没能放进待发：{err}",
	"ui.state.pending_more":     "还有 {n} 条待发（改档或撤销在待发条上）",
	"ui.state.queued":           "排队",
	"ui.state.steer":            "引导",

	"ui.state.buf_full_line":   "输入缓冲已满，此行未执行",
	"ui.state.buf_full_branch": "输入缓冲已满，分叉切换未执行",
	"ui.state.buf_full_attach": "输入缓冲已满，附件未发送",
	"ui.state.tray_quit":       "菜单退出",

	// ── 对话面块与气泡（interact.go / chip.go / think.go / confirm_ui.go 的事实卡）
	"ui.chat.none_selected":      "（未选择）",
	"ui.chat.thinking_raw_title": "思考原文 · {id}",
	"ui.chat.chip_pending":       "待确认",
	"ui.chat.chip_result_ok":     "结果 ✓",
	"ui.chat.chip_result_fail":   "结果 ✗",
	"ui.chat.chip_raw_title":     "工具调用 · {name}",
	"ui.chat.chip_running":       "运行中…",
	"ui.chat.thinking_live":      "思考中…",
	"ui.chat.thought":            "已思考",
	"ui.chat.thought_secs":       "已思考 {n} 秒",
	"ui.chat.tool_not_returned":  "（未返回——本轮已结束）",
	"ui.chat.list_sep":           "、",

	"ui.chat.facts_not_ready":   "（未就绪）",
	"ui.chat.facts_head":        "{profile} · {title}（{id}）",
	"ui.chat.facts_state_both":  "（{level}，{effort}）",
	"ui.chat.facts_state_level": "（{level}）",
	"ui.chat.ctx_mode_est":      "估算",
	"ui.chat.ctx_mode_exact":    "精确",
	"ui.chat.ctx_usage":         "上下文 {used}/{max} tokens（{pct}%，{mode}）",
	"ui.chat.tokens_total":      "累计 in {in} / out {out} tokens",
	"ui.chat.tokens_last":       "上轮 in {in} / out {out} tokens",

	// ── 输入区（input.go / voice.go）
	"ui.input.startup_hint":       "Aquarius — 输入 /help 查看命令，/quit 退出；停止生成点右侧方块",
	"ui.input.placeholder":        "随便问，或输入命令…",
	"ui.input.tip_attach":         "附件",
	"ui.input.tip_send":           "发送",
	"ui.input.tip_voice":          "语音输入",
	"ui.input.tip_stop_recording": "停止录音",
	"ui.input.tip_stop":           "停止",
	"ui.input.voice_failed":       "语音输入失败：{err}",

	// ── 提问面板（ask_ui.go；与终端那套 ask.* 无关）
	"ui.ask.counter":          "{i}/{total} 个问题",
	"ui.ask.free_placeholder": "自己输入答案…",
	"ui.ask.free_label":       "自己输入…",
	"ui.ask.free_label_value": "自己输入：{text}",
	"ui.ask.ignore":           "忽略",
	"ui.ask.next":             "下一步",
	"ui.ask.submit":           "提交",

	// ── 确认（confirm_ui.go 的动作提示 / history_confirm.go 的跨窗确认卡）
	"ui.confirm.elevate":            "把权限提到 {level}",
	"ui.confirm.deny_with_reason":   "拒绝：{reason}",
	"ui.confirm.needed":             "需要确认",
	"ui.confirm.shared_note":        "主窗与本窗显示的是同一次确认，任一处作答即可。",
	"ui.confirm.reason_placeholder": "拒绝原因（可留空）…",

	// ── 附件（attach.go / paste.go / model_submit.go）
	"ui.attach.pasted_image":  "粘贴的图片",
	"ui.attach.file_count":    "{n} 个文件",
	"ui.attach.dir_not_file":  "{name} 是文件夹，请拖入或选择具体文件",
	"ui.attach.marker_named":  "〔附件：{name}〕",
	"ui.attach.marker_count":  "〔附件 {n} 个：{names}〕",
	"ui.attach.limit_ignored": "一条消息最多 {n} 个附件，已忽略 {name}",

	// ── 图片（imgview.go / imgthumb.go / paste.go）
	"ui.img.placeholder":        "图片",
	"ui.img.err_unreadable":     "读不到这张图，请重新选择或粘贴",
	"ui.img.err_read_failed":    "读取图片失败，请重试",
	"ui.img.err_not_image":      "这个文件不是可识别的图片",
	"ui.img.err_too_large":      "图片过大（{w}×{h}），暂不支持预览",
	"ui.img.err_decode":         "图片解码失败，换一张试试",
	"ui.img.view_hint":          "滚轮缩放 · 拖动平移 · Esc 关闭",
	"ui.img.decoding":           "解码中…",
	"ui.img.no_image":           "没有可显示的图片",
	"ui.img.too_large":          "图片过大（{mb} MB，上限 {limit}），未添加",
	"ui.img.too_large_named":    "图片过大（{mb} MB，上限 {limit}），未添加：{name}",
	"ui.img.paste_read_failed":  "读取剪贴板图片失败：{err}",
	"ui.img.attach_limit_paste": "一条消息最多 {n} 个附件，这张图未添加",

	// ── 命令折叠条与补全浮层（cmd.go / compl.go / compl_args.go）
	"ui.cmd.header_commands": "折叠 {n} 条命令",
	"ui.cmd.header_notes":    "{n} 条提示",
	"ui.cmd.expand":          "点击展开",
	"ui.cmd.collapse":        "点击收起",
	"ui.cmd.more_hint":       "还有 {n} 条，继续输入过滤",
	"ui.cmd.no_args_label":   "（无参数）",
	"ui.cmd.no_args_tail":    "这个命令不带参数，回车直接执行",
	"ui.cmd.repeatable":      "可给多次",
	"ui.cmd.tail_sep":        "；",

	// ── 会话历史窗（history*.go）
	"ui.history.toast_copied_id": "已复制节点 ID",
	"ui.history.zoom":            "缩放 {pct}",
	"ui.history.fit_tree":        "装下整树",
	"ui.history.reset_view":      "回到初始态",
	"ui.history.focus_line":      "聚焦当前历史",
	"ui.history.focus_node":      "聚焦当前节点",
	"ui.history.edits_count":     "已改 {n} 处",
	"ui.history.discard":         "放弃",
	"ui.history.status_counts":   "共 {n} 条 · 呈现 {m}",
	"ui.history.legend_depth":    "色带",
	"ui.history.legend_shapes":   "形状：□ 你 · ● 助手 · ◇ 系统 · ◯ 会话根 · △ 注入",

	"ui.history.sessions":    "会话",
	"ui.history.import":      "导入",
	"ui.history.export":      "导出",
	"ui.history.select":      "选择",
	"ui.history.new_session": "新会话",
	"ui.history.row_meta":    "{time} · {n} 条",

	"ui.history.export_selected_title": "导出选中的会话",
	"ui.history.select_empty":          "没有会话可选（先新建或导入一个）",
	"ui.history.select_all":            "全选",
	"ui.history.select_none":           "取消全选",
	"ui.history.selected_count":        "已选 {n} 个",

	"ui.history.summary_hint":        "插在 {id} 之前；这个节点之上的历史此后不再回传。「插入」用你写的正文，「用模型总结以上历史」让模型来写。",
	"ui.history.summary_by_model":    "用模型总结以上历史",
	"ui.history.summary_insert":      "插入",
	"ui.history.summary_placeholder": "写摘要正文…",

	"ui.history.filter_session_files": "会话文件 (*.aqx;*.json;*.jsonl)",
	"ui.history.filter_aqx":           "Aquarius 归档 (*.aqx)",
	"ui.history.filter_all":           "所有文件 (*.*)",
	"ui.history.import_title":         "导入会话",
	"ui.history.export_title":         "导出会话",

	"ui.history.import_count":         "导入 {n} 个",
	"ui.history.import_pick_title":    "选择要导入的会话",
	"ui.history.import_pick_hint":     "这份备份里有 {n} 个会话，默认全要；不要的取消勾选。导进来的都是新会话，不动现有的。",
	"ui.history.import_pick_selected": "已选 {n} / {total}",
	"ui.history.no_title":             "（无标题）",
	"ui.history.messages_count":       "{n} 条",
	"ui.img.attach_unwired":           `未接入附件库`,
	"ui.history.tree_edit":            "树编辑",
}
