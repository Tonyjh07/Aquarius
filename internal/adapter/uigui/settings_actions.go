package uigui

import (
	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui/platform"

	"fmt"
	"strconv"
	"strings"

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
	if p.RawJSON != "" {
		f.status = "已保存（config.json 原文已替换；provider/密钥类改动重启生效）"
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
		RawJSON: p.RawJSON,
	}
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

// providerPatchOf 组装单条 provider 提交（编辑区现值 → patch；apiKey 只写不回显）。
func providerPatchOf(pf *providerForm) ProviderPatch {
	return ProviderPatch{
		Orig:        pf.orig,
		Name:        strings.TrimSpace(pf.name.Text()),
		BaseURL:     strings.TrimSpace(pf.baseURL.Text()),
		APIKey:      strings.TrimSpace(pf.apiKey.Text()),
		Models:      strings.TrimSpace(pf.models.Text()),
		Tokenizer:   strings.TrimSpace(pf.tokenizer.Text()),
		Unsupported: strings.TrimSpace(pf.unsupported.Text()),
	}
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
}

// runProviderTest 连通性测试（D110②，真实最小请求）：goroutine 跑 Test（网络 +
// 短超时，不得卡帧循环），结果经 testResults 通道回灌帧循环（win.Invalidate 唤醒）。
func (u *UI) runProviderTest(f *settingsForm, pf *providerForm) {
	if u.opts.ProviderMgr == nil {
		f.provErr = true
		f.provStatus = "provider 编辑未接线"
		return
	}
	patch := providerPatchOf(pf)
	go func() {
		err := u.opts.ProviderMgr.Test(patch)
		select {
		case f.testResults <- testOutcome{name: patch.Name, err: err}:
		default: // 槽满丢弃最旧（测试结果是最简反馈，不积压）
			select {
			case <-f.testResults:
			default:
			}
			f.testResults <- testOutcome{name: patch.Name, err: err}
		}
		if f.win != nil {
			f.win.Invalidate() // 帧循环重绘排空结果（并发安全）
		}
	}()
}

// drainTestResults 帧循环排空连通性测试结果（最新一条覆盖展示）。
func (f *settingsForm) drainTestResults() {
	for {
		select {
		case r := <-f.testResults:
			if r.err != nil {
				f.provErr = true
				f.provStatus = "连通测试 " + r.name + " 失败：" + r.err.Error()
			} else {
				f.provErr = false
				f.provStatus = "连通测试 " + r.name + " 通过"
			}
		default:
			return
		}
	}
}

// setHotkey 更新快捷键配置原子槽并请求托盘线程重注册（设置窗保存；注册归属托盘
// 线程——RegisterHotKey 归调用线程，跨线程只能投消息，§15.1）。headless/非窗口
// 平台 = 槽更新即止。
func (u *UI) setHotkey(hk string) {
	u.hotkeyCfg.Store(hk)
	u.plat.ReloadHotkey()
}
