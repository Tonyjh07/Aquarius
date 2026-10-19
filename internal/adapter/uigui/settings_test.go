package uigui

// 设置窗 headless 测试（§15.5）：快照填表（含密钥不回显）、保存流（patch 组装 +
// 命令入队 + 主题/快捷键热生效）、错误与只读路径——全部无窗口。

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fillSnapshot 标准快照（测试基准）。
func fillSnapshot() SettingsSnapshot {
	return SettingsSnapshot{
		Permission: "permissive",
		Model:      "m1", Think: true, Effort: "high", Hotkey: "Ctrl+B", Theme: "dark",
	}
}

// TestNewSettingsFormSnapshot 快照填表：各控件取到同名键值；密钥字段恒空（D35）。
func TestNewSettingsFormSnapshot(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	if f.readOnly {
		t.Fatal("读写回调齐备不应只读")
	}
	if f.model.Text() != "m1" {
		t.Errorf("文本档填表错误: model=%q", f.model.Text())
	}
	if f.hotkey.Text() != "Ctrl+B" || !f.think.Value {
		t.Errorf("快捷键/思考档填表错误: hotkey=%q think=%v", f.hotkey.Text(), f.think.Value)
	}
	if f.perm.value() != "permissive" || f.effort.value() != "high" || f.theme.value() != "dark" {
		t.Errorf("枚举档填表错误: perm=%q effort=%q theme=%q",
			f.perm.value(), f.effort.value(), f.theme.value())
	}
}

// TestNewSettingsFormDefaults 快照空值：权限对齐默认档 strict（防误发切换）、
// 主题对齐 system、档位 = 未设。
func TestNewSettingsFormDefaults(t *testing.T) {
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return SettingsSnapshot{} }})
	f := newSettingsForm(u)
	if f.perm.value() != "strict" {
		t.Errorf("空快照权限 = %q, want strict（perm.DefaultLevel）", f.perm.value())
	}
	if f.theme.value() != "system" || f.effort.value() != "" {
		t.Errorf("空快照枚举档: theme=%q effort=%q", f.theme.value(), f.effort.value())
	}
}

// TestNewSettingsFormReadOnly 无任何回调 = 只读占位（空表 + 保存报错）。
func TestNewSettingsFormReadOnly(t *testing.T) {
	u := newHeadless(t, Options{})
	f := newSettingsForm(u)
	if !f.readOnly {
		t.Fatal("回调全 nil 应为只读占位")
	}
	u.saveSettings(f)
	if !f.err || !strings.Contains(f.status, "只读") {
		t.Errorf("只读保存反馈 = (%v, %q)", f.err, f.status)
	}
}

// TestSaveSettingsHeadless 保存流全链：patch 组装 → 写回调 → 内核命令入队（inCh）
// → 主题热生效（事件循环消息）→ 快捷键原子槽 → 反馈与快照基准更新。
func TestSaveSettingsHeadless(t *testing.T) {
	restoreLight(t)
	var gotPatch SettingsPatch
	called := 0
	u := newHeadless(t, Options{
		Theme: "system",
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{
				Permission: "strict", Model: "m1",
				Think: true, Effort: "", Theme: "system",
			}
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) {
			called++
			gotPatch = p
			return []port.Command{
				{Name: "permission", Args: []string{"permissive"}},
				{Name: "effort", Args: []string{"low"}},
			}, nil
		},
	})
	f := newSettingsForm(u)
	f.perm.setValue("permissive")
	f.effort.setValue("low")
	f.model.SetText("m2")
	f.hotkey.SetText("Ctrl+B")
	f.theme.setValue("dark")
	u.saveSettings(f)

	if called != 1 {
		t.Fatalf("写回调调用 = %d, want 1", called)
	}
	if gotPatch.Permission != "permissive" || gotPatch.Theme != "dark" ||
		gotPatch.Hotkey != "Ctrl+B" || gotPatch.Effort != "low" {
		t.Errorf("patch = %+v", gotPatch)
	}
	if gotPatch.Model != "m2" {
		t.Errorf("模型名 = %q, want m2（表单改动直传）", gotPatch.Model)
	}
	if len(u.inCh) != 2 {
		t.Fatalf("排队命令 = %d, want 2", len(u.inCh))
	}
	for _, want := range []string{"permission", "effort"} {
		in := <-u.inCh
		if in.Command == nil || in.Command.Name != want {
			t.Errorf("排队命令 = %+v, want %s", in.Command, want)
		}
	}
	drainSync(t, u) // themeMsg 落进状态机后才可读
	if u.themeMode != "dark" {
		t.Errorf("主题热生效 = %q, want dark", u.themeMode)
	}
	if hk, _ := u.hotkeyCfg.Load().(string); hk != "Ctrl+B" {
		t.Errorf("快捷键槽 = %q, want Ctrl+B", hk)
	}
	if f.err || !strings.Contains(f.status, "已保存") {
		t.Errorf("反馈 = (%v, %q)", f.err, f.status)
	}
	if f.snap.Theme != "dark" || f.snap.Model != "m2" {
		t.Errorf("二次保存基准未更新: %+v", f.snap)
	}
}

// TestSaveSettingsErrors 错误路径：写回调失败/快捷键非法均不排队命令、不热生效、
// 反馈错误态；快捷键非法在写回调之前拦截（不落盘）。
func TestSaveSettingsErrors(t *testing.T) {
	t.Run("写回调失败", func(t *testing.T) {
		restoreLight(t)
		u := newHeadless(t, Options{
			Settings: func() SettingsSnapshot { return fillSnapshot() },
			ApplySettings: func(p SettingsPatch) ([]port.Command, error) {
				return []port.Command{{Name: "model", Args: []string{"m2"}}},
					errors.New("磁盘已满")
			},
		})
		f := newSettingsForm(u)
		u.saveSettings(f)
		if !f.err || !strings.Contains(f.status, "保存失败") {
			t.Errorf("反馈 = (%v, %q)", f.err, f.status)
		}
		if len(u.inCh) != 0 {
			t.Errorf("失败后不应排队命令，实排 %d", len(u.inCh))
		}
	})
	t.Run("快捷键非法", func(t *testing.T) {
		restoreLight(t)
		called := 0
		u := newHeadless(t, Options{
			Settings: func() SettingsSnapshot { return fillSnapshot() },
			ApplySettings: func(p SettingsPatch) ([]port.Command, error) {
				called++
				return nil, nil
			},
		})
		f := newSettingsForm(u)
		f.hotkey.SetText("Nope+Nope")
		u.saveSettings(f)
		if !f.err || !strings.Contains(f.status, "快捷键非法") {
			t.Errorf("反馈 = (%v, %q)", f.err, f.status)
		}
		if called != 0 {
			t.Error("快捷键非法必须在写回调之前拦截（不落盘）")
		}
	})
}

// TestSaveSettingsThemeUnchanged 主题档未改动 = 不投递 themeMsg（活动快照指针
// 不换；applyTheme 每次投递都会存新指针，可作跳过判据）。
func TestSaveSettingsThemeUnchanged(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{
		Theme:         "light",
		Settings:      func() SettingsSnapshot { return fillSnapshot() }, // Theme = dark ≠ light…
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	}) // …表单按快照填 dark，与快照一致 → 跳过投递
	drainSync(t, u)
	before := u.pal.Load()
	f := newSettingsForm(u)
	u.saveSettings(f)
	drainSync(t, u)
	if u.pal.Load() != before {
		t.Error("主题未改动仍投递了 themeMsg（应按 snap 基准跳过）")
	}
	if u.themeMode != "light" {
		t.Errorf("主题档应保持 light，实为 %q", u.themeMode)
	}
}

// TestSaveSettingsZoomKnobs D90 三旋钮保存流：缩放 cycle/字号/窗口尺寸进 patch、
// zoom 槽热更（下帧 Metric 咽喉点生效）、窗口尺寸 headless no-op（mainHWND=0）、
// 快照基准更新含新字段。
func TestSaveSettingsZoomKnobs(t *testing.T) {
	restoreLight(t)
	var gotPatch SettingsPatch
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			s := fillSnapshot()
			s.Scale, s.FontSize = 1.25, 15
			s.WindowWidth, s.WindowHeight = 608, 460
			return s
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) {
			gotPatch = p
			return nil, nil
		},
	})
	f := newSettingsForm(u)
	if got := f.scale.value(); got != "1.25" {
		t.Fatalf("缩放档填表 = %q, want 1.25", got)
	}
	f.scale.setValue("1.5")
	f.fontSize.SetText("20")
	f.winW.SetText("304")
	f.winH.SetText("920")
	u.saveSettings(f)

	if gotPatch.Scale != 1.5 || gotPatch.FontSize != 20 ||
		gotPatch.WindowWidth != 304 || gotPatch.WindowHeight != 920 {
		t.Fatalf("patch 旋钮 = %+v", gotPatch)
	}
	if z := u.zoomLoad(); z != (zoomKnobs{scale: 1.5, fontSp: 20}) {
		t.Errorf("zoom 槽 = %+v, want {1.5 20}（热更）", z)
	}
	if f.err || !strings.Contains(f.status, "已保存") {
		t.Errorf("反馈 = (%v, %q)", f.err, f.status)
	}
	if f.snap.Scale != 1.5 || f.snap.FontSize != 20 ||
		f.snap.WindowWidth != 304 || f.snap.WindowHeight != 920 {
		t.Errorf("二次保存基准 = %+v", f.snap)
	}
}

