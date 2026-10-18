package main

// 人格模块树装配根侧（S7/D144 + S7b/D145）：config `prompts` 段读写往返、模块名校验、
// 内置模块清单、组合预览与运行环境取值。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/uigui"
	"github.com/Tonyjh07/Aquarius/internal/app"
)

// TestPromptsRoundTrip config prompts 段读写往返：空名行跳过、children/persona 清洗去重
// 保序、模块视图按名排序（JSON map 无序）、空正文不写 text 键。
func TestPromptsRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
		t.Fatalf("写模板: %v", err)
	}
	p := uigui.SettingsPatch{
		SystemPrompt: "LEGACY",
		PromptModules: []uigui.PromptModuleView{
			{Name: " house ", Text: "HOUSE:", Children: []string{"identity", " identity ", "", "style"}},
			{Name: "identity", Text: "IDENT"},
			{Name: "empty-group", Children: []string{"identity"}}, // 纯组：无 text 键
			{Name: "   ", Text: "未填名的新增行"},                        // 空名行 = 尚未创建，跳过
		},
		PromptPersona: []string{" house ", "identity", "house", ""},
	}
	if err := persistSettingsTextKeys(cfgPath, p); err != nil {
		t.Fatalf("persistSettingsTextKeys: %v", err)
	}

	s := readTextSettings(cfgPath)
	names := make([]string, 0, len(s.PromptModules))
	for _, m := range s.PromptModules {
		names = append(names, m.Name)
	}
	if got := strings.Join(names, ","); got != "empty-group,house,identity" {
		t.Fatalf("模块视图顺序 = %q, want 按名排序（map 无序，展示须确定）", got)
	}
	if got := s.PromptModules[1].Children; len(got) != 2 || got[0] != "identity" || got[1] != "style" {
		t.Errorf("children 清洗 = %v, want [identity style]", got)
	}
	if got := s.PromptPersona; len(got) != 2 || got[0] != "house" || got[1] != "identity" {
		t.Errorf("persona 清洗 = %v, want [house identity]", got)
	}
	if s.SystemPrompt != "LEGACY" {
		t.Errorf("system_prompt = %q, want LEGACY（@default 正文来源）", s.SystemPrompt)
	}
	if len(s.Builtins) == 0 {
		t.Error("快照内置模块清单为空（设置窗无法展示可引用内置模块）")
	}

	// 落盘结构 = modules: name → {text?, children?[]}；纯组不写 text 键。
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("读回 config: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("解析 config: %v", err)
	}
	pSec, ok := generic["prompts"].(map[string]any)
	if !ok {
		t.Fatalf("config 缺 prompts 段: %s", data)
	}
	mods, ok := pSec["modules"].(map[string]any)
	if !ok {
		t.Fatalf("prompts.modules 不是对象: %v", pSec["modules"])
	}
	if len(mods) != 3 {
		t.Fatalf("落盘模块数 = %d, want 3（空名行不进）: %v", len(mods), mods)
	}
	house, _ := mods["house"].(map[string]any)
	if house["text"] != "HOUSE:" {
		t.Errorf("house.text = %v", house["text"])
	}
	if kids, _ := house["children"].([]any); len(kids) != 2 || kids[0] != "identity" || kids[1] != "style" {
		t.Errorf("house.children = %v", house["children"])
	}
	houseGroup, _ := mods["empty-group"].(map[string]any)
	if _, has := houseGroup["text"]; has {
		t.Errorf("纯组不应写 text 键: %v", houseGroup)
	}
	if kids, _ := houseGroup["children"].([]any); len(kids) != 1 || kids[0] != "identity" {
		t.Errorf("empty-group.children = %v", houseGroup["children"])
	}

	// 二次往返（读回的快照原样写回）应逐键稳定——设置窗「打开 → 保存」不改动未编辑内容。
	if err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{
		SystemPrompt: s.SystemPrompt, PromptModules: s.PromptModules, PromptPersona: s.PromptPersona,
	}); err != nil {
		t.Fatalf("二次写回: %v", err)
	}
	back := readTextSettings(cfgPath)
	again := make([]string, 0, len(back.PromptModules))
	for _, m := range back.PromptModules {
		again = append(again, m.Name)
	}
	if got := strings.Join(again, ","); got != "empty-group,house,identity" {
		t.Errorf("二次往返模块 = %q", got)
	}
	if len(back.PromptPersona) != 2 || back.PromptPersona[0] != "house" {
		t.Errorf("二次往返 persona = %v", back.PromptPersona)
	}
}

