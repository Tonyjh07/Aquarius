package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/assets"
)

// readmeCells 取一份正文里所有表格行的第一格，返回其中的反引号词（去掉结尾的 `/`）。
//
// 只扫以 `| ` 开头的行——正文里 `--profile <名字>` 这类行内代码不参与比对。
func readmeCells(src []byte) []string {
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cell := strings.Split(line, "|")[1]
		// 按反引号切开，奇数位即词。
		chunks := strings.Split(cell, "`")
		for i := 1; i < len(chunks); i += 2 {
			out = append(out, strings.TrimSuffix(chunks[i], "/"))
		}
	}
	return out
}

// TestRenderDataReadmeIsStable 同一版本渲染两次逐字节相同：判等靠字节比较，抖动一次
// 就等于每次启动都重写。
func TestRenderDataReadmeIsStable(t *testing.T) {
	a := renderDataReadme("v0.7.0")
	b := renderDataReadme("v0.7.0")
	if string(a) != string(b) {
		t.Fatal("同一版本两次渲染结果不同")
	}
	text := string(a)
	if !strings.HasPrefix(text, dataReadmeMarker) {
		t.Fatalf("首行不是生成标记：%q", strings.SplitN(text, "\n", 2)[0])
	}
	if !strings.Contains(text, "v0.7.0") {
		t.Fatal("首行没有带上版本号")
	}
	for _, want := range []string{"# Aquarius 数据目录 / Data directory", "## 中文", "## English"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少 %q", want)
		}
	}
	if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		t.Fatal("结尾应恰好一个换行")
	}
}

// TestRenderDataReadmeFollowsVersion 版本一变内容就变：升级后说明必须跟着换。
func TestRenderDataReadmeFollowsVersion(t *testing.T) {
	if string(renderDataReadme("v0.7.0")) == string(renderDataReadme("v0.8.0")) {
		t.Fatal("版本不同但渲染结果相同")
	}
	if !strings.Contains(string(renderDataReadme("")), "dev") {
		t.Fatal("空版本应回落为 dev")
	}
}

// TestDataReadmeCoversBothLanguages 中英两份的表格条目必须一致：一边加了条目另一边
// 忘了加，两个读者看到的是两张不同的表。
func TestDataReadmeCoversBothLanguages(t *testing.T) {
	zh := readmeCells(assets.DataReadmeZH)
	en := readmeCells(assets.DataReadmeEN)
	if len(zh) == 0 || len(en) == 0 {
		t.Fatal("没有解析到表格条目")
	}
	sorted := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.Strings(out)
		return out
	}
	zh, en = sorted(zh), sorted(en)
	if strings.Join(zh, ",") != strings.Join(en, ",") {
		t.Fatalf("中英条目不一致\nzh: %v\nen: %v", zh, en)
	}
}

// TestDataReadmeEntriesExistInStorageDoc 表里的条目名必须能在 storage.md 的目录树里找到。
// 这是唯一能拦住「代码里那份说明与磁盘口径分家」的一条：新增目录时改了 storage.md 却忘了
// 改 README，测试红。
//
// 带 `<...>`（参数化路径）与 `*`（通配）的条目跳过——它们在正文里的写法与目录树不同。
func TestDataReadmeEntriesExistInStorageDoc(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "storage.md"))
	if err != nil {
		t.Skipf("读不到 docs/storage.md（不在仓库内跑）：%v", err)
	}
	text := string(doc)
	for _, name := range readmeCells(assets.DataReadmeZH) {
		if strings.ContainsAny(name, "<*/") {
			continue
		}
		if !strings.Contains(text, name) {
			t.Errorf("README 里的 %q 在 docs/storage.md 的目录树里找不到", name)
		}
	}
}

// TestDataReadmeLinksBothLanguages 两段都要带仓库与 storage.md 的链接。读者常常只有这个
// 目录，没有仓库——链接是唯一能把「完整口径在哪」指出去的东西，少一边就等于少一位读者。
func TestDataReadmeLinksBothLanguages(t *testing.T) {
	links := []string{
		"https://github.com/Tonyjh07/Aquarius",
		"https://github.com/Tonyjh07/Aquarius/blob/main/docs/storage.md",
	}
	for name, src := range map[string][]byte{"zh.md": assets.DataReadmeZH, "en.md": assets.DataReadmeEN} {
		for _, want := range links {
			if !strings.Contains(string(src), want) {
				t.Errorf("%s 缺少链接 %s", name, want)
			}
		}
	}
}

