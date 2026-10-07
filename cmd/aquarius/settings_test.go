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
		Permission: "strict",
		Model:      "m1", Think: true, Effort: "medium", Hotkey: "", Theme: "system",
	}
	base := func() uigui.SettingsPatch {
		return uigui.SettingsPatch{
			Permission: "strict",
			Model:      "m1", Think: true, Effort: "medium", Theme: "system",
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
// 读回同值；其余键（permissions 段等）经泛键改写保留。D110②：provider 编辑走
// ProviderManager.Apply（providers 列表结构），不在文本键范围（见 providers_test）。
func TestPersistReadTextSettingsRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	p := uigui.SettingsPatch{
		Permission: "permissive", Model: "m9",
		Hotkey: "Ctrl+Alt+K", Theme: "dark",
		Notify: true, TTS: false,
		MaxTurns: 12, MaxContextTokens: 32000, CompactThreshold: 0.8,
		ToolOutputChars: 15000, ToolTimeoutSec: 90,
		SystemPrompt: "你是个简洁的助手",
	}
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}
	s := readTextSettings(cfgPath)
	if s.Hotkey != "Ctrl+Alt+K" || s.Theme != "dark" {
		t.Errorf("文本键读回 = %+v", s)
	}
	if !s.Notify || s.TTS || s.MaxTurns != 12 || s.MaxContextTokens != 32000 ||
		s.CompactThreshold != 0.8 || s.ToolOutputChars != 15000 || s.ToolTimeoutSec != 90 {
		t.Errorf("输出/限额读回 = %+v", s)
	}
	if s.SystemPrompt != "你是个简洁的助手" {
		t.Errorf("system_prompt = %q", s.SystemPrompt)
	}
	if len(s.MCPServers) != 0 || len(s.Plugins) != 0 {
		t.Errorf("MCP/插件名 = %v / %v, want 空", s.MCPServers, s.Plugins)
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
				APIKey string `json:"api_key"`
			} `json:"providers"`
		} `json:"model"`
	}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("解析 config: %v", err)
	}
	if back.Model.Primary != "openai" {
		t.Errorf("model.primary = %q, want openai（文本键不触碰 provider 段）", back.Model.Primary)
	}
	if back.Model.Providers[0].APIKey != "secret:AQUARIUS_OPENAI_KEY" {
		t.Errorf("providers 密钥应不受文本键影响: %+v", back.Model)
	}
}

// TestWriteRawConfig 高级页整文件替换（Q10）：合法 JSON 落盘（原文即最终态），
// 非法 JSON 拒写且原文件不动。
func TestWriteRawConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	before, _ := os.ReadFile(cfgPath)

	if err := writeRawConfig(cfgPath, "{not json"); err == nil || !strings.Contains(err.Error(), "原文件未动") {
		t.Fatalf("非法 JSON err = %v", err)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("解析失败后原文件应保持不变（Q10）")
	}

	raw := string(before)
	raw = strings.Replace(raw, `"notify": true`, `"notify": false`, 1)
	raw = strings.Replace(raw, `"gpt-4o-mini"`, `"my-model"`, 2)
	raw = strings.Replace(raw, `  "system_prompt": "",`, `  "system_prompt": "", "custom_key": {"a": 1},`, 1)
	if err := writeRawConfig(cfgPath, raw); err != nil {
		t.Fatalf("合法 raw 写回: %v", err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), `"custom_key"`) || !strings.Contains(string(data), `"my-model"`) {
		t.Errorf("raw 替换应整文件生效: %s", data)
	}
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("落盘非合法 JSON: %v", err)
	}
}

// TestReadTextSettingsMCPNames MCP/插件只读名清单（sorted）与 RawJSON 原文。
func TestReadTextSettingsMCPNames(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := `{"mcpServers": {"web": {"transport":"stdio"}, "docs": {}}, "plugins": {"x": {"enabled": true}}, "output": {"notify": true}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap := readTextSettings(cfgPath)
	if len(snap.MCPServers) != 2 || snap.MCPServers[0] != "docs" || snap.MCPServers[1] != "web" {
		t.Errorf("MCPServers = %v, want sorted [docs web]", snap.MCPServers)
	}
	if len(snap.Plugins) != 1 || snap.Plugins[0] != "x" {
		t.Errorf("Plugins = %v", snap.Plugins)
	}
	if !snap.Notify {
		t.Errorf("Notify = %v", snap.Notify)
	}
	if !strings.Contains(snap.RawJSON, "mcpServers") {
		t.Errorf("RawJSON 应为原文: %.60s", snap.RawJSON)
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
	if s := readTextSettings(filepath.Join(t.TempDir(), "missing.json")); s.Hotkey != "" || s.Theme != "" ||
		s.Notify || s.SystemPrompt != "" || s.RawJSON != "" || len(s.MCPServers) > 0 {
		t.Errorf("缺失文件读回 = %+v, want 零值", s)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("写坏文件: %v", err)
	}
	if s := readTextSettings(bad); s.Hotkey != "" || s.Theme != "" || s.MaxTurns != 0 {
		t.Errorf("坏文件读回 = %+v, want 零值（RawJSON 照空）", s)
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
