package uigui

// 设置窗 headless 测试（§15.5）：快照填表（含密钥不回显）、保存流（patch 组装 +
// 命令入队 + 主题/快捷键热生效）、错误与只读路径——全部无窗口。

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fillSnapshot 标准快照（测试基准）。
func fillSnapshot() SettingsSnapshot {
	return SettingsSnapshot{
		Permission: "permissive", Provider: "openai", BaseURL: "https://x/v1",
		Model: "m1", Think: true, Effort: "high", Hotkey: "Ctrl+B", Theme: "dark",
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
	if f.provider.Text() != "openai" || f.model.Text() != "m1" ||
		f.baseURL.Text() != "https://x/v1" {
		t.Errorf("文本档填表错误: provider=%q model=%q baseURL=%q",
			f.provider.Text(), f.model.Text(), f.baseURL.Text())
	}
	if f.apiKey.Text() != "" {
		t.Error("密钥只写不回显（D35）：字段必须恒空")
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
				Permission: "strict", Provider: "openai", Model: "m1",
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
	f.provider.SetText("  newp  ")
	f.hotkey.SetText("Ctrl+B")
	f.theme.setValue("dark")
	u.saveSettings(f)

	if called != 1 {
		t.Fatalf("写回调调用 = %d, want 1", called)
	}
	if gotPatch.Permission != "permissive" || gotPatch.Provider != "newp" ||
		gotPatch.Theme != "dark" || gotPatch.Hotkey != "Ctrl+B" || gotPatch.Effort != "low" {
		t.Errorf("patch = %+v", gotPatch)
	}
	if gotPatch.Model != "m1" {
		t.Errorf("模型名 = %q, want m1（表单未改 = 快照原值）", gotPatch.Model)
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
	if f.snap.Theme != "dark" || f.snap.Provider != "newp" {
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
