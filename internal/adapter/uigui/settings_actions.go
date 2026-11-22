package uigui

// 本文件只留核心档的保存流（保存 / 提交 / 结果行 / 新快照）与 patch 采集；
// 采集用的判等与换算见 settings_fields.go，配置侧的管理动作见 settings_admin_actions.go。

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// saveSettings 保存流（渲染外的可 headless 测段）：采集表单成 patch（采集时校验，非法
// 就地拦下、不触达写回调）→ 提交编排（写回调、命令排队、GUI 侧热生效、反馈文案）。
//
// 两段分开是因为这两段改的东西不同：`patch` 只读表单与快照、不产生副作用，可以单独测；
// 提交那一段才碰通道与原子槽。原先混在一个 271 行的函数里，校验与热生效交替出现，
// 想确认「非法输入到底有没有写入磁盘」得把整段读完。
func (u *UI) saveSettings(f *settingsForm) {
	if f.readOnly || u.opts.ApplySettings == nil {
		f.err = true
		f.status = i18n.T("ui.settings.act.not_wired")
		return
	}
	p, err := f.patch()
	if err != nil {
		f.err = true
		f.status = err.Error()
		return
	}
	u.commitSettings(f, p)
}

// commitSettings 提交已校验的 patch：写回调（装配根持久化文本键并返回内核命令计划）→
// 命令经 inCh 排队（与键入同路径串行执行、回执进转写区；队列 256 深、主循环持续排空，
// 阻塞投递不致死锁）→ 主题/快捷键/缩放/窗口 GUI 侧热生效 → 反馈文案写回表单 → 快照
// 基准跟着走（再存一次不会因为「以为没改」把用户刚填的值丢掉）。
func (u *UI) commitSettings(f *settingsForm, p SettingsPatch) {
	cmds, err := u.opts.ApplySettings(p)
	if err != nil {
		f.err = true
		f.status = i18n.Tf("ui.settings.act.save_failed", "err", err.Error())
		return
	}
	for i := range cmds {
		c := cmds[i]
		u.inCh <- port.UserInput{Command: &c}
	}
	if p.Theme != f.snap.Theme {
		_ = u.post(themeMsg{mode: p.Theme}) // 主窗主题热生效（事件循环串行，§15.4）
	}
	if p.Hotkey != f.snap.Hotkey {
		u.setHotkey(p.Hotkey) // 原子槽 + 托盘线程重注册（失败回退 Ctrl+Alt+A，§15.1）
	}
	if p.Scale != f.snap.Scale || p.FontSize != f.snap.FontSize {
		u.SetZoom(p.Scale, p.FontSize) // D90：下帧 Metric 咽喉点生效（原子换存）
	}
	if p.WindowWidth != f.snap.WindowWidth || p.WindowHeight != f.snap.WindowHeight {
		u.SetWindowSize(p.WindowWidth, p.WindowHeight) // D90：窗口线程 SetWindowPos（headless no-op）
	}
	f.err = false
	f.status = savedStatus(p, f.snap, f.snapChain)
	f.snap = nextSnapshot(p, f.snap)
	f.snapChain = append([]FallbackLink(nil), p.Fallback...)
}

