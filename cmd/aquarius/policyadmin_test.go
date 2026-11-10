package main

// 放行档位的装配根侧测试（D280/D282）：写回往返、热生效、校验报错、快照投影。
//
// 档位挂在工具页的工具行上，所以入口是 toolsAdmin（SetToolPolicy / SetAgentPolicy），
// 写回本身在 wiring.setOverride。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
)

// newPolicyTestWiring 一个只带策略相关字段的 wiring（无窗口、无网络）。
func newPolicyTestWiring(t *testing.T, raw string) (*wiring, *toolsAdmin, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &wiring{
		cfgPath: path,
		cfg:     &fileConfig{},
		persist: &cfgWriter{path: path},
		policy:  newPolicyHolder(nil),
		lvl:     newLevelHolder(perm.Strict),
	}
	return w, &toolsAdmin{w: w}, path
}

// readOverrides 读回 config 里的 permissions.overrides 原始 JSON。
func readOverrides(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Permissions struct {
			Level     string           `json:"level"`
			Overrides []map[string]any `json:"overrides"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("配置不是合法 JSON：%v\n%s", err, data)
	}
	if doc.Permissions.Level != "strict" {
		t.Fatalf("写回吞掉了 level 键：%s", data)
	}
	return doc.Permissions.Overrides
}

// TestToolPolicyRoundTrip 加一条 → 改档 → 外部代理一条 → 删一条：内存表、holder、
// 磁盘三者同步，且写回不动 permissions 的其它键。
func TestToolPolicyRoundTrip(t *testing.T) {
	t.Parallel()
	w, a, path := newPolicyTestWiring(t, `{"permissions":{"level":"strict"}}`)

	if err := a.SetToolPolicy("file_write", "allow"); err != nil {
		t.Fatalf("加一条：%v", err)
	}
	rows := readOverrides(t, path)
	if len(rows) != 1 || rows[0]["origin"] != "toolrun" || rows[0]["tool"] != "file_write" || rows[0]["mode"] != "allow" {
		t.Fatalf("写盘条目 = %v", rows)
	}
	if got := w.policy.Get(); len(got) != 1 || got[0].Mode != perm.ModeAllow {
		t.Fatalf("holder = %+v", got)
	}
	if w.toolsRev.Load() == 0 {
		t.Fatalf("Revision 未递增（界面不会重建行）")
	}

	// 改档：同一工具覆盖，不新增第二条。
	if err := a.SetToolPolicy("file_write", "ask"); err != nil {
		t.Fatalf("改档：%v", err)
	}
	rows = readOverrides(t, path)
	if len(rows) != 1 || rows[0]["mode"] != "ask" {
		t.Fatalf("改档后 = %v", rows)
	}

	// 外部代理整来源：不写 tool 键。
	if err := a.SetAgentPolicy("allow"); err != nil {
		t.Fatalf("外部代理整体：%v", err)
	}
	rows = readOverrides(t, path)
	if len(rows) != 2 {
		t.Fatalf("应有两条：%v", rows)
	}
	for _, r := range rows {
		if r["origin"] == "acpgate" {
			if _, ok := r["tool"]; ok {
				t.Fatalf("空工具不该写 tool 键：%v", r)
			}
		}
	}

	// 删（空档位）：条目消失，表仍在。
	if err := a.SetToolPolicy("file_write", ""); err != nil {
		t.Fatalf("删除：%v", err)
	}
	rows = readOverrides(t, path)
	if len(rows) != 1 || rows[0]["origin"] != "acpgate" {
		t.Fatalf("删除后 = %v", rows)
	}
	if got := w.policy.Get(); len(got) != 1 || got[0].Origin != perm.OriginAgent {
		t.Fatalf("删除后 holder = %+v", got)
	}

	// 删到最后一条：overrides 写成显式空数组（键保留）。
	if err := a.SetAgentPolicy(""); err != nil {
		t.Fatalf("删最后一条：%v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"overrides": []`) {
		t.Fatalf("空表应落显式空数组：%s", data)
	}
}

// TestToolPolicyRejectsBadInput 拼错一律报错且不落盘（静默跳过会让用户以为已设过）。
func TestToolPolicyRejectsBadInput(t *testing.T) {
	t.Parallel()
	_, a, path := newPolicyTestWiring(t, `{"permissions":{"level":"strict"}}`)
	cases := []struct {
		name string
		do   func() error
	}{
		{"未知工具", func() error { return a.SetToolPolicy("no_such_tool", "ask") }},
		{"未知档位", func() error { return a.SetToolPolicy("file_write", "deny") }},
		{"外部代理未知档位", func() error { return a.SetAgentPolicy("deny") }},
	}
	for _, tc := range cases {
		if err := tc.do(); err == nil {
			t.Fatalf("%s: 应报错", tc.name)
		}
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "overrides") {
		t.Fatalf("失败路径不该落盘：%s", data)
	}
}

// TestToolsSnapshotCarriesPolicy 快照把例外表摊到行上：本地工具各自一档，外部代理
// 一条，Level 供说明行取词。
func TestToolsSnapshotCarriesPolicy(t *testing.T) {
	t.Parallel()
	w, a, _ := newPolicyTestWiring(t, `{"permissions":{"level":"strict"}}`)
	if err := a.SetToolPolicy("file_write", "allow"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetAgentPolicy("ask"); err != nil {
		t.Fatal(err)
	}
	snap := a.Snapshot()
	if snap.Level != "strict" {
		t.Fatalf("Level = %q", snap.Level)
	}
	if snap.AgentPolicy != "ask" {
		t.Fatalf("AgentPolicy = %q", snap.AgentPolicy)
	}
	var got string
	var exec bool
	for _, b := range snap.Builtins {
		switch b.Name {
		case "file_write":
			got = b.Policy
		case "term_exec":
			exec = b.Exec
		}
	}
	if got != "allow" {
		t.Fatalf("file_write 档位 = %q", got)
	}
	if !exec {
		t.Fatalf("term_exec 应带执行类标记")
	}
	_ = w
}
