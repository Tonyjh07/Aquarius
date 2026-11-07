package i18n

// zhCmdConfigTexts 命令行侧的配置与装配：config*.go 的键说明与校验报错、启动与 profile 管理、
// 装配期提示。
//
// 四段前缀：cmd.config.（配置解析与校验）、cmd.boot.（启动与主循环）、cmd.profile.（profile 名与
// profile 文件）、cmd.asm.（端口与界面装配期的提示与告警）。第二段收用途：err 校验失败、warn
// 装配期告警、flag 命令行开关、notice 运行期一次性提示。
//
// 旧格式迁移的 JSON 示例不在这张表里：它与语言无关，留在代码里作为常量，两种语言各自拼上去——
// 示例里的花括号不该被占位符扫描碰到。
var zhCmdConfigTexts = map[string]string{

	// config.go：技能目录、外部服务端点、secret 引用
	"cmd.config.skills.dir_not_abs": `skills.dirs[{i}]="{dir}" 不是绝对路径（技能目录必须写绝对路径）`,
	"cmd.config.endpoint.from_missing": `引用的服务 "{from}" 不在 model.providers 内` +
		`（先在「模型服务」页添加，或直接填端点与密钥）`,
	"cmd.config.endpoint.missing": `端点未配置（填 base_url，或引用一个 model.providers 里的条目）`,
	"cmd.config.secret.name_missing": `api_key 的 secret: 引用缺少环境变量名，当前为 "{ref}"` +
		`（或改为直接填明文）`,

	// config_io.go：读写与指针
	"cmd.config.err.legacy_config":   `检测到旧格式 config`,
	"cmd.config.err.read":            `读取 {path}`,
	"cmd.config.err.parse":           `解析配置 {path}`,
	"cmd.config.err.parse_short":     `解析 {path}`,
	"cmd.config.err.encode":          `编码 config`,
	"cmd.config.err.placeholder_max": `ui.input_placeholder 最多 {max} 个字符`,
	"cmd.config.err.write":           `写入 {path}`,

	// config_io.go：旧格式单项判别（%w 由代码拼在前面，这里只有说明文字）
	"cmd.config.err.legacy_flat": `{path} 的 model 段为单 provider 平铺字段` +
		`（检测到 base_url/api_key/tokenizer/provider）。` + "\n" +
		`provider 须写成列表（model.providers）：` + "\n",
	"cmd.config.err.legacy_flat_tail": `请手工改写（不做自动迁移），原平铺字段退役；` +
		`字段说明见 docs/configuration.md。`,

	"cmd.config.err.legacy_shape": `{path} 的 model.providers[{index}] 是旧形状` +
		`（api_key 单值 / models 字符串数组 / provider 上的 vision）。` + "\n" +
		`模型与提供商已改版：provider 承载「怎么连」，模型承载「能做什么」。` + "\n",
	"cmd.config.err.legacy_shape_notes": `迁移要点：api_key → keys[]（多把即密钥池）；` +
		`models 字符串 → 对象（能力位写进 caps，` + "\n" +
		`省略 caps 按协议缺省）；provider 上的 vision 移到对应模型的 caps；fallback 字符串 →` + "\n" +
		`provider/model 二元组（省略 model = 该 provider 的 models[0]）。` + "\n",
	"cmd.config.err.legacy_shape_tail": `请手工改写（不做自动迁移）；字段说明见 docs/configuration.md。`,

	"cmd.config.err.legacy_fallback": `{path} 的 model.fallback 仍是 provider 名字数组。` + "\n" +
		`降级链已改为逐项指定模型：` +
		`"fallback": [ { "provider": "mimo", "model": "mimo-v2.6-flash" } ]` + "\n" +
		`（省略 model = 该 provider 的 models[0]）。请手工改写（不做自动迁移），` +
		`见 docs/configuration.md。`,

	// config_io.go：指针文件含旧格式键（手工迁移步骤）
	"cmd.config.err.legacy_keys": `{path} 含旧格式键 "{key}"。` + "\n" +
		`profile 已改为目录化：根 config.json 仅存当前 profile 指针（{"profile":"<name>"}），` + "\n" +
		`主配置落 profiles/<name>/config.json。请手工处置（不做自动迁移），` +
		`例如迁移到 {profile} profile：` + "\n" +
		`  1. 把 {path} 移动为 {target}` + "\n" +
		`  2. 新建根 {path}，内容：{pointer}` + "\n" +
		`若该 profile 已存在请改用其他名字或手工合并；字段说明见 docs/configuration.md。`,

	"cmd.config.err.pointer_no_key":     `{path} 缺 profile 键（指针格式：{"profile":"<name>"}）`,
	"cmd.config.err.pointer_not_string": `{path} 的 profile 键须为非空字符串`,

	// config_model.go：降级链窄于当前模型的提示（词表与两处分隔符）
	"cmd.config.sep.list":          `、`,
	"cmd.config.sep.item":          `；`,
	"cmd.config.caps.tools":        `工具调用`,
	"cmd.config.caps.tools_short":  `工具`,
	"cmd.config.caps.reasoning":    `思考`,
	"cmd.config.caps.vision":       `图片输入`,
	"cmd.config.caps.vision_short": `图片`,
	"cmd.config.narrow.chain":      `降级链窄于当前模型 {model} 的能力，整链按不支持 {lost} 处理`,
	"cmd.config.narrow.weak_item":  `{provider}/{model} 缺 {missing}`,
	"cmd.config.narrow.weak":       `（{items}）`,
	"cmd.config.narrow.tail":       `；检查 model.fallback 各模型的 caps`,

	// config_model.go：采样参数校验
	"cmd.config.err.sampling_temperature": `{where} 的 sampling.temperature = {value} 超出范围` +
		`（该协议接受 0..{max}）`,
	"cmd.config.err.sampling_top_p": `{where} 的 sampling.top_p = {value} 超出范围（接受 0..{max}）`,
	"cmd.config.err.sampling_max_tokens": `{where} 的 sampling.max_tokens 不可为负数（{n}）；` +
		`不限制就留空`,
	"cmd.config.err.sampling_stop": `{where} 的 sampling.stop 含空串（停止串不能为空）`,

	// config_model.go：model 段结构校验
	"cmd.config.err.provider_name_empty": `model.providers[{i}].name 不可为空`,
	"cmd.config.err.provider_name_dup":   `model.providers[{i}].name "{name}" 重复`,
	"cmd.config.err.provider_kind": `model.providers[{i}] ({provider}) 的 kind "{kind}" 不认识` +
		`（可选：{a} / {b} / {c}）`,
	"cmd.config.err.provider_base_url_empty": `model.providers[{i}] ({provider}) 的 base_url 不可为空`,
	"cmd.config.err.provider_models_empty": `model.providers[{i}] ({provider}) 的 models 不可为空` +
		`（首个 = 缺省请求模型）`,
	"cmd.config.err.model_name_empty": `model.providers[{i}] ({provider}) 的 models[{j}].name 不可为空`,
	"cmd.config.err.model_name_dup":   `model.providers[{i}] ({provider}) 的模型 "{name}" 重复`,
	"cmd.config.err.context_window_neg": `model.providers[{i}] ({provider}) 的模型 "{name}" 的 ` +
		`context_window 不可为负数（{n}）；不声明就留空`,
	"cmd.config.err.cost_neg": `model.providers[{i}] ({provider}) 的模型 "{name}" 的 ` +
		`cost_in / cost_out 不可为负数`,
	"cmd.config.err.no_model_enabled": `model.providers[{i}] ({provider}) 的模型全部未启用；` +
		`至少启用一个（enabled: false 只是清单占位，不参与选型）`,
	"cmd.config.err.primary_missing":      `model.primary "{name}" 不在 model.providers 内`,
	"cmd.config.err.fallback_no_provider": `model.fallback 每项都要写 provider`,
	"cmd.config.err.fallback_missing":     `model.fallback "{provider}" 不在 model.providers 内`,
	"cmd.config.err.fallback_primary": `model.fallback 不应含当前生效的 {provider}/{model}` +
		`（那就是 primary 本身）`,
	"cmd.config.err.fallback_dup": `model.fallback 重复项 "{key}"`,
	"cmd.config.key_label":        `第 {n} 把`,

	// config_delegate.go：外部代理转交
	"cmd.config.err.delegate_timeout_neg":     `delegate.timeout_sec 不能为负（0 = 不限）：{n}`,
	"cmd.config.err.delegate_timeout_max":     `delegate.timeout_sec 超出上限（<= {max} 秒）：{n}`,
	"cmd.config.err.delegate_negative":        `delegate.max_concurrent / max_total 不能为负`,
	"cmd.config.err.delegate_name_missing":    `delegate.agents[{i}]：缺 name`,
	"cmd.config.err.delegate_name_dup":        `delegate.agents[{i}]：name "{name}" 重复`,
	"cmd.config.err.delegate_command_missing": `delegate.agents[{i}]（{name}）：缺 command`,
	"cmd.config.err.delegate_workdir": `delegate.agents[{i}]（{name}）：workdir 必须是绝对路径：` +
		`"{workdir}"`,
	"cmd.config.err.delegate_default_disabled": `delegate.agents[{i}]（{name}）：` +
		`default 的代理不能同时 enabled=false`,
	"cmd.config.err.delegate_default_multi": `delegate.agents 里有多条 default（{n} 条）：只能有一条`,

	// config_audio.go：语音
	"cmd.config.err.input_language":     `input.language="{lang}" 仅支持 auto | zh | en`,
	"cmd.config.err.audio_kind":         `{name}.kind="{kind}" 不认识（支持 {kinds}）`,
	"cmd.config.err.audio_endpoint":     `{name} 选了 {kind} 方式就要填端点（base_url 或引用一个服务）`,
	"cmd.config.err.audio_model_path":   `{name}.model_path 不可为空（本地识别要指定 ggml 模型文件）`,
	"cmd.config.err.audio_model":        `{name}.model 不可为空（配置了服务就要写明音频模型名）`,
	"cmd.config.err.audio_unknown":      `未知的语音能力 "{which}"`,
	"cmd.config.err.audio_unconfigured": `未配置语音服务`,

	// config_web.go：联网
	"cmd.config.err.web_search_kind": `web.search.providers[{i}].kind="{kind}" 不认识` +
		`（搜索侧支持 {a} / {b}）`,
	"cmd.config.err.web_fetch_kind": `web.fetch.providers[{i}].kind="{kind}" 不认识` +
		`（抓取侧支持 {a} / {b}）`,
	"cmd.config.err.web_search_count": `web.search.count={n} 超出范围（{min}-{max}；0 = 缺省 8）`,
	"cmd.config.err.web_fetch_chars": `web.fetch.max_chars={n} 超出范围（{min}-{max}；` +
		`0 = 缺省 16000）`,
	"cmd.config.err.web_from_missing": `from="{from}" 不在 model.providers 内` +
		`（填 base_url 独立配置，或引用一个已有的条目）`,

	// main.go：命令行 flag 帮助（-h 里出现）
	"cmd.boot.flag.data":       `数据目录（默认 ~/.aquarius）`,
	"cmd.boot.flag.profile":    `选择 profile（本次运行临时覆盖根 config.json 的指针；须已存在）`,
	"cmd.boot.flag.model":      `覆盖 model.name`,
	"cmd.boot.flag.base_url":   `覆盖 model.base_url`,
	"cmd.boot.flag.yes":        `跳过逐次确认（等价对每个确认回答 y；危险操作慎用）`,
	"cmd.boot.flag.log_level":  `运行日志级别（debug | info | warn | error；默认取 ui.log_level）`,
	"cmd.boot.flag.log_format": `运行日志形态（text | json；只影响本次运行）`,
	"cmd.boot.flag.version":    `打印版本并退出`,

	// main.go / bootstrap_profile.go：启动与主循环
	"cmd.boot.banner":                `Aquarius — 输入 /help 查看命令，/quit 退出；Ctrl+C 取消当前生成`,
	"cmd.boot.interrupted_delegates": `上次退出时中断了 {n} 笔转交（已补记到发起会话）`,
	"cmd.boot.steer_retry":           `已中断这一步，用引导消息重新请求`,

	"cmd.boot.err.home_dir":        `无法确定家目录`,
	"cmd.boot.err.mkdir_data":      `创建数据目录 {path}`,
	"cmd.boot.err.mkdir_profile":   `创建 profile 目录 {path}`,
	"cmd.boot.err.mkdir_sandbox":   `创建特权目录 {path}`,
	"cmd.boot.err.mkdir_skills":    `创建技能目录 {path}`,
	"cmd.boot.err.secret_store":    `装配本机密钥库`,
	"cmd.boot.err.write_config":    `生成配置失败`,
	"cmd.boot.err.read_new_config": `读取刚生成的配置 {path}`,
	"cmd.boot.err.read_config":     `读取配置失败`,
	"cmd.boot.err.ui_kind":         `ui.kind="{kind}" 仅支持 repl | tui | gui`,

	// profiles.go / config_io.go：profile 名与 profile 文件
	"cmd.profile.err.empty":           `profile 名不可为空`,
	"cmd.profile.err.whitespace":      `profile 名首尾不可含空白: "{name}"`,
	"cmd.profile.err.invalid":         `profile 名非法: "{name}"`,
	"cmd.profile.err.charset":         `profile 名不可含 / \ : 且长度 ≤64: "{name}"`,
	"cmd.profile.err.control":         `profile 名不可含控制字符: "{name}"`,
	"cmd.profile.err.exists":          `profile "{name}" 已存在`,
	"cmd.profile.err.mkdir":           `创建 profile 目录`,
	"cmd.profile.err.read_current":    `读取当前 profile 配置`,
	"cmd.profile.err.already_current": `已运行在 profile "{name}"`,
	"cmd.profile.err.not_exist":       `profile "{name}" 不存在（可先「新建」或「复制当前」）`,
	"cmd.profile.err.write_pointer":   `生成 profile 指针 {path}`,
	"cmd.profile.err.read_pointer":    `读取 profile 指针 {path}`,
	"cmd.profile.err.not_found":       `--profile {name} 不存在（profiles/ 下可用：{available}）`,

	// assemble_ports.go / assemble_ui.go / 小文件：装配期提示
	"cmd.asm.err.no_provider":      `还没有配置模型提供商：在设置窗「模型」一栏填好提供商（名称、接口地址、密钥、模型名）后即可对话`,
	"cmd.asm.err.no_key":           `provider "{provider}" 没有可用的密钥`,
	"cmd.asm.err.fallback_missing": `model.fallback 引用的 provider "{provider}" 不存在`,
	"cmd.asm.key.rotated":          `密钥 {from} 被限流或额度耗尽，本轮改用 {to}`,
	"cmd.asm.key.exhausted":        `{provider} 的密钥都在冷却，最早 {time} 之后恢复`,
	"cmd.asm.name.asr":             `语音识别`,
	"cmd.asm.name.tts":             `语音合成`,
	"cmd.asm.err.skill_installer":  `技能安装面`,
	"cmd.asm.err.characters_dir":   `角色卡目录`,
	"cmd.asm.notice.truncate":      `硬保底截断：上下文超预算，已省略 {n} 条最旧消息`,
	"cmd.asm.notice.fallback": `模型服务 {from} 网络类错误，本轮已降级到 {to}` +
		`（Q5：状态行一次性提示）`,

	"cmd.asm.err.voice_input_unwired": `语音输入未装配`,
	"cmd.asm.err.voice_preview_unconfigured": `语音合成未配置：先在「语音」页选服务与模型` +
		`（保存后重启生效）`,
	"cmd.asm.err.voice_player_unwired": `语音播放未装配`,
	"cmd.asm.err.read_synth_audio":     `读取合成音频`,

	"cmd.asm.err.mkdir_memory":       `创建记忆目录`,
	"cmd.asm.err.create_memory_file": `创建记忆文件 {path}`,
	"cmd.asm.err.run_editor":         `运行编辑器 {editor}`,

	"cmd.asm.err.list_sessions": `列出会话`,
	"cmd.asm.err.load_session":  `加载会话 {id}`,

	"cmd.asm.mcp.param_name": `参数`,
	"cmd.asm.mcp.param_desc": `这个 prompt 的参数，按服务端声明的顺序填`,

	"cmd.asm.err.output_deliver": `输出器 {name}`,
	"cmd.asm.err.start_process":  `启动 {cmd}`,

	"cmd.asm.err.write_back_config": `写回 config`,
	"cmd.asm.err.env_unset":         `环境变量 {name} 未设置（config 的 api_key 引用它）`,
}