// TestSaveSettingsZoomDefaults 空快照旋钮归一：缩放/字号空值填表为缺省（1.0/15），
// 窗口尺寸空 = 保持缺省（0）——保存不误写非零。
func TestSaveSettingsZoomDefaults(t *testing.T) {
	restoreLight(t)
	var gotPatch SettingsPatch
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return SettingsSnapshot{} },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { gotPatch = p; return nil, nil },
	})
	f := newSettingsForm(u)
	u.saveSettings(f)
	if gotPatch.Scale != 1 || gotPatch.FontSize != 15 {
		t.Errorf("空快照旋钮 patch = %v/%v, want 1/15", gotPatch.Scale, gotPatch.FontSize)
	}
	if gotPatch.WindowWidth != 0 || gotPatch.WindowHeight != 0 {
		t.Errorf("空窗口尺寸 patch = %d/%d, want 0/0（缺省）",
			gotPatch.WindowWidth, gotPatch.WindowHeight)
	}
	if z := u.zoomLoad(); z != (zoomKnobs{scale: 1, fontSp: 15}) {
		t.Errorf("zoom 槽 = %+v, want {1 15}", z)
	}
}

// TestSaveSettingsZoomErrors 旋钮非法输入在写回调之前拦截（不落盘、不热更）。
func TestSaveSettingsZoomErrors(t *testing.T) {
	cases := []struct {
		name       string
		font, w, h string
		wantMsg    string
	}{
		{"字号非数值", "abc", "", "", "字号"},
		{"窗口宽非数值", "", "3O4", "920", "窗口"},
		{"窗口高负值", "", "304", "-1", "窗口"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restoreLight(t)
			called := 0
			u := newHeadless(t, Options{
				Settings: func() SettingsSnapshot {
					s := fillSnapshot()
					s.Scale, s.FontSize = 1, 15
					return s
				},
				ApplySettings: func(p SettingsPatch) ([]port.Command, error) { called++; return nil, nil },
			})
			f := newSettingsForm(u)
			f.fontSize.SetText(c.font)
			f.winW.SetText(c.w)
			f.winH.SetText(c.h)
			u.saveSettings(f)
			if !f.err || !strings.Contains(f.status, c.wantMsg) {
				t.Errorf("反馈 = (%v, %q), want 含 %q", f.err, f.status, c.wantMsg)
			}
			if called != 0 {
				t.Error("非法输入必须在写回调之前拦截")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// profile 区（D110③/修订④）
// ---------------------------------------------------------------------------

// fakeProfiles ProfilesManager 假实现（动作分派与反馈断言用）。
type fakeProfiles struct {
	snap     ProfilesSnapshot
	created  []string
	copied   []string
	switched []string
	errOn    map[string]error // 动作名 → 注入错误
}

func (f *fakeProfiles) Snapshot() ProfilesSnapshot { return f.snap }
func (f *fakeProfiles) Create(name string) error {
	f.created = append(f.created, name)
	return f.errOn["create"]
}
func (f *fakeProfiles) Copy(name string) error {
	f.copied = append(f.copied, name)
	return f.errOn["copy"]
}
func (f *fakeProfiles) Switch(name string) error {
	f.switched = append(f.switched, name)
	return f.errOn["switch"]
}

// TestProfileActionsHeadless profile 三动作：分派经 ProfilesManager、快照刷新、
// 成功/失败反馈文案、空名拦截、未接线报错。
func TestProfileActionsHeadless(t *testing.T) {
	mgr := &fakeProfiles{snap: ProfilesSnapshot{Current: "default", Items: []string{"default"}}}
	u := newHeadless(t, Options{Profiles: mgr})
	f := newSettingsForm(u)
	if f.profiles.Current != "default" || len(f.profiles.Items) != 1 {
		t.Fatalf("开窗快照 = %+v", f.profiles)
	}

	// 空名拦截：三动作都不触达。
	for _, act := range []string{"create", "copy", "switch"} {
		u.profileAction(f, act)
		if !f.err || !strings.Contains(f.status, "填写 profile 名") {
			t.Fatalf("%s 空名: (%v, %q)", act, f.err, f.status)
		}
	}
	if len(mgr.created)+len(mgr.copied)+len(mgr.switched) != 0 {
		t.Fatal("空名不应触达管理面")
	}

	// 新建成功：分派 + 清单刷新 + 反馈带重启提示。
	f.profName.SetText("work")
	u.profileAction(f, "create")
	if f.err || len(mgr.created) != 1 || mgr.created[0] != "work" {
		t.Fatalf("create 分派 = %+v (%v, %q)", mgr.created, f.err, f.status)
	}
	if !strings.Contains(f.status, "重启") {
		t.Fatalf("create 反馈 = %q, want 重启提示", f.status)
	}
	if !strings.Contains(f.status, "切换") {
		t.Fatalf("create 反馈 = %q, want 引导切换", f.status)
	}

	// 复制成功：分派 + 「仅配置」语义提示。
	mgr.snap = ProfilesSnapshot{Current: "default", Items: []string{"default", "work"}}
	u.profileAction(f, "copy")
	if f.err || len(mgr.copied) != 1 || mgr.copied[0] != "work" {
		t.Fatalf("copy 分派 = %+v", mgr.copied)
	}
	if !strings.Contains(f.status, "仅配置") {
		t.Fatalf("copy 反馈 = %q, want 仅配置语义", f.status)
	}

	// 切换成功：分派 + 重启生效提示。
	u.profileAction(f, "switch")
	if f.err || len(mgr.switched) != 1 || mgr.switched[0] != "work" {
		t.Fatalf("switch 分派 = %+v", mgr.switched)
	}
	if !strings.Contains(f.status, "已切换 profile → work") || !strings.Contains(f.status, "重启") {
		t.Fatalf("switch 反馈 = %q", f.status)
	}

	// 失败路径：错误红字带动作名与报因。
	mgr.errOn = map[string]error{"switch": errors.New("profile %q 不存在")}
	f.profName.SetText("nope")
	u.profileAction(f, "switch")
	if !f.err || !strings.Contains(f.status, "切换失败") || !strings.Contains(f.status, "不存在") {
		t.Fatalf("switch 失败反馈 = (%v, %q)", f.err, f.status)
	}
}

// TestProfileActionsUnwired 未接线（TUI/repl 或老装配）时 profile 动作报错不触达。
func TestProfileActionsUnwired(t *testing.T) {
	u := newHeadless(t, Options{})
	f := newSettingsForm(u)
	f.profName.SetText("x")
	u.profileAction(f, "create")
	if !f.err || !strings.Contains(f.status, "未接线") {
		t.Fatalf("未接线反馈 = (%v, %q)", f.err, f.status)
	}
}

// ---------------------------------------------------------------------------
// provider 编辑区（D110②）
// ---------------------------------------------------------------------------

// fakeProviderMgr ProviderManager 假实现（Apply/Test 分派断言）。
type fakeProviderMgr struct {
	snap        []ProviderSnapshot
	applied     [][]ProviderPatch
	primaryGot  []string
	testErr     error
	testPatches []ProviderPatch
	models      []string
	modelsErr   error
	modelsGot   []ProviderPatch
}

func (f *fakeProviderMgr) Snapshot() []ProviderSnapshot { return f.snap }
func (f *fakeProviderMgr) Apply(primary string, ps []ProviderPatch) error {
	for _, p := range ps { // 镜像 cmd 侧校验口径（空名拒写），失败不记账
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("provider 名字不可为空")
		}
	}
	if primary == "" {
		return errors.New("primary 未命中")
	}
	f.primaryGot = append(f.primaryGot, primary)
	f.applied = append(f.applied, ps)
	return nil
}
func (f *fakeProviderMgr) Test(p ProviderPatch) error {
	f.testPatches = append(f.testPatches, p)
	return f.testErr
}

func (f *fakeProviderMgr) Models(p ProviderPatch) ([]string, error) {
	f.modelsGot = append(f.modelsGot, p)
	return f.models, f.modelsErr
}

// TestProviderEditorHeadless 编辑区生命周期：快照填表（primary 标记、密钥恒空）、
// 新增/设主/删除、全量应用分派（Orig 保留）、异步连通测试回灌。
func TestProviderEditorHeadless(t *testing.T) {
	mgr := &fakeProviderMgr{snap: []ProviderSnapshot{
		{Name: "a", BaseURL: "http://a/v1", Models: "m1", Primary: true},
		{Name: "b", BaseURL: "http://b/v1", Models: "mb"},
	}}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	if len(f.provs) != 2 || f.primaryIdx != 0 {
		t.Fatalf("编辑区 = %d 条, primary = %d", len(f.provs), f.primaryIdx)
	}
	if f.provs[0].apiKey.Text() != "" {
		t.Error("密钥只写不回显（D35）：编辑档必须恒空")
	}

	// 新增 + 设主 + 应用：patch 全量（Orig 保留）+ primary 分派。
	f.addProviderForm()
	u.applyProviderChanges(f) // 帧外可测段：新增的第 3 条无名字 → 报错
	if !f.provErr || !strings.Contains(f.provStatus, "名字") {
		t.Fatalf("空名应用反馈 = (%v, %q)", f.provErr, f.provStatus)
	}
	latest := f.provs[len(f.provs)-1]
	latest.name.SetText("c")
	latest.baseURL.SetText("http://c/v1")
	latest.models.SetText("mc")
	u.applyProviderChanges(f)
	if f.provErr || len(mgr.applied) != 1 {
		t.Fatalf("应用反馈 = (%v, %q)", f.provErr, f.provStatus)
	}
	got := mgr.applied[0]
	if len(got) != 3 || got[0].Orig != "a" || got[2].Orig != "" {
		t.Fatalf("patches = %+v", got)
	}
	if mgr.primaryGot[0] != "a" {
		t.Fatalf("primary = %q, want a", mgr.primaryGot[0])
	}

	// 异步连通测试：结果经 testResults 回灌（帧循环外直接读）。
	u.runProviderTest(f, f.provs[0])
	out := <-f.testResults
	if out.err != nil || out.name != "a" {
		t.Fatalf("test result = %+v", out)
	}
	mgr.testErr = errors.New("401 unauthorized")
	u.runProviderTest(f, f.provs[1])
	out = <-f.testResults
	if out.err == nil {
		t.Fatal("test 失败应回灌错误")
	}
}

// TestFetchModelsButton 「获取模型」（D153）：拉列表 → 自动填入模型名行 + 候选 chip；
// 点 chip = **替换**（不追加）；失败不动输入框；结果按 provider 名回灌（期间删过条目就丢弃）。
func TestFetchModelsButton(t *testing.T) {
	mgr := &fakeProviderMgr{
		snap:   []ProviderSnapshot{{Name: "a", BaseURL: "http://a/v1", Models: "old", Primary: true}},
		models: []string{"m1", "m2", "m3"},
	}
	u := newHeadless(t, Options{ProviderMgr: mgr})
	f := newSettingsForm(u)
	pf := f.provs[0]
	// 等异步结果（goroutine 写通道），再回投走真正的排空/落表路径。
	wait := func() {
		t.Helper()
		out := <-f.testResults
		f.testResults <- out
		f.drainTestResults()
	}

	u.runFetchModels(f, pf)
	wait()
	if len(mgr.modelsGot) != 1 || mgr.modelsGot[0].Name != "a" {
		t.Fatalf("Models 调用 = %+v", mgr.modelsGot)
	}
	if got := pf.models.Text(); got != "m1, m2, m3" {
		t.Fatalf("自动填入 = %q", got)
	}
	if len(pf.cands) != 3 || len(pf.candClicks) != 3 {
		t.Fatalf("候选 chip = %d/%d", len(pf.cands), len(pf.candClicks))
	}
	if !strings.Contains(f.provStatus, "已自动填入 3 个候选") {
		t.Fatalf("状态行 = %q", f.provStatus)
	}

	// 点第二个 chip = 替换（不追加）。chip 的消费点在帧首事件循环里，故跑一帧真渲染。
	th := testTheme()
	frame := func() {
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(720, 560)),
		}, th, u, f)
	}
	pf.candClicks[1].Click()
	frame()
	if got := pf.models.Text(); got != "m2" {
		t.Fatalf("点 chip 后模型名 = %q, want m2（替换而非追加）", got)
	}

	// 失败：不动模型名（用户手写内容不被清掉），状态行报因。
	pf.models.SetText("hand")
	mgr.modelsErr = errors.New("403 forbidden")
	u.runFetchModels(f, pf)
	wait()
	if got := pf.models.Text(); got != "hand" {
		t.Fatalf("失败后模型名 = %q, want hand", got)
	}
	if !f.provErr || !strings.Contains(f.provStatus, "失败") {
		t.Fatalf("失败反馈 = (%v, %q)", f.provErr, f.provStatus)
	}

	// 成功但用户手写过：只挂候选，不覆盖（「未覆盖已填内容」）。
	mgr.modelsErr = nil
	u.runFetchModels(f, pf)
	wait()
	if got := pf.models.Text(); got != "hand" {
		t.Fatalf("手写内容被覆盖：%q, want hand", got)
	}
	if len(pf.cands) != 3 {
		t.Fatalf("未覆盖时仍应挂出候选：%v", pf.cands)
	}
	if !strings.Contains(f.provStatus, "未覆盖") {
		t.Fatalf("状态行 = %q, want 含「未覆盖」", f.provStatus)
	}

	// 结果回灌按名字：名字对不上（期间删过该条目）→ 整条结果丢弃，不落到别的条目上。
	other := &providerForm{}
	for _, ed := range []*widget.Editor{&other.name, &other.baseURL, &other.apiKey, &other.models, &other.tokenizer, &other.unsupported} {
		ed.SingleLine = true
	}
	other.name.SetText("b")
	other.models.SetText("keep")
	f.provs = []*providerForm{other}
	mgr.modelsErr = nil
	u.runFetchModels(f, pf) // pf 已不在列表里（名字 a）
	<-f.testResults
	f.drainTestResults()
	if got := other.models.Text(); got != "keep" {
		t.Fatalf("结果落到了别的 provider：模型名 = %q, want keep", got)
	}
	if len(other.cands) != 0 {
		t.Fatalf("结果落到了别的 provider：候选 = %v", other.cands)
	}
}

