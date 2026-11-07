package acpgate

import "testing"

// TestExecutionControl 控制文件清单：命中的要问，普通项目文件不要问（多问一次就是一次
// 用户看着没关系、实际在消耗注意力的打扰）。
func TestExecutionControl(t *testing.T) {
	hits := map[string]string{
		"/repo/.git/hooks/pre-commit":      ".git/hooks",
		`C:/repo/.git/hooks/post-checkout`: ".git/hooks",
		"/repo/.git/config":                ".git/config",
		"/repo/.gitmodules":                ".gitmodules",
		"/repo/.github/workflows/ci.yml":   ".github/workflows",
		"/repo/.vscode/settings.json":      ".vscode",
		"/repo/sub/.idea/workspace.xml":    ".idea",
		"/repo/.cursor/rules.md":           ".cursor",
		"/home/u/.claude/settings.json":    ".claude",
		"/repo/.codex/config.toml":         ".codex",
		"/repo/.mcp.json":                  ".mcp.json",
		`C:/repo/.GIT/Hooks/pre-push`:      ".git/hooks",
	}
	for p, want := range hits {
		if got := executionControl(p); got != want {
			t.Fatalf("executionControl(%q) = %q, want %q", p, got, want)
		}
	}
	plain := []string{
		"/repo/.git/HEAD",
		"/repo/README.md",
		"/repo/src/main.go",
		"/repo/.github/ISSUE_TEMPLATE/bug.md",
		"/repo/docs/.vscode.md",
		"/repo/.mcp.jsonc",
	}
	for _, p := range plain {
		if got := executionControl(p); got != "" {
			t.Fatalf("executionControl(%q) = %q, want 空（普通项目文件不该问）", p, got)
		}
	}
}
