package perm

import "testing"

// TestOverridesResolve 例外表按「工具精确 > 来源通配」取，不同来源互不串。
func TestOverridesResolve(t *testing.T) {
	ov := Overrides{
		{Origin: OriginAgent, Mode: ModeAsk},
		{Origin: OriginTool, Tool: "file_write", Mode: ModeAllow},
		{Origin: OriginTool, Tool: "term_exec", Mode: ModeAsk},
	}

	cases := []struct {
		name   string
		origin Origin
		tool   string
		want   Mode
		ok     bool
	}{
		{"工具精确命中", OriginTool, "file_write", ModeAllow, true},
		{"工具精确收紧", OriginTool, "term_exec", ModeAsk, true},
		{"来源通配命中", OriginAgent, "anything", ModeAsk, true},
		{"没有条目", OriginTool, "file_read", "", false},
		{"另一来源不串", OriginAgent, "file_write", ModeAsk, true},
	}
	for _, tc := range cases {
		got, ok := ov.Resolve(tc.origin, tc.tool)
		if ok != tc.ok || (ok && got.Mode != tc.want) {
			t.Errorf("%s: Resolve = %+v/%v, want mode=%s ok=%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestOverridesApply 两个方向都覆盖矩阵（D280 第 2 条）。
func TestOverridesApply(t *testing.T) {
	allowMatrix := Overrides{{Origin: OriginTool, Tool: "file_write", Mode: ModeAsk}}
	if got := allowMatrix.Apply(Allow, OriginTool, "file_write"); got != Ask {
		t.Errorf("矩阵 Allow 被收紧 = %s, want ask", got)
	}
	askMatrix := Overrides{{Origin: OriginTool, Tool: "term_exec", Mode: ModeAllow}}
	if got := askMatrix.Apply(Ask, OriginTool, "term_exec"); got != Allow {
		t.Errorf("矩阵 Ask 被放宽 = %s, want allow", got)
	}
	assist := Overrides{{Origin: OriginTool, Tool: "file_write", Mode: ModeAssist}}
	if got := assist.Apply(Ask, OriginTool, "file_write"); got != Assist {
		t.Errorf("assist = %s, want assist", got)
	}
	if got := askMatrix.Apply(Ask, OriginTool, "file_read"); got != Ask {
		t.Errorf("无命中应保留矩阵 = %s, want ask", got)
	}
}

// TestHardTarget 硬名单：执行控制类文件与控制目录之外的写入不拦。
func TestHardTarget(t *testing.T) {
	sensitive := []string{`C:\Users\me\.aquarius`, "/home/me/.aquarius"}
	cases := []struct {
		target string
		want   bool
	}{
		{`C:\proj\.git\hooks\pre-commit`, true},
		{`C:\proj\.GIT\Hooks\pre-commit`, true},
		{`/proj/.git/config`, true},
		{`/proj/.vscode/settings.json`, true},
		{`/proj/.mcp.json`, true},
		{`C:\proj\src\main.go`, false},
		{`C:\Users\me\.aquarius\secrets.json`, true},
		{`C:\Users\me\.aquarius2\x`, false}, // 前缀不按目录边界误判
		{`/home/me/.aquarius2/x`, false},
		{"", false},
	}
	for _, tc := range cases {
		if got := HardTarget(tc.target, sensitive); got != tc.want {
			t.Errorf("HardTarget(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// TestParsePolicyNames 档位与来源的解析：大小写与空白宽容，拼错报错。
func TestParsePolicyNames(t *testing.T) {
	for _, raw := range []string{"ask", " Ask ", "ASSIST", "allow"} {
		if _, err := ParseMode(raw); err != nil {
			t.Errorf("ParseMode(%q): %v", raw, err)
		}
	}
	if _, err := ParseMode("deny"); err == nil {
		t.Error("deny 不是合法档位（禁用不在判定层）")
	}
	for _, raw := range []string{"toolrun", " ACPGATE "} {
		if _, err := ParseOrigin(raw); err != nil {
			t.Errorf("ParseOrigin(%q): %v", raw, err)
		}
	}
	if _, err := ParseOrigin("ui"); err == nil {
		t.Error("ui 不是策略来源")
	}
}