// TestSaveSettingsFullGroupsHeadless 全量分组页保存流（S4/D110③）：输出布尔恒写、
// 限制数值空 = 不改/非法拦截、提示词恒写、raw JSON 改动进 patch。
func TestSaveSettingsFullGroupsHeadless(t *testing.T) {
	restoreLight(t)
	var got SettingsPatch
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{
				Permission: "strict", Model: "m1", Think: true, Theme: "system",
				Notify: true, MaxTurns: 8, MaxContextTokens: 64000,
				CompactThreshold: 0.7, ToolOutputChars: 20000, ToolTimeoutSec: 60,
				SystemPrompt: "旧人格", MCPServers: []string{"docs", "web"},
				RawJSON: `{"a": 1}`,
			}
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
	})
	f := newSettingsForm(u)
	if !f.notify.Value || f.maxTurns.Text() != "8" || f.compact.Text() != "0.7" {
		t.Fatalf("分组页填表: notify=%v maxTurns=%q compact=%q", f.notify.Value, f.maxTurns.Text(), f.compact.Text())
	}
	if len(f.snap.MCPServers) != 2 {
		t.Fatalf("MCP 只读清单 = %v", f.snap.MCPServers)
	}

	// 非法数值拦截：压缩阈值越界不落盘。
	f.compact.SetText("1.5")
	u.saveSettings(f)
	if !f.err || !strings.Contains(f.status, "压缩阈值") {
		t.Fatalf("阈值拦截 = (%v, %q)", f.err, f.status)
	}
	f.compact.SetText("0.8")
	f.tts.Value = true
	f.maxCtx.SetText("") // 空 = 不改
	f.sysPrompt.SetText("新人格")
	f.rawCfg.SetText(`{"a": 2}`) // 改动 → 进 patch
	u.saveSettings(f)
	if f.err {
		t.Fatalf("保存反馈 = %q", f.status)
	}
	if !got.Notify || !got.TTS || got.MaxTurns != 8 || got.MaxContextTokens != 0 ||
		got.CompactThreshold != 0.8 || got.SystemPrompt != "新人格" {
		t.Fatalf("patch = %+v", got)
	}
	if got.RawJSON != `{"a": 2}` {
		t.Fatalf("raw patch = %q", got.RawJSON)
	}
	// raw 与当前快照一致（TrimSpace 口径）→ 不进 patch（got.RawJSON 留零值）。
	f.rawCfg.SetText(strings.TrimSpace(f.snap.RawJSON))
	u.saveSettings(f)
	if got.RawJSON != "" {
		t.Fatalf("raw 未改动不应进 patch: %q", got.RawJSON)
	}
}

