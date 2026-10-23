package main

// 设置窗装配根侧（§15.7 读写契约，D60/D61）：读 = config 文本键 + 运行态快照；
// 写 = 单一 patch 回调——文本键泛键改写（persistConfig 复用），权限/模型/思考/档位
// 给出内核命令计划交设置窗排队执行（与键入命令同路径：串行、回执进转写区——
// 壳内不旁路，热生效与 /permission /model /think /effort 同口径）。

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// textSettings 设置窗文本键与全量分组页数据（config 文件即事实源）。D110②：
// provider/base_url/api_key 编辑走 ProviderManager（providers 列表结构），不在
// 此处；MCPServers/Plugins 只读取名（结构化编辑走高级页 raw JSON，Q10）。
type textSettings struct {
	SystemPrompt string        `json:"system_prompt"`
	Prompts      promptsConfig `json:"prompts"`
	Output       outputConfig  `json:"output"`
	Input        inputConfig   `json:"input"` // D179：录音语种
	Audio        audioConfig   `json:"audio"` // D179：识别与合成
	Web          webConfig     `json:"web"`   // D200：联网搜索与抓取
	Limits       limitsConfig  `json:"limits"`
	// Model 采样参数（D204）：只读 model.sampling 这一段——provider/模型编辑走
	// ProviderManager，不在这里。
	Model struct {
		Sampling samplingConfig `json:"sampling"`
	} `json:"model"`
	MCPServers map[string]struct {
		Transport string `json:"transport"`
	} `json:"mcpServers"`
	Plugins map[string]struct {
		Enabled *bool `json:"enabled"`
	} `json:"plugins"`
	UI struct {
		Hotkey       string  `json:"hotkey"`
		Theme        string  `json:"theme"`
		Scale        float64 `json:"scale"`         // D90：元素缩放倍率（0 = 缺省 1.0）
		FontSize     float64 `json:"font_size"`     // D90：正文字号 sp（0 = 缺省 15）
		WindowWidth  int     `json:"window_width"`  // D90：主窗像素宽（0 = 缺省）
		WindowHeight int     `json:"window_height"` // D90：主窗像素高
	} `json:"ui"`
}

// readTextSettings 读 config 的设置窗文本键与全量分组页数据（运行态四键由调用方
// 补；读失败/解析失败返回零值——快照容错不阻断开窗，Q10；RawJSON 原文照带）。
func readTextSettings(cfgPath string) uigui.SettingsSnapshot {
	var raw textSettings
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return uigui.SettingsSnapshot{}
	}
	_ = json.Unmarshal(data, &raw) // 解析失败 = 快照零值 + 原文照呈（高级页手工修）
	snap := uigui.SettingsSnapshot{
		SystemPrompt:     raw.SystemPrompt,
		PromptModules:    promptViewsOf(raw.Prompts),
		PromptPersona:    append([]string(nil), raw.Prompts.Persona...),
		Builtins:         builtinModuleViews(),
		Notify:           raw.Output.Notify,
		TTS:              raw.Output.TTS,
		MaxTurns:         raw.Limits.MaxTurns,
		MaxContextTokens: raw.Limits.MaxContextTokens,
		CompactThreshold: raw.Limits.CompactThreshold,
		ToolOutputChars:  raw.Limits.ToolOutputChars,
		ToolTimeoutSec:   raw.Limits.ToolTimeoutSec,
		Hotkey:           raw.UI.Hotkey,
		Theme:            raw.UI.Theme,
		Scale:            raw.UI.Scale,
		FontSize:         raw.UI.FontSize,
		WindowWidth:      raw.UI.WindowWidth,
		WindowHeight:     raw.UI.WindowHeight,
		RawJSON:          string(data),
		// 语音（D179）：文件即事实源（服务/模型改动重启生效，与快照口径一致）。
		ASRProvider: raw.Audio.ASR.Provider,
		ASRModel:    raw.Audio.ASR.Model,
		ASRLang:     raw.Input.Language,
		TTSProvider: raw.Audio.TTS.Provider,
		TTSModel:    raw.Audio.TTS.Model,
		TTSVoice:    raw.Audio.TTS.Voice,
		TTSDesign:   raw.Audio.TTS.Design,
		TTSStyle:    raw.Audio.TTS.Style,
		TTSTags:     raw.Audio.TTS.Tags,
		// 联网（D200）：提供商列表与本地抓取参数，文件即事实源。
		WebSearchProviders: webProviderViewsOf(raw.Web.Search.Providers),
		WebSearchCount:     raw.Web.Search.Count,
		WebFetchProviders:  webProviderViewsOf(raw.Web.Fetch.Providers),
		WebFetchMaxChars:   raw.Web.Fetch.MaxChars,
		WebFetchProxy:      raw.Web.Fetch.Proxy,
		WebFetchUserAgent:  raw.Web.Fetch.UserAgent,
		WebFetchRobots:     raw.Web.Fetch.RespectRobots == nil || *raw.Web.Fetch.RespectRobots,
		WebFetchPrivate:    raw.Web.Fetch.AllowPrivateIPs,
		WebFetchTimeoutSec: raw.Web.Fetch.TimeoutSec,
		// 采样参数（D204）：整链缺省；空串 = 该项不发送（与写 0 区分）。
		SamplingTemperature: numText(raw.Model.Sampling.Temperature),
		SamplingTopP:        numText(raw.Model.Sampling.TopP),
		SamplingMaxTokens:   intText(raw.Model.Sampling.MaxTokens),
		SamplingStop:        stopsText(raw.Model.Sampling.Stop),
	}
	for name := range raw.MCPServers {
		snap.MCPServers = append(snap.MCPServers, name)
	}
	for name := range raw.Plugins {
		snap.Plugins = append(snap.Plugins, name)
	}
	sort.Strings(snap.MCPServers)
	sort.Strings(snap.Plugins)
	return snap
}

