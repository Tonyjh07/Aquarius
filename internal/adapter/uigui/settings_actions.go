package uigui

// 本文件只留核心档的保存流（保存 / 提交 / 结果行 / 新快照）与 patch 采集；
// 采集用的判等与换算见 settings_fields.go，配置侧的管理动作见 settings_admin_actions.go。

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// saveSettings 保存流（渲染外的可 headless 测段）：采集表单成 patch（采集时校验，非法
// 就地拦下、不触达写回调）→ 提交编排（写回调、命令排队、GUI 侧热生效、反馈文案）。
//
// 两段分开是因为它们改的东西不同：`patch` 只读表单与快照、不产生副作用，可以单独测；
// 提交那一段才碰通道与原子槽。原先混在一个 271 行的函数里，校验与热生效交替出现，
// 想确认「非法输入到底有没有落盘」得把整段读完。
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
	// 放最后：语言是这批里**最晚生效**的一个（要重启），同时改了别的也先说这一句，
	// 免得用户以为界面马上会变。
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
	}
}

// patch 采集表单成提交 patch（纯读：只碰表单与快照，无副作用，可单独测）。
//
// 校验口径三条：空 = 保持快照原值（数值项）、非法即拒（不落盘、不热更）、布尔恒写。
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
	}
	if p.Model == "" {
		p.Model = f.snap.Model // 空模型名 = 保持当前
	}
	// D90 三旋钮（写回调前校验，非法不落盘）：缩放 = cycle 档（恒合法）；字号/窗口
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
			return SettingsPatch{}, fmt.Errorf("%s：%w", i18n.T("ui.settings.err.hotkey"), err)
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
	// 常量给出提示，界面先拦一道，用户不必等落盘才知道越界）。
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
	if v := strings.TrimSpace(f.rawCfg.Text()); v != "" && v != strings.TrimSpace(f.snap.RawJSON) {
		p.RawJSON = v
	}
	return p, nil
}