// TestSettingsFrameRowsFillViewport 设置窗行列表真渲染回归（S4c 根因防线；S4d 分页后按
// 新几何与新帧结构复检）。
//
// 复现过的故障：`settingsForm` 的 `material.List` 未设 `layout.Axis` → 按**水平**轴向做
// 视口预算（按视口宽而非视口高迭代），内容仍按垂直堆叠绘制；累计两行即判定"填满"，
// 整个表单只画出首行，其余是空白窗底与越界的整宽底色带。
//
// 判据取用户可见事实而非内部结构：把设置窗按真窗尺寸/缩放离屏光栅化，要求**页内容区**
// 的下半幅与底带确有内容（轴向错配时下半幅恒为纯底色）。参照实现 = 真窗首帧
// 900×700px（`winSettings` 几何 720×560dp × 1.25 缩放）；底色取自左侧导航栏（纯
// windowBg），统计范围限定在全局底栏之上的页内容区。
func TestSettingsFrameRowsFillViewport(t *testing.T) {
	const (
		viewW, viewH = 900, 700 // 真窗首帧像素尺寸（720×560dp @1.25）
		pxPerDp      = 1.25     // 真窗 Metric（Win32 DPI 96→120 档实测值）
		navWpx       = 140 * pxPerDp
	)
	sz := image.Pt(viewW, viewH)

	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{
				Permission: "strict", Model: "m1", Theme: "system",
				Scale: 1.0, FontSize: 15,
				MaxTurns: 8, MaxContextTokens: 128000, CompactThreshold: 0.8,
				ToolOutputChars: 4000, ToolTimeoutSec: 120,
				SystemPrompt: "persona", RawJSON: `{"model":{"name":"m1"}}`,
			}
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default"}},
	})
	f := newSettingsForm(u)
	th := testTheme()

	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
		Constraints: layout.Exact(sz),
	}
	settingsFrame(gtx, th, u, f)
	if err := w.Frame(&ops); err != nil {
		t.Fatalf("离屏帧提交失败: %v", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: sz})
	if err := w.Screenshot(img); err != nil {
		t.Fatalf("离屏取像失败: %v", err)
	}

	// 底色 = 窗体铺底（windowBg；导航栏顶角无任何控件覆盖）；逐行统计页内容区与底色
	// 不同的像素（文字/控件/底板）。
	bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA)
	contentBottom := viewH - 100 // 全局底栏（≈64dp@1.25）之上 = 页内容区
	rowsWith := func(y0, y1 int) (rows, px int) {
		for y := y0; y < y1; y++ {
			n := 0
			for x := int(navWpx) + 2; x < viewW; x++ {
				if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) != bg {
					n++
				}
			}
			if n > 0 {
				rows++
				px += n
			}
		}
		return rows, px
	}

	halfRows, halfPx := rowsWith(contentBottom/2, contentBottom)
	if halfRows < 20 || halfPx < 300 {
		t.Errorf("页内容区下半幅内容过少：有内容的行数=%d 像素=%d（want ≥20 行 / ≥300 px）"+
			"——列表很可能按错误轴向迭代、只画出首行（S4c 根因）", halfRows, halfPx)
	}
	// 底带（内容区末 12%）也必须有内容：轴向错配时内容在首行之后即断流。
	band := contentBottom - contentBottom*12/100
	bottomRows, _ := rowsWith(band, contentBottom)
	if bottomRows < 5 {
		t.Errorf("页内容区底带（末 12%%）内容行数=%d，want ≥5——行列表未填满视口", bottomRows)
	}
	// 上幅有内容（正向对照：窗体确实渲染了表单而非全空）。
	topRows, _ := rowsWith(0, contentBottom/4)
	if topRows < 5 {
		t.Errorf("页内容区上幅内容行数=%d，want ≥5——表单未渲染", topRows)
	}
}

// TestSettingsRowGeometryTracksDensity 多度量几何回归（D113 验收补充）：行几何必须随
// Metric 缩放——固定裸 px 定高在高 DPI 下裁掉编辑器文字底部（CJK 字面行高 25px > 内容区
// 20px @1.25）、标签列变窄导致「接口地址」折行。判据取用户可见事实的几何等价物：
// ① editorBox 返回高 ≥ 编辑器同度量实测行高 + 2×Dp(8)（不裁字）且 ≥ Dp(fieldH)（按压区下限）；
// ② fieldRow 控件起点 = 行宽 − Dp(fieldLabelW) − Dp(10)（标签列随密度缩放，不折行）。
// 裸 px 实现（旧）在 dpi≠1 时两项皆红；dpi=1 恒绿作正向对照。
func TestSettingsRowGeometryTracksDensity(t *testing.T) {
	th := testTheme()
	const rowW = 600
	for _, dpi := range []float32{1, 1.25, 1.5, 2} {
		t.Run(fmt.Sprintf("dpi%.2f", dpi), func(t *testing.T) {
			m := unit.Metric{PxPerDp: dpi, PxPerSp: dpi}

			// 编辑器自然行高（同度量独立实例实测——编辑器有状态，不与被测框共用）。
			var mops op.Ops
			med := &widget.Editor{}
			med.SingleLine = true
			edDims := material.Editor(th, med, "").Layout(layout.Context{
				Ops: &mops, Metric: m,
				Constraints: layout.Constraints{Max: image.Pt(rowW, 1e6)},
			})

			var ops op.Ops
			gtx := layout.Context{
				Ops: &ops, Metric: m,
				Constraints: layout.Constraints{Max: image.Pt(rowW, 1e6)},
			}
			ed := &widget.Editor{}
			ed.SingleLine = true
			box := editorBox(th, ed, "hint")(gtx)
			if want := edDims.Size.Y + 2*gtx.Dp(8); box.Size.Y < want {
				t.Errorf("dpi %.2f: 编辑框高 %dpx < 编辑器行高 %dpx + 2×Dp(8)——文字底部被裁（D113 验收补充）",
					dpi, box.Size.Y, want)
			}
			if want := gtx.Dp(fieldH); box.Size.Y < want {
				t.Errorf("dpi %.2f: 编辑框高 %dpx < 下限 Dp(fieldH)=%dpx", dpi, box.Size.Y, want)
			}

			// 标签列宽（探针控件记录收到的 Flexed 约束）。口径：fieldLabelW 含右间距
			//（Inset{Right:10} 把标签 Min 压到 fieldLabelW−10，rigid 总占位 = fieldLabelW），
			// 故控件起点 = 行宽 − Dp(fieldLabelW)——随密度缩放即不折行。
			var gotCtrlMax int
			fieldRow(gtx, th, "接口地址", func(gtx layout.Context) layout.Dimensions {
				gotCtrlMax = gtx.Constraints.Max.X
				return layout.Dimensions{}
			})
			if want := rowW - gtx.Dp(fieldLabelW); gotCtrlMax != want {
				t.Errorf("dpi %.2f: 控件起点宽 %dpx，want %dpx（行宽 − Dp(fieldLabelW)）——标签列未随密度缩放",
					dpi, gotCtrlMax, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 分页导航（S4d/D142）
// ---------------------------------------------------------------------------

// TestSettingsPageNamesContract 页名集与页序为契约（导航项与页标题同源，D162 起由注册表
// 推导）；每页行计划非空且首行为页标题——防「某页忘了挂行」；提示词行只挂「系统提示词」
// 页（S7b/D145）。另锁两条注册表结构不变量：页常量与注册表长度一致、Key 唯一。
func TestSettingsPageNamesContract(t *testing.T) {
	want := []string{"常规", "模型", "密钥库", "对话", "系统提示词", "工具与 MCP", "高级"}
	if settingsPageCount != len(want) {
		t.Fatalf("settingsPageCount = %d, want %d", settingsPageCount, len(want))
	}
	if int(pageConstCount) != settingsPageCount {
		t.Fatalf("pageConstCount = %d, settingsPageCount = %d（加页必须同时改常量块与注册表）",
			int(pageConstCount), settingsPageCount)
	}
	for i, name := range want {
		if settingsPageNames[i] != name {
			t.Errorf("页 %d 名 = %q, want %q", i, settingsPageNames[i], name)
		}
		if settingsPages[i].Key() != name {
			t.Errorf("注册表页 %d Key = %q, want %q", i, settingsPages[i].Key(), name)
		}
		if i > 0 && settingsPages[i].Key() == settingsPages[i-1].Key() {
			t.Errorf("注册表页 %d 与上一页 Key 重复（%q）", i, settingsPages[i].Key())
		}
	}
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default"}},
		ProviderMgr:   &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "a", Primary: true}}},
	})
	f := newSettingsForm(u)
	for p := 0; p < settingsPageCount; p++ {
		rows := f.pageRows(p)
		if len(rows) < 2 {
			t.Fatalf("页 %d（%s）行计划过短: %d 行", p, settingsPageNames[p], len(rows))
		}
		if rows[0].kind != rowTitle {
			t.Errorf("页 %d（%s）首行 = %v, want rowTitle", p, settingsPageNames[p], rows[0].kind)
		} else if rows[0].text != settingsPageNames[p] {
			t.Errorf("页 %d 标题文案 = %q, want %q", p, rows[0].text, settingsPageNames[p])
		}
	}

	// 「模型」页必须含模型名行（D110② 起该行在 UI 上失位，S4d 补回）。
	found := false
	for _, r := range f.pageRows(pageModel) {
		if r.kind == rowModelName {
			found = true
		}
	}
	if !found {
		t.Error("「模型」页缺 rowModelName 行（当前模型名在 UI 上无处可改）")
	}
	// S7b/D145：提示词行归「系统提示词」页；「对话」页只留限额——防两页都挂或都不挂
	// （拆页时最容易犯的错是把 pageChat 的行计划整体复制一份而忘了删原处）。行数随快照
	// 变化的模块/引用/内置行在 TestPromptRowsLiveOnPromptPage 里带快照判定。
	promptFixed := []rowKind{rowSysPrompt, rowPromptNote, rowPromptGrid, rowPromptRefAdd, rowPromptPreview}
	has := func(page int, kind rowKind) bool {
		for _, r := range f.pageRows(page) {
			if r.kind == kind {
				return true
			}
		}
		return false
	}
	for _, k := range promptFixed {
		if !has(pagePrompt, k) {
			t.Errorf("「系统提示词」页缺行 %v", k)
		}
		if has(pageChat, k) {
			t.Errorf("「对话」页仍有提示词行 %v（D145 后应只留限额）", k)
		}
	}
	for _, k := range []rowKind{rowMaxTurns, rowMaxCtx, rowCompact, rowToolChars, rowToolTo} {
		if !has(pageChat, k) {
			t.Errorf("「对话」页缺限额行 %v", k)
		}
		if has(pagePrompt, k) {
			t.Errorf("「系统提示词」页混入限额行 %v", k)
		}
	}
}

// TestSettingsNativeRowsCoveredEveryPage D162：**行型覆盖**——每页 Rows() 产出的每一行
// 都必须被「框架通用行型」或「该页 Body」认领。改前未认领的行静默落进 rowBody 的 default
// 兜底、被画成别的控件（高级页 raw JSON 编辑器）；这里把那条静默去掉，用本测试响亮兜住：
// 新增行型忘了在对应页分派 = 本测试立刻红。
func TestSettingsNativeRowsCoveredEveryPage(t *testing.T) {
	secretsAdmin := &fakeSecretsAdmin{snap: SecretsSnapshot{Entries: []SecretEntry{{Name: "AQUARIUS_OPENAI_API_KEY", Mask: "••••••1234"}}}}
	secretsAdmin.rev = 1 // 让开窗/帧首同步即采纳（Revision 变了）
	u := newHeadless(t, Options{
		Settings:    func() SettingsSnapshot { return promptSnapshot() },
		Tools:       &fakeToolsAdmin{snap: ToolsSnapshot{Revision: 1, Builtins: []BuiltinToolState{{Name: "think", Enabled: true}}, Plugins: []PluginToolState{{Name: "p", Source: "config", Enabled: true}}}},
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{{Name: "a", Primary: true}}},
		Secrets:     secretsAdmin,
	})
	f := newSettingsForm(u)
	f.page = pageTools
	f.syncToolsSnapshot() // 帧首同款：同步工具快照（铺出内置/插件行）
	f.toolsStatus = "ok"  // 让状态行也进行表（覆盖 rowToolsStatus 行型）
	f.page = pageSecrets
	f.syncSecretsSnapshot() // 同步密钥库快照（铺出条目行）
	f.secretsStatus = "ok"  // 覆盖 rowSecretStatus 行型
	th := testTheme()

	for p := 0; p < settingsPageCount; p++ {
		rows := f.pageRows(p)
		for i := range rows {
			var ops op.Ops
			gtx := layout.Context{
				Ops:         &ops,
				Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Constraints: layout.Constraints{Max: image.Pt(540, 400)},
			}
			if _, ok := frameworkRow(gtx, th, rows[i]); ok {
				continue
			}
			if _, ok := settingsPages[p].Body(gtx, th, f, rows, i); !ok {
				t.Errorf("页 %d（%s）第 %d 行（kind=%d）无人认领——加行型时忘了在对应页 Body 分派",
					p, settingsPageNames[p], i, rows[i].kind)
			}
		}
	}
}