// persistSettingsTextKeys 无内核命令覆盖的键一次泛键改写（ui.theme/hotkey + D90 三
// 旋钮 scale/font_size/window_width/window_height。D110②：provider/base_url/api_key
// 由 ProviderManager.Apply 全量写回，不在此列。有覆盖的键由命令各自写回，cfgWriteMu
// 串行防丢更新）。
// theme 先归一校验（"" → system），非法值拒写防落盘；旋钮数值原样落盘（UI 侧
// clampWindowPx/clampKnobs 统一夹取，config 侧不重复校验）。
func persistSettingsTextKeys(cfgPath string, p uigui.SettingsPatch) error {
	switch p.Theme {
	case "":
		p.Theme = "system"
	case "system", "light", "dark":
	default:
		return fmt.Errorf("非法主题档 %q（须为 system|light|dark）", p.Theme)
	}
	if err := validatePromptModules(p.PromptModules); err != nil {
		return err
	}
	// 降级链（S11-7①/D176）：仅当 patch 带链（nil = 本条路径不管链）时才校验与写回；
	// 高级页整文件替换时跳过校验（那时 facts 来自用户手写的文件本身）。
	if p.Fallback != nil && p.RawJSON == "" {
		if err := validateFallbackChain(cfgPath, p.Fallback); err != nil {
			return err
		}
	}
	// 语音（D179）：引用的 provider 必须存在、配了服务就要有模型名，语种只认三档。
	// 高级页整文件替换时跳过（与降级链同口径：那一份配置以用户手写的文件为准）。
	if p.RawJSON == "" {
		if err := validateAudioSettings(cfgPath, p); err != nil {
			return err
		}
		// 联网（D200）：kind / 借用的对话 provider / 数值范围，与启动期 normalizeWeb 同口径。
		if err := validateWebSettings(cfgPath, p); err != nil {
			return err
		}
	}
	if p.RawJSON != "" {
		// 高级页（Q10）：整文件替换——JSON 对象校验后原子写（cfgWriteMu 串行与其他
		// 写回互斥）；原文即最终态，未知键天然保留。后续 mutate 在新文件上照常叠加
		// 本 patch 的其余键（读-改-写重新读盘，口径一致）。
		if err := writeRawConfig(cfgPath, p.RawJSON); err != nil {
			return err
		}
	}
	// 采样参数（D204）：只有设置窗整段提交时才管这一段（窄写回不碰采样）。写前跑一遍
	// 启动期同一条 normalize——落盘的配置必须能过启动校验，否则重启后连对话都起不来。
	var sampling samplingConfig
	if p.SamplingSet && p.RawJSON == "" {
		var err error
		if sampling, err = formSampling(p); err != nil {
			return err
		}
		if err := validateSamplingSettings(cfgPath, sampling); err != nil {
			return err
		}
	}
	return persistConfig(cfgPath, func(generic map[string]any) {
		uiSec, _ := generic["ui"].(map[string]any)
		if uiSec == nil {
			uiSec = map[string]any{}
			generic["ui"] = uiSec
		}
		uiSec["theme"] = p.Theme
		uiSec["hotkey"] = p.Hotkey
		uiSec["scale"] = p.Scale
		uiSec["font_size"] = p.FontSize
		uiSec["window_width"] = p.WindowWidth
		uiSec["window_height"] = p.WindowHeight
		// 输出（布尔无留空语义，恒写）。
		outSec, _ := generic["output"].(map[string]any)
		if outSec == nil {
			outSec = map[string]any{}
			generic["output"] = outSec
		}
		outSec["notify"] = p.Notify
		outSec["tts"] = p.TTS
		// 语音（D179，恒全量写）：audio.asr / audio.tts 各自引用 provider；kind 不在此
		// 管理（高级页手写的值原样保留），voice/design/style/tags 清空 = 回到缺省。
		audioSec, _ := generic["audio"].(map[string]any)
		if audioSec == nil {
			audioSec = map[string]any{}
			generic["audio"] = audioSec
		}
		setChannel := func(name string, ch map[string]any) {
			sec, _ := audioSec[name].(map[string]any)
			if sec == nil {
				sec = map[string]any{}
				audioSec[name] = sec
			}
			for k, v := range ch {
				sec[k] = v
			}
		}
		setChannel("asr", map[string]any{
			"provider": p.ASRProvider,
			"model":    p.ASRModel,
		})
		setChannel("tts", map[string]any{
			"provider": p.TTSProvider,
			"model":    p.TTSModel,
			"voice":    p.TTSVoice,
			"design":   p.TTSDesign,
			"style":    p.TTSStyle,
			"tags":     p.TTSTags,
		})
		inSec, _ := generic["input"].(map[string]any)
		if inSec == nil {
			inSec = map[string]any{}
			generic["input"] = inSec
		}
		lang := strings.TrimSpace(p.ASRLang)
		if lang == "" {
			lang = "auto"
		}
		inSec["language"] = lang
		// 限额（数值 0 = 不改，表单留空口径）。
		limSec, _ := generic["limits"].(map[string]any)
		if limSec == nil {
			limSec = map[string]any{}
			generic["limits"] = limSec
		}
		if p.MaxTurns > 0 {
			limSec["max_turns"] = p.MaxTurns
		}
		if p.MaxContextTokens > 0 {
			limSec["max_context_tokens"] = p.MaxContextTokens
		}
		if p.CompactThreshold > 0 {
			limSec["compact_threshold"] = p.CompactThreshold
		}
		if p.ToolOutputChars > 0 {
			limSec["tool_output_chars"] = p.ToolOutputChars
		}
		if p.ToolTimeoutSec > 0 {
			limSec["tool_timeout_sec"] = p.ToolTimeoutSec
		}
		// 人格（恒写：清空 = 合法语义「内置默认」，D20）。
		generic["system_prompt"] = p.SystemPrompt
		// 人格模块树（S7/D144，恒全量写）：modules = name → {text?, children?[]}，persona = 有序引用。
		pSec, _ := generic["prompts"].(map[string]any)
		if pSec == nil {
			pSec = map[string]any{}
			generic["prompts"] = pSec
		}
		mods := map[string]any{}
		for _, m := range p.PromptModules {
			name := strings.TrimSpace(m.Name)
			if name == "" {
				continue // 未填名的新增行 = 尚未创建
			}
			node := map[string]any{}
			if text := strings.TrimSpace(m.Text); text != "" {
				node["text"] = m.Text
			}
			if children := cleanRefs(m.Children); len(children) > 0 {
				node["children"] = children
			}
			mods[name] = node
		}
		pSec["modules"] = mods
		pSec["persona"] = cleanRefs(p.PromptPersona)
		// 降级链（S11-7①/D176 口径三）：恒全量写（空 = 清空）；链条目 {provider, model?}。
		if p.Fallback != nil {
			fbs := make([]any, 0, len(p.Fallback))
			for _, l := range p.Fallback {
				entry := map[string]any{"provider": strings.TrimSpace(l.Provider)}
				if m := strings.TrimSpace(l.Model); m != "" {
					entry["model"] = m
				}
				fbs = append(fbs, entry)
			}
			model := modelSection(generic)
			model["fallback"] = fbs
		}
		// 联网（D200）：提供商列表恒全量写（nil = 这条路径不管联网）；本地抓取参数
		// 数值 0 = 不改、布尔恒写。
		writeWebSection(generic, p)
		// 采样参数（D204）：整段替换（空 = 删掉该段 = 全不发送）。
		if p.SamplingSet {
			writeSamplingSection(generic, sampling)
		}
	})
}

