package uigui

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"

	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// saveSettings 保存流（渲染外的可 headless 测段）：组装 patch → 写回调（装配根持久化
// 文本键并返回内核命令计划）→ 命令经 inCh 排队（与键入同路径串行执行、回执进转写区；
// 队列 256 深、主循环持续排空，阻塞投递不致死锁）→ 主题/快捷键 GUI 侧热生效 →
// 反馈文案写回表单。
func (u *UI) saveSettings(f *settingsForm) {
	if f.readOnly || u.opts.ApplySettings == nil {
		f.err = true
		f.status = "设置窗未接线（只读占位）"
		return
	}
	p := SettingsPatch{
		Permission: f.perm.value(),
		Unattended: f.unattend.Value,
		Model:      strings.TrimSpace(f.model.Text()),
		Think:      f.think.Value,
		Effort:     f.effort.value(),
		Hotkey:     strings.TrimSpace(f.hotkey.Text()),
		Theme:      f.theme.value(),
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
		if err != nil || n < fontSpMin || n > fontSpMax {
			f.err = true
			f.status = fmt.Sprintf("字号须为 %g–%g 的数值", fontSpMin, fontSpMax)
			return
		}
		p.FontSize = n
	}
	if v := strings.TrimSpace(f.winW.Text()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			f.err = true
			f.status = "窗口宽须为非负像素数值"
			return
		}
		p.WindowWidth = n
	}
	if v := strings.TrimSpace(f.winH.Text()); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			f.err = true
			f.status = "窗口高须为非负像素数值"
			return
		}
		p.WindowHeight = n
	}
	if p.Hotkey != "" {
		if err := platform.ValidateHotkey(p.Hotkey); err != nil {
			f.err = true
			f.status = "快捷键非法：" + err.Error()
			return
		}
	}
	// S4/D110③ 全量分组页：输出布尔恒写；限制数值空 = 保持；提示词恒写；
	// raw JSON 改动 = 整文件替换（cmd 侧 JSON 校验，解析失败原文件不动）。
	p.Notify = f.notify.Value
	p.TTS = f.tts.Value
	if v := strings.TrimSpace(f.maxTurns.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			f.err = true
			f.status = "最大轮数须为非负整数"
			return
		}
		p.MaxTurns = n
	}
	if v := strings.TrimSpace(f.maxCtx.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			f.err = true
			f.status = "上下文上限须为非负整数"
			return
		}
		p.MaxContextTokens = n
	}
	if v := strings.TrimSpace(f.compact.Text()); v != "" {
		n, perr := strconv.ParseFloat(v, 64)
		if perr != nil || n < 0 || n > 1 {
			f.err = true
			f.status = "压缩阈值须为 0–1 的小数"
			return
		}
		p.CompactThreshold = n
	}
	if v := strings.TrimSpace(f.toolChars.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			f.err = true
			f.status = "工具输出上限须为非负整数"
			return
		}
		p.ToolOutputChars = n
	}
	if v := strings.TrimSpace(f.toolTo.Text()); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			f.err = true
			f.status = "工具超时须为非负整数"
			return
		}
		p.ToolTimeoutSec = n
	}
	p.SystemPrompt = f.sysPrompt.Text()
	// S7/D144：人格模块树 + 有序引用（恒全量写；空名行在 views 里跳过、cmd 侧校验模块名）。
	p.PromptModules = f.promptViews()
	p.PromptPersona = f.promptRefList()
	// S11-7①/D176：降级链恒全量写（空 = 清空）；provider 名为空的行为空行被跳过，
	// cmd 侧再校验 provider 名存在于 providers 列表。
	p.Fallback = f.fallbackList()
	// 语音（D179）：恒全量写；provider 空 = 该能力关闭（模型名原样保留）。
	p.ASRProvider = f.asrProv.value()
	p.ASRModel = strings.TrimSpace(f.asrModel.Text())
	p.ASRLang = f.asrLang.value()
	p.TTSProvider = f.ttsProv.value()
	p.TTSModel = strings.TrimSpace(f.ttsModel.Text())
	p.TTSVoice = strings.TrimSpace(f.ttsVoice.Text())
	p.TTSDesign = strings.TrimSpace(f.ttsDesign.Text())
	p.TTSStyle = strings.TrimSpace(f.ttsStyle.Text())
	p.TTSTags = strings.TrimSpace(f.ttsTags.Text())
	if v := strings.TrimSpace(f.rawCfg.Text()); v != "" && v != strings.TrimSpace(f.snap.RawJSON) {
		p.RawJSON = v
	}
	cmds, err := u.opts.ApplySettings(p)
	if err != nil {
		f.err = true
		f.status = "保存失败：" + err.Error()
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
	f.status = "已保存"
	if p.Model != f.snap.Model {
		f.status = "已保存（模型名经 /model 热切换）"
	}
	if p.ASRProvider != f.snap.ASRProvider || p.ASRModel != f.snap.ASRModel ||
		p.TTSProvider != f.snap.TTSProvider || p.TTSModel != f.snap.TTSModel {
		f.status = "已保存（语音服务与模型的改动重启后生效）"
	}
	if p.RawJSON != "" {
		f.status = "已保存（config.json 原文已替换；provider/密钥类改动重启生效）"
	}
	if !promptModulesEqual(f.snap.PromptModules, p.PromptModules) || !slices.Equal(f.snap.PromptPersona, p.PromptPersona) {
		f.status = "已保存（人格组合只对新会话生效，重启后应用）"
	}
	if !slices.Equal(p.Fallback, f.snapChain) {
		f.status = "已保存（降级链改动重启后生效）"
	}
	f.snap = SettingsSnapshot{
		Permission: p.Permission, Model: p.Model, Think: p.Think, Effort: p.Effort,
		Hotkey: p.Hotkey, Theme: p.Theme,
		Scale: p.Scale, FontSize: p.FontSize,
		WindowWidth: p.WindowWidth, WindowHeight: p.WindowHeight,
		Notify: p.Notify, TTS: p.TTS,
		MaxTurns: p.MaxTurns, MaxContextTokens: p.MaxContextTokens,
		CompactThreshold: p.CompactThreshold, ToolOutputChars: p.ToolOutputChars,
		ToolTimeoutSec: p.ToolTimeoutSec, SystemPrompt: p.SystemPrompt,
		PromptModules: p.PromptModules, PromptPersona: p.PromptPersona,
		Builtins: f.snap.Builtins, RawJSON: p.RawJSON,
		Fallback: p.Fallback, FallbackNote: f.snap.FallbackNote,
		ASRProvider: p.ASRProvider, ASRModel: p.ASRModel, ASRLang: p.ASRLang,
		TTSProvider: p.TTSProvider, TTSModel: p.TTSModel,
		TTSVoice: p.TTSVoice, TTSDesign: p.TTSDesign,
		TTSStyle: p.TTSStyle, TTSTags: p.TTSTags,
	}
	f.snapChain = append([]FallbackLink(nil), p.Fallback...)
}