func TestPromptRowsLiveOnPromptPage(t *testing.T) {
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return promptSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	count := func(page int, kinds ...rowKind) int {
		n := 0
		for _, r := range f.pageRows(page) {
			for _, k := range kinds {
				if r.kind == k {
					n++
				}
			}
		}
		return n
	}
	kinds := []rowKind{rowPromptGrid, rowPromptRef, rowPromptBuiltin, rowPromptPreview}
	for _, page := range []int{pageChat, pageGeneral, pageModel, pageSecrets, pageTools, pageAdv} {
		if n := count(page, kinds...); n != 0 {
			t.Errorf("页 %s 出现了 %d 条提示词行（应只在「系统提示词」页）", settingsPageNames[page], n)
		}
	}
	if got, want := count(pagePrompt, rowPromptGrid), 1; got != want {
		t.Errorf("「系统提示词」页模块网格行 = %d, want %d", got, want)
	}
	if got, want := count(pagePrompt, rowPromptRef), 2; got != want {
		t.Errorf("「系统提示词」页引用行 = %d, want %d", got, want)
	}
	if got, want := count(pagePrompt, rowPromptBuiltin), 2; got != want {
		t.Errorf("「系统提示词」页内置模块行 = %d, want %d", got, want)
	}
	if got, want := count(pagePrompt, rowPromptPreview), 1; got != want {
		t.Errorf("「系统提示词」页预览行 = %d, want %d", got, want)
	}
}

// fakeToolsAdmin ToolsAdmin 测试替身（可变快照 + 动作记录）。
type fakeToolsAdmin struct {
	mu       sync.Mutex
	snap     ToolsSnapshot
	actions  []string
	grantErr error
}

func (a *fakeToolsAdmin) Snapshot() ToolsSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

func (a *fakeToolsAdmin) SetBuiltinEnabled(name string, enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Builtins {
		if a.snap.Builtins[i].Name == name {
			a.snap.Builtins[i].Enabled = enabled
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "builtin:"+name)
	return nil
}

func (a *fakeToolsAdmin) SetPluginEnabled(name string, enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name {
			a.snap.Plugins[i].Enabled = enabled
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "plugin:"+name)
	return nil
}

// GrantCap 手动预授权：真的把能力加回 Granted（供「再点 = 撤销」的分支判定）。
func (a *fakeToolsAdmin) GrantCap(name string, cap string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name && !hasStr(a.snap.Plugins[i].Granted, cap) {
			a.snap.Plugins[i].Granted = append(a.snap.Plugins[i].Granted, cap)
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "grant:"+name+":"+cap)
	return a.grantErr
}

// RevokeGrant 撤销能力：真实语义 = 同时停用插件 + 清该授权（再启用重走确认）。
func (a *fakeToolsAdmin) RevokeGrant(name string, cap string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name {
			a.snap.Plugins[i].Enabled = false
			a.snap.Plugins[i].Granted = delStr(a.snap.Plugins[i].Granted, cap)
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "revoke:"+name+":"+cap)
	return nil
}

// RevokeTrust 撤销启动信任：清 Trusted + 状态转 revoked（撤销入口随之消失）。
func (a *fakeToolsAdmin) RevokeTrust(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.snap.Plugins {
		if a.snap.Plugins[i].Name == name {
			a.snap.Plugins[i].Trusted = false
			a.snap.Plugins[i].Status = "revoked"
		}
	}
	a.snap.Revision++
	a.actions = append(a.actions, "trust:"+name)
	return nil
}

func (a *fakeToolsAdmin) Logs(name string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "logs:"+name)
	return "日志：" + name, nil
}

// actionCount / actionList 加锁读动作记录（动作 goroutine 写，测试 goroutine 读）。
func (a *fakeToolsAdmin) actionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.actions)
}

func (a *fakeToolsAdmin) actionList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.actions...)
}

// hasStr / delStr 小清单辅助。
func hasStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func delStr(list []string, s string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// TestSettingsWindowGeometry S4d/D142：分页后窗加宽到 720×560（min 620×420）——左侧
// 140dp 导航栏之外仍留 ≈552dp 页内容区（改前 = 532dp，不得倒退）。
func TestSettingsWindowGeometry(t *testing.T) {
	w, h, minW, minH := winSettings.geometry()
	if w != 720 || h != 560 || minW != 620 || minH != 420 {
		t.Fatalf("几何 = (%d,%d,%d,%d), want (720,560,620,420)", w, h, minW, minH)
	}
	if got := w - int(navColumnDp); got < 532 {
		t.Errorf("页内容区宽 = %d, want ≥532（不低于分页前）", got)
	}
}

// TestSettingsNavClickSwitchesPage 导航项点击 = 切页（帧首消费，本帧即按新页渲染）；
// 切页不动任何控件状态（跨页编辑保留），且每页各自的滚动列表独立。
func TestSettingsNavClickSwitchesPage(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
	})
	f := newSettingsForm(u)
	th := testTheme()
	frame := func() {
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(720, 560)),
		}, th, u, f)
	}

	frame()
	if f.page != pageGeneral {
		t.Fatalf("初始页 = %d, want %d（常规）", f.page, pageGeneral)
	}
	f.model.SetText("m-keep") // 跨页编辑保留探针
	f.pages[pageGeneral].ScrollBy(3)
	frame()
	if f.pages[pageGeneral].Position.First < 3 {
		t.Fatalf("常规页滚动后 First = %d, want ≥3", f.pages[pageGeneral].Position.First)
	}
	f.navBtns[pageAdv].Click()
	frame()
	if f.page != pageAdv {
		t.Fatalf("点击「高级」导航项后 page = %d, want %d", f.page, pageAdv)
	}
	if f.model.Text() != "m-keep" {
		t.Errorf("切页后模型名档 = %q, want m-keep（跨页编辑应保留）", f.model.Text())
	}
	// 滚动位置按页独立：别的页没滚动过，原页位置保留。
	if got := f.pages[pageAdv].Position.First; got != 0 {
		t.Errorf("高级页未滚动过，First = %d, want 0（每页独立列表）", got)
	}
	if got := f.pages[pageGeneral].Position.First; got < 3 {
		t.Errorf("切页后常规页滚动位置丢失：First = %d, want ≥3", got)
	}
	// 回切：导航项可反复使用，页状态、控件状态与滚动位置都还在。
	f.navBtns[pageModel].Click()
	frame()
	if f.page != pageModel {
		t.Fatalf("回切「模型」后 page = %d, want %d", f.page, pageModel)
	}
	if f.model.Text() != "m-keep" {
		t.Errorf("回切后模型名档 = %q, want m-keep", f.model.Text())
	}
	f.navBtns[pageGeneral].Click()
	frame()
	if got := f.pages[pageGeneral].Position.First; got < 3 {
		t.Errorf("回切常规页后滚动位置丢失：First = %d, want ≥3", got)
	}
}

