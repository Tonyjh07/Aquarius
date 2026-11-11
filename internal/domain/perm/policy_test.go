package perm

import (
	"path/filepath"
	"testing"
)

// TestOverridesResolve 例外表按「目录级 > 工具级 > 来源通配」取，不同来源互不串。
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
		got, ok := ov.Resolve(tc.origin, tc.tool, "")
		if ok != tc.ok || (ok && got.Mode != tc.want) {
			t.Errorf("%s: Resolve = %+v/%v, want mode=%s ok=%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestOverridesDirScope 目录级条目只在目标落进那个目录时命中（D282）；
// 目标为空（执行类）时目录级一律不命中，免得一条「某目录内放行」罩住没有路径的调用。
func TestOverridesDirScope(t *testing.T) {
	ov := Overrides{
		{Origin: OriginTool, Tool: "file_write", Mode: ModeAllow, Dir: "/proj/a"},
		{Origin: OriginTool, Tool: "file_write", Mode: ModeAllow, Dir: "/proj/a/deep"},
		{Origin: OriginTool, Tool: "file_write", Mode: ModeAsk},
	}

	cases := []struct {
		name   string
		target string
		want   Mode
		wantDy string
	}{
		{"目录内命中", "/proj/a/x.txt", ModeAllow, "/proj/a"},
		{"嵌套目录取更深的", "/proj/a/deep/x.txt", ModeAllow, "/proj/a/deep"},
		{"目录外退回工具级", "/proj/b/x.txt", ModeAsk, ""},
		{"目标为空不命中目录级", "", ModeAsk, ""},
	}
	for _, tc := range cases {
		got, ok := ov.Resolve(OriginTool, "file_write", tc.target)
		if !ok || got.Mode != tc.want || got.Dir != tc.wantDy {
			t.Errorf("%s: Resolve(%q) = %+v/%v, want mode=%s dir=%q", tc.name, tc.target, got, ok, tc.want, tc.wantDy)
		}
	}

	// 只有目录级、没有工具级：目录外等于没有条目，矩阵结论原样留下。
	only := Overrides{{Origin: OriginTool, Tool: "file_write", Mode: ModeAllow, Dir: "/proj/a"}}
	if _, ok := only.Resolve(OriginTool, "file_write", "/proj/b/x.txt"); ok {
		t.Error("目录外不应命中")
	}
	if got := only.Apply(Ask, OriginTool, "file_write", "/proj/b/x.txt"); got != Ask {
		t.Errorf("目录外应保留矩阵 = %s, want ask", got)
	}
}

// TestOverridesApply 两个方向都覆盖矩阵（D280 第 2 条）。
func TestOverridesApply(t *testing.T) {
	allowMatrix := Overrides{{Origin: OriginTool, Tool: "file_write", Mode: ModeAsk}}
	if got := allowMatrix.Apply(Allow, OriginTool, "file_write", ""); got != Ask {
		t.Errorf("矩阵 Allow 被收紧 = %s, want ask", got)
	}
	askMatrix := Overrides{{Origin: OriginTool, Tool: "term_exec", Mode: ModeAllow}}
	if got := askMatrix.Apply(Ask, OriginTool, "term_exec", ""); got != Allow {
		t.Errorf("矩阵 Ask 被放宽 = %s, want allow", got)
	}
	assist := Overrides{{Origin: OriginTool, Tool: "file_write", Mode: ModeAssist}}
	if got := assist.Apply(Ask, OriginTool, "file_write", ""); got != Assist {
		t.Errorf("assist = %s, want assist", got)
	}
	if got := askMatrix.Apply(Ask, OriginTool, "file_read", ""); got != Ask {
		t.Errorf("无命中应保留矩阵 = %s, want ask", got)
	}
}

// TestAutoAllow 确认框里点「以后都允许」生成的例外（D282）：文件类收窄到目标所在
// 目录，执行类与没有目标的操作落到工具级。
func TestAutoAllow(t *testing.T) {
	// 期望值按平台分隔符写：AutoAllow 存的是 filepath 口径，配置只在同一台机器上读。
	wantDir := filepath.FromSlash("/proj/a")
	dir := AutoAllow(OriginTool, "file_write", filepath.FromSlash("/proj/a/x.txt"), true)
	if dir.Mode != ModeAllow || dir.Dir != wantDir || dir.Tool != "file_write" {
		t.Errorf("文件类 = %+v, want dir=%s", dir, wantDir)
	}
	exec := AutoAllow(OriginTool, "term_exec", filepath.FromSlash("/proj/a"), true)
	if exec.Dir != "" || exec.Mode != ModeAllow {
		t.Errorf("执行类 = %+v, want 工具级", exec)
	}
	noTarget := AutoAllow(OriginTool, "list_dir", "", false)
	if noTarget.Dir != "" {
		t.Errorf("无目标 = %+v, want 工具级", noTarget)
	}
	// 相对路径的父目录是 "."：那不是一处有意义的范围，落回工具级。
	if rel := AutoAllow(OriginTool, "file_write", "x.txt", true); rel.Dir != "" {
		t.Errorf("相对路径 = %+v, want 工具级", rel)
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

// TestIsExec 执行类判定：只有命令/任务由模型现编的那两个算执行类；其余内置工具
// （含写文件）不是——写文件有目标路径，执行类没有可预审的内容。
func TestIsExec(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"term_exec", "job_start"} {
		if !IsExec(name) {
			t.Fatalf("%s 应是执行类", name)
		}
	}
	for _, name := range []string{"", "file_write", "file_read", "web_fetch", "mcp:s:run"} {
		if IsExec(name) {
			t.Fatalf("%s 不该是执行类", name)
		}
	}
}
