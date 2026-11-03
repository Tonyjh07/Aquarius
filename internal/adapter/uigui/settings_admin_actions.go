package uigui

// 配置侧的管理动作：profile 三动作、provider 变更应用、连通测试、拉模型与能力探测、热键写入。

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func (u *UI) profileAction(f *settingsForm, act string) {
	if u.opts.Profiles == nil {
		f.err = true
		f.status = i18n.T("ui.settings.profile.not_wired")
		return
	}
	name := strings.TrimSpace(f.profName.Text())
	if name == "" {
		f.err = true
		f.status = i18n.T("ui.settings.profile.need_name")
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
		err = fmt.Errorf("%s %q", i18n.T("ui.settings.profile.unknown_act"), act)
	}
	if err != nil {
		f.err = true
		f.status = i18n.Tf("ui.settings.profile.failed", "action", profileActionLabel(act), "err", err.Error())
		return
	}
	f.err = false
	f.profiles = u.opts.Profiles.Snapshot() // 新建/复制后清单刷新，立即可切换
	switch act {
	case "create":
		f.status = i18n.Tf("ui.settings.profile.created", "name", name)
	case "copy":
		f.status = i18n.Tf("ui.settings.profile.copied", "name", name)
	case "switch":
		f.status = i18n.Tf("ui.settings.profile.switched", "name", name)
	}
}

// providerPatchOf 组装单条 provider 提交（编辑区现值 → patch；密钥值只写不回显）。

// applyProviderChanges provider 区「应用变更」（D110②）：全量 patch + primary →
// ProviderManager.Apply（cmd 侧校验与泛键写回，重启生效）。与核心档保存流独立——
// 两类写路径互不混叠。
func (u *UI) applyProviderChanges(f *settingsForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = i18n.T("ui.settings.prov.not_wired")
		return
	}
	if len(f.provs) == 0 {
		f.provErr = true
		f.provStatus = i18n.T("ui.settings.prov.need_one")
		return
	}
	// 单价语法先在表单侧拦（D205）：留空合法（不声明），填了就必须是非负数字。
	for _, pf := range f.provs {
		for _, mf := range pf.modelForms {
			if badCost(mf.costIn.Text()) || badCost(mf.costOut.Text()) {
				f.provErr = true
				f.provStatus = i18n.Tf("ui.settings.prov.bad_cost",
					"model", strings.TrimSpace(mf.name.Text()))
				return
			}
		}
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
		f.provStatus = i18n.Tf("ui.settings.prov.apply_failed", "err", err.Error())
		return
	}
	f.provErr = false
	f.provStatus = i18n.T("ui.settings.prov.applied")
	f.loadProviders(u.opts.ProviderMgr.Snapshot()) // 以落盘事实源重建编辑区（orig 对齐、primary 回正）
	f.provOpen = -1                                // 重建后全收起（新形态下的稳定落点）
}

// runProviderTest 连通性测试（D110②，真实最小请求）：goroutine 跑 Test（网络 +
// 短超时，不得卡帧循环），结果经 testResults 通道回灌帧循环（win.Invalidate 唤醒）。
// 只报成功时延（模拟稿「连通正常 · 128 ms」），失败报原因不带毫秒。
func (u *UI) runProviderTest(f *settingsForm, pf *providerForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = i18n.T("ui.settings.prov.not_wired")
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
		f.provStatus = i18n.T("ui.settings.prov.not_wired")
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
				f.provStatus = i18n.Tf("ui.settings.prov.test_failed", "name", r.name, "err", r.err.Error())
			} else {
				f.provErr = false
				f.provStatus = i18n.Tf("ui.settings.prov.test_ok", "ms", strconv.FormatInt(r.ms, 10))
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
		mf.probeStatus = i18n.Tf("ui.settings.prov.probe_failed", "err", r.err.Error())
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
		pf.fetchNote = i18n.Tf("ui.settings.prov.fetch_failed", "err", r.err.Error())
		if !r.auto {
			f.provErr = true
			f.provStatus = pf.fetchNote
		}
		return
	}
	if len(r.models) == 0 {
		pf.fetchErr = true
		pf.fetchNote = i18n.T("ui.settings.prov.fetch_empty")
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
		pf.fetchNote = i18n.Tf("ui.settings.prov.fetch_all_known", "total", strconv.Itoa(total))
	default:
		pf.fetchNote = i18n.Tf("ui.settings.prov.fetch_added",
			"added", strconv.Itoa(added), "total", strconv.Itoa(total))
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
		mf.probeStatus = i18n.T("ui.settings.prov.not_wired")
		return
	}
	if mf.probeBusy {
		return // 还在跑：重复点不叠发请求（否则一轮点击能连开好几条 30s 探测）
	}
	model := strings.TrimSpace(mf.name.Text())
	if model == "" {
		mf.probeErr = true
		mf.probeStatus = i18n.T("ui.settings.prov.probe_need_model")
		return
	}
	if strings.TrimSpace(pf.name.Text()) == "" {
		mf.probeErr = true
		mf.probeStatus = i18n.T("ui.settings.prov.probe_need_name")
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
	s := i18n.Tf("ui.settings.prov.probe_counts",
		"sup", strconv.Itoa(sup), "unsup", strconv.Itoa(unsup))
	if unk > 0 {
		s += i18n.Tf("ui.settings.prov.probe_unknown", "unk", strconv.Itoa(unk))
	}
	switch {
	case out.Written:
		s += i18n.T("ui.settings.prov.probe_written")
	case out.Pending && sup+unsup > 0:
		s += i18n.T("ui.settings.prov.probe_pending")
	case sup+unsup > 0:
		s += i18n.T("ui.settings.prov.probe_same")
	default:
		s += i18n.T("ui.settings.prov.probe_none")
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