// TestSettingsPagesRenderContent 逐页离屏单帧渲染：每页**内容区**必须确有内容
// （S4c 同款防线的按页复制——分页后「某页空白」是新的可能故障）。
func TestSettingsPagesRenderContent(t *testing.T) {
	const (
		viewW, viewH = 900, 700 // 真窗首帧像素尺寸（720×560dp @1.25）
		pxPerDp      = 1.25
		navWpx       = 175 // 140dp × 1.25
		barPx        = 100 // 全局底栏（≈64dp@1.25）占位
	)
	w, err := headless.NewWindow(viewW, viewH)
	if err != nil {
		t.Skipf("离屏渲染不可用（无 GPU/软件后端）: %v", err)
	}
	defer w.Release()

	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot {
			return SettingsSnapshot{
				Permission: "strict", Model: "m1", Theme: "system",
				Scale: 1.0, FontSize: 15,
				MaxTurns: 8, MaxContextTokens: 128000, CompactThreshold: 0.8,
				ToolOutputChars: 4000, ToolTimeoutSec: 120,
				SystemPrompt: "persona", MCPServers: []string{"docs"},
				RawJSON: `{"model":{"name":"m1"}}`,
			}
		},
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Profiles:      &fakeProfiles{snap: ProfilesSnapshot{Current: "default", Items: []string{"default"}}},
		ProviderMgr: &fakeProviderMgr{snap: []ProviderSnapshot{
			{Name: "openai", BaseURL: "https://api.openai.com/v1", Models: "m1", Primary: true},
		}},
	})
	f := newSettingsForm(u)
	th := testTheme()

	for p := 0; p < settingsPageCount; p++ {
		var ops op.Ops
		gtx := layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: pxPerDp, PxPerSp: pxPerDp},
			Constraints: layout.Exact(image.Pt(viewW, viewH)),
		}
		f.page = p
		settingsFrame(gtx, th, u, f)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("页 %d 离屏帧提交失败: %v", p, err)
		}
		img := image.NewRGBA(image.Rectangle{Max: image.Pt(viewW, viewH)})
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("页 %d 离屏取像失败: %v", p, err)
		}
		bg := color.RGBAModel.Convert(img.At(2, 2)).(color.RGBA) // 导航栏顶角 = windowBg
		px := 0
		for y := 0; y < viewH-barPx; y++ { // 底栏之上 = 页内容区（底栏恒有内容，不作判据）
			for x := navWpx + 2; x < viewW; x++ {
				if color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) != bg {
					px++
				}
			}
		}
		t.Logf("页 %d（%s）内容区非底色像素 = %d", p, settingsPageNames[p], px)
		if px < 1000 {
			t.Errorf("页 %d（%s）内容区非底色像素 = %d，want ≥1000——该页可能空白",
				p, settingsPageNames[p], px)
		}
	}
}

// TestRowInsetContract 行腔留白是六页共用的单一旋钮（D148）。
//
// 用户对「模块表单」与「运行卡」两处提了同一诉求（上下需要更多留白）——那说明是
// 全局密度问题。这条测试锁住「只在一个地方定义」这个事实：如果有人把某页改回自己的
// 内边距，六页就会重新变得疏密不一，而肉眼很难逐页比对发现。
func TestRowInsetContract(t *testing.T) {
	// 14 是修订前的值（文字贴描边）；抬到 18 是本决策的可观测结果。
	if rowInsetDp < 18 {
		t.Fatalf("rowInsetDp = %d，want ≥18（D148：行腔留白不得退回贴边密度）", rowInsetDp)
	}
	// 上界防止后续为了「更松」一路加码把设置页撑成一屏两行。
	if rowInsetDp > 24 {
		t.Fatalf("rowInsetDp = %d，want ≤24（再大设置页一屏放不下三行）", rowInsetDp)
	}
}

// TestPromptPageHasModuleGridRow 「系统提示词」页的模块库整块占**一个**行（D148），
// 且模块数据一行不少。网格化后如果网格自己被合并/吞掉，保存时就会丢掉用户模块
// ——这是最容易在「改布局」名义下犯的错。
//
// 行数恒为 1：模块增删只改网格内容、不改行计划（这正是「一屏看到全部模块」的实现
// 方式），所以断言的是「网格行存在且唯一」，不是「行数 == 模块数」。
func TestPromptPageHasModuleGridRow(t *testing.T) {
	f := &settingsForm{page: pagePrompt}
	for _, n := range []string{"house", "identity", "style"} {
		pf := &promptModuleForm{}
		pf.name.SetText(n)
		f.promptMods = append(f.promptMods, pf)
	}
	rows := f.pageRows(pagePrompt)
	grid := 0
	for _, r := range rows {
		if r.kind == rowPromptGrid {
			grid++
		}
	}
	if grid != 1 {
		t.Fatalf("rowPromptGrid 行数 = %d，want 1（模块库整块一行）", grid)
	}
	// 模块数据本身必须原样留在表里（网格只改呈现）。
	if got := len(f.promptViews()); got != 3 {
		t.Fatalf("模块数 = %d，want 3（网格化不得吞模块）", got)
	}
}

// TestModuleGridMove 拖动落位 = 数组重排（D148）：从 i 挪到 j 后，原 i 的模块应
// 落在 j 位，其余保持相对顺序。
func TestModuleGridMove(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	build := func() *settingsForm {
		f := &settingsForm{}
		for _, n := range names {
			pf := &promptModuleForm{}
			pf.name.SetText(n)
			f.promptMods = append(f.promptMods, pf)
		}
		return f
	}
	cases := []struct {
		from, to int
		want     []string
	}{
		{0, 3, []string{"b", "c", "d", "a", "e"}}, // 往后移：中间元素左移填空
		{4, 1, []string{"a", "e", "b", "c", "d"}}, // 往前移：中间元素右移腾位
		{2, 2, []string{"a", "b", "c", "d", "e"}}, // 原地：no-op
		{0, 4, []string{"b", "c", "d", "e", "a"}},
		{3, 0, []string{"d", "a", "b", "c", "e"}},
	}
	for _, tc := range cases {
		f := build()
		f.movePromptModule(tc.from, tc.to)
		got := make([]string, 0, len(f.promptMods))
		for _, pf := range f.promptMods {
			got = append(got, pf.name.Text())
		}
		if len(got) != len(tc.want) {
			t.Fatalf("move(%d→%d)：模块数 = %d，want %d", tc.from, tc.to, len(got), len(tc.want))
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("move(%d→%d) = %v，want %v", tc.from, tc.to, got, tc.want)
			}
		}
	}
}

// TestModuleEditorRenameSyncsRefs 改名要同步人格引用（D148）。
//
// 模块名就是身份：引用列表里存的是名字，改名后不同步 = 组合里那一段凭空消失，
// 而表单上看着一切正常（引用名还在，只是再也对不上任何模块）。
func TestModuleEditorRenameSyncsRefs(t *testing.T) {
	f := &settingsForm{page: pagePrompt}
	pf := &promptModuleForm{}
	pf.name.SetText("house")
	pf.text.SetText("HOUSE:")
	f.promptMods = append(f.promptMods, pf)
	for _, n := range []string{"house", "style"} {
		rf := &promptRefForm{}
		rf.name.SetText(n)
		f.promptRefs = append(f.promptRefs, rf)
	}

	f.openModuleEditor(0)
	f.modEdit.name.SetText("household")
	f.commitModuleEditor()

	if got := f.promptMods[0].name.Text(); got != "household" {
		t.Fatalf("模块名 = %q，want %q", got, "household")
	}
	if got := f.promptRefs[0].name.Text(); got != "household" {
		t.Fatalf("引用 0 = %q，want %q（改名须同步引用）", got, "household")
	}
	if got := f.promptRefs[1].name.Text(); got != "style" {
		t.Fatalf("引用 1 = %q，want %q（无关引用不得被改）", got, "style")
	}
	if f.modEdit.open {
		t.Fatal("保存后弹窗应关闭")
	}
}

// TestModuleEditorAddAndDelete 弹窗的新增/删除（D148）：新增追加一个模块行，
// 删除按弹窗打开时的下标移除。
func TestModuleEditorAddAndDelete(t *testing.T) {
	f := &settingsForm{page: pagePrompt}
	f.openModuleEditor(-1)
	f.modEdit.name.SetText("fresh")
	f.commitModuleEditor()
	if len(f.promptMods) != 1 || f.promptMods[0].name.Text() != "fresh" {
		t.Fatalf("新增后模块 = %v，want [fresh]", f.promptViews())
	}
	f.openModuleEditor(0)
	f.delPromptModule(f.modEdit.idx)
	f.closeModuleEditor()
	if len(f.promptMods) != 0 {
		t.Fatalf("删除后模块数 = %d，want 0", len(f.promptMods))
	}
}