// savedStatus 保存成功的反馈文案：基础句 + 按「哪些改动要重启才生效」追加后缀。
// 多条都命中时后者覆盖前者——那正是重排后的语义（越靠后越晚生效），用户不必读四句。
func savedStatus(p SettingsPatch, snap SettingsSnapshot, snapChain []FallbackLink) string {
	status := i18n.T("ui.settings.act.saved")
	if p.Model != snap.Model {
		status = i18n.T("ui.settings.act.saved_model")
	}
	if p.ASRProvider != snap.ASRProvider || p.ASRModel != snap.ASRModel ||
		p.ASRBaseURL != snap.ASRBaseURL ||
		p.TTSProvider != snap.TTSProvider || p.TTSModel != snap.TTSModel ||
		p.TTSBaseURL != snap.TTSBaseURL || p.ASRKey != "" || p.TTSKey != "" {
		status = i18n.T("ui.settings.act.saved_audio")
	}
	if p.RawJSON != "" {
		status = i18n.T("ui.settings.act.saved_raw")
	}
	if !promptModulesEqual(snap.PromptModules, p.PromptModules) || !slices.Equal(snap.PromptPersona, p.PromptPersona) {
		status = i18n.T("ui.settings.act.saved_prompt")
	}
	if !slices.Equal(p.Fallback, snapChain) {
		status = i18n.T("ui.settings.act.saved_fallback")
	}
	if !webProvidersEqual(p.WebSearchProviders, snap.WebSearchProviders) ||
		!webProvidersEqual(p.WebFetchProviders, snap.WebFetchProviders) {
		status = i18n.T("ui.settings.act.saved_web")
	}
	// 放最后：语言与「装配期才读」的两组（呈现模式 / decide）都是要重启的；重启句
	// 排最晚，同时改了别的也先说这一句，免得用户以为马上生效。
	if p.ToolsMode != "" || p.DecideSet {
		status = i18n.T("ui.settings.act.saved_restart")
	}
	if langOf(p.Lang) != langOf(snap.Lang) {
		status = i18n.T("ui.settings.act.saved_lang")
	}
	return status
}

// langOf 界面语言的归一值：config 键缺失（快照 ""）与显式 auto 是同一档。不归一的话，
// 第一次保存（快照 "" → 表单 auto）会被当成「改了语言」，白提示一句重启。
func langOf(v string) string {
	if v == "" {
		return "auto"
	}
	return v
}

// nextSnapshot 保存后的快照基准：文本键直接取 patch，密钥形态按 patch 的新值与旧形态
// 推出（值不回显，界面只记「已存 / 明文 / 引用名」），联网提供商按 kind 对齐地重算。
func nextSnapshot(p SettingsPatch, snap SettingsSnapshot) SettingsSnapshot {
	// 工具页两组（呈现模式 / decide）：patch 带 Set 位才更新快照基准，未改动的保存
	// 保持原值（与 DecideSet 的「不创建段」规则对齐）。
	toolsMode := snap.ToolsMode
	if p.ToolsMode != "" {
		toolsMode = p.ToolsMode
	}
	decMode, decBase, decModel := snap.DecideMode, snap.DecideBaseURL, snap.DecideModel
	decTimeout, decTask := snap.DecideTimeoutMS, snap.DecideTaskChars
	decWarm, decDeny := snap.DecideWarmup, snap.DecideDenyEnabled
	decRisk, decHuman, decDenyAbove := snap.DecideRiskBelow, snap.DecideHumanBelow, snap.DecideHumanAbove
	if p.DecideSet {
		decMode, decBase, decModel = p.DecideMode, p.DecideBaseURL, p.DecideModel
		decTimeout, decTask = p.DecideTimeoutMS, p.DecideTaskChars
		decWarm, decDeny = *p.DecideWarmup, *p.DecideDenyEnabled
		decRisk, decHuman, decDenyAbove = p.DecideRiskBelow, p.DecideHumanBelow, p.DecideHumanAbove
	}
	return SettingsSnapshot{
		Permission: p.Permission, Model: p.Model, Think: p.Think, Effort: p.Effort,
		Hotkey: p.Hotkey, Theme: p.Theme, Lang: p.Lang,
		Scale: p.Scale, FontSize: p.FontSize,
		WindowWidth: p.WindowWidth, WindowHeight: p.WindowHeight,
		Notify: p.Notify, TTS: p.TTS,
		MaxTurns: p.MaxTurns, MaxContextTokens: p.MaxContextTokens,
		CompactThreshold: p.CompactThreshold, ToolOutputChars: p.ToolOutputChars,
		ToolTimeoutSec: p.ToolTimeoutSec, SystemPrompt: p.SystemPrompt,
		BackupKeep:    p.BackupKeep,
		PromptModules: p.PromptModules, PromptPersona: p.PromptPersona,
		Builtins: snap.Builtins, RawJSON: p.RawJSON,
		Fallback: p.Fallback, FallbackNote: snap.FallbackNote,
		ASRProvider: p.ASRProvider, ASRModel: p.ASRModel, ASRLang: p.ASRLang,
		ASRBaseURL:  p.ASRBaseURL,
		ASRKeySaved: keyShapeAfterSave(p.ASRKey, snap.ASRKeySaved),
		ASRKeyPlain: keyShapePlainAfterSave(p.ASRKey, snap.ASRKeyPlain),
		ASRKeyRef:   keyRefAfterSave(p.ASRKey, snap.ASRKeyRef),
		TTSProvider: p.TTSProvider, TTSModel: p.TTSModel,
		TTSBaseURL:  p.TTSBaseURL,
		TTSKeySaved: keyShapeAfterSave(p.TTSKey, snap.TTSKeySaved),
		TTSKeyPlain: keyShapePlainAfterSave(p.TTSKey, snap.TTSKeyPlain),
		TTSKeyRef:   keyRefAfterSave(p.TTSKey, snap.TTSKeyRef),
		TTSVoice:    p.TTSVoice, TTSDesign: p.TTSDesign,
		TTSStyle: p.TTSStyle, TTSTags: p.TTSTags,
		WebSearchProviders: webProviderViews(p.WebSearchProviders, snap.WebSearchProviders),
		WebSearchCount:     p.WebSearchCount,
		WebFetchProviders:  webProviderViews(p.WebFetchProviders, snap.WebFetchProviders),
		WebFetchMaxChars:   p.WebFetchMaxChars,
		WebFetchProxy:      p.WebFetchProxy,
		WebFetchUserAgent:  p.WebFetchUserAgent,
		WebFetchRobots:     p.WebFetchRobots,
		WebFetchPrivate:    p.WebFetchPrivate,
		WebFetchTimeoutSec: p.WebFetchTimeoutSec,
		ToolsMode:          toolsMode,
		DecideMode:         decMode,
		DecideBaseURL:      decBase,
		DecideModel:        decModel,
		DecideKeySaved:     keyShapeAfterSave(p.DecideKey, snap.DecideKeySaved),
		DecideKeyPlain:     keyShapePlainAfterSave(p.DecideKey, snap.DecideKeyPlain),
		DecideKeyRef:       keyRefAfterSave(p.DecideKey, snap.DecideKeyRef),
		DecideTimeoutMS:    decTimeout,
		DecideTaskChars:    decTask,
		DecideWarmup:       decWarm,
		DecideRiskBelow:    decRisk,
		DecideHumanBelow:   decHuman,
		DecideDenyEnabled:  decDeny,
		DecideHumanAbove:   decDenyAbove,
	}
}