// fallbackList 组装降级链提交（编辑区现值 → []FallbackLink）。provider 名与模型名都为空
// 的行整体跳过（用户点了「添加」却没填任何东西）——避免落一条无主的链项；模型名允许为空
// （= 取该 provider 的缺省模型）。
func (f *settingsForm) fallbackList() []FallbackLink {
	out := make([]FallbackLink, 0, len(f.chain))
	for _, ff := range f.chain {
		prov := strings.TrimSpace(ff.chainProvider(f.chainChoices))
		model := strings.TrimSpace(ff.model.Text())
		if prov == "" && model == "" {
			continue
		}
		out = append(out, FallbackLink{Provider: prov, Model: model})
	}
	return out
}

// promptModulesEqual 模块树视图等价（名称 + 正文 + 子模块序；保存提示的变更判定）。
func promptModulesEqual(a, b []PromptModuleView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Text != b[i].Text || !slices.Equal(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

// profileActionLabel 三动作的反馈前缀（渲染与反馈共用）。
func profileActionLabel(act string) string {
	switch act {
	case "create":
		return "新建"
	case "copy":
		return "复制"
	case "switch":
		return "切换"
	}
	return act
}

// profileAction profile 区三动作（D110③/修订④）：名字非空校验 → ProfilesManager
// 执行 → 反馈写表单状态（成功绿/错误红，同保存流口径）并刷新快照。headless 可测
// （不进帧渲染）。
func (u *UI) profileAction(f *settingsForm, act string) {
	if u.opts.Profiles == nil {
		f.err = true
		f.status = "profile 管理未接线"
		return
	}
	name := strings.TrimSpace(f.profName.Text())
	if name == "" {
		f.err = true
		f.status = "请先填写 profile 名"
		return
	}
	var err error
	switch act {
	case "create":
		err = u.opts.Profiles.Create(name)
	case "copy":
		err = u.opts.Profiles.Copy(name)
	case "switch":
		err = u.opts.Profiles.Switch(name)
	default:
		err = fmt.Errorf("未知动作 %q", act)
	}
	if err != nil {
		f.err = true
		f.status = "profile " + profileActionLabel(act) + "失败：" + err.Error()
		return
	}
	f.err = false
	f.profiles = u.opts.Profiles.Snapshot() // 新建/复制后清单刷新，立即可切换
	switch act {
	case "create":
		f.status = "已新建 profile " + name + "（模板已生成；填「Profile 名」后点「切换」启用，重启生效）"
	case "copy":
		f.status = "已复制当前 profile 配置到 " + name + "（仅配置不含会话数据；重启生效）"
	case "switch":
		f.status = "已切换 profile → " + name + "（重启生效）"
	}
}

// providerPatchOf 组装单条 provider 提交（编辑区现值 → patch；密钥值只写不回显）。
func providerPatchOf(pf *providerForm) ProviderPatch {
	p := ProviderPatch{
		Orig:        pf.orig,
		Name:        strings.TrimSpace(pf.name.Text()),
		Kind:        pf.kind.value(),
		BaseURL:     strings.TrimSpace(pf.baseURL.Text()),
		Tokenizer:   strings.TrimSpace(pf.tokenizer.Text()),
		Unsupported: strings.TrimSpace(pf.unsupported.Text()),
	}
	for _, kf := range pf.keyForms {
		p.Keys = append(p.Keys, ProviderKeyPatch{
			Value: strings.TrimSpace(kf.value.Text()),
			Label: strings.TrimSpace(kf.label.Text()),
		})
	}
	for _, mf := range pf.modelForms {
		name := strings.TrimSpace(mf.name.Text())
		if name == "" {
			continue // 空名模型行 = 尚未创建（cmd 侧同口径跳过）
		}
		win := 0
		if v := strings.TrimSpace(mf.contextWin.Text()); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				win = n
			}
		}
		mp := ProviderModelPatch{Name: name, ContextWindow: win, Disabled: !mf.enabled.Value}
		// 只有用户动过能力勾选才把声明固化成显式（未动 = nil，保持键缺失/原声明形态）。
		if mf.touched() {
			names := mf.capNames()
			mp.Caps = &names
		}
		p.Models = append(p.Models, mp)
	}
	return p
}