// TestModuleMetaRefCount 磁贴元信息给出「组/叶 + 被引用次数」（D148）：
// 改了模块却不知道有没有被用上，就只能靠记忆去比对引用段。
func TestModuleMetaRefCount(t *testing.T) {
	pf := &promptModuleForm{}
	pf.name.SetText("house")
	pf.children.SetText("a, b")
	refs := func(names ...string) []*promptRefForm {
		out := make([]*promptRefForm, 0, len(names))
		for _, n := range names {
			rf := &promptRefForm{}
			rf.name.SetText(n)
			out = append(out, rf)
		}
		return out
	}
	if got, want := moduleMeta(pf, refs()), "组头 · 未引用"; got != want {
		t.Fatalf("meta = %q，want %q", got, want)
	}
	if got, want := moduleMeta(pf, refs("house", "house", "style")), "组头 · 被引用 2 次"; got != want {
		t.Fatalf("meta = %q，want %q", got, want)
	}
	leaf := &promptModuleForm{}
	leaf.name.SetText("style")
	if got, want := moduleMeta(leaf, refs("style")), "正文 · 被引用 1 次"; got != want {
		t.Fatalf("meta = %q，want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// 工具与 MCP 页（S6/D143）
// ---------------------------------------------------------------------------

// toolsPageUI 搭一个带假 ToolsAdmin 的 headless 设置窗。
func toolsPageUI(t *testing.T, snap ToolsSnapshot) (*UI, *settingsForm, *fakeToolsAdmin) {
	t.Helper()
	ta := &fakeToolsAdmin{snap: snap}
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return fillSnapshot() },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { return nil, nil },
		Tools:         ta,
	})
	f := newSettingsForm(u)
	return u, f, ta
}

func toolsFrame(f *settingsForm, u *UI) {
	var ops op.Ops
	settingsFrame(layout.Context{
		Ops:         &ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(720, 560)),
	}, testTheme(), u, f)
}

// clickToolAction 工具页动作的确定性驱动：清状态行 → 点击按钮 → 轮询帧直到「假实现已
// 记录下一个动作」且「状态行已回灌」。帧内顺序 = 分发 → 排空 → 快照同步，故回灌当帧快照
// 即已更新（连续动作的分支判定据此可证）。
func clickToolAction(t *testing.T, u *UI, f *settingsForm, ta *fakeToolsAdmin, key, wantStatus string) {
	t.Helper()
	btn := f.toolBtns[key]
	if btn == nil {
		t.Fatalf("按钮 %q 未渲染（已渲染 %d 个）", key, len(f.toolBtns))
	}
	f.toolsStatus, f.toolsErr = "", false
	wantN := ta.actionCount() + 1
	btn.Click()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		toolsFrame(f, u)
		if ta.actionCount() >= wantN && f.toolsStatus != "" {
			if f.toolsErr {
				t.Fatalf("%s 失败：%s", key, f.toolsStatus)
			}
			if !strings.Contains(f.toolsStatus, wantStatus) {
				t.Fatalf("%s 状态行 = %q, want 含 %q", key, f.toolsStatus, wantStatus)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s 超时：actions=%v status=%q", key, ta.actionList(), f.toolsStatus)
}

// TestToolsPageSnapshotSync 工具页动作全链路：点击 → 异步执行 → 状态行回灌 + 快照重建
// （内置启用 / 插件启停 / 逐能力撤销与预授权 / 撤销信任 / 日志）。假 ToolsAdmin 带真实
// 状态语义（撤销清授权 + 停用、预授权加回），故「同一按钮再点翻转分支」可证。
func TestToolsPageSnapshotSync(t *testing.T) {
	restoreLight(t)
	snap := ToolsSnapshot{Revision: 1,
		Builtins: []BuiltinToolState{{Name: "think", Label: "思考草稿", Enabled: false}},
		Plugins: []PluginToolState{
			{Name: "web", Source: "config", Transport: "stdio", Status: "ready", Enabled: true,
				Capabilities: []string{"network"}, Granted: []string{"network"}},
			{Name: "secure", Source: "plugins", Transport: "stdio", Status: "ready", Enabled: true, Trusted: true},
		},
	}
	u, f, ta := toolsPageUI(t, snap)
	f.navBtns[pageTools].Click()
	toolsFrame(f, u)
	if f.page != pageTools {
		t.Fatalf("page = %d, want 工具与 MCP", f.page)
	}
	// 首帧即同步快照（Revision 1 ≠ 初值 0）。
	if len(f.toolsSnap.Builtins) != 1 || len(f.toolsSnap.Plugins) != 2 {
		t.Fatalf("快照未同步: %+v", f.toolsSnap)
	}

	// ① 内置启用（think 当前禁用 → 点击 = 启用）。
	clickToolAction(t, u, f, ta, "b:think", "内置工具已更新")
	if !f.toolsSnap.Builtins[0].Enabled {
		t.Fatalf("启用后快照未更新: %+v", f.toolsSnap.Builtins[0])
	}

	// ② 插件停用（web 当前启用 → 点击 = 停用）。
	clickToolAction(t, u, f, ta, "p:web:toggle", "插件状态已更新")
	if f.toolsSnap.Plugins[0].Enabled {
		t.Fatalf("停用后快照未更新: %+v", f.toolsSnap.Plugins[0])
	}

	// ③ 逐能力撤销（network 已授权 → 撤销）：假实现同时清授权 + 停用。
	clickToolAction(t, u, f, ta, "p:web:cap:network", "已撤销能力")
	if hasStr(f.toolsSnap.Plugins[0].Granted, "network") || f.toolsSnap.Plugins[0].Enabled {
		t.Fatalf("撤销后快照未更新: %+v", f.toolsSnap.Plugins[0])
	}

	// ④ 同一按钮再点 = 未授权 → 手动预授权（分支随快照翻转）。
	clickToolAction(t, u, f, ta, "p:web:cap:network", "已预授权")
	if !hasStr(f.toolsSnap.Plugins[0].Granted, "network") {
		t.Fatalf("预授权后快照未更新: %+v", f.toolsSnap.Plugins[0])
	}

	// ⑤ 打开日志。
	clickToolAction(t, u, f, ta, "p:web:logs", "日志：web")

	// ⑥ 撤销启动信任（入口仅 source=plugins 且 Trusted 时渲染）。
	clickToolAction(t, u, f, ta, "p:secure:trust", "信任已撤销")
	if f.toolsSnap.Plugins[1].Trusted {
		t.Fatalf("撤销信任后快照未更新: %+v", f.toolsSnap.Plugins[1])
	}

	want := []string{
		"builtin:think", "plugin:web", "revoke:web:network",
		"grant:web:network", "logs:web", "trust:secure",
	}
	if got := ta.actionList(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("动作序列 = %v, want %v", got, want)
	}
}

// TestToolsPageNotWired 未接线（Tools = nil）：工具页只读占位，不空白。
func TestToolsPageNotWired(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	th := testTheme()
	f.navBtns[pageTools].Click()
	var ops op.Ops
	settingsFrame(layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}, th, u, f)
	if f.page != pageTools {
		t.Fatalf("page = %d", f.page)
	}
	rows := f.pageRows(pageTools)
	if len(rows) < 2 || rows[0].kind != rowTitle || rows[1].kind != rowToolsEmpty {
		t.Fatalf("未接线页行计划 = %+v, want 标题 + 占位", rows)
	}
}

// ---------------------------------------------------------------------------
// 密钥库页（S9-6b/D161）
// ---------------------------------------------------------------------------

// fakeSecretsAdmin SecretsAdmin 测试替身：内存键值表 + Revision 递增 + 动作记录。
// 语义对齐 secretbin：Put 空值/空名报错、Delete 不存在报错、快照按名字排序。
type fakeSecretsAdmin struct {
	mu      sync.Mutex
	snap    SecretsSnapshot
	rev     int64
	store   map[string]string
	actions []string
}

func (a *fakeSecretsAdmin) Snapshot() SecretsSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.snap
	s.Revision = a.rev
	return s
}

func (a *fakeSecretsAdmin) Put(name, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "put:"+name)
	if strings.TrimSpace(name) == "" {
		return errors.New("密钥名不可为空")
	}
	if strings.TrimSpace(value) == "" {
		return errors.New("值不可为空")
	}
	if a.store == nil {
		a.store = map[string]string{}
	}
	a.store[name] = value
	a.rebuild()
	return nil
}

func (a *fakeSecretsAdmin) Delete(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, "delete:"+name)
	if _, ok := a.store[name]; !ok {
		return errors.New("密钥库中已没有同名条目")
	}
	delete(a.store, name)
	a.rebuild()
	return nil
}

// rebuild 从 store 重建快照条目（按名字排序，掩码只出尾 4 位；与 secretbin 口径一致）。
func (a *fakeSecretsAdmin) rebuild() {
	names := make([]string, 0, len(a.store))
	for n := range a.store {
		names = append(names, n)
	}
	sort.Strings(names)
	a.snap.Entries = a.snap.Entries[:0]
	for _, n := range names {
		v := []rune(a.store[n])
		mask := strings.Repeat("•", 6)
		if len(v) >= 8 {
			mask += string(v[len(v)-4:])
		}
		a.snap.Entries = append(a.snap.Entries, SecretEntry{Name: n, Mask: mask})
	}
	a.rev++
}

func (a *fakeSecretsAdmin) actionList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.actions...)
}

// secretsPageUI 搭一个带假 SecretsAdmin 的 headless 设置窗。
func secretsPageUI(t *testing.T, snap SecretsSnapshot, store map[string]string) (*UI, *settingsForm, *fakeSecretsAdmin) {
	t.Helper()
	sa := &fakeSecretsAdmin{snap: snap, store: store}
	if store == nil {
		sa.store = map[string]string{}
	}
	// Revision 起点 0 会被 newSettingsForm 当成「未变」而跳过——带初始条目的用例把
	// rev 抬到 1，开窗即采纳快照（帧首同款语义）。
	if len(snap.Entries) > 0 {
		sa.rev = 1
	}
	u := newHeadless(t, Options{
		Settings: func() SettingsSnapshot { return fillSnapshot() },
		Secrets:  sa,
	})
	f := newSettingsForm(u)
	return u, f, sa
}

