package uigui

// 设置窗 headless 测试（§15.5）核心档：快照填表（含密钥不回显）、保存流（patch 组装 +
// 命令入队 + 主题/快捷键热生效）、错误与只读路径——全部无窗口。
//
// 按页拆分的测试在 settings_<page>_test.go（profile / providers / page / tools / secrets /
// prompt / audio / web）；本文件只留跨页共用的核心流与测试基准快照。

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// snapProv 测试用 provider 快照（名字 / 端点 / 主标记 + 声明模型名；能力位与窗口留空，
// 需要时用例自己填）。
func snapProv(name, base string, primary bool, models ...string) ProviderSnapshot {
	s := ProviderSnapshot{Name: name, BaseURL: base, Primary: primary}
	for _, m := range models {
		s.Models = append(s.Models, ProviderModelSnapshot{Name: m, Enabled: true})
	}
	return s
}

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
// 主题对齐 system、档位 = 未设、无人值守为关。
func TestNewSettingsFormDefaults(t *testing.T) {
	u := newHeadless(t, Options{Settings: func() SettingsSnapshot { return SettingsSnapshot{} }})
	f := newSettingsForm(u)
	if f.perm.value() != "strict" {
		t.Errorf("空快照权限 = %q, want strict（perm.DefaultLevel）", f.perm.value())
	}
	if f.theme.value() != "system" || f.effort.value() != "" {
		t.Errorf("空快照枚举档: theme=%q effort=%q", f.theme.value(), f.effort.value())
	}
	if f.unattend.Value {
		t.Error("无人值守缺省应为关")
	}
}

// TestSettingsUnattendedRow D185：无人值守开关在「常规」页权限区、紧跟权限等级行
// （同一个议题的开关不该散到别的页）；快照 true 时勾上、保存回写 patch。
func TestSettingsUnattendedRow(t *testing.T) {
	var got SettingsPatch
	snap := fillSnapshot()
	snap.Unattended = true
	u := newHeadless(t, Options{
		Settings:      func() SettingsSnapshot { return snap },
		ApplySettings: func(p SettingsPatch) ([]port.Command, error) { got = p; return nil, nil },
	})
	f := newSettingsForm(u)
	rows := f.pageRows(pageGeneral)
	idx := -1
	for i, r := range rows {
		if r.kind == rowUnattended {
			idx = i
			break
		}
	}
	if idx <= 0 {
		t.Fatalf("「常规」页缺 rowUnattended（idx=%d）", idx)
	}
	if prev := rows[idx-1].kind; prev != rowPerm {
		t.Fatalf("无人值守行应紧跟权限等级行：前一行 = %v", prev)
	}
	if !f.unattend.Value {
		t.Fatal("快照 Unattended=true 时开关应勾上")
	}
	u.saveSettings(f)
	if !got.Unattended {
		t.Fatal("保存应把开关状态带进 patch")
	}
	// 关掉再保存 → patch 反映关（布尔恒写，不做「留空 = 不改」）。
	f.unattend.Value = false
	u.saveSettings(f)
	if got.Unattended {
		t.Fatal("取消勾选后 patch 应为 false")
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

// TestPatchHasNoSideEffects 采集与提交分成两段后，`patch` 必须是纯读：非法输入只返回错误，
// 不碰写回调、不投递事件、不动快照基准；合法输入也只产出 patch。保存流之所以能先拦后提交，
// 靠的就是这一条——校验失败时磁盘与界面状态都不该发生任何变化。
func TestPatchHasNoSideEffects(t *testing.T) {
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
	before := f.snap
	beforeChain := len(f.snapChain)

	// 非法：返回错误，表单只记错误反馈。
	f.fontSize.SetText("abc")
	if _, err := f.patch(); err == nil {
		t.Fatal("非法字号应返回错误")
	}
	if called != 0 {
		t.Error("patch 不应触达写回调")
	}
	if f.snap.FontSize != before.FontSize || f.snap.Model != before.Model ||
		len(f.snap.PromptModules) != len(before.PromptModules) || f.snap.RawJSON != before.RawJSON {
		t.Error("patch 不应改动快照基准")
	}
	if f.status != "" || f.err {
		t.Errorf("patch 不应自己写反馈: (%v, %q)", f.err, f.status)
	}

	// 合法：产出 patch，依然无副作用。
	f.fontSize.SetText("18")
	p, err := f.patch()
	if err != nil {
		t.Fatalf("合法字号应通过: %v", err)
	}
	if p.FontSize != 18 {
		t.Errorf("patch 字号 = %g", p.FontSize)
	}
	if called != 0 || f.snap.FontSize != before.FontSize || f.err {
		t.Errorf("patch 引入了副作用: called=%d err=%v", called, f.err)
	}
	if len(f.snapChain) != beforeChain {
		t.Error("patch 不应改动降级链基准")
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
