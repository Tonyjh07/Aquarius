package main

// tools_settings_test.go 设置窗工具页两组装配期配置（v1.0.0）：呈现模式（tools.mode）
// 与模型辅助决策（decide）的写回、校验与快照回读。decide 段的默认形态是「不存在」——
// 没改过的保存不得创建这一段，这里一并锁住。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// toolsCfgPath 最小可启动配置 + 可选预置段。
func toolsCfgPath(t *testing.T, extra string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{
  "model": {
    "providers": [{"name": "a", "base_url": "http://a/v1", "models": [{"name": "m1"}]}],
    "primary": "a",
    "name": "m1"` + extra + `
  },
  "ui": {"kind": "repl"}
}`
	if err := writeTestFile(cfgPath, cfg); err != nil {
		t.Fatalf("写配置: %v", err)
	}
	return cfgPath
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestToolsModePersist(t *testing.T) {
	cfgPath := toolsCfgPath(t, "")
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark", ToolsMode: "ptc"}); err != nil {
		t.Fatalf("写回: %v", err)
	}
	if snap := readTextSettings(cfgPath); snap.ToolsMode != "ptc" {
		t.Fatalf("快照 ToolsMode = %q, want ptc", snap.ToolsMode)
	}
	// 非法值拒写，原值保留。
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark", ToolsMode: "wild"}); err == nil {
		t.Fatal("词表外值应拒写")
	}
	if snap := readTextSettings(cfgPath); snap.ToolsMode != "ptc" {
		t.Fatalf("拒写后应保留原值: %q", snap.ToolsMode)
	}
}

func TestDecidePersistCreatesSection(t *testing.T) {
	cfgPath := toolsCfgPath(t, "")
	p := uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "shadow",
		DecideBaseURL: "http://127.0.0.1:11434", DecideModel: "clef-flash",
		DecideTimeoutMS: 2000, DecideTaskChars: 120,
		DecideWarmup: boolPtr(false), DecideDenyEnabled: boolPtr(true),
		DecideRiskBelow: 0.4, DecideHumanBelow: 0.1, DecideHumanAbove: 0.9,
		DecideKey: "secret:DECIDE_KEY"}
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("写回: %v", err)
	}
	snap := readTextSettings(cfgPath)
	if snap.DecideMode != "shadow" || snap.DecideBaseURL != "http://127.0.0.1:11434" ||
		snap.DecideModel != "clef-flash" || snap.DecideTimeoutMS != 2000 || snap.DecideTaskChars != 120 ||
		snap.DecideWarmup || !snap.DecideDenyEnabled ||
		snap.DecideRiskBelow != 0.4 || snap.DecideHumanBelow != 0.1 || snap.DecideHumanAbove != 0.9 {
		t.Fatalf("快照 = %+v", snap)
	}
	if !snap.DecideKeySaved || snap.DecideKeyRef != "secret:DECIDE_KEY" {
		t.Fatalf("密钥形态 = (%v, %q)", snap.DecideKeySaved, snap.DecideKeyRef)
	}
	// 明文键形态标记。
	cfgPath2 := toolsCfgPath(t, "")
	if err := persistSettingsTextKeys(cfgPath2, uigui.SettingsPatch{Theme: "dark",
		DecideSet: true, DecideMode: "off", DecideKey: "plaintext-key"}); err != nil {
		t.Fatalf("明文写回: %v", err)
	}
	if snap := readTextSettings(cfgPath2); !snap.DecideKeyPlain {
		t.Fatalf("明文键应标 plain: %+v", snap)
	}
}

func TestDecidePersistKeepsKeyOnEmpty(t *testing.T) {
	cfgPath := toolsCfgPath(t, "")
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark",
		DecideSet: true, DecideMode: "shadow", DecideKey: "secret:KEEP"}); err != nil {
		t.Fatalf("首写: %v", err)
	}
	// 留空 = 保持原引用。
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark",
		DecideSet: true, DecideMode: "on"}); err != nil {
		t.Fatalf("二写: %v", err)
	}
	snap := readTextSettings(cfgPath)
	if snap.DecideMode != "on" || snap.DecideKeyRef != "secret:KEEP" {
		t.Fatalf("快照 = (mode %q, ref %q)", snap.DecideMode, snap.DecideKeyRef)
	}
}

func TestDecidePersistNoSetLeavesSectionAbsent(t *testing.T) {
	cfgPath := toolsCfgPath(t, "")
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "dark"}); err != nil {
		t.Fatalf("写回: %v", err)
	}
	snap := readTextSettings(cfgPath)
	if snap.DecideMode != "" || snap.DecideBaseURL != "" {
		t.Fatalf("未改动的保存不得创建 decide 段: %+v", snap)
	}
}

func TestDecidePersistRejectsBadValues(t *testing.T) {
	cases := []struct {
		label string
		patch uigui.SettingsPatch
	}{
		{"mode 词表外", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "wild"}},
		{"timeout 越下界", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "off", DecideTimeoutMS: 100}},
		{"timeout 越上界", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "off", DecideTimeoutMS: 20000}},
		{"task_chars 越界", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "off", DecideTaskChars: 500}},
		{"risk 越界", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "off", DecideRiskBelow: 3}},
		{"human 越界", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "off", DecideHumanBelow: 1.5}},
		{"deny above 越界", uigui.SettingsPatch{Theme: "dark", DecideSet: true, DecideMode: "off", DecideHumanAbove: 1.5}},
	}
	for _, tc := range cases {
		cfgPath := toolsCfgPath(t, "")
		if err := persistSettingsTextKeys(cfgPath, tc.patch); err == nil {
			t.Fatalf("%s: 应拒写", tc.label)
		}
		if snap := readTextSettings(cfgPath); snap.DecideMode != "" {
			t.Fatalf("%s: 拒写后段不应存在: %+v", tc.label, snap)
		}
	}
}

func boolPtr(v bool) *bool { return &v }
