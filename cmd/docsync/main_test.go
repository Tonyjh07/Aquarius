package main

import (
	"strings"
	"testing"
)

// TestSyncTextInsertsAfterH1 标记插在首个 H1 之后、正文之前（渲染时不可见）。
func TestSyncTextInsertsAfterH1(t *testing.T) {
	src := "# 标题\n\n正文第一行\n"
	got := syncText(src, "2026-10-21", "f1b9ed1")
	want := "# 标题\n<!-- docs-sync: 2026-10-21 @ f1b9ed1 -->\n\n正文第一行\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// TestSyncTextIdempotent 重复同步不叠加标记，且换日期/哈希只改那一行。
func TestSyncTextIdempotent(t *testing.T) {
	src := "# 标题\n<!-- docs-sync: 2026-10-20 @ aaaaaaa -->\n\n正文\n"
	once := syncText(src, "2026-10-21", "f1b9ed1")
	twice := syncText(once, "2026-10-21", "f1b9ed1")
	if once != twice {
		t.Fatalf("非幂等:\n once %q\ntwice %q", once, twice)
	}
	if n := strings.Count(twice, "docs-sync:"); n != 1 {
		t.Fatalf("标记条数 = %d，应为 1:\n%s", n, twice)
	}
	if !strings.Contains(twice, "2026-10-21 @ f1b9ed1") {
		t.Fatalf("未换成新标记:\n%s", twice)
	}
	if !strings.Contains(twice, "\n正文\n") {
		t.Fatalf("正文被破坏:\n%q", twice)
	}
}

// TestSyncTextCRLF 输入是 CRLF 时输出统一为 LF（仓库属性要求）。
func TestSyncTextCRLF(t *testing.T) {
	got := syncText("# 标题\r\n\r\n正文\r\n", "2026-10-21", "f1b9ed1")
	if strings.Contains(got, "\r") {
		t.Fatalf("残留 CR: %q", got)
	}
}

// TestSyncTextNoH1 没有 H1 的文件原样返回（不改无标题片段）。
func TestSyncTextNoH1(t *testing.T) {
	src := "## 只有二级标题\n\n正文\n"
	if got := syncText(src, "2026-10-21", "f1b9ed1"); got != src {
		t.Fatalf("应原样返回，got %q", got)
	}
}

// TestSyncTextKeepsInlineExample 正文里的同形示例（开发指南的用法说明）不被当成标记。
func TestSyncTextKeepsInlineExample(t *testing.T) {
	src := "# 指南\n\n正文\n\n```markdown\n# 标题\n<!-- docs-sync: 2026-10-20 @ aaaaaaa -->\n```\n"
	got := syncText(src, "2026-10-21", "f1b9ed1")
	if strings.Count(got, "docs-sync:") != 2 {
		t.Fatalf("应保留正文示例并新增头部标记:\n%s", got)
	}
	if !strings.Contains(got, "# 指南\n<!-- docs-sync: 2026-10-21 @ f1b9ed1 -->\n\n正文") {
		t.Fatalf("头部标记位置不对:\n%s", got)
	}
	if !strings.Contains(got, "2026-10-20 @ aaaaaaa") {
		t.Fatalf("正文示例被改:\n%s", got)
	}
}

// TestMarkerRoundTrip 写进去的标记能被 hasMarker 认出来（-check 依赖它）。
func TestMarkerRoundTrip(t *testing.T) {
	src := "# 标题\n\n正文\n"
	if hasMarker(src) {
		t.Fatal("无标记的文件不应被认作已标记")
	}
	if !hasMarker(syncText(src, "2026-10-21", "f1b9ed1")) {
		t.Fatal("同步后应带标记")
	}
}

// TestIsDocPath 覆盖范围：docs/ 下 .md 与根 README.md；DESIGN/AGENTS 不参与。
func TestIsDocPath(t *testing.T) {
	cases := map[string]bool{
		"docs/guide.md":                true,
		"docs/releases/v0.1.0.md":      true,
		"README.md":                    true,
		"DESIGN.md":                    false,
		"AGENTS.md":                    false,
		"internal/adapter/readme.md":   false,
		"cmd/docsync/main.go":          false,
		"docs/relation-map-design.md":  true,
		"docs/relation-map-physics.md": true,
		"docs/configuration.md.bak":    false,
	}
	for path, want := range cases {
		if got := isDocPath(path); got != want {
			t.Errorf("isDocPath(%q) = %v，应为 %v", path, got, want)
		}
	}
}
