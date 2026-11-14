package i18n

// zhGuiPageCoreTexts 设置窗的五页：常规、模型、模型服务、联网、语音。这五页的字段最多、说明句最长，单独立一份。
//
// 这五页是**字段页**：一行 = 「短标签 + 长说明（占位）+ 单位」。标签与说明分两条 key
// （`..._label` / `..._hint`）——中文里「温度」与那句话是两个语言成分，拼成一句英文就没法
// 换语序了。分区标题另收 `...section`；按档位说话的解释（协议档、联网类型、语音回落）
// 一档一条 key，不做模板拼接。
var zhGuiPageCoreTexts = map[string]string{

	// 常规页：Profile 区（分区标题 "Profile" 是配置里的术语，不搬）
	"ui.page.general.section.profile":    "Profile",
	"ui.page.general.section.perm":       "权限",
	"ui.page.general.section.appearance": "外观与呼出",
	"ui.page.general.section.output":     "输出",
	"ui.page.general.section.storage":    "存储",

	"ui.page.general.prof_current_label": "当前",
	"ui.page.general.prof_current_avail": "（可用：{names}）",
	"ui.page.general.prof_name_label":    "Profile 名",
	"ui.page.general.prof_name_hint":     "新建 / 复制 / 切换的目标名",
	"ui.page.general.prof_create":        "新建",
	"ui.page.general.prof_copy":          "复制当前",
	"ui.page.general.prof_switch":        "切换（重启生效）",
	"ui.page.general.welcome_label":      "欢迎引导",
	"ui.page.general.welcome_open":       "打开",
	"ui.page.general.welcome_hint":       "模型接入向导 + 快捷键 / 右键菜单 / 常用命令",

	// 常规页：权限与外观
	"ui.page.general.perm_label":        "权限等级",
	"ui.page.general.unattended_label":  "无人值守",
	"ui.page.general.unattended_check":  "越界操作当场拒绝，不等人确认",
	"ui.page.general.unattended_hint":   "权限等级不变；你自己输入的命令确认照常询问",
	"ui.page.general.hotkey_label":      "全局快捷键",
	"ui.page.general.hotkey_hint":       "如 Alt+A（空 = 默认）",
	"ui.page.general.theme_label":       "主题",
	"ui.page.general.lang_label":        "界面语言",
	"ui.page.general.lang_hint":         "重启后生效；AQUARIUS_UI_LANG 环境变量优先",
	"ui.page.general.placeholder_label": "输入框占位",
	"ui.page.general.placeholder_hint":  "留空 = 默认提示（随界面语言）；最多 {max} 字符",
	"ui.page.general.lang_auto":         "跟随系统",
	"ui.page.general.scale_label":       "元素缩放",
	"ui.page.general.font_size_label":   "字号",
	"ui.page.general.font_size_hint":    "正文字号 {min}–{max}（最终 = 字号 × 缩放）",
	"ui.page.general.win_size_label":    "窗口尺寸",
	"ui.page.general.notify_check":      "桌面通知（回答完成时）",
	"ui.page.general.tts_check":         "语音播报（回答完成后自动播放）",
	"ui.page.general.backup_keep_label": "备份代数",
	"ui.page.general.backup_keep_hint":  "会话树保留的备份份数 {min}–{max}（空 = 默认 3）",

	// 模型页：当前模型 / 思考与推理
	"ui.page.model.section.current":  "当前模型",
	"ui.page.model.section.thinking": "思考与推理",
	"ui.page.model.section.sampling": "采样参数（留空 = 交服务端默认）",
	"ui.page.model.section.chain":    "降级链",

	"ui.page.model.model_name_label": "当前模型名",
	"ui.page.model.model_name_hint":  "model.name；改动保存后经 /model 热切换",
	"ui.page.model.think_check":      "原生思考（/think 总开关，关时忽略推理档位）",
	"ui.page.model.effort_label":     "推理档位",

	// 模型页：采样参数（top_p 是 config 键名，不搬）
	"ui.page.model.temp_label":    "温度",
	"ui.page.model.temp_hint":     "{min}–{max}；越低越稳，越高越发散。留空 = 服务端默认，写 0 = 每次都取最可能的词",
	"ui.page.model.topp_hint":     "{min}–{max}；只从概率累计到该比例的候选里挑。留空 = 服务端默认",
	"ui.page.model.max_label":     "输出上限",
	"ui.page.model.max_hint":      "tokens；单轮最多生成多少。留空 = 不发送（仍受本轮预算约束）",
	"ui.page.model.stop_label":    "停止串",
	"ui.page.model.stop_hint":     "JSON 数组，如 [\"\\n\", \"END\"]；生成到这些串就停。留空 = 不发送",
	"ui.page.model.sampling_note": "留空 = 这个字段不发，由服务端取自家默认；写 0 = 明确要 0（温度 0 就是每次取最可能的词）。这里填的是整条链的缺省；只想改某个模型，就在配置文件里给那个模型加 sampling。",

	// 模型页：降级链
	"ui.page.model.prov_label":  "提供者",
	"ui.page.model.prov_picker": "降级链提供者",
	"ui.page.model.model_label": "模型",
	"ui.page.model.model_hint":  "空 = 取该提供者的缺省模型；可填未声明的名字",
	"ui.page.model.chain_add":   "＋ 添加降级项",

	// 模型服务页：卡片基本字段（"Tokenizer" 是术语，不搬）
	"ui.page.providers.section.list": "模型服务（provider 列表）",
	"ui.page.providers.name_label":   "名称",
	"ui.page.providers.name_hint":    "provider 唯一标识（primary/fallback 按名引用）",
	"ui.page.providers.kind_label":   "协议",
	"ui.page.providers.base_label":   "接口地址",
	"ui.page.providers.base_hint":    "如 https://api.deepseek.com/v1",

	// 模型服务页：协议档各一条说明（按档位分开，不拼模板）
	"ui.page.providers.hint.kind.openai_responses": "OpenAI Responses API（/responses）",
	"ui.page.providers.hint.kind.anthropic":        "Anthropic Messages API（/messages）",
	"ui.page.providers.hint.kind.openai":           "OpenAI 兼容 /chat/completions",

	"ui.page.providers.tokenizer_hint":    "本地 tokenizer.json 路径；空 = 估算",
	"ui.page.providers.unsupported_label": "不认参数",
	"ui.page.providers.unsupported_hint":  "逗号分隔；这些参数会被剔除，不发往服务端",

	// 模型服务页：密钥池
	"ui.page.providers.head.keys":      "密钥池",
	"ui.page.providers.key_add":        "＋ 添加密钥",
	"ui.page.providers.key_value_hint": "留空 = 保持；填明文则转入本机密钥库；以 secret: 开头则照写引用",
	"ui.page.providers.key_label_hint": "展示名（空 = 第 N 把）",

	// 模型服务页：单槽读侧状态（值永不回显）
	"ui.page.providers.keyslot.saved": "当前引用：{ref}（值在密钥库，不回显）",
	"ui.page.providers.keyslot.plain": "当前为明文（重启会在日志中提示；重填一次即转入密钥库）",
	"ui.page.providers.keyslot.empty": "当前未存值（回落缺省引用）",

	// 模型服务页：模型列表（含分页）
	"ui.page.providers.models_head":         "模型列表",
	"ui.page.providers.models_count":        "共 {total} 个 · 已启用 {on} 个",
	"ui.page.providers.models_fetch":        "获取模型",
	"ui.page.providers.pager_hint":          "第 {page}/{pages} 页",
	"ui.page.providers.pager_prev":          "‹ 上一页",
	"ui.page.providers.pager_next":          "下一页 ›",
	"ui.page.providers.model_name_hint":     "模型名（服务端标识）",
	"ui.page.providers.model_ctx_hint":      "窗口 tokens",
	"ui.page.providers.model_cost_in_hint":  "输入 $/M",
	"ui.page.providers.model_cost_out_hint": "输出 $/M",
	"ui.page.providers.model_add":           "＋ 添加模型",
	"ui.page.providers.probe":               "探测能力",

	// 模型服务页：卡片动作与尾部落盘
	"ui.page.providers.test":          "测试连通",
	"ui.page.providers.set_primary":   "设为主",
	"ui.page.providers.primary_badge": "★ 主",
	"ui.page.providers.prov_add":      "＋ 新增 provider",
	"ui.page.providers.apply":         "应用 provider 变更",
	"ui.page.providers.apply_note":    "变更全量写回 config，重启生效；「测试连通」「探测能力」发真实请求。",

	// 联网页：三个分区与各自的列表
	"ui.page.web.section.search": "搜索（按顺序尝试，失败换下一家）",
	"ui.page.web.section.fetch":  "网页抓取（按顺序尝试，失败换下一家）",
	"ui.page.web.section.local":  "本地抓取（不经过第三方，直接取网页正文）",

	"ui.page.web.search_add":  "＋ 添加搜索提供商",
	"ui.page.web.count_label": "结果条数",
	"ui.page.web.count_hint":  "缺省 8（{min}–{max}）；工具参数可逐次覆盖（空 = 保持）",
	"ui.page.web.fetch_add":   "＋ 添加抓取提供商",
	"ui.page.web.chars_label": "正文上限",
	"ui.page.web.chars_hint":  "字符；缺省 16000（{min}–{max}）（空 = 保持）",

	// 联网页：本地抓取的网络与安全参数
	"ui.page.web.proxy_label":   "代理",
	"ui.page.web.proxy_hint":    "如 http://127.0.0.1:7897；空 = 读环境变量",
	"ui.page.web.ua_hint":       "空 = 浏览器形态缺省值（很多站点拒绝非浏览器 UA）",
	"ui.page.web.robots_check":  "遵守 robots.txt（站点声明不许抓的路径就不抓）",
	"ui.page.web.private_check": "允许访问本机与内网地址（只用于调试本机服务）",
	"ui.page.web.timeout_label": "超时",
	"ui.page.web.timeout_hint":  "秒；缺省 30（空 = 保持）",
	"ui.page.web.note":          "两组列表都是有序的：先试第一家，失败换下一家。端点与密钥可以自己填，也可以选「借用」再点「填入」——那会把「模型服务」里那条的端点与密钥引用抄过来，之后照样能改。密钥只写不回显（留空 = 保持）。本地抓取不需要密钥，但会拒绝内网与回环地址。提供商与安全参数的改动保存后重启生效。",

	// 联网页：类型档各一条说明（按档位分开，不拼模板）
	"ui.page.web.hint.kind.deepseek": "端点与密钥可自填，也可借「模型服务」",
	"ui.page.web.hint.kind.brave":    "需订阅令牌",
	"ui.page.web.hint.kind.local":    "本机抓取，无需密钥",

	// 联网页：一条提供商的字段
	"ui.page.web.from_none":          "不借",
	"ui.page.web.kind_label":         "类型",
	"ui.page.web.kind_picker.search": "搜索类型",
	"ui.page.web.kind_picker.fetch":  "抓取类型",
	"ui.page.web.endpoint_label":     "端点",
	"ui.page.web.endpoint_hint":      "如 https://api.deepseek.com；留空 = 用「借用」那条的",
	"ui.page.web.from_label":         "借用",
	"ui.page.web.from_picker":        "借用服务",
	"ui.page.web.fill":               "填入",
	"ui.page.web.fill_hint":          "把所选服务的端点与密钥引用抄进本条目，之后可改",
	"ui.page.web.key_label":          "密钥",
	"ui.page.web.key_hint":           "secret:名 或明文；留空 = 保持 / 借用",
	"ui.page.web.country_lang_label": "国家 / 语言",
	"ui.page.web.country_hint":       "如 CN（空 = 服务端缺省）",
	"ui.page.web.lang_hint":          "如 zh-hans（空 = 服务端缺省）",
	"ui.page.web.item_count_label":   "本条条数",
	"ui.page.web.item_count_hint":    "空 = 用上面的「结果条数」",

	// 联网页：密钥框下方的形态说明（值永不回显）
	"ui.page.web.keynote.saved":    "已存引用 {ref}；留空 = 保持不变",
	"ui.page.web.keynote.plain":    "当前是明文（启动会警告）；填新值覆盖，留空 = 保持不变",
	"ui.page.web.keynote.fallback": "留空 = 用「借用」那条的；都没有就按类型回落：brave 用 AQUARIUS_BRAVE_API_KEY，deepseek 搜索用 AQUARIUS_DEEPSEEK_SEARCH_API_KEY，deepseek 抓取用 AQUARIUS_DEEPSEEK_FETCH_API_KEY",

	// 语音页：识别与合成的分区 / 方式
	"ui.page.audio.section.asr":        "语音识别（说话转文字）",
	"ui.page.audio.section.tts":        "语音合成（文字转语音）",
	"ui.page.audio.asr_kind_label":     "识别方式",
	"ui.page.audio.tts_kind_label":     "合成方式",
	"ui.page.audio.asr_service_picker": "语音识别服务",
	"ui.page.audio.tts_service_picker": "语音合成服务",
	"ui.page.audio.service_label":      "服务",
	"ui.page.audio.fill":               "填入",
	"ui.page.audio.fill_hint":          "抄所选服务的端点与密钥引用进本页，之后可改",

	// 语音页：端点 / 模型 / 语种
	"ui.page.audio.endpoint_label":  "端点",
	"ui.page.audio.endpoint_hint":   "如 https://api.example.com/v1；留空 = 用「服务」那条的",
	"ui.page.audio.asr_model_label": "识别模型",
	"ui.page.audio.asr_model_hint":  "如 mimo-v2.5-asr / whisper-1",
	"ui.page.audio.asr_lang_label":  "录音语种",
	"ui.page.audio.tts_model_label": "合成模型",
	"ui.page.audio.tts_model_hint":  "如 mimo-v2.5-tts / -voicedesign / -voiceclone",

	// 语音页：音色与风格
	"ui.page.audio.voice_label":  "音色",
	"ui.page.audio.voice_hint":   "预置音色名；克隆模型填样本文件绝对路径",
	"ui.page.audio.design_label": "音色描述",
	"ui.page.audio.design_hint":  "音色设计模型必填（如：温柔的女声）",
	"ui.page.audio.style_label":  "风格指令",
	"ui.page.audio.style_hint":   "自然语言（如：语速稍快，语气轻松）",
	"ui.page.audio.tags_label":   "风格标签",
	"ui.page.audio.tags_hint":    "如：温柔 语速稍快（会拼在正文前）",
	"ui.page.audio.vad_label":    "按说话分段",
	"ui.page.audio.vad_hint":     "停顿够长就切成独立的一段、逐段转写（默认开）",
	"ui.page.audio.note":         "识别与合成各自是一份完整配置：先选方式（协议），再填本页给出的字段——本地 whisper 只有模型文件，其余方式才有服务与端点。需要端点或密钥的那几档，可以自己填，也可以选一个「服务」再点「填入」，那会把该条目的端点与密钥引用抄过来，之后照样能改。密钥只写不回显（留空 = 保持）。改方式、服务、端点或模型需保存并重启。音色、音色描述与风格保存后对此后的播报生效；试听按当前填写的内容合成一句。",

	// 语音页：本地 whisper 的模型文件（只这一档用到，按档位单列）
	"ui.page.audio.model_path_label": "模型文件",
	"ui.page.audio.model_path_hint":  "本地 whisper 的 ggml 模型绝对路径（如 …\\ggml-base-q5_1.bin）",
	"ui.page.audio.model_path_note":  "模型不在仓库里，需要自己下载：到 huggingface.co/ggerganov/whisper.cpp 取 ggml-base-q5_1.bin（约 60MB，中文效果与速度的平衡点）。只在「本地 whisper」这一档用到。",

	// 语音页：密钥与形态说明（识别 / 合成各一条回落句）
	"ui.page.audio.key_label":            "密钥",
	"ui.page.audio.key_hint":             "secret:名 或明文；留空 = 保持 / 借用",
	"ui.page.audio.keynote.saved":        "已存引用 {ref}；留空 = 保持不变",
	"ui.page.audio.keynote.plain":        "当前是明文（启动会警告）；填新值覆盖，留空 = 保持不变",
	"ui.page.audio.keynote.fallback_asr": "留空 = 借用所选服务的密钥；未选服务则回落 secret:AQUARIUS_ASR_API_KEY",
	"ui.page.audio.keynote.fallback_tts": "留空 = 借用所选服务的密钥；未选服务则回落 secret:AQUARIUS_TTS_API_KEY",

	// 语音页：试听
	"ui.page.audio.preview_label":         "试听",
	"ui.page.audio.preview_play":          "播放示例",
	"ui.page.audio.preview_busy":          "合成中…",
	"ui.page.audio.preview_text":          "你好，这里是 Aquarius。这段声音听起来怎么样？",
	"ui.page.audio.status.synthesizing":   "正在合成…",
	"ui.page.audio.status.playing":        "已开始播放",
	"ui.page.audio.status.preview_failed": "试听失败：{err}",
	"ui.page.audio.status.unavailable":    "试听不可用：语音合成未配置",
}