// capNames 该模型行勾选的能力名（按 port.CapNames 顺序；空 = 三项全关的显式声明）。
func (mf *providerModelForm) capNames() []string {
	var out []string
	if mf.capsTools.Value {
		out = append(out, port.CapTools)
	}
	if mf.capsReason.Value {
		out = append(out, port.CapReasoning)
	}
	if mf.capsVision.Value {
		out = append(out, port.CapVision)
	}
	return out
}

// applyProviderChanges provider 区「应用变更」（D110②）：全量 patch + primary →
// ProviderManager.Apply（cmd 侧校验与泛键写回，重启生效）。与核心档保存流独立——
// 两类写路径互不混叠。
func (u *UI) applyProviderChanges(f *settingsForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = "provider 编辑未接线"
		return
	}
	if len(f.provs) == 0 {
		f.provErr = true
		f.provStatus = "至少保留一个 provider"
		return
	}
	patches := make([]ProviderPatch, 0, len(f.provs))
	for _, pf := range f.provs {
		patches = append(patches, providerPatchOf(pf))
	}
	primary := ""
	if f.primaryIdx >= 0 && f.primaryIdx < len(f.provs) {
		primary = strings.TrimSpace(f.provs[f.primaryIdx].name.Text())
	}
	if err := u.opts.ProviderMgr.Apply(primary, patches); err != nil {
		f.provErr = true
		f.provStatus = "应用失败：" + err.Error()
		return
	}
	f.provErr = false
	f.provStatus = "已应用 provider 变更（重启生效）"
	f.loadProviders(u.opts.ProviderMgr.Snapshot()) // 以落盘事实源重建编辑区（orig 对齐、primary 回正）
	f.provOpen = -1                                // 重建后全收起（新形态下的稳定落点）
}