// patch 采集表单成提交 patch（纯读：只碰表单与快照，无副作用，可单独测）。
//
// 校验规则三条：空 = 保持快照原值（数值项）、非法即拒（不写入磁盘、不热更）、布尔恒写。
// 所有取值范围读 port.Settings*——写回侧的权威校验用的是同一张表，见 D230。
//
// 报错时返回零值 patch 而不是「填到一半的那份」：拿着半成品去提交，等于把没校验过的
// 字段按零值写进配置——那比整份不动危险得多。
func (f *settingsForm) patch() (SettingsPatch, error) {
	p := SettingsPatch{
		Permission: f.perm.value(),
		Unattended: f.unattend.Value,
		Model:      strings.TrimSpace(f.model.Text()),
		Think:      f.think.Value,
		Effort:     f.effort.value(),
		Hotkey:     strings.TrimSpace(f.hotkey.Text()),
		Theme:      f.theme.value(),
		Lang:       f.uiLang.value(),
		// D279：占位文字恒写（"" = 清空自定义、回落内置），单行档已在表单侧限定。
		Placeholder: strings.TrimSpace(f.placeholder.Text()),
	}
	if n := utf8.RuneCountInString(p.Placeholder); n > port.SettingsPlaceholderMax {
		return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.placeholder",
			"max", port.SettingsPlaceholderMax, "n", n))
	}
	if p.Model == "" {
		p.Model = f.snap.Model // 空模型名 = 保持当前
	}
	// D90 三旋钮（写回调前校验，非法不写入磁盘）：缩放 = cycle 档（恒合法）；字号/窗口
	// 尺寸空 = 保持快照原值，数值非法/越界拦截。
	p.Scale = f.snap.Scale
	if v, err := strconv.ParseFloat(f.scale.value(), 64); err == nil {
		p.Scale = v
	}
	if v := strings.TrimSpace(f.fontSize.Text()); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < port.SettingsFontSizeMin || n > port.SettingsFontSizeMax {
			return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.font_size",
				"min", port.SettingsFontSizeMin, "max", port.SettingsFontSizeMax))
		}
		p.FontSize = n
	}
	if v := strings.TrimSpace(f.winW.Text()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return SettingsPatch{}, errors.New(i18n.T("ui.settings.err.win_width"))
		}
		p.WindowWidth = n
	}
	if v := strings.TrimSpace(f.winH.Text()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return SettingsPatch{}, errors.New(i18n.T("ui.settings.err.win_height"))
		}
		p.WindowHeight = n
	}
	if p.Hotkey != "" {
		if err := platform.ValidateHotkey(p.Hotkey); err != nil {
			return SettingsPatch{}, fmt.Errorf("%s: %w", i18n.T("ui.settings.err.hotkey"), err)
		}
	}
	// S4/D110③ 全量分组页：输出布尔恒写；限制数值空 = 保持；提示词恒写；
	// raw JSON 改动 = 整文件替换（cmd 侧 JSON 校验，解析失败原文件不动）。
	p.Notify = f.notify.Value
	p.TTS = f.tts.Value
	if v := strings.TrimSpace(f.maxTurns.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			return SettingsPatch{}, errors.New(i18n.T("ui.settings.err.max_turns"))
		}
		p.MaxTurns = n
	}
	if v := strings.TrimSpace(f.maxCtx.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			return SettingsPatch{}, errors.New(i18n.T("ui.settings.err.max_ctx"))
		}
		p.MaxContextTokens = n
	}
	if v := strings.TrimSpace(f.compact.Text()); v != "" {
		n, perr := strconv.ParseFloat(v, 64)
		if perr != nil || n < port.SettingsCompactMin || n > port.SettingsCompactMax {
			return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.compact",
				"min", port.SettingsCompactMin, "max", port.SettingsCompactMax))
		}
		p.CompactThreshold = n
	}
	if v := strings.TrimSpace(f.toolChars.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			return SettingsPatch{}, errors.New(i18n.T("ui.settings.err.tool_chars"))
		}
		p.ToolOutputChars = n
	}
	if v := strings.TrimSpace(f.toolTo.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			return SettingsPatch{}, errors.New(i18n.T("ui.settings.err.tool_timeout"))
		}
		p.ToolTimeoutSec = n
	}
	// D254：会话树保留的备份代数。空 = 保持现值；填了必须是表内区间（写回侧按同一组
	// 常量给出提示，界面先拦一道，用户不必等写入磁盘才知道越界）。
	if v := strings.TrimSpace(f.backupKeep.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < port.SettingsBackupKeepMin || n > port.SettingsBackupKeepMax {
			return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.backup_keep",
				"min", port.SettingsBackupKeepMin, "max", port.SettingsBackupKeepMax))
		}
		p.BackupKeep = n
	}
	p.SystemPrompt = f.sysPrompt.Text()
	// S7/D144：人格模块树 + 有序引用（恒全量写；空名行在 views 里跳过、cmd 侧校验模块名）。
	p.PromptModules = f.promptViews()
	p.PromptPersona = f.promptRefList()
	// S11-7①/D176：降级链恒全量写（空 = 清空）；provider 名为空的行为空行被跳过，
	// cmd 侧再校验 provider 名存在于 providers 列表。
	p.Fallback = f.fallbackList()
	// 语音（D179/D222）：恒全量写；provider 空 = 不借任何条目（端点与密钥全靠自己填）。
	p.ASRProvider = f.asrProv.value()
	p.ASRKind = f.asrKind.value()
	p.ASRBaseURL = strings.TrimSpace(f.asrBaseURL.Text())
	p.ASRKey = strings.TrimSpace(f.asrKey.Text())
	p.ASRModel = strings.TrimSpace(f.asrModel.Text())
	p.ASRModelPath = strings.TrimSpace(f.asrPath.Text())
	p.ASRLang = f.asrLang.value()
	p.TTSProvider = f.ttsProv.value()
	p.TTSKind = f.ttsKind.value()
	p.TTSBaseURL = strings.TrimSpace(f.ttsBaseURL.Text())
	p.TTSKey = strings.TrimSpace(f.ttsKey.Text())
	p.TTSModel = strings.TrimSpace(f.ttsModel.Text())
	p.TTSVoice = strings.TrimSpace(f.ttsVoice.Text())
	p.TTSDesign = strings.TrimSpace(f.ttsDesign.Text())
	p.TTSStyle = strings.TrimSpace(f.ttsStyle.Text())
	p.TTSTags = strings.TrimSpace(f.ttsTags.Text())
	p.VADEnabled = f.vadOn.Value // D295：按说话分段（默认开）
	p.VADSet = true              // 表单提交管这一项；窄写回不给这个标志
	// 联网（D200）：两组提供商恒全量写（空 = 清空，= 该能力不可用）；数值空 = 保持
	// 快照原值、非法拦截；布尔恒写。密钥框留空 = 不带值（cmd 侧保持原引用）。
	p.WebSearchProviders = f.webProviderPatches(true)
	p.WebFetchProviders = f.webProviderPatches(false)
	p.WebSearchCount = f.snap.WebSearchCount
	if v := strings.TrimSpace(f.webCount.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < port.SettingsSearchCountMin || n > port.SettingsSearchCountMax {
			return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.search_count",
				"min", port.SettingsSearchCountMin, "max", port.SettingsSearchCountMax))
		}
		p.WebSearchCount = n
	}
	p.WebFetchMaxChars = f.snap.WebFetchMaxChars
	if v := strings.TrimSpace(f.webMaxChars.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < port.SettingsFetchCharsMin || n > port.SettingsFetchCharsMax {
			return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.fetch_chars",
				"min", port.SettingsFetchCharsMin, "max", port.SettingsFetchCharsMax))
		}
		p.WebFetchMaxChars = n
	}
	p.WebFetchTimeoutSec = f.snap.WebFetchTimeoutSec
	if v := strings.TrimSpace(f.webTimeout.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < port.SettingsFetchTimeoutMin || n > port.SettingsFetchTimeoutMax {
			return SettingsPatch{}, fmt.Errorf("%s", i18n.Tf("ui.settings.err.fetch_timeout",
				"min", port.SettingsFetchTimeoutMin, "max", port.SettingsFetchTimeoutMax))
		}
		p.WebFetchTimeoutSec = n
	}
	p.WebFetchProxy = strings.TrimSpace(f.webProxy.Text())
	p.WebFetchUserAgent = strings.TrimSpace(f.webUA.Text())
	p.WebFetchRobots = f.webRobots.Value
	p.WebFetchPrivate = f.webPrivate.Value
	// 采样参数（D204）：空串 = 该项不发送；有值必须是合法数字（0 是有效取值）。
	// SamplingSet = 本次保存整段管采样（窄写回不设这个位，别把用户配好的参数抹平）。
	// 解析与范围走 port.ParseSamplingText——写回侧 formSampling 调的是同一个函数。
	p.SamplingSet = true
	p.SamplingTemperature = strings.TrimSpace(f.samplingTemp.Text())
	p.SamplingTopP = strings.TrimSpace(f.samplingTopP.Text())
	p.SamplingMaxTokens = strings.TrimSpace(f.samplingMax.Text())
	p.SamplingStop = strings.TrimSpace(f.samplingStop.Text())
	if _, err := port.ParseSamplingText(p.SamplingTemperature, p.SamplingTopP,
		p.SamplingMaxTokens, p.SamplingStop); err != nil {
		return SettingsPatch{}, err
	}
	// 工具呈现模式与 decide（实验性）：采集与校验收在 decidePatch，patch 只接线
	// （funcsize 闸门：patch 不得继续长胖）。
	applyDecide, err := f.decidePatch()
	if err != nil {
		return SettingsPatch{}, err
	}
	applyDecide(&p)
	if v := strings.TrimSpace(f.rawCfg.Text()); v != "" && v != strings.TrimSpace(f.snap.RawJSON) {
		p.RawJSON = v
	}
	return p, nil
}

