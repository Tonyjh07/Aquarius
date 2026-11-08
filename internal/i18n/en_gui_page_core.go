package i18n

// enGuiPageCoreTexts 与 zhGuiPageCoreTexts 同 key 同顺序（同构测试盯着）。
var enGuiPageCoreTexts = map[string]string{

	// General page: sections (the "Profile" section title is a config term, left in code)
	"ui.page.general.section.profile":    "Profile",
	"ui.page.general.section.perm":       "Permissions",
	"ui.page.general.section.appearance": "Appearance & summon",
	"ui.page.general.section.output":     "Output",
	"ui.page.general.section.storage":    "Storage",

	"ui.page.general.prof_current_label": "Current",
	"ui.page.general.prof_current_avail": " (available: {names})",
	"ui.page.general.prof_name_label":    "Profile name",
	"ui.page.general.prof_name_hint":     "target name for create / copy / switch",
	"ui.page.general.prof_create":        "New",
	"ui.page.general.prof_copy":          "Copy current",
	"ui.page.general.prof_switch":        "Switch (restart to apply)",
	"ui.page.general.welcome_label":      "Welcome guide",
	"ui.page.general.welcome_open":       "Open",
	"ui.page.general.welcome_hint":       "model setup wizard + hotkey / context menu / common commands",

	// General page: permissions and appearance
	"ui.page.general.perm_label":        "Permission level",
	"ui.page.general.unattended_label":  "Unattended",
	"ui.page.general.unattended_check":  "reject out-of-scope actions on the spot, without waiting for confirmation",
	"ui.page.general.unattended_hint":   "permission level unchanged; confirmations for the commands you type yourself still apply",
	"ui.page.general.hotkey_label":      "Global hotkey",
	"ui.page.general.hotkey_hint":       "e.g. Alt+A (empty = default)",
	"ui.page.general.theme_label":       "Theme",
	"ui.page.general.lang_label":        "Interface language",
	"ui.page.general.lang_hint":         "takes effect after restart; the AQUARIUS_UI_LANG environment variable takes precedence",
	"ui.page.general.placeholder_label": "Input placeholder",
	"ui.page.general.placeholder_hint":  "empty = built-in hint (follows the UI language); up to {max} characters",
	"ui.page.general.lang_auto":         "Follow system",
	"ui.page.general.scale_label":       "Element scale",
	"ui.page.general.font_size_label":   "Font size",
	"ui.page.general.font_size_hint":    "body font size {min}–{max} (final = size × scale)",
	"ui.page.general.win_size_label":    "Window size",
	"ui.page.general.notify_check":      "desktop notification (when the answer is done)",
	"ui.page.general.tts_check":         "speak the answer (plays automatically when done)",
	"ui.page.general.backup_keep_label": "Backup generations",
	"ui.page.general.backup_keep_hint":  "backups kept for the session tree: {min}–{max} (empty = default 3)",

	// Model page: current model, thinking and reasoning
	"ui.page.model.section.current":  "Current model",
	"ui.page.model.section.thinking": "Thinking & reasoning",
	"ui.page.model.section.sampling": "Sampling (empty = server default)",
	"ui.page.model.section.chain":    "Fallback chain",

	"ui.page.model.model_name_label": "Current model name",
	"ui.page.model.model_name_hint":  "model.name; saved changes hot-switch through /model",
	"ui.page.model.think_check":      "native thinking (/think master switch; the reasoning effort is ignored when off)",
	"ui.page.model.effort_label":     "Reasoning effort",

	// Model page: sampling (top_p is a config key, left in code)
	"ui.page.model.temp_label":    "Temperature",
	"ui.page.model.temp_hint":     "{min}–{max}; lower is steadier, higher is more varied. Empty = server default, 0 = always take the most likely token",
	"ui.page.model.topp_hint":     "{min}–{max}; only pick from candidates whose cumulative probability reaches this share. Empty = server default",
	"ui.page.model.max_label":     "Output limit",
	"ui.page.model.max_hint":      "tokens; the most to generate in one turn. Empty = not sent (still bounded by this turn's budget)",
	"ui.page.model.stop_label":    "Stop sequences",
	"ui.page.model.stop_hint":     "JSON array, e.g. [\"\\n\", \"END\"]; generation stops at these strings. Empty = not sent",
	"ui.page.model.sampling_note": "Empty = the field is not sent and the server falls back to its own default; 0 = you explicitly want 0 (temperature 0 always takes the most likely token). This is the default for the whole chain; to change a single model, add sampling to that model in the config file.",

	// Model page: fallback chain
	"ui.page.model.prov_label":  "Provider",
	"ui.page.model.prov_picker": "Fallback provider",
	"ui.page.model.model_label": "Model",
	"ui.page.model.model_hint":  "empty = the provider's default model; undeclared names are allowed",
	"ui.page.model.chain_add":   "+ Add fallback",

	// Model services page: basic card fields ("Tokenizer" is a term, left in code)
	"ui.page.providers.section.list": "Model services (providers)",
	"ui.page.providers.name_label":   "Name",
	"ui.page.providers.name_hint":    "unique provider id (primary/fallback refer to it by name)",
	"ui.page.providers.kind_label":   "Protocol",
	"ui.page.providers.base_label":   "Endpoint",
	"ui.page.providers.base_hint":    "e.g. https://api.deepseek.com/v1",

	// Model services page: one line per protocol option
	"ui.page.providers.hint.kind.openai_responses": "OpenAI Responses API (/responses)",
	"ui.page.providers.hint.kind.anthropic":        "Anthropic Messages API (/messages)",
	"ui.page.providers.hint.kind.openai":           "OpenAI-compatible /chat/completions",

	"ui.page.providers.tokenizer_hint":    "path to a local tokenizer.json; empty = estimate",
	"ui.page.providers.unsupported_label": "Unsupported params",
	"ui.page.providers.unsupported_hint":  "comma-separated; these parameters are stripped and never sent",

	// Model services page: key pool
	"ui.page.providers.head.keys":      "Key pool",
	"ui.page.providers.key_add":        "+ Add key",
	"ui.page.providers.key_value_hint": "empty = keep; plaintext moves into the local key store; a secret: prefix is stored as a reference",
	"ui.page.providers.key_label_hint": "display name (empty = key N)",

	// Model services page: read-side state of one slot (values are never shown)
	"ui.page.providers.keyslot.saved": "current reference: {ref} (the value lives in the key store, never shown)",
	"ui.page.providers.keyslot.plain": "currently plaintext (a warning is logged on restart; enter it once to move it into the key store)",
	"ui.page.providers.keyslot.empty": "no value stored (falls back to the default reference)",

	// Model services page: model list and pager
	"ui.page.providers.models_head":         "Model list",
	"ui.page.providers.models_count":        "{total} total · {on} enabled",
	"ui.page.providers.models_fetch":        "Fetch models",
	"ui.page.providers.pager_hint":          "page {page}/{pages}",
	"ui.page.providers.pager_prev":          "‹ Prev",
	"ui.page.providers.pager_next":          "Next ›",
	"ui.page.providers.model_name_hint":     "model name (server-side id)",
	"ui.page.providers.model_ctx_hint":      "context tokens",
	"ui.page.providers.model_cost_in_hint":  "input $/M",
	"ui.page.providers.model_cost_out_hint": "output $/M",
	"ui.page.providers.model_add":           "+ Add model",
	"ui.page.providers.probe":               "Probe capabilities",

	// Model services page: card actions and the apply row
	"ui.page.providers.test":          "Test connection",
	"ui.page.providers.set_primary":   "Set as primary",
	"ui.page.providers.primary_badge": "★ Primary",
	"ui.page.providers.prov_add":      "+ New provider",
	"ui.page.providers.apply":         "Apply provider changes",
	"ui.page.providers.apply_note":    "Changes are written back to config in full and take effect after restart; Test connection and Probe capabilities send real requests.",

	// Web page: three sections and their lists
	"ui.page.web.section.search": "Search (tried in order, next one on failure)",
	"ui.page.web.section.fetch":  "Web fetch (tried in order, next one on failure)",
	"ui.page.web.section.local":  "Local fetch (reads the page text directly, no third party)",

	"ui.page.web.search_add":  "+ Add search provider",
	"ui.page.web.count_label": "Result count",
	"ui.page.web.count_hint":  "default 8 ({min}–{max}); tool arguments can override it per call (empty = keep)",
	"ui.page.web.fetch_add":   "+ Add fetch provider",
	"ui.page.web.chars_label": "Max characters",
	"ui.page.web.chars_hint":  "characters; default 16000 ({min}–{max}) (empty = keep)",

	// Web page: network and safety settings for local fetch
	"ui.page.web.proxy_label":   "Proxy",
	"ui.page.web.proxy_hint":    "e.g. http://127.0.0.1:7897; empty = read environment variables",
	"ui.page.web.ua_hint":       "empty = browser-like default (many sites reject non-browser UAs)",
	"ui.page.web.robots_check":  "respect robots.txt (skip paths the site declares off-limits)",
	"ui.page.web.private_check": "allow local and intranet addresses (only for debugging local services)",
	"ui.page.web.timeout_label": "Timeout",
	"ui.page.web.timeout_hint":  "seconds; default 30 (empty = keep)",
	"ui.page.web.note":          "Both lists are ordered: the first entry is tried first, the next one on failure. You can fill in the endpoint and key yourself, or pick one to borrow and press Fill — that copies the endpoint and key reference from the matching Model services entry, and you can still change them afterwards. Keys are write-only and never shown (empty = keep). Local fetch needs no key, but it refuses intranet and loopback addresses. Provider and safety changes take effect after saving and restarting.",

	// Web page: one line per kind
	"ui.page.web.hint.kind.deepseek": "fill in endpoint and key yourself, or borrow from Model services",
	"ui.page.web.hint.kind.brave":    "requires a subscription token",
	"ui.page.web.hint.kind.local":    "fetches locally, no key needed",

	// Web page: fields of one provider
	"ui.page.web.from_none":          "Don't borrow",
	"ui.page.web.kind_label":         "Type",
	"ui.page.web.kind_picker.search": "Search type",
	"ui.page.web.kind_picker.fetch":  "Fetch type",
	"ui.page.web.endpoint_label":     "Endpoint",
	"ui.page.web.endpoint_hint":      "e.g. https://api.deepseek.com; empty = use the borrowed entry's",
	"ui.page.web.from_label":         "Borrow",
	"ui.page.web.from_picker":        "Borrow from",
	"ui.page.web.fill":               "Fill",
	"ui.page.web.fill_hint":          "copy its endpoint and key reference into this entry; you can still edit them",
	"ui.page.web.key_label":          "Key",
	"ui.page.web.key_hint":           "secret:name or plaintext; empty = keep / borrow",
	"ui.page.web.country_lang_label": "Country / language",
	"ui.page.web.country_hint":       "e.g. CN (empty = server default)",
	"ui.page.web.lang_hint":          "e.g. zh-hans (empty = server default)",
	"ui.page.web.item_count_label":   "Count for this entry",
	"ui.page.web.item_count_hint":    "empty = use the result count above",

	// Web page: key state note under the key box (values are never shown)
	"ui.page.web.keynote.saved":    "stored reference {ref}; empty = keep unchanged",
	"ui.page.web.keynote.plain":    "currently plaintext (warned at startup); enter a new value to overwrite, empty = keep unchanged",
	"ui.page.web.keynote.fallback": "empty = use the borrowed entry's; if there is none, fall back by type: brave uses AQUARIUS_BRAVE_API_KEY, deepseek search uses AQUARIUS_DEEPSEEK_SEARCH_API_KEY, deepseek fetch uses AQUARIUS_DEEPSEEK_FETCH_API_KEY",

	// Audio page: sections and methods
	"ui.page.audio.section.asr":        "Speech recognition (speech to text)",
	"ui.page.audio.section.tts":        "Speech synthesis (text to speech)",
	"ui.page.audio.asr_kind_label":     "Recognition method",
	"ui.page.audio.tts_kind_label":     "Synthesis method",
	"ui.page.audio.asr_service_picker": "Recognition service",
	"ui.page.audio.tts_service_picker": "Synthesis service",
	"ui.page.audio.service_label":      "Service",
	"ui.page.audio.fill":               "Fill",
	"ui.page.audio.fill_hint":          "copy its endpoint and key reference into this page; you can still edit them",

	// Audio page: endpoint, model, language
	"ui.page.audio.endpoint_label":  "Endpoint",
	"ui.page.audio.endpoint_hint":   "e.g. https://api.example.com/v1; empty = use the selected service's",
	"ui.page.audio.asr_model_label": "Recognition model",
	"ui.page.audio.asr_model_hint":  "e.g. mimo-v2.5-asr / whisper-1",
	"ui.page.audio.asr_lang_label":  "Recording language",
	"ui.page.audio.tts_model_label": "Synthesis model",
	"ui.page.audio.tts_model_hint":  "e.g. mimo-v2.5-tts / -voicedesign / -voiceclone",

	// Audio page: voice and style
	"ui.page.audio.voice_label":  "Voice",
	"ui.page.audio.voice_hint":   "preset voice name; for cloning models, the absolute path of a sample file",
	"ui.page.audio.design_label": "Voice description",
	"ui.page.audio.design_hint":  "required for voice design models (e.g. a gentle female voice)",
	"ui.page.audio.style_label":  "Style instruction",
	"ui.page.audio.style_hint":   "plain language (e.g. a bit faster, relaxed tone)",
	"ui.page.audio.tags_label":   "Style tags",
	"ui.page.audio.tags_hint":    "e.g. gentle slightly faster (appended in front of the text)",
	"ui.page.audio.note":         "Recognition and synthesis are two complete configurations: pick a method (protocol) first, then fill in the fields this page shows — local whisper takes only a model file, every other method also has a service and an endpoint. For the methods that need an endpoint or key you can fill them in yourself, or pick a Service and press Fill: that copies the entry's endpoint and key reference, and you can still change them afterwards. Keys are write-only and never shown (empty = keep). Changing the method, service, endpoint or model requires saving and restarting. Voice, voice description and style take effect for later speech once saved; the preview synthesizes one sentence from what is currently filled in.",

	// Audio page: the local whisper model file (only that method uses it)
	"ui.page.audio.model_path_label": "Model file",
	"ui.page.audio.model_path_hint":  "absolute path of the local whisper ggml model (e.g. …\\ggml-base-q5_1.bin)",
	"ui.page.audio.model_path_note":  "The model is not in the repo, download it yourself: get ggml-base-q5_1.bin from huggingface.co/ggerganov/whisper.cpp (about 60MB, a good balance of Chinese quality and speed). Used only by the local whisper method.",

	// Audio page: keys and their state notes (one fallback line per channel)
	"ui.page.audio.key_label":            "Key",
	"ui.page.audio.key_hint":             "secret:name or plaintext; empty = keep / borrow",
	"ui.page.audio.keynote.saved":        "stored reference {ref}; empty = keep unchanged",
	"ui.page.audio.keynote.plain":        "currently plaintext (warned at startup); enter a new value to overwrite, empty = keep unchanged",
	"ui.page.audio.keynote.fallback_asr": "empty = borrow the selected service's key; with no service selected it falls back to secret:AQUARIUS_ASR_API_KEY",
	"ui.page.audio.keynote.fallback_tts": "empty = borrow the selected service's key; with no service selected it falls back to secret:AQUARIUS_TTS_API_KEY",

	// Audio page: preview
	"ui.page.audio.preview_label":         "Preview",
	"ui.page.audio.preview_play":          "Play sample",
	"ui.page.audio.preview_busy":          "Synthesizing…",
	"ui.page.audio.preview_text":          "Hello, this is Aquarius. How does this voice sound?",
	"ui.page.audio.status.synthesizing":   "Synthesizing…",
	"ui.page.audio.status.playing":        "Playback started",
	"ui.page.audio.status.preview_failed": "Preview failed: {err}",
	"ui.page.audio.status.unavailable":    "Preview unavailable: speech synthesis is not configured",
}