// runProviderTest 连通性测试（D110②，真实最小请求）：goroutine 跑 Test（网络 +
// 短超时，不得卡帧循环），结果经 testResults 通道回灌帧循环（win.Invalidate 唤醒）。
// 只报成功时延（模拟稿「连通正常 · 128 ms」），失败报原因不带毫秒。
func (u *UI) runProviderTest(f *settingsForm, pf *providerForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = "provider 编辑未接线"
		return
	}
	patch := providerPatchOf(pf)
	go func() {
		start := time.Now()
		err := u.opts.ProviderMgr.Test(patch)
		postOutcome(f, testOutcome{kind: probePing, name: patch.Name, err: err,
			ms: time.Since(start).Milliseconds()})
	}()
}

// runFetchModels 拉一次 /models 并落进模型列表（D153/D181）：auto = 切到本页时自动触发
// （每窗一次），否则是用户点「获取模型」。两者走同一条结果路径——新增行一律**默认未启用**，
// 由用户勾选后随「应用 provider 变更」落盘。与连通测试同一异步口径（goroutine + 结果通道
// + Invalidate）；**只读**，不改配置。
func (u *UI) runFetchModels(f *settingsForm, pf *providerForm, auto bool) {
	if u.opts.ProviderMgr == nil {
		if auto {
			return // 自动拉取没接线就静默（这是进入页面的附带动作，不该报错占屏）
		}
		f.provErr = true
		f.provStatus = "provider 编辑未接线"
		return
	}
	patch := providerPatchOf(pf)
	go func() {
		start := time.Now()
		models, err := u.opts.ProviderMgr.Models(patch)
		postOutcome(f, testOutcome{kind: probeModels, name: patch.Name, err: err,
			ms: time.Since(start).Milliseconds(), models: models, auto: auto})
	}()
}

// postOutcome 投递异步探针结果（槽满丢最旧：只是状态反馈，不积压），并唤醒帧循环。
func postOutcome(f *settingsForm, out testOutcome) {
	select {
	case f.testResults <- out:
	default:
		select {
		case <-f.testResults:
		default:
		}
		f.testResults <- out
	}
	if f.win != nil {
		f.win.Invalidate() // 帧循环重绘排空结果（并发安全）
	}
}

// drainTestResults 帧循环排空异步探针结果（最新一条覆盖展示）。
func (f *settingsForm) drainTestResults() {
	for {
		select {
		case r := <-f.testResults:
			switch r.kind {
			case probeModels:
				f.applyModels(r)
				continue
			case probeCaps:
				f.applyProbeResult(r)
				continue
			}
			if r.err != nil {
				f.provErr = true
				f.provStatus = "连通测试 " + r.name + " 失败：" + r.err.Error()
			} else {
				f.provErr = false
				f.provStatus = "连通正常 · " + strconv.FormatInt(r.ms, 10) + " ms"
			}
		default:
			return
		}
	}
}

