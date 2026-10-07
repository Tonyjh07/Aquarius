package uigui

// 设置窗 headless 测试（§15.5）：快照填表（含密钥不回显）、保存流（patch 组装 +
// 命令入队 + 主题/快捷键热生效）、错误与只读路径——全部无窗口。

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

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

// TestSettingsFrameRowsFillViewport 设置窗行列表真渲染回归（S4c 根因防线）。
//
// 复现过的故障：`settingsForm.list` 未设 `layout.Axis` → `layout.List` 按**水平**
// 轴向做视口预算（按视口宽 700px 而非视口高 775px 迭代），而内容仍按垂直堆叠绘制；
// 累计两行即判定"填满"，整个表单只画出首行，其余是空白窗底与越界的整宽底色带。
//
// 判据取用户可见事实而非内部结构：把设置窗按真窗尺寸/缩放离屏光栅化，要求窗体
// **下半幅与底带确有内容**（轴向错配时下半幅恒为纯底色）。参照实现 = 真窗首帧
// 700×775px（`winSettings` 几何 560×620dp × 1.25 缩放）。
func TestSettingsFrameRowsFillViewport(t *testing.T) {
	const (
		viewW, viewH = 700, 775 // 真窗首帧像素尺寸（560×620dp @1.25）
		pxPerDp      = 1.25     // 真窗 Metric（Win32 DPI 96→120 档实测值）
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
	th := newTheme()

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

	// 底色 = 窗体铺底（windowBg）；逐行统计与底色不同的像素（文字/控件/底板）。
	bg := color.RGBAModel.Convert(img.At(2, viewH-2)).(color.RGBA)
	rowsWith := func(y0, y1 int) (rows, px int) {
		for y := y0; y < y1; y++ {
			n := 0
			for x := 0; x < viewW; x++ {
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

	halfRows, halfPx := rowsWith(viewH/2, viewH)
	if halfRows < 20 || halfPx < 300 {
		t.Errorf("窗体下半幅内容过少：有内容的行数=%d 像素=%d（want ≥20 行 / ≥300 px）"+
			"——列表很可能按错误轴向迭代、只画出首行（S4c 根因）", halfRows, halfPx)
	}
	// 底带（末 12%）也必须有内容：轴向错配时内容在首行之后即断流。
	bottomRows, _ := rowsWith(viewH-viewH*12/100, viewH)
	if bottomRows < 5 {
		t.Errorf("窗底带（末 12%%）内容行数=%d，want ≥5——行列表未填满视口", bottomRows)
	}
	// 上幅有内容（正向对照：窗体确实渲染了表单而非全空）。
	topRows, _ := rowsWith(0, viewH/4)
	if topRows < 5 {
		t.Errorf("窗体上幅内容行数=%d，want ≥5——表单未渲染", topRows)
	}
}