// validateFallbackChain 降级链校验（S11-7①/D176）：provider 名不得为空、必须存在于
// config 的 providers 列表；二元组不得重复；不得指向当前生效的 primary 当前模型。
// 与启动期 modelConfig.normalize 同口径，只是运行在这里以「设置窗保存前」拦截
// （否则落盘即进入一份启动会被拒的配置）。
func validateFallbackChain(cfgPath string, links []uigui.FallbackLink) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range cfg.Model.Providers {
		known[p.Name] = true
	}
	curModel := cfg.Model.Name
	if curModel == "" {
		if pp := cfg.Model.primaryProvider(); pp != nil {
			curModel = pp.models0()
		}
	}
	seen := map[string]bool{}
	for _, l := range links {
		prov := strings.TrimSpace(l.Provider)
		model := strings.TrimSpace(l.Model)
		if prov == "" {
			return fmt.Errorf("降级链每项都要选提供者")
		}
		if !known[prov] {
			return fmt.Errorf("降级链的提供者 %q 不在 provider 列表内（先在「模型服务」页添加或改名）", prov)
		}
		if prov == cfg.Model.Primary && (model == "" || model == curModel) {
			return fmt.Errorf("降级链不应含当前生效的 %s/%s（那就是主模型本身）", prov, curModel)
		}
		key := prov + "/" + model
		if seen[key] {
			return fmt.Errorf("降级链重复项 %q", key)
		}
		seen[key] = true
	}
	return nil
}