// TestDataReadmeStaysShort 单语正文行数上限。这份东西的用处是速查，一旦长到要翻页，
// 读者会去翻的就不是它了；上限比现状留一点余量，只为挡住持续膨胀。
func TestDataReadmeStaysShort(t *testing.T) {
	const limit = 70
	for name, src := range map[string][]byte{
		"zh.md": assets.DataReadmeZH,
		"en.md": assets.DataReadmeEN,
	} {
		if n := strings.Count(string(src), "\n"); n > limit {
			t.Errorf("%s 有 %d 行，超过上限 %d", name, n, limit)
		}
	}
}

// TestEnsureDataReadmeCreates 目录里没有就写一份。
func TestEnsureDataReadmeCreates(t *testing.T) {
	dir := t.TempDir()
	out, err := ensureDataReadme(dir, "v0.7.0")
	if err != nil || out != readmeCreated {
		t.Fatalf("out=%v err=%v，期望新建", out, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, dataReadmeName))
	if err != nil {
		t.Fatalf("读完失败：%v", err)
	}
	if string(got) != string(renderDataReadme("v0.7.0")) {
		t.Fatal("落盘内容与渲染结果不同")
	}
}

// TestEnsureDataReadmeLeavesIdenticalFileAlone 内容相同时连 mtime 都不动：写一遍会把
// 「这个文件什么时候变的」这条信息抹掉。
func TestEnsureDataReadmeLeavesIdenticalFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, dataReadmeName)
	if _, err := ensureDataReadme(dir, "v0.7.0"); err != nil {
		t.Fatalf("首次写入失败：%v", err)
	}
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("设置 mtime 失败：%v", err)
	}
	out, err := ensureDataReadme(dir, "v0.7.0")
	if err != nil || out != readmeUnchanged {
		t.Fatalf("out=%v err=%v，期望原样不动", out, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("取文件信息失败：%v", err)
	}
	if !info.ModTime().Equal(stamp) {
		t.Fatalf("mtime 被改写：%v", info.ModTime())
	}
}

// TestEnsureDataReadmeRewritesMarkedFile 有标记、版本变了就重写。
func TestEnsureDataReadmeRewritesMarkedFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureDataReadme(dir, "v0.7.0"); err != nil {
		t.Fatalf("首次写入失败：%v", err)
	}
	out, err := ensureDataReadme(dir, "v0.8.0")
	if err != nil || out != readmeUpdated {
		t.Fatalf("out=%v err=%v，期望重写", out, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, dataReadmeName))
	if err != nil {
		t.Fatalf("读完失败：%v", err)
	}
	if !strings.Contains(string(got), "v0.8.0") {
		t.Fatal("重写后仍是旧版本")
	}
}

// TestEnsureDataReadmeKeepsUserFile 没有标记的文件是用户自己写的，保留原样。
func TestEnsureDataReadmeKeepsUserFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, dataReadmeName)
	mine := []byte("# 我自己的笔记\n")
	if err := os.WriteFile(path, mine, 0o644); err != nil {
		t.Fatalf("铺底失败：%v", err)
	}
	out, err := ensureDataReadme(dir, "v0.7.0")
	if err != nil || out != readmeKept {
		t.Fatalf("out=%v err=%v，期望保留", out, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读完失败：%v", err)
	}
	if string(got) != string(mine) {
		t.Fatal("用户文件被覆盖了")
	}
}

// TestEnsureDataReadmeReportsFailure 同名目录占位时报失败而不是 panic。
func TestEnsureDataReadmeReportsFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, dataReadmeName), 0o755); err != nil {
		t.Fatalf("铺底失败：%v", err)
	}
	out, err := ensureDataReadme(dir, "v0.7.0")
	if out != readmeFailed || err == nil {
		t.Fatalf("out=%v err=%v，期望失败并带原因", out, err)
	}
}
