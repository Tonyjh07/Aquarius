package i18n

// zhGuiPageAdminTexts 设置窗的其余九页：系统提示词、工具与 MCP、外部代理、技能、角色、密钥库、对话、高级、关于。
var zhGuiPageAdminTexts = map[string]string{

	// 工具与 MCP 页
	"ui.page.tools.unwired":         "未接线（只读占位）",
	"ui.page.tools.section_builtin": "内置工具（开关即时生效，无需重启）",
	"ui.page.tools.section_mcp":     "MCP 与插件（来源 config / 插件目录）",
	"ui.page.tools.no_plugins":      "没有已声明的插件（config mcpServers 或 插件目录）",

	// 外部代理页
	"ui.page.delegate.unwired":        "未接线（只读占位）",
	"ui.page.delegate.section_agents": "清单（config delegate.agents）",
	"ui.page.delegate.empty": "一个外部代理都没有。在 config.json 的 delegate.agents 里写一条（至少要有 name 与 command），" +
		"重启后这里会出现它；没配代理时模型拿不到 delegate* 工具。",
	"ui.page.delegate.note": "「可用」只探命令在不在 PATH 上，不做握手——启动时挨个拉起代理是" +
		"拿用户的时间换一张清单，所以标题与版本要等它真的跑过一次才有。" +
		"在这里改的都是 config，**重启生效**：转交管理器按启动时的配置装配。" +
		"要删掉一个代理请改 config.json（这里只改不删）。",
	"ui.page.delegate.refresh":      "重新检测",
	"ui.page.delegate.refresh_hint": "装在别处之后再扫一遍 PATH；不改任何配置",
	"ui.page.delegate.rescanned":    "已重新检测（可用性按 PATH 现算）",

	// 技能页
	"ui.page.skills.unwired":           "未接线（只读占位）",
	"ui.page.skills.section_installed": "已安装技能",
	"ui.page.skills.empty":             "还没有技能——下面给一个 SKILL.md、文件夹、压缩包或 GitHub 链接即可装进来",
	"ui.page.skills.section_install":   "安装 / 导入",
	"ui.page.skills.note": "支持单文件 SKILL.md、技能文件夹、.zip 压缩包与 GitHub 链接（可写 #分支）。" +
		"装进来的是副本：更新会覆盖这份副本里的本地改动，卸载只删这份副本。" +
		"同名技能不覆盖——先卸载再装。",
	"ui.page.skills.source_label":       "来源",
	"ui.page.skills.source_hint":        "绝对路径或 GitHub 链接（如 https://github.com/o/r 或 …#分支）",
	"ui.page.skills.install":            "安装",
	"ui.page.skills.pick_file":          "选择文件…",
	"ui.page.skills.pick_dir":           "选择文件夹…",
	"ui.page.skills.pick_hint":          "选中路径只填进上面的来源框，再点「安装」才落盘",
	"ui.page.skills.section_dirs":       "技能目录（按顺序查找，同名先命中者生效）",
	"ui.page.skills.dir_add_label":      "添加目录",
	"ui.page.skills.dir_add_hint":       "额外技能根（绝对路径）",
	"ui.page.skills.rescan":             "重新扫描",
	"ui.page.skills.section_warnings":   "未加载的条目",
	"ui.page.skills.confirm_uninstall":  "确认卸载",
	"ui.page.skills.update":             "更新",
	"ui.page.skills.uninstall":          "卸载",
	"ui.page.skills.remove_dir":         "移除",
	"ui.page.skills.status_dir_added":   "已添加技能目录（立即生效）",
	"ui.page.skills.status_rescanned":   "已重新扫描技能目录",
	"ui.page.skills.status_uninstalled": "已卸载技能 {name}",
	"ui.page.skills.status_dir_removed": "已移除技能目录 {path}",

	// 角色页
	"ui.page.characters.unwired":          "未接线（只读占位）",
	"ui.page.characters.section_imported": "已导入角色卡",
	"ui.page.characters.read_failed":      "读取角色卡目录失败：{err}",
	"ui.page.characters.empty":            "还没有角色卡——下面选一个卡文件（.png / .json / .charx）导入",
	"ui.page.characters.section_import":   "导入角色卡",
	"ui.page.characters.note": "支持 TavernCard 角色卡：PNG 图卡、card.json 与 .charx 卡包。" +
		"导入的是副本，只读不改原卡；同名不覆盖——先删除再导入。",
	"ui.page.characters.source_label":   "卡文件",
	"ui.page.characters.source_hint":    "角色卡文件绝对路径（.png / .json / .charx）",
	"ui.page.characters.import":         "导入",
	"ui.page.characters.pick_file":      "选择文件…",
	"ui.page.characters.pick_hint":      "选中路径只填进上面的来源框，再点「导入」才落盘",
	"ui.page.characters.start_chat":     "开始对话",
	"ui.page.characters.confirm_delete": "确认删除",
	"ui.page.characters.need_path":      "请先填卡文件路径（或用「选择文件…」）",
	"ui.page.characters.imported":       "已提交导入：{path}（结果见主窗对话）",
	"ui.page.characters.started":        "已开始对话：{slug}",
	"ui.page.characters.deleted":        "已删除角色卡：{slug}",

	// 密钥库页
	"ui.page.secrets.unwired":         "未接线（只读占位）",
	"ui.page.secrets.section_entries": "已存密钥（值不可回显，只给尾 4 位）",
	"ui.page.secrets.empty":           "密钥库还是空的——下面填名字与值即可存入",
	"ui.page.secrets.section_add":     "新增 / 覆盖",
	"ui.page.secrets.name_label":      "密钥名",
	"ui.page.secrets.name_hint":       "如 AQUARIUS_OPENAI_API_KEY；引用写作 secret:<名>",
	"ui.page.secrets.value_label":     "密钥值",
	"ui.page.secrets.value_hint":      "存入后不回显；留空 = 不写",
	"ui.page.secrets.add":             "存入密钥库",
	"ui.page.secrets.confirm_delete":  "确认删除",

	// 对话页（限额五项）
	"ui.page.chat.section_limits":    "限额",
	"ui.page.chat.max_turns":         "最大轮数",
	"ui.page.chat.max_turns_hint":    "单次提问的工具调用轮数上限（空 = 保持）",
	"ui.page.chat.max_ctx":           "上下文上限",
	"ui.page.chat.max_ctx_hint":      "tokens；自动压缩与截断的预算分母（空 = 保持）",
	"ui.page.chat.compact":           "压缩阈值",
	"ui.page.chat.compact_hint":      "{min}–{max}；占用超阈值触发自动压缩（空 = 保持）",
	"ui.page.chat.tool_chars":        "工具输出上限",
	"ui.page.chat.tool_chars_hint":   "字符；超出截断（空 = 保持）",
	"ui.page.chat.tool_timeout":      "工具超时",
	"ui.page.chat.tool_timeout_hint": "秒；缺省单次调用超时（空 = 保持）",

	// 高级页
	"ui.page.adv.section_raw": "config.json 原文",
	"ui.page.adv.raw_hint":    "整文件编辑；保存前校验 JSON，解析失败则原文件不动",

	// 关于页
	"ui.page.about.tagline": "你的极简 AI 助手",
	"ui.page.about.privacy": "不收集使用数据，也不做联网上报：会话、附件与配置都留在本机；" +
		"除了你自己配置的模型服务、语音服务与 MCP server 之外，本程序不与其它服务通信。",
	"ui.page.about.section_build":   "构建",
	"ui.page.about.version_label":   "构建版本",
	"ui.page.about.runtime_label":   "运行环境",
	"ui.page.about.section_project": "项目",
	"ui.page.about.project_label":   "项目主页",
	"ui.page.about.section_legal":   "版权与许可",
	"ui.page.about.copyright_label": "版权",
	"ui.page.about.license_label":   "许可证",
	"ui.page.about.legal_note":      "图标与产品名归作者所有；许可证全文见程序目录与项目主页。",
	"ui.page.about.open_project":    "打开项目主页",
	"ui.page.about.copy_link":       "复制链接",
	"ui.page.about.dev_build":       "开发构建（未注入版本号）",
	"ui.page.about.no_browser":      "打不开浏览器：平台未接线；可以复制链接后手动打开",
	"ui.page.about.open_failed":     "打不开浏览器：{err}；可以复制链接后手动打开",
	"ui.page.about.opened":          "已在浏览器中打开",
	"ui.page.about.copied":          "链接已复制",
}
