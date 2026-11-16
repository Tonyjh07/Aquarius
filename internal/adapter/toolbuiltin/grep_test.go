package toolbuiltin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grepFixture 造一棵小目录树用于内容搜索。
func grepFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "alpha\nneedle here\nomega\n")
	write("sub/b.txt", "nothing\nNEEDLE upper\n")
	write(".git/config", "needle in the repository metadata\n")
	write("blob.bin", "needle\x00binary\n")
	write("regex.md", "foo(1)\nbar[2]\n")
	return dir
}

// TestFileGrepFindsLineNumbers 命中行给 `路径:行号:内容`，行号从 1 起。
func TestFileGrepFindsLineNumbers(t *testing.T) {
	dir := grepFixture(t)
	r := pick(t, New(Deps{}), "file_grep")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"needle"}`))
	if err != nil {
		t.Fatal(err)
	}
	// 大小写敏感：只有 a.txt 的第二行命中。
	if !strings.Contains(res.Output, filepath.Join(dir, "a.txt")+":2:needle here") {
		t.Fatalf("output missing the expected hit:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "NEEDLE upper") {
		t.Fatalf("case-sensitive search matched uppercase:\n%s", res.Output)
	}
	// .git 与二进制都不进结果。
	if strings.Contains(res.Output, "repository metadata") || strings.Contains(res.Output, "binary") {
		t.Fatalf("search leaked skipped content:\n%s", res.Output)
	}
}

// TestFileGrepIgnoreCase 折叠大小写后两条都命中。
func TestFileGrepIgnoreCase(t *testing.T) {
	dir := grepFixture(t)
	r := pick(t, New(Deps{}), "file_grep")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"needle","ignore_case":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "needle here") || !strings.Contains(res.Output, "NEEDLE upper") {
		t.Fatalf("ignore_case missed one of the hits:\n%s", res.Output)
	}
}

// TestFileGrepLiteralSpecialChars 没有 regex=true 时，正则元字符按字面算。
func TestFileGrepLiteralSpecialChars(t *testing.T) {
	dir := grepFixture(t)
	r := pick(t, New(Deps{}), "file_grep")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"foo(1)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "foo(1)") {
		t.Fatalf("literal search failed:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "bar[2]") {
		t.Fatalf("literal search behaved like a regex:\n%s", res.Output)
	}
}

// TestFileGrepRegex 正则可跨行内容匹配任意一元字符组。
func TestFileGrepRegex(t *testing.T) {
	dir := grepFixture(t)
	r := pick(t, New(Deps{}), "file_grep")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"ba[rz]","regex":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "bar[2]") {
		t.Fatalf("regex search failed:\n%s", res.Output)
	}
	if _, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"(","regex":true}`)); err == nil {
		t.Fatal("want an error for an invalid regular expression")
	}
}

// TestFileGrepHitLimit 命中过多时截断并说明触的是哪道上限。
func TestFileGrepHitLimit(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxGrepHits+30; i++ {
		fmt.Fprintf(&b, "hit %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	r := pick(t, New(Deps{}), "file_grep")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"hit"}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(res.Output, "\n") + 1; n > maxGrepHits+1 {
		t.Fatalf("output has %d lines, want at most %d plus the notice", n, maxGrepHits)
	}
	if !strings.Contains(res.Output, "hit the limit") {
		t.Fatalf("limit notice missing:\n%s", res.Output)
	}
}

// TestFileGrepRejectsBadArgs 非绝对路径与空 query 都明确报错（与其它文件工具同口径）。
func TestFileGrepRejectsBadArgs(t *testing.T) {
	r := pick(t, New(Deps{}), "file_grep")
	if _, err := r.Execute(context.Background(), call(`{"path":"relative/dir","query":"x"}`)); err == nil {
		t.Fatal("want an error for a relative path")
	}
	if _, err := r.Execute(context.Background(), call(`{"path":`+js(t.TempDir())+`,"query":"  "}`)); err == nil {
		t.Fatal("want an error for an empty query")
	}
}

// TestFileGrepNoMatch 没有命中给明确的空态，不是空字符串。
func TestFileGrepNoMatch(t *testing.T) {
	dir := grepFixture(t)
	r := pick(t, New(Deps{}), "file_grep")
	res, err := r.Execute(context.Background(), call(`{"path":`+js(dir)+`,"query":"nothing-like-this-anywhere"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != emptyMatches {
		t.Fatalf("output = %q, want the empty-state text", res.Output)
	}
}
