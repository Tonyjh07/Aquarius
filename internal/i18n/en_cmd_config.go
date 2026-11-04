package i18n

// enCmdConfigTexts 与 zhCmdConfigTexts 同 key 同顺序（同构测试盯着）。
var enCmdConfigTexts = map[string]string{

	// config.go
	"cmd.config.skills.dir_not_abs": `skills.dirs[{i}]="{dir}" is not an absolute path` +
		` (skill directories must be absolute)`,
	"cmd.config.endpoint.from_missing": `Referenced service "{from}" is not in model.providers` +
		` (add it on the Model Services page, or fill in the endpoint and key directly)`,
	"cmd.config.endpoint.missing": `Endpoint not configured` +
		` (set base_url, or reference an entry in model.providers)`,
	"cmd.config.secret.name_missing": `The secret: reference in api_key has no environment ` +
		`variable name (currently "{ref}"); use plain text instead`,

	// config_io.go
	"cmd.config.err.legacy_config":   `Legacy config detected`,
	"cmd.config.err.read":            `Reading {path}`,
	"cmd.config.err.parse":           `Parsing config {path}`,
	"cmd.config.err.parse_short":     `Parsing {path}`,
	"cmd.config.err.encode":          `Encoding config`,
	"cmd.config.err.placeholder_max": `ui.input_placeholder is limited to {max} characters`,
	"cmd.config.err.write":           `Writing {path}`,

	"cmd.config.err.legacy_flat": `The model section in {path} uses the legacy flat ` +
		`single-provider fields (base_url/api_key/tokenizer/provider found).` + "\n" +
		`Providers must be a list (model.providers):` + "\n",
	"cmd.config.err.legacy_flat_tail": `Please rewrite by hand (no automatic migration); ` +
		`the flat fields are retired. See docs/configuration.md.`,

	"cmd.config.err.legacy_shape": `model.providers[{index}] in {path} uses the old shape ` +
		`(api_key as a single value / models as a string array / vision on the provider).` + "\n" +
		`Providers and models were redesigned: the provider says how to connect, ` +
		`the model says what it can do.` + "\n",
	"cmd.config.err.legacy_shape_notes": `Migration: api_key → keys[] (several keys form a pool); ` +
		`models strings → objects (write capabilities into caps, ` + "\n" +
		`omitted caps fall back to the protocol defaults); vision on the provider moves into ` +
		`the matching model's caps; fallback strings →` + "\n" +
		`provider/model pairs (omit model = that provider's models[0]).` + "\n",
	"cmd.config.err.legacy_shape_tail": `Please rewrite by hand (no automatic migration); ` +
		`see docs/configuration.md for the fields.`,

	"cmd.config.err.legacy_fallback": `model.fallback in {path} is still an array of provider ` +
		`names.` + "\n" +
		`The fallback chain now names a model per entry: ` +
		`"fallback": [ { "provider": "mimo", "model": "mimo-v2.6-flash" } ]` + "\n" +
		`(omit model = that provider's models[0]). Please rewrite by hand ` +
		`(no automatic migration); see docs/configuration.md.`,

	"cmd.config.err.legacy_keys": `{path} contains the legacy key "{key}".` + "\n" +
		`Profiles are now directory-based: the root config.json holds only the current ` +
		`profile pointer ({"profile":"<name>"}),` + "\n" +
		`and the main config lives in profiles/<name>/config.json. Please migrate by hand ` +
		`(no automatic migration), e.g. to the {profile} profile:` + "\n" +
		`  1. Move {path} to {target}` + "\n" +
		`  2. Create the root {path} with: {pointer}` + "\n" +
		`If that profile already exists, pick another name or merge by hand; ` +
		`see docs/configuration.md for the fields.`,

	"cmd.config.err.pointer_no_key":     `{path} has no profile key (pointer format: {"profile":"<name>"})`,
	"cmd.config.err.pointer_not_string": `The profile key in {path} must be a non-empty string`,

	// config_model.go
	"cmd.config.sep.list":          `, `,
	"cmd.config.sep.item":          `; `,
	"cmd.config.caps.tools":        `tool calling`,
	"cmd.config.caps.tools_short":  `tools`,
	"cmd.config.caps.reasoning":    `reasoning`,
	"cmd.config.caps.vision":       `image input`,
	"cmd.config.caps.vision_short": `images`,
	"cmd.config.narrow.chain": `The fallback chain is narrower than model {model}; ` +
		`the whole chain is treated as not supporting {lost}`,
	"cmd.config.narrow.weak_item": `{provider}/{model} lacks {missing}`,
	"cmd.config.narrow.weak":      ` ({items})`,
	"cmd.config.narrow.tail":      `; check the caps of each model in model.fallback`,

	"cmd.config.err.sampling_temperature": `{where}: sampling.temperature = {value} is out of ` +
		`range (this protocol accepts 0..{max})`,
	"cmd.config.err.sampling_top_p": `{where}: sampling.top_p = {value} is out of range ` +
		`(accepts 0..{max})`,
	"cmd.config.err.sampling_max_tokens": `{where}: sampling.max_tokens cannot be negative ({n}); ` +
		`leave it empty for no limit`,
	"cmd.config.err.sampling_stop": `{where}: sampling.stop contains an empty string ` +
		`(a stop string cannot be empty)`,

	// config_model.go：model 段结构校验
	"cmd.config.err.provider_name_empty": `model.providers[{i}].name cannot be empty`,
	"cmd.config.err.provider_name_dup":   `model.providers[{i}].name "{name}" is duplicated`,
	"cmd.config.err.provider_kind": `model.providers[{i}] ({provider}) has unknown kind "{kind}"` +
		` (choose from: {a} / {b} / {c})`,
	"cmd.config.err.provider_base_url_empty": `model.providers[{i}] ({provider}): base_url cannot be empty`,
	"cmd.config.err.provider_models_empty": `model.providers[{i}] ({provider}): models cannot be ` +
		`empty (the first one is the default request model)`,
	"cmd.config.err.model_name_empty": `model.providers[{i}] ({provider}): models[{j}].name cannot be empty`,
	"cmd.config.err.model_name_dup":   `model.providers[{i}] ({provider}): model "{name}" is duplicated`,
	"cmd.config.err.context_window_neg": `model.providers[{i}] ({provider}): model "{name}" has a ` +
		`negative context_window ({n}); leave it empty to omit it`,
	"cmd.config.err.cost_neg": `model.providers[{i}] ({provider}): model "{name}" has a ` +
		`negative cost_in / cost_out`,
	"cmd.config.err.no_model_enabled": `model.providers[{i}] ({provider}): no model is enabled; ` +
		`enable at least one (enabled: false only keeps the entry, it is not selectable)`,
	"cmd.config.err.primary_missing":      `model.primary "{name}" is not in model.providers`,
	"cmd.config.err.fallback_no_provider": `Every model.fallback entry needs a provider`,
	"cmd.config.err.fallback_missing":     `model.fallback "{provider}" is not in model.providers`,
	"cmd.config.err.fallback_primary": `model.fallback must not contain the active ` +
		`{provider}/{model} (that is the primary itself)`,
	"cmd.config.err.fallback_dup": `model.fallback has a duplicate entry "{key}"`,
	"cmd.config.key_label":        `key #{n}`,

	// config_delegate.go
	"cmd.config.err.delegate_timeout_neg": `delegate.timeout_sec cannot be negative ` +
		`(0 = unlimited): {n}`,
	"cmd.config.err.delegate_timeout_max": `delegate.timeout_sec exceeds the limit ` +
		`(<= {max} seconds): {n}`,
	"cmd.config.err.delegate_negative":        `delegate.max_concurrent / max_total cannot be negative`,
	"cmd.config.err.delegate_name_missing":    `delegate.agents[{i}]: missing name`,
	"cmd.config.err.delegate_name_dup":        `delegate.agents[{i}]: name "{name}" is duplicated`,
	"cmd.config.err.delegate_command_missing": `delegate.agents[{i}] ({name}): missing command`,
	"cmd.config.err.delegate_workdir": `delegate.agents[{i}] ({name}): workdir must be an ` +
		`absolute path: "{workdir}"`,
	"cmd.config.err.delegate_default_disabled": `delegate.agents[{i}] ({name}): the default agent ` +
		`cannot also be enabled=false`,
	"cmd.config.err.delegate_default_multi": `delegate.agents has more than one default ` +
		`({n} entries): only one is allowed`,

	// config_audio.go
	"cmd.config.err.input_language": `input.language="{lang}" supports only auto | zh | en`,
	"cmd.config.err.audio_kind":     `{name}.kind="{kind}" is not recognized (supported: {kinds})`,
	"cmd.config.err.audio_endpoint": `{name} uses the {kind} mode, so an endpoint is required ` +
		`(base_url or a referenced service)`,
	"cmd.config.err.audio_model_path": `{name}.model_path cannot be empty ` +
		`(local recognition needs a ggml model file)`,
	"cmd.config.err.audio_model": `{name}.model cannot be empty ` +
		`(a configured service needs the audio model name)`,
	"cmd.config.err.audio_unknown":      `Unknown audio channel "{which}"`,
	"cmd.config.err.audio_unconfigured": `No speech service configured`,

	// config_web.go
	"cmd.config.err.web_search_kind": `web.search.providers[{i}].kind="{kind}" is not recognized ` +
		`(search side supports {a} / {b})`,
	"cmd.config.err.web_fetch_kind": `web.fetch.providers[{i}].kind="{kind}" is not recognized ` +
		`(fetch side supports {a} / {b})`,
	"cmd.config.err.web_search_count": `web.search.count={n} is out of range ({min}-{max}; 0 = default 8)`,
	"cmd.config.err.web_fetch_chars": `web.fetch.max_chars={n} is out of range ({min}-{max}; ` +
		`0 = default 16000)`,
	"cmd.config.err.web_from_missing": `from="{from}" is not in model.providers ` +
		`(set base_url for a standalone setup, or reference an existing entry)`,

	// main.go: flag help
	"cmd.boot.flag.data": `Data directory (default ~/.aquarius)`,
	"cmd.boot.flag.profile": `Choose a profile (overrides the root config.json pointer for ` +
		`this run; the profile must exist)`,
	"cmd.boot.flag.model":    `Override model.name`,
	"cmd.boot.flag.base_url": `Override model.base_url`,
	"cmd.boot.flag.yes": `Skip each confirmation (answer y to every prompt; ` +
		`use with care on dangerous operations)`,
	"cmd.boot.flag.log_level": `Run log level (debug | info | warn | error; ` +
		`defaults to ui.log_level)`,
	"cmd.boot.flag.log_format": `Run log format (text | json; this run only)`,
	"cmd.boot.flag.version":    `Print the version and exit`,

	// main.go / bootstrap_profile.go
	"cmd.boot.banner": `Aquarius — type /help for commands, /quit to exit; ` +
		`Ctrl+C cancels the current generation`,
	"cmd.boot.interrupted_delegates": `{n} delegation(s) were interrupted at last exit ` +
		`(recorded in their sessions)`,
	"cmd.boot.steer_retry": `Interrupted this step; retrying with the steering message`,

	"cmd.boot.err.home_dir":        `Cannot determine the home directory`,
	"cmd.boot.err.mkdir_data":      `Creating data directory {path}`,
	"cmd.boot.err.mkdir_profile":   `Creating profile directory {path}`,
	"cmd.boot.err.mkdir_sandbox":   `Creating privileged directory {path}`,
	"cmd.boot.err.mkdir_skills":    `Creating skill directory {path}`,
	"cmd.boot.err.secret_store":    `Setting up the local secret store`,
	"cmd.boot.err.write_config":    `Failed to generate the config`,
	"cmd.boot.err.read_new_config": `Reading the freshly generated config {path}`,
	"cmd.boot.err.read_config":     `Failed to read the config`,
	"cmd.boot.err.ui_kind":         `ui.kind="{kind}" supports only repl | tui | gui`,

	// profiles.go / config_io.go
	"cmd.profile.err.empty":           `The profile name cannot be empty`,
	"cmd.profile.err.whitespace":      `The profile name cannot start or end with whitespace: "{name}"`,
	"cmd.profile.err.invalid":         `Invalid profile name: "{name}"`,
	"cmd.profile.err.charset":         `The profile name cannot contain / \ : and must be at most 64 characters: "{name}"`,
	"cmd.profile.err.control":         `The profile name cannot contain control characters: "{name}"`,
	"cmd.profile.err.exists":          `Profile "{name}" already exists`,
	"cmd.profile.err.mkdir":           `Creating profile directory`,
	"cmd.profile.err.read_current":    `Reading the current profile config`,
	"cmd.profile.err.already_current": `Already running on profile "{name}"`,
	"cmd.profile.err.not_exist":       `Profile "{name}" does not exist (create a new one or copy the current one first)`,
	"cmd.profile.err.write_pointer":   `Writing the profile pointer {path}`,
	"cmd.profile.err.read_pointer":    `Reading the profile pointer {path}`,
	"cmd.profile.err.not_found":       `--profile {name} does not exist (available under profiles/: {available})`,

	// assemble_ports.go / assemble_ui.go / small files
	"cmd.asm.err.no_provider":      `No model provider is configured yet. Add one under Settings → Model (name, endpoint, key, model) and the chat will work`,
	"cmd.asm.err.no_key":           `Provider "{provider}" has no usable key`,
	"cmd.asm.err.fallback_missing": `model.fallback references provider "{provider}", which does not exist`,
	"cmd.asm.key.rotated":          `Key {from} was rate limited or ran out of quota; using {to} this round`,
	"cmd.asm.key.exhausted":        `All keys of {provider} are cooling down; earliest recovery at {time}`,
	"cmd.asm.name.asr":             `Speech recognition`,
	"cmd.asm.name.tts":             `Speech synthesis`,
	"cmd.asm.err.skill_installer":  `Skill installer`,
	"cmd.asm.char.prefix":          `Character card: `,
	"cmd.asm.err.characters_dir":   `Character card directory`,
	"cmd.asm.notice.truncate": `Hard-limit truncation: context over budget, ` +
		`dropped the {n} oldest messages`,
	"cmd.asm.notice.fallback": `Model service {from} had a network error; ` +
		`fell back to {to} for this round`,

	"cmd.asm.err.voice_input_unwired": `Voice input is not wired up`,
	"cmd.asm.err.voice_preview_unconfigured": `Speech synthesis is not configured: ` +
		`pick a service and model on the Voice page (takes effect after restart)`,
	"cmd.asm.err.voice_player_unwired": `Voice playback is not wired up`,
	"cmd.asm.err.read_synth_audio":     `Reading the synthesized audio`,

	"cmd.asm.err.mkdir_memory":       `Creating memory directory`,
	"cmd.asm.err.create_memory_file": `Creating memory file {path}`,
	"cmd.asm.err.run_editor":         `Running editor {editor}`,

	"cmd.asm.err.list_sessions": `Listing sessions`,
	"cmd.asm.err.load_session":  `Loading session {id}`,

	"cmd.asm.err.plugin_index":  `[plugin] resource index failed`,
	"cmd.asm.err.plugin_search": `[plugin] resource search failed`,
	"cmd.asm.mcp.param_name":    `Arguments`,
	"cmd.asm.mcp.param_desc":    `Arguments for this prompt, in the order the server declares`,

	"cmd.asm.err.output_deliver": `Output adapter {name}`,
	"cmd.asm.err.start_process":  `Starting {cmd}`,

	"cmd.asm.err.write_back_config": `Write back config`,
	"cmd.asm.err.env_unset":         `Environment variable {name} is not set (the config's api_key refers to it)`,
}
