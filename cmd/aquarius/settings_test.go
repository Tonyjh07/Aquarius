package main

// 设置窗装配根侧测试：内核命令计划 diff、文本键写读往返与主题档拒写（§15.7/D61）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
)

// TestSettingsCommands 命令计划：diff 才发命令、空模型名跳过、空档位 = off 清除、
// 文本键（provider/base_url/api_key/hotkey/theme）不进命令计划。
func TestSettingsCommands(t *testing.T) {
	cur := uigui.SettingsSnapshot{
		Permission: "strict", Provider: "openai", BaseURL: "https://x/v1",
		Model: "m1", Think: true, Effort: "medium", Hotkey: "", Theme: "system",
	}
	base := func() uigui.SettingsPatch {
		return uigui.SettingsPatch{
			Permission: "strict", Provider: "openai", BaseURL: "https://x/v1",
			Model: "m1", Think: true, Effort: "medium", Theme: "system",
		}
	}
	cases := []struct {
		name string
		mut  func(*uigui.SettingsPatch)
		want []string // "name arg..."；nil = 不发命令
	}{
		{"无变更不发命令", nil, nil},
		{"权限切换", func(p *uigui.SettingsPatch) { p.Permission = "permissive" },
			[]string{"permission permissive"}},
		{"模型切换", func(p *uigui.SettingsPatch) { p.Model = "m2" },
			[]string{"model m2"}},
		{"空模型名保持当前", func(p *uigui.SettingsPatch) { p.Model = "" }, nil},
		{"思考关闭", func(p *uigui.SettingsPatch) { p.Think = false },
			[]string{"think off"}},
		{"思考开启", func(p *uigui.SettingsPatch) { p.Think = true },
			nil /* cur 已 true */},
		{"档位清除", func(p *uigui.SettingsPatch) { p.Effort = "" },
			[]string{"effort off"}},
		{"档位切换", func(p *uigui.SettingsPatch) { p.Effort = "high" },
			[]string{"effort high"}},
		{"多键并发变更", func(p *uigui.SettingsPatch) {
			p.Permission = "full-access"
			p.Model = "m2"
			p.Think = false
			p.Effort = "low"
		}, []string{"permission full-access", "model m2", "think off", "effort low"}},
		{"文本键不进命令计划", func(p *uigui.SettingsPatch) {
			p.Provider = "y"
			p.BaseURL = "https://y/v1"
			p.APIKey = "secret:K"
			p.Hotkey = "Ctrl+B"
			p.Theme = "dark"
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base()
			if c.mut != nil {
				c.mut(&p)
			}
			got := settingsCommands(cur, p)
			if len(got) != len(c.want) {
				t.Fatalf("命令数 = %d (%+v), want %d (%v)", len(got), got, len(c.want), c.want)
			}
			for i, cmd := range got {
				line := cmd.Name
				if len(cmd.Args) > 0 {
					line += " " + strings.Join(cmd.Args, " ")
				}
				if line != c.want[i] {
					t.Errorf("命令[%d] = %q, want %q", i, line, c.want[i])
				}
			}
		})
	}
}

// TestPersistReadTextSettingsRoundTrip 文本键写读往返：写回后 readTextSettings
// 读回同值；其余键（permissions 段等）经泛键改写保留；快照结构无密钥字段。
// D110②：provider 字段 = primary 选择器（须为 providers 已有名字，此处沿用模板名），
// base_url/api_key 写入 primary 条目。
func TestPersistReadTextSettingsRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	p := uigui.SettingsPatch{
		Permission: "permissive", Provider: "openai", BaseURL: "https://x/v1",
		APIKey: "secret:AQUARIUS_OPENAI_KEY", Model: "m9",
		Hotkey: "Ctrl+Alt+K", Theme: "dark",
	}
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if s.Provider != "openai" || s.BaseURL != "https://x/v1" ||
		s.Hotkey != "Ctrl+Alt+K" || s.Theme != "dark" {
		t.Errorf("文本键读回 = %+v", s)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("读回 config: %v", err)
	}
	if !strings.Contains(string(data), `"level": "strict"`) {
		t.Error("permissions 段未保留（泛键改写口径破坏了无关键）")
	}
	if !strings.Contains(string(data), `"name": "gpt-4o-mini"`) {
		t.Error("model.name 未保留（命令写回的键不应被文本键改写触碰）")
	}
	var back struct {
		Model struct {
			Primary   string `json:"primary"`
			Providers []struct {
				Name    string `json:"name"`
				BaseURL string `json:"base_url"`
				APIKey  string `json:"api_key"`
			} `json:"providers"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("解析 config: %v", err)
	}
	if back.Model.Primary != "openai" || len(back.Model.Providers) != 1 ||
		back.Model.Providers[0].BaseURL != "https://x/v1" ||
		back.Model.Providers[0].APIKey != "secret:AQUARIUS_OPENAI_KEY" {
		t.Errorf("providers 写回 = %+v", back.Model)
	}
}

// TestPersistSettingsTextKeysUnknownProvider D110②：provider 选择器给出 providers
// 外的名字 → 拒写（含 ui 键一并不动，防半套落盘）。
func TestPersistSettingsTextKeysUnknownProvider(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	before, _ := os.ReadFile(cfgPath)
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Provider: "no-such", Theme: "dark",
	}); err == nil {
		t.Fatal("未知 provider 应拒写")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("拒写后文件应保持不变")
	}
}

// TestPersistSettingsTextKeysTheme 主题档归一与拒写："" → system 落盘；枚举外拒写
// 且原文件不动（D61 落盘口径）。
func TestPersistSettingsTextKeysTheme(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: ""}); err != nil {
		t.Fatalf("空 theme 应归一为 system: %v", err)
	}
	if s := readTextSettings(cfgPath); s.Theme != "system" {
		t.Errorf("theme 落盘 = %q, want system", s.Theme)
	}
	before, _ := os.ReadFile(cfgPath)
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{Theme: "blue"}); err == nil {
		t.Fatal("枚举外主题档应拒写")
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("拒写后文件应保持不变")
	}
}

// TestReadTextSettingsFallback 读失败/解析失败返回零值（快照容错不阻断开窗）。
func TestReadTextSettingsFallback(t *testing.T) {
	if s := readTextSettings(filepath.Join(t.TempDir(), "missing.json")); s != (uigui.SettingsSnapshot{}) {
		t.Errorf("缺失文件读回 = %+v, want 零值", s)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("写坏文件: %v", err)
	}
	if s := readTextSettings(bad); s != (uigui.SettingsSnapshot{}) {
		t.Errorf("坏文件读回 = %+v, want 零值", s)
	}
}

// TestPersistReadTextSettingsZoomKnobs D90 三旋钮写读往返：scale/font_size/
// window_width/window_height 写回后读回同值，无关键（permissions/limits）不受影响。
func TestPersistReadTextSettingsZoomKnobs(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		Theme: "system", Scale: 1.5, FontSize: 20,
		WindowWidth: 304, WindowHeight: 920,
	}); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if s.Scale != 1.5 || s.FontSize != 20 || s.WindowWidth != 304 || s.WindowHeight != 920 {
		t.Errorf("旋钮读回 = %v/%v %dx%d, want 1.5/20 304x920",
			s.Scale, s.FontSize, s.WindowWidth, s.WindowHeight)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("读回 config: %v", err)
	}
	if !strings.Contains(string(data), `"max_context_tokens": 64000`) {
		t.Error("limits 段未保留（泛键改写口径破坏了无关键）")
	}
}