// applyProbeResult 「探测能力」结果落回该模型行（行内反馈）与 provider 区状态行。
// D181：判定明确的结论**一律**回填勾选——写回配置了就跟基线一起推进；没写回（模型还没
// 应用，或声明本就一致）就只改勾选，让 touched() 为真，等「应用 provider 变更」一并落盘。
func (f *settingsForm) applyProbeResult(r testOutcome) {
	pf := f.provByName(r.name)
	if pf == nil {
		return // provider 已被删除：结果丢弃
	}
	mf := pf.modelByName(r.model)
	if mf == nil {
		return
	}
	if r.err != nil {
		mf.probeBusy = false
		mf.probeErr = true
		mf.probeStatus = "探测失败：" + r.err.Error()
		return
	}
	mf.probeBusy = false
	mf.probeErr = false
	mf.probeStatus = probeSummary(r.outcome)
	definite := 0
	for _, res := range r.outcome.Results {
		if res.Verdict == port.ProbeInconclusive {
			continue
		}
		definite++
		on := res.Verdict == port.ProbeSupported
		switch res.Capability {
		case port.CapTools:
			mf.capsTools.Value = on
		case port.CapReasoning:
			mf.capsReason.Value = on
		case port.CapVision:
			mf.capsVision.Value = on
		}
	}
	if r.outcome.Written {
		mf.explicit = true
		// 写回了能力声明：勾选就是配置里的生效值，基线一并推进——避免把「探测写回」
		// 误判成「用户动了勾选」再固化一次。
		mf.baseTools, mf.baseReason, mf.baseVision = mf.capsTools.Value, mf.capsReason.Value, mf.capsVision.Value
	} else if definite > 0 {
		// 没写回但拿到了明确结论：勾选已按结论更新、基线保持不动（touched() 即为真），
		// 点「应用 provider 变更」时就会把这几位固化成显式声明。
		mf.probeStatus = probeSummary(r.outcome)
	}
}

// modelByName 该 provider 编辑区里按名找模型行（异步探测结果回灌）。
func (pf *providerForm) modelByName(name string) *providerModelForm {
	for _, mf := range pf.modelForms {
		if strings.TrimSpace(mf.name.Text()) == name {
			return mf
		}
	}
	return nil
}

// applyModels 「获取模型」/自动拉取的结果落表单（D181）：服务端报的模型名全部并进模型列表
// （已存在的名字跳过——重复拉一次不该长出两行同名），新增行默认**未启用**，由用户勾选。
// 反馈只落在本卡片的模型列表头下（auto 时不占全局状态行）。
func (f *settingsForm) applyModels(r testOutcome) {
	pf := f.provByName(r.name)
	if pf == nil {
		return // 该 provider 已被删除：结果丢弃，不落到别的条目上
	}
	if r.err != nil {
		pf.fetchErr = true
		pf.fetchNote = "拉取模型列表失败：" + r.err.Error()
		if !r.auto {
			f.provErr = true
			f.provStatus = pf.fetchNote
		}
		return
	}
	if len(r.models) == 0 {
		pf.fetchErr = true
		pf.fetchNote = "服务端未返回任何模型"
		if !r.auto {
			f.provErr = true
			f.provStatus = pf.fetchNote
		}
		return
	}
	total := len(r.models)
	existing := map[string]bool{}
	for _, mf := range pf.modelForms {
		if n := strings.TrimSpace(mf.name.Text()); n != "" {
			existing[n] = true
		}
	}
	added := 0
	for _, name := range r.models {
		if existing[name] {
			continue
		}
		pf.modelForms = append(pf.modelForms, newModelRow(name, pf.kind.value(), false))
		existing[name] = true
		added++
	}
	pf.fetchErr = false
	switch {
	case added == 0:
		pf.fetchNote = "服务端返回 " + strconv.Itoa(total) + " 个模型，都已在列表中"
	default:
		pf.fetchNote = "已补入 " + strconv.Itoa(added) + " 个模型（共 " + strconv.Itoa(total) +
			" 个，新行默认未启用）"
	}
	if !r.auto {
		f.provErr = false
		f.provStatus = pf.fetchNote
	}
}