// validateAudioSettings 语音配置保存前校验（D179）：provider 可留空（= 关闭该能力），
// 填了就必须存在于 providers 列表；填了 provider 就必须有模型名；语种只认三档。
// 与 normalizeAudio 同口径，只是提前到落盘前——否则写进去的是一份启动会拒的配置。
func validateAudioSettings(cfgPath string, p uigui.SettingsPatch) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, pc := range cfg.Model.Providers {
		known[pc.Name] = true
	}
	check := func(label, prov, model string) error {
		prov = strings.TrimSpace(prov)
		model = strings.TrimSpace(model)
		if prov == "" {
			return nil
		}
		if !known[prov] {
			return fmt.Errorf("%s引用的服务 %q 不在 provider 列表内（先在「模型服务」页添加）", label, prov)
		}
		if model == "" {
			return fmt.Errorf("%s选了服务就要填模型名", label)
		}
		return nil
	}
	if err := check("语音识别", p.ASRProvider, p.ASRModel); err != nil {
		return err
	}
	if err := check("语音合成", p.TTSProvider, p.TTSModel); err != nil {
		return err
	}
	switch strings.TrimSpace(p.ASRLang) {
	case "", "auto", "zh", "en":
	default:
		return fmt.Errorf("录音语种只能是自动识别、中文或英文")
	}
	return nil
}

// fallbackLinksOf 降级链配置 → 设置窗快照项（model 空 = 该 provider 的缺省模型）。
func fallbackLinksOf(fbs []fallbackEntry) []uigui.FallbackLink {
	out := make([]uigui.FallbackLink, 0, len(fbs))
	for _, fb := range fbs {
		out = append(out, uigui.FallbackLink{Provider: fb.Provider, Model: fb.Model})
	}
	return out
}

// writeRawConfig 高级页整文件替换（S4/D110③/Q10）：JSON 对象校验通过才落盘，
// 解析失败报因且原文件不动。
func writeRawConfig(cfgPath, raw string) error {
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return fmt.Errorf("JSON 解析失败（原文件未动）: %w", err)
	}
	cfgWriteMu.Lock()
	defer cfgWriteMu.Unlock()
	if err := atomicfile.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		return fmt.Errorf("写入 %s: %w", cfgPath, err)
	}
	return nil
}

// settingsCommands 内核命令计划：patch 与运行态快照 diff 才发命令（无变更不进
// 转写区）。空值语义：空模型名 = 保持当前不发；空档位 = 清除（/effort off，D34）。
func settingsCommands(cur uigui.SettingsSnapshot, p uigui.SettingsPatch) []port.Command {
	var cmds []port.Command
	if p.Permission != "" && p.Permission != cur.Permission {
		cmds = append(cmds, port.Command{Name: "permission", Args: []string{p.Permission}})
	}
	if p.Unattended != cur.Unattended {
		// D185：开关是布尔，命令面要显式档位（无参 = 切换，设置窗必须能表达「设成这个值」）。
		on := "off"
		if p.Unattended {
			on = "on"
		}
		cmds = append(cmds, port.Command{Name: "unattended", Args: []string{on}})
	}
	if p.Model != "" && p.Model != cur.Model {
		cmds = append(cmds, port.Command{Name: "model", Args: []string{p.Model}})
	}
	if p.Think != cur.Think {
		on := "off"
		if p.Think {
			on = "on"
		}
		cmds = append(cmds, port.Command{Name: "think", Args: []string{on}})
	}
	if p.Effort != cur.Effort {
		e := p.Effort
		if e == "" {
			e = "off"
		}
		cmds = append(cmds, port.Command{Name: "effort", Args: []string{e}})
	}
	return cmds
}