// decidePatch 采集工具页两组装配期配置（呈现模式 + decide 全键），返回一个把结果
// 写进 patch 的闭包与校验错误。校验规则与写回侧同表（port.SettingsDecide*）：
// 数值空 = 不改；decide 任一字段相对快照有变化才置 DecideSet——没改过的保存不得
// 顺手创建这一段（段缺失 = 实验功能「不存在」的默认形态）。密钥框恒空（只写不回
// 显），非空才算改。装配期读的配置改了只能重启生效（savedStatus 提示）。
func (f *settingsForm) decidePatch() (func(*SettingsPatch), error) {
	noop := func(*SettingsPatch) {}
	var muts []func(*SettingsPatch)
	if m := f.toolsMode.value(); m != toolsModeNorm(f.snap.ToolsMode) {
		mode := m
		muts = append(muts, func(p *SettingsPatch) { p.ToolsMode = mode })
	}
	changed := f.decideMode.value() != decideModeNorm(f.snap.DecideMode) ||
		strings.TrimSpace(f.decideBase.Text()) != f.snap.DecideBaseURL ||
		strings.TrimSpace(f.decideKey.Text()) != "" ||
		strings.TrimSpace(f.decideModel.Text()) != f.snap.DecideModel ||
		f.decideWarmup.Value != f.snap.DecideWarmup ||
		f.decideDeny.Value != f.snap.DecideDenyEnabled
	parseNum := func(v string, min, max int, key string) (int, error) {
		if v == "" {
			return 0, nil
		}
		n, perr := strconv.Atoi(v)
		if perr != nil || n < min || n > max {
			return 0, fmt.Errorf("%s", i18n.Tf(key, "min", min, "max", max))
		}
		return n, nil
	}
	parseFlt := func(v string, max float64, key string) (float64, error) {
		if v == "" {
			return 0, nil
		}
		n, perr := strconv.ParseFloat(v, 64)
		if perr != nil || n <= 0 || n > max {
			return 0, fmt.Errorf("%s", i18n.Tf(key, "max", max))
		}
		return n, nil
	}
	timeout, err := parseNum(strings.TrimSpace(f.decideTimeout.Text()),
		port.SettingsDecideTimeoutMin, port.SettingsDecideTimeoutMax, "ui.settings.err.decide_timeout")
	if err != nil {
		return noop, err
	}
	taskChars, err := parseNum(strings.TrimSpace(f.decideTaskChars.Text()),
		0, port.SettingsDecideTaskCharsMax, "ui.settings.err.decide_taskchars")
	if err != nil {
		return noop, err
	}
	riskBelow, err := parseFlt(strings.TrimSpace(f.decideRisk.Text()),
		port.SettingsDecideRiskBelowMax, "ui.settings.err.decide_risk")
	if err != nil {
		return noop, err
	}
	humanBelow, err := parseFlt(strings.TrimSpace(f.decideHuman.Text()),
		port.SettingsDecideHumanBelowMax, "ui.settings.err.decide_human")
	if err != nil {
		return noop, err
	}
	denyAbove, err := parseFlt(strings.TrimSpace(f.decideDenyAbove.Text()),
		port.SettingsDecideHumanAboveMax, "ui.settings.err.decide_deny_above")
	if err != nil {
		return noop, err
	}
	if changed || timeout != 0 || taskChars != 0 || riskBelow != 0 || humanBelow != 0 || denyAbove != 0 {
		muts = append(muts, func(p *SettingsPatch) {
			p.DecideSet = true
			p.DecideMode = f.decideMode.value()
			p.DecideBaseURL = strings.TrimSpace(f.decideBase.Text())
			p.DecideKey = strings.TrimSpace(f.decideKey.Text())
			p.DecideModel = strings.TrimSpace(f.decideModel.Text())
			p.DecideTimeoutMS = timeout
			p.DecideTaskChars = taskChars
			warmup := f.decideWarmup.Value
			p.DecideWarmup = &warmup
			deny := f.decideDeny.Value
			p.DecideDenyEnabled = &deny
			p.DecideRiskBelow = riskBelow
			p.DecideHumanBelow = humanBelow
			p.DecideHumanAbove = denyAbove
		})
	}
	if len(muts) == 0 {
		return noop, nil
	}
	return func(p *SettingsPatch) {
		for _, m := range muts {
			m(p)
		}
	}, nil
}