// TestPersistPromptsValidation 模块名校验：`@` 前缀（保留给内置）与重名均在落盘前拒绝，
// 原文件不动（校验先于写，且报因可读）。
func TestPersistPromptsValidation(t *testing.T) {
	cases := []struct {
		name  string
		mods  []uigui.PromptModuleView
		match string
	}{
		{"内置前缀", []uigui.PromptModuleView{{Name: "@mine", Text: "X"}}, "保留"},
		{"重名", []uigui.PromptModuleView{{Name: "a", Text: "A"}, {Name: " a ", Text: "B"}}, "重复"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0o644); err != nil {
				t.Fatalf("写模板: %v", err)
			}
			err := persistSettingsTextKeys(cfgPath, uigui.SettingsPatch{PromptModules: tc.mods})
			if err == nil {
				t.Fatalf("%v 应报错", tc.name)
			}
			if !strings.Contains(err.Error(), tc.match) {
				t.Errorf("报因 = %v, want 含 %q", err, tc.match)
			}
			data, rerr := os.ReadFile(cfgPath)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if string(data) != defaultConfig {
				t.Error("校验失败仍改动了 config（应落盘前拒绝）")
			}
		})
	}
}

// TestBuiltinModuleViews 内置模块展示清单与引擎契约同源（名集 = app.BuiltinModuleNames，
// 顺序一致），每个都有中文说明（缺 label 会退化成「内置模块」占位）。
func TestBuiltinModuleViews(t *testing.T) {
	views := builtinModuleViews()
	want := app.BuiltinModuleNames()
	if len(views) != len(want) {
		t.Fatalf("内置清单 = %d, want %d", len(views), len(want))
	}
	for i, w := range want {
		if views[i].Name != w {
			t.Errorf("内置[%d] = %q, want %q（展示序即引用序）", i, views[i].Name, w)
		}
		if views[i].Label == "" || views[i].Label == "内置模块" {
			t.Errorf("内置 %s 缺专属说明（显示为 %q）", views[i].Name, views[i].Label)
		}
	}
	for _, need := range []string{"@default", "@env", "@env.date"} {
		found := false
		for _, v := range views {
			if v.Name == need {
				found = true
			}
		}
		if !found {
			t.Errorf("内置清单缺 %s", need)
		}
	}
}

// TestPreviewPromptsCompose 组合预览 = 设置窗当前（未保存）表单现状 → 最终 system 文本，
// 与新建会话同口径：组头 + 子模块换行、顶层引用空行连接、环境事实取本次装配、日期取当天。
func TestPreviewPromptsCompose(t *testing.T) {
	w := &wiring{
		cfg:         &fileConfig{UI: uiConfig{Kind: "gui"}},
		sandboxDir:  t.TempDir(), // 绝对路径（跨平台）才附沙盒行
		toolTimeout: 60 * time.Second,
	}
	got := w.previewPrompts([]uigui.PromptModuleView{
		{Name: "house", Text: "HOUSE:", Children: []string{"identity", "style"}},
		{Name: "identity", Text: "IDENT"},
		{Name: "style", Text: "STYLE"},
	}, []string{"house", "@env"}, "LEGACY")

	if !strings.HasPrefix(got, "HOUSE:\nIDENT\nSTYLE") {
		t.Errorf("组展开 = %q, want 组头 + 子模块换行接续", got)
	}
	for _, want := range []string{"\n\nRuntime environment:", "- Platform: ", "- Terminal: gui (",
		"- Privileged sandbox directory: ", "- Tool timeout: default 60s"} {
		if !strings.Contains(got, want) {
			t.Errorf("预览缺 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LEGACY") {
		t.Errorf("persona 未引用 @default，兜底串不应出现:\n%s", got)
	}

	// 未保存的新增空名行不进预览（与保存同口径）；persona 空 = 默认引用（@default + @env）。
	got = w.previewPrompts([]uigui.PromptModuleView{{Name: "  ", Text: "DRAFT"}}, nil, "LEGACY")
	if !strings.HasPrefix(got, "LEGACY") {
		t.Errorf("默认引用应从 @default（= system_prompt 正文）起: %q", got)
	}
	if !strings.Contains(got, "Runtime environment:") || strings.Contains(got, "DRAFT") {
		t.Errorf("默认引用应含 @env 且不含未填名草稿: %q", got)
	}
	// 日期取「当天」：容差到昨天（跨零点竞态只影响本断言，不引入 flake）。
	idx := strings.Index(got, "- Current date: ")
	if idx < 0 {
		t.Fatalf("默认引用应含会话当天日期: %q", got)
	}
	rest := strings.TrimSpace(got[idx+len("- Current date: "):])
	if len(rest) < 10 {
		t.Fatalf("日期串过短: %q", rest)
	}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	if d := rest[:10]; d != today && d != yesterday {
		t.Errorf("日期 = %q, want %q（或昨天 %q）", d, today, yesterday)
	}

	// 全空（无模块、无引用、无兜底）⇒ 回落内置默认人格（不返回空串——空 system 会让用户莫名其妙）。
	empty := w.previewPrompts(nil, nil, "")
	if !strings.Contains(empty, "You are Aquarius") {
		t.Errorf("全空应回落内置默认人格: %q", empty)
	}
}
