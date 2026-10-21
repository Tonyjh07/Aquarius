package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo 建一个临时仓库（有 go.mod，便于 repoRoot 与 targets 工作），返回根目录。
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境无 git")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/tmp\n\ngo 1.21\n")
	write("README.md", "# T\n\n正文\n")
	write("docs/a.md", "# A\n\n正文\n")
	write("DESIGN.md", "# D\n\n正文\n")
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// TestRunCommitModeOnlyTouchesThatCommit 只刷新该提交改过的文档，其余保持原标记。
func TestRunCommitModeOnlyTouchesThatCommit(t *testing.T) {
	dir := gitRepo(t)

	// 第二次提交：只改 docs/a.md 与 README.md（DESIGN.md 不参与）。
	for _, f := range []string{"docs/a.md", "README.md"} {
		path := filepath.Join(dir, filepath.FromSlash(f))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, []byte("新增一行\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "docs"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	var out, errBuf bytes.Buffer
	if code := run([]string{"-root", dir, "-commit", "HEAD"}, &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	for _, f := range []string{"docs/a.md", "README.md"} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if !hasMarker(string(raw)) {
			t.Fatalf("%s 缺标记:\n%s", f, raw)
		}
	}
	// 未改动的 docs/b.md 不产生标记，DESIGN.md 也不该被动。
	if _, err := os.Stat(filepath.Join(dir, "docs", "b.md")); err == nil {
		t.Fatal("测试前提：不应存在 docs/b.md")
	}
	design, err := os.ReadFile(filepath.Join(dir, "DESIGN.md"))
	if err != nil {
		t.Fatal(err)
	}
	if hasMarker(string(design)) {
		t.Fatalf("DESIGN.md 不该有标记:\n%s", design)
	}
	// 标记的哈希是父提交（代码基线），不是本提交自身。
	head, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD^").Output()
	if err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(dir, "docs", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a), "@ "+strings.TrimSpace(string(parent))) {
		t.Fatalf("标记应指向父提交 %s:\n%s", parent, a)
	}
	if strings.Contains(string(a), "@ "+strings.TrimSpace(string(head))) {
		t.Fatalf("标记不该指向本提交自身:\n%s", a)
	}
}

// TestRunCheckReportsMissing 缺标记 → 退出 1；补齐后 → 退出 0。
func TestRunCheckReportsMissing(t *testing.T) {
	dir := gitRepo(t)
	var out, errBuf bytes.Buffer
	if code := run([]string{"-root", dir, "-check"}, &out, &errBuf); code != 1 {
		t.Fatalf("缺标记时 code = %d，应为 1", code)
	}
	out.Reset()
	if code := run([]string{"-root", dir}, &out, &errBuf); code != 0 {
		t.Fatalf("补齐时 code = %d, stderr = %s", code, errBuf.String())
	}
	out.Reset()
	if code := run([]string{"-root", dir, "-check"}, &out, &errBuf); code != 0 {
		t.Fatalf("补齐后 code = %d, stdout = %s", code, out.String())
	}
}
