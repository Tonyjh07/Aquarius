package main

// 工具与 MCP 管理面装配根侧测试（S6/D143）：禁用集推导、写盘往返、日志格式化、
// toolsAdmin.SetBuiltinEnabled（热更 runner + 落盘 + Revision 递增）——全部无窗口。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestBuiltinDisabledDerivation 默认推导不改今天行为（D143）：键缺失 ⇒ ["think"]；
// model.think_tool=true ⇒ 空集（兼容读取）；键存在 = 显式清单。
func TestBuiltinDisabledDerivation(t *testing.T) {
	cases := []struct {
		name      string
		tools     toolsConfig
		thinkTool bool
		want      map[string]bool
	}{
		{"键缺失 + think_tool 缺省", toolsConfig{}, false, map[string]bool{"think": true}},
		{"键缺失 + think_tool=true（兼容）", toolsConfig{}, true, nil},
		{"显式清单", toolsConfig{Disabled: []string{"think", "context_compact"}}, true,
			map[string]bool{"think": true, "context_compact": true}},
		{"显式空清单", toolsConfig{Disabled: []string{}}, true, map[string]bool{}},
	}
	for _, tc := range cases {
		w := &wiring{cfg: &fileConfig{Tools: tc.tools, Model: modelConfig{ThinkTool: tc.thinkTool}}}
		got := w.builtinDisabled()
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
		for n := range tc.want {
			if !got[n] {
				t.Fatalf("%s: 缺 %s（got %v）", tc.name, n, got)
			}
		}
	}
}

// TestPersistToolsDisabledRoundTrip 写回 tools.disabled 后读回（空集也落显式空数组）。
func TestPersistToolsDisabledRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{\"model\":{}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &cfgWriter{path: path}
	if err := w.persistToolsDisabled([]string{"think"}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"tools": {`) || !strings.Contains(string(data), `"disabled": [`) {
		t.Fatalf("写盘缺 tools.disabled: %s", data)
	}
	// 空集也写显式空数组（≠ 键缺失推导）。
	if err := w.persistToolsDisabled(nil); err != nil {
		t.Fatalf("persist 空集: %v", err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), `"disabled": []`) {
		t.Fatalf("空集应落显式空数组: %s", data)
	}
}

// TestFormatPluginLogs 日志格式化：带名 = 逐行（新→旧）；无参 = 各插件摘要。
func TestFormatPluginLogs(t *testing.T) {
	rows := []decorate.ToolCallLine{
		{TS: "t1", Name: "mcp:web:search", DurMS: 5, OK: true},
		{TS: "t2", Name: "mcp:web:fetch", DurMS: 9, OK: false, Err: "timeout"},
		{TS: "t3", Name: "mcp:docs:read", DurMS: 2, OK: true},
		{TS: "t4", Name: "builtin_think", DurMS: 1, OK: true}, // 非 mcp 前缀忽略
	}
	empty := formatPluginLogs(nil, "", 10)
	if !strings.Contains(empty, "尚无插件调用记录") {
		t.Fatalf("空 = %q", empty)
	}
	sum := formatPluginLogs(rows, "", 10)
	if !strings.Contains(sum, "web") || !strings.Contains(sum, "docs") ||
		!strings.Contains(sum, "调用2, 失败1") || strings.Contains(sum, "builtin_think") {
		t.Fatalf("摘要缺项: %s", sum)
	}
	detail := formatPluginLogs(rows, "web", 10)
	if !strings.Contains(detail, "mcp:web:search") || !strings.Contains(detail, "timeout") {
		t.Fatalf("明细缺项: %s", detail)
	}
	// 新→旧：明细首行应为最新（t2 fetch 在 t1 search 之前）。
	if i := strings.Index(detail, "mcp:web:fetch"); i < 0 || i > strings.Index(detail, "mcp:web:search") {
		t.Fatalf("明细未按新→旧: %s", detail)
	}
}

// TestToolsAdminSetBuiltinEnabled 内置启停：热更 runner + 写回 config + Revision 递增；
// 未知名报错；禁用 → runner.Specs 过滤 + Execute unknown。
func TestToolsAdminSetBuiltinEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{\"tools\":{\"disabled\":[\"think\"]}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := toolrun.New(toolrun.Options{
		Tools: []port.Tool{thinkStub()},
		Level: func() perm.Level { return perm.Strict },
	})
	w := &wiring{
		disabledTools: newDisabledHolder(map[string]bool{"think": true}),
		runner:        runner,
		persist:       &cfgWriter{path: path},
	}
	ta := &toolsAdmin{w: w}

	if err := ta.SetBuiltinEnabled("think", true); err != nil {
		t.Fatalf("启用: %v", err)
	}
	if w.toolsRev.Load() != 1 {
		t.Fatalf("Revision = %d, want 1", w.toolsRev.Load())
	}
	specs, _ := runner.Specs(t.Context())
	if len(specs) != 1 || specs[0].Name != "think" {
		t.Fatalf("启用后 specs = %+v, want 含 think", specs)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"disabled": []`) {
		t.Fatalf("启用后应落空数组: %s", data)
	}
	if err := ta.SetBuiltinEnabled("think", false); err != nil {
		t.Fatalf("停用: %v", err)
	}
	specs, _ = runner.Specs(t.Context())
	if len(specs) != 0 {
		t.Fatalf("停用后 specs = %+v, want 空", specs)
	}
	if err := ta.SetBuiltinEnabled("no_such_tool", true); err == nil {
		t.Fatal("未知名应报错")
	}
}

// thinkStub 一个最小 think 工具（Specs/Execute 用）。
func thinkStub() port.Tool {
	return &stubSpecTool{name: "think"}
}

// stubSpecTool 只有 Spec 的工具（Specs 过滤验证用；Execute 不应被调到）。
type stubSpecTool struct{ name string }

func (s *stubSpecTool) Spec() tool.Spec { return tool.Spec{Name: s.name, Risk: tool.Safe} }

func (s *stubSpecTool) Execute(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{OK: true}, nil
}