// TestSecretsPageRows 密钥库页行计划：有条目时每有一条 → 一行（名字），再加新增区。
func TestSecretsPageRows(t *testing.T) {
	u, f, _ := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{
		{Name: "AQUARIUS_OPENAI_API_KEY", Mask: "••••••1234"},
		{Name: "MCP_DOCS_TOKEN", Mask: "••••••"},
	}}, nil)
	_ = u
	f.secretsSnap = f.secrets.Snapshot() // 铺快照（帧首同款）
	var names []string
	for _, r := range f.pageRows(pageSecrets) {
		if r.kind == rowSecretEntry {
			names = append(names, r.text)
		}
	}
	if len(names) != 2 || names[0] != "AQUARIUS_OPENAI_API_KEY" || names[1] != "MCP_DOCS_TOKEN" {
		t.Fatalf("条目行 = %v, want 两条按名排", names)
	}
}

// TestSecretsPagePut 新增/覆盖：填入键名与值 → 存入 → 假管理面记录 + 状态行含名字 +
// **值字段恒清空**（值永不回显）。
func TestSecretsPagePut(t *testing.T) {
	restoreLight(t)
	u, f, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
	f.secretName.SetText("AQUARIUS_OPENAI_API_KEY")
	f.secretValue.SetText("sk-secret-value-1234")
	u.secretsAction(f, "put", "")

	if got := sa.actionList(); len(got) != 1 || got[0] != "put:AQUARIUS_OPENAI_API_KEY" {
		t.Fatalf("动作 = %v", got)
	}
	if f.secretsErr || !strings.Contains(f.secretsStatus, "AQUARIUS_OPENAI_API_KEY") {
		t.Fatalf("反馈 = (%v, %q)", f.secretsErr, f.secretsStatus)
	}
	if f.secretsStatus == "" || strings.Contains(f.secretsStatus, "sk-secret-value-1234") {
		t.Fatalf("状态行不得回显值：%q", f.secretsStatus)
	}
	if f.secretValue.Text() != "" {
		t.Fatalf("值字段应恒清空，实为 %q", f.secretValue.Text())
	}
	if f.secretName.Text() != "" {
		t.Fatalf("成功后键名应清空，实为 %q", f.secretName.Text())
	}
	// 快照已刷新（Revision 变了 → 条目行出现）。
	if len(f.secretsSnap.Entries) != 1 || f.secretsSnap.Entries[0].Name != "AQUARIUS_OPENAI_API_KEY" {
		t.Fatalf("快照未刷新: %+v", f.secretsSnap)
	}
}

// TestSecretsPagePutValidation 空名/空值拦截：不触达管理面，值字段仍清空（不留在框里）。
func TestSecretsPagePutValidation(t *testing.T) {
	restoreLight(t)
	cases := []struct{ name, value, wantMsg string }{
		{"", "sk-x-1234", "密钥名"},
		{"KEY", "", "密钥值"},
	}
	for _, c := range cases {
		u, f, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
		f.secretName.SetText(c.name)
		f.secretValue.SetText(c.value)
		u.secretsAction(f, "put", "")
		if !f.secretsErr || !strings.Contains(f.secretsStatus, c.wantMsg) {
			t.Fatalf("%q/%q: 反馈 = (%v, %q), want 含 %q",
				c.name, c.value, f.secretsErr, f.secretsStatus, c.wantMsg)
		}
		if n := len(sa.actionList()); n != 0 {
			t.Fatalf("%q/%q: 不应触达管理面，实 %d 次", c.name, c.value, n)
		}
		if f.secretValue.Text() != "" {
			t.Fatalf("校验失败也应清空值字段：%q", f.secretValue.Text())
		}
	}
}

// TestSecretsPageMask 掩码口径：只出尾 4 位；值短于 8 位整条打码（长值不可整条暴露）。
func TestSecretsPageMask(t *testing.T) {
	_, _, sa := secretsPageUI(t, SecretsSnapshot{}, nil)
	if err := sa.Put("LONG", "sk-abcdefgh-9876"); err != nil {
		t.Fatal(err)
	}
	if err := sa.Put("SHORT", "abc"); err != nil {
		t.Fatal(err)
	}
	snap := sa.Snapshot()
	byName := map[string]string{}
	for _, e := range snap.Entries {
		byName[e.Name] = e.Mask
	}
	if got := byName["LONG"]; got != "••••••9876" {
		t.Fatalf("长值掩码 = %q, want ••••••9876", got)
	}
	if got := byName["SHORT"]; got != "••••••" {
		t.Fatalf("短值掩码 = %q, want 整条打码", got)
	}
	if strings.Contains(byName["LONG"], "abcdefgh") {
		t.Fatalf("掩码泄漏了中段值：%q", byName["LONG"])
	}
}

// TestSecretsPageDeleteNeedsConfirm 删除走行内二次确认：点「删除」只进确认态（不触达）；
// 点「取消」退回；点「确认删除」才真删。
func TestSecretsPageDeleteNeedsConfirm(t *testing.T) {
	restoreLight(t)
	u, f, sa := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{{Name: "K1", Mask: "••••••1234"}}}, map[string]string{"K1": "sk-1234abcd"})
	f.syncSecretsSnapshot() // 帧首同款：铺条目行与按钮
	th := testTheme()
	frame := func() {
		var ops op.Ops
		settingsFrame(layout.Context{
			Ops:         &ops,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(720, 560)),
		}, th, u, f)
	}
	f.page = pageSecrets
	frame()
	if f.secretBtns["del:K1"] == nil {
		t.Fatalf("删除按钮未渲染（已渲染 %d 个）", len(f.secretBtns))
	}

	// ① 点「删除」→ 进确认态，不触达管理面。
	f.secretBtns["del:K1"].Click()
	frame()
	if f.secretConfirm != "K1" {
		t.Fatalf("点删除后 secretConfirm = %q, want K1", f.secretConfirm)
	}
	if n := len(sa.actionList()); n != 0 {
		t.Fatalf("二次确认前不应删除，实 %d 次", n)
	}

	// ② 点「取消」→ 退出确认态。
	f.secretBtns["cancel:K1"].Click()
	frame()
	if f.secretConfirm != "" {
		t.Fatalf("取消后 secretConfirm = %q, want 空", f.secretConfirm)
	}
	if n := len(sa.actionList()); n != 0 {
		t.Fatalf("取消不应删除，实 %d 次", n)
	}

	// ③ 再点删除 → 确认删除 → 真删 + 快照条目消失。
	f.secretBtns["del:K1"].Click()
	frame()
	f.secretBtns["confirm:K1"].Click()
	frame()
	if got := sa.actionList(); len(got) != 1 || got[0] != "delete:K1" {
		t.Fatalf("确认删除动作 = %v", got)
	}
	if f.secretsErr || !strings.Contains(f.secretsStatus, "K1") {
		t.Fatalf("删除反馈 = (%v, %q)", f.secretsErr, f.secretsStatus)
	}
	if len(f.secretsSnap.Entries) != 0 {
		t.Fatalf("删除后快照条目 = %+v, want 空", f.secretsSnap.Entries)
	}
}

// TestSecretsPageConfirmClearedOnSnapshot 条目在别处被删（Revision 变化且条目消失）→
// 行内二次确认目标被清掉，免得确认按钮指着一条已不在的条目。
func TestSecretsPageConfirmClearedOnSnapshot(t *testing.T) {
	u, f, sa := secretsPageUI(t, SecretsSnapshot{Entries: []SecretEntry{{Name: "K1", Mask: "••••••1234"}}}, map[string]string{"K1": "sk-1234abcd"})
	f.syncSecretsSnapshot()
	f.secretConfirm = "K1"
	if err := sa.Delete("K1"); err != nil { // 外部删除（Revision 递增且 K1 消失）
		t.Fatal(err)
	}
	f.syncSecretsSnapshot()
	if f.secretConfirm != "" {
		t.Fatalf("条目消失后 secretConfirm = %q, want 空", f.secretConfirm)
	}
	_ = u
}

// TestSecretsPageNotWired 未接线（Secrets = nil）：只读占位 + 存入按钮禁用，动作报错。
func TestSecretsPageNotWired(t *testing.T) {
	restoreLight(t)
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return fillSnapshot() }})
	f := newSettingsForm(u)
	rows := f.pageRows(pageSecrets)
	found := false
	for _, r := range rows {
		if r.kind == rowSecretEmpty {
			found = true
		}
	}
	if len(rows) < 2 || rows[0].kind != rowTitle || !found {
		t.Fatalf("未接线页行计划 = %+v, want 标题 + 占位", rows)
	}
	f.secretName.SetText("X")
	f.secretValue.SetText("sk-val-1234")
	u.secretsAction(f, "put", "")
	if !f.secretsErr || !strings.Contains(f.secretsStatus, "未接线") {
		t.Fatalf("未接线反馈 = (%v, %q)", f.secretsErr, f.secretsStatus)
	}
}
