package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := evidence{
		SHA:       "abc1234",
		Dirty:     true,
		Profile:   "fast",
		StartedAt: time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC),
		Jobs: []jobResult{
			{Name: "fmt", Status: stPass, DurationMS: 900},
			{Name: "vet", Status: stFail, Reason: "退出码非零", Output: "boom"},
		},
		Passed: 1, Failed: 1,
	}
	if err := writeRecord(root, want); err != nil {
		t.Fatalf("写证据失败：%v", err)
	}

	got, err := readRecord(root)
	if err != nil {
		t.Fatalf("读证据失败：%v", err)
	}
	ev, ok := got["fast"]
	if !ok {
		t.Fatal("没有取回 fast 档")
	}
	if ev.SHA != want.SHA || !ev.Dirty || len(ev.Jobs) != 2 || ev.Jobs[1].Output != "boom" {
		t.Fatalf("内容对不上：%+v", ev)
	}
}

func TestRecordKeepsOtherProfiles(t *testing.T) {
	root := t.TempDir()
	if err := writeRecord(root, evidence{Profile: "fast", SHA: "aaa"}); err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(root, evidence{Profile: "full", SHA: "bbb"}); err != nil {
		t.Fatal(err)
	}
	got, _ := readRecord(root)
	if got["fast"].SHA != "aaa" || got["full"].SHA != "bbb" {
		t.Fatalf("后一档不该覆盖前一档：%+v", got)
	}
}

func TestReadRecordToleratesBrokenFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(recordPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath(root), []byte("{ 这不是 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readRecord(root)
	if err != nil {
		t.Fatalf("坏文件不该让门禁报错，得到 %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("坏文件应视作没有历史，得到 %v", got)
	}
}

func TestWriteJSONCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "ci-report.json")
	if err := writeJSON(path, evidence{SHA: "x"}); err != nil {
		t.Fatalf("写 JSON 失败：%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("文件不存在：%v", err)
	}
}

func TestGitHeadInRepo(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skip("不在 git 仓库内，跳过")
	}
	sha, _, err := gitHead(root)
	if err != nil {
		t.Fatalf("取提交号失败：%v", err)
	}
	if len(sha) < 7 {
		t.Fatalf("提交号看着不对：%q", sha)
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("0123456789abcdef"); got != "0123456" {
		t.Errorf("应取前 7 位，得到 %q", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("短于 7 位应原样返回，得到 %q", got)
	}
}