// appendModelCand 点选候选 chip = 追加一个模型行（已存在则跳过）。
func (pf *providerForm) appendModelCand(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for _, mf := range pf.modelForms {
		if strings.TrimSpace(mf.name.Text()) == name {
			return
		}
	}
	pf.modelForms = append(pf.modelForms, newModelRow(name, pf.kind.value(), true))
}

// runProbeModel 单个模型行的「探测能力」（S11-7③/D174）：与 `/model probe` 同一条写回
// 逻辑（ProviderManager.Probe → 装配根 probeModel），只是把结论带回这一行展示。
// goroutine 跑（网络 + 可能 30s×3，不得卡帧循环），结果经 testResults 回灌。
func (u *UI) runProbeModel(f *settingsForm, pf *providerForm, mf *providerModelForm) {
	if u.opts.ProviderMgr == nil {
		mf.probeErr = true
		mf.probeStatus = "provider 编辑未接线"
		return
	}
	if mf.probeBusy {
		return // 还在跑：重复点不叠发请求（否则一轮点击能连开好几条 30s 探测）
	}
	model := strings.TrimSpace(mf.name.Text())
	if model == "" {
		mf.probeErr = true
		mf.probeStatus = "先填模型名再探测"
		return
	}
	if strings.TrimSpace(pf.name.Text()) == "" {
		mf.probeErr = true
		mf.probeStatus = "先填 provider 名再探测"
		return
	}
	patch := providerPatchOf(pf)
	// 开跑即置忙并清掉上一轮结论：转圈要立刻出现，旧结论留着会让人以为是这次的。
	mf.probeBusy, mf.probeErr, mf.probeStatus = true, false, ""
	go func() {
		out, err := u.opts.ProviderMgr.Probe(model, patch)
		postOutcome(f, testOutcome{kind: probeCaps, name: patch.Name, model: model, err: err, outcome: out})
	}()
}

// probeSummary 探测结论一行文案：三项逐档计数（支持 / 不支持 / 无结论），三档相加恒等于
// 实际探测项数——只写前两档会让人以为「第三项没测」。无结论的项保持原声明不动。
func probeSummary(out ProbeOutcome) string {
	var sup, unsup, unk int
	for _, r := range out.Results {
		switch r.Verdict {
		case port.ProbeSupported:
			sup++
		case port.ProbeUnsupported:
			unsup++
		default:
			unk++
		}
	}
	s := strconv.Itoa(sup) + " 项支持 / " + strconv.Itoa(unsup) + " 项不支持"
	if unk > 0 {
		s += " / " + strconv.Itoa(unk) + " 项无结论"
	}
	switch {
	case out.Written:
		s += "（已写回配置，重启生效）"
	case out.Pending && sup+unsup > 0:
		s += "（已按结论勾选；点「应用 provider 变更」写入配置）"
	case sup+unsup > 0:
		s += "（与现有声明一致）"
	default:
		s += "（无明确结论，未改动声明）"
	}
	return s
}

// provByName 按 provider 名找编辑行（异步结果回灌用；改名过程中按当前输入匹配）。
func (f *settingsForm) provByName(name string) *providerForm {
	for _, pf := range f.provs {
		if strings.TrimSpace(pf.name.Text()) == name {
			return pf
		}
	}
	return nil
}

// setHotkey 更新快捷键配置原子槽并请求托盘线程重注册（设置窗保存；注册归属托盘
// 线程——RegisterHotKey 归调用线程，跨线程只能投消息，§15.1）。headless/非窗口
// 平台 = 槽更新即止。
func (u *UI) setHotkey(hk string) {
	u.hotkeyCfg.Store(hk)
	u.plat.ReloadHotkey()
}
