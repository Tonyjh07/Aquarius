package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	write("AGENTS.md", "# A\n\n正文\n")
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

	// 第二次提交：只改 docs/a.md 与 README.md（AGENTS.md 不参与）。
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
	// 未改动的 docs/b.md 不产生标记，根上不在覆盖范围的 AGENTS.md 也不该被动。
	if _, err := os.Stat(filepath.Join(dir, "docs", "b.md")); err == nil {
		t.Fatal("测试前提：不应存在 docs/b.md")
	}
	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if hasMarker(string(agents)) {
		t.Fatalf("AGENTS.md 不该有标记:\n%s", agents)
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

// TestRunCommitModeSkipsMarkerOnlyCommit 只改了标记行的提交不刷新日期（否则一次
// 提交会把顺带加标记的页面日期全刷成同一天）。
func TestRunCommitModeSkipsMarkerOnlyCommit(t *testing.T) {
	dir := gitRepo(t)

	// 先给 docs/a.md 补标记并提交（这一步是「只动标记」）。
	var out, errBuf bytes.Buffer
	if code := run([]string{"-root", dir}, &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "markers"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if cmdErr := cmd.Run(); cmdErr != nil {
			t.Fatalf("git %v: %v", args, cmdErr)
		}
	}
	before, err := os.ReadFile(filepath.Join(dir, "docs", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasMarker(string(before)) {
		t.Fatalf("测试前提：docs/a.md 应已带标记:\n%s", before)
	}
	// 用 -commit 处理那笔「只动标记」的提交：应报「没有改动文档」，标记一字不动。
	out.Reset()
	if code := run([]string{"-root", dir, "-commit", "HEAD"}, &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "没有改动文档") {
		t.Fatalf("只改标记的提交不该被刷新:\n%s", out.String())
	}
	after, err := os.ReadFile(filepath.Join(dir, "docs", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("标记被无谓改动:\nbefore %s\nafter  %s", before, after)
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

// git 在 dir 里执行一条命令，返回去空白输出。
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// appendTo 往仓库里的文件追加一行。
func appendTo(t *testing.T, dir, name, line string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte(line)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// read 读仓库里的文件。
func read(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestRunHistoryFillsCommittedDoc 工作区干净的文档也要按历史补标记：
// 日期/基线取它最近一次内容改动的那笔提交，而不是「今天 + HEAD」。
func TestRunHistoryFillsCommittedDoc(t *testing.T) {
	dir := gitRepo(t)

	// 第二笔提交改 docs/a.md；之后再提交一笔只动代码的提交，让 HEAD 与内容提交分开。
	appendTo(t, dir, "docs/a.md", "补充一段\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "docs: a")
	contentDay := git(t, dir, "log", "-1", "--format=%ad", "--date=short", "HEAD")
	parent := git(t, dir, "rev-parse", "--short", "HEAD^")

	appendTo(t, dir, "go.mod", "\n// touch\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "chore: touch code")
	head := git(t, dir, "rev-parse", "--short", "HEAD")

	var out, errBuf bytes.Buffer
	if code := run([]string{"-root", dir}, &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	got := read(t, dir, "docs/a.md")
	if !strings.Contains(got, contentDay+" @ "+parent) {
		t.Fatalf("标记应为内容提交的日期 @ 它的父提交 %s @ %s:\n%s", contentDay, parent, got)
	}
	if strings.Contains(got, "@ "+head) {
		t.Fatalf("标记不该指向只改代码的 HEAD:\n%s", got)
	}
	if today := time.Now().Format("2006-01-02"); contentDay != today && strings.Contains(got, today+" @") {
		t.Fatalf("干净文档不该被刷成今天:\n%s", got)
	}
}

// TestRunHistorySkipsMarkerOnlyCommit 只改标记行的提交不算内容改动：历史要往回找，
// 顺带保证重复跑幂等（第二次不写任何文件）。
func TestRunHistorySkipsMarkerOnlyCommit(t *testing.T) {
	dir := gitRepo(t)

	appendTo(t, dir, "docs/a.md", "补充一段\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "docs: a")
	contentDay := git(t, dir, "log", "-1", "--format=%ad", "--date=short", "HEAD")
	parent := git(t, dir, "rev-parse", "--short", "HEAD^")

	// 手工塞一个错误的标记并单独提交（模拟「补标记」那类提交）。
	body := read(t, dir, "docs/a.md")
	body = strings.Replace(body, "# A\n", "# A\n<!-- docs-sync: 2020-01-01 @ deadbee -->\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "docs", "a.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "docs: markers")

	var out, errBuf bytes.Buffer
	if code := run([]string{"-root", dir}, &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	got := read(t, dir, "docs/a.md")
	if !strings.Contains(got, contentDay+" @ "+parent) {
		t.Fatalf("应跳过只改标记的提交，回到内容提交 %s @ %s:\n%s", contentDay, parent, got)
	}

	// 再跑一次：标记已是历史值，不该再动。
	out.Reset()
	if code := run([]string{"-root", dir}, &out, &errBuf); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errBuf.String())
	}
	if again := read(t, dir, "docs/a.md"); again != got {
		t.Fatalf("重复跑改了文件:\nbefore %s\nafter  %s", got, again)
	}
}
