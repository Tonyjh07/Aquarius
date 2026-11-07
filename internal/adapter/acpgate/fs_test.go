package acpgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/pathres"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestReadTextSlice line/limit 是 1 基行切片：边界、越尾、不给、CRLF 与末尾无换行。
func TestReadTextSlice(t *testing.T) {
	content := "one\r\ntwo\r\nthree"
	f := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cases := []struct {
		name      string
		line, lim int
		want      string
	}{
		{"整个文件", 1, 0, content},
		{"从第一行起给一行的量", 1, 1, "one\r\n"},
		{"第二行一行", 2, 1, "two\r\n"},
		{"末行无换行", 3, 1, "three"},
		{"从第二行读到尾", 2, 0, "two\r\nthree"},
		{"行号越过尾部", 9, 2, ""},
		{"limit 超出剩余行", 3, 10, "three"},
		{"行号为 0 按第一行", 0, 1, "one\r\n"},
		{"负数行号按第一行", -5, 1, "one\r\n"},
		{"负数 limit 按不限", 2, -1, "two\r\nthree"},
	}
	for _, c := range cases {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		got, err := readTextSlice(fh, c.line, c.lim)
		fh.Close()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestFsGate fs 请求的共同前置：会话不属于本次、相对路径、正在停 —— 三种都当场拒。
func TestFsGate(t *testing.T) {
	r := newRun("d1", Agent{Name: "fake"}, port.DelegateSpec{}, "c1")
	r.setSession("sess-1")
	set, err := pathres.NewRootSet([]string{t.TempDir()})
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	r.setRoots(set)
	c := &client{m: &Manager{}, r: r}

	if err := c.fsGate(context.Background(), "other", absPathForTest(t)); err == nil {
		t.Fatal("会话不符应拒")
	}
	if err := c.fsGate(context.Background(), "sess-1", filepath.Join("rel", "f.txt")); err == nil {
		t.Fatal("相对路径应拒")
	}
	if err := c.fsGate(context.Background(), "sess-1", absPathForTest(t)); err != nil {
		t.Fatalf("正常请求不该被拒: %v", err)
	}
	r.requestStop(stopKilled)
	if err := c.fsGate(context.Background(), "sess-1", absPathForTest(t)); err == nil {
		t.Fatal("正在停的转交不该再受理 fs 请求")
	}
}

// TestFsRequestsInRoot 一次完整的 fs 往返：读切片、整读、写新文件（父目录不存在）、
// 越界写与越界读被拒且盘上没变；`Wrote` 只记经手的那个文件。
func TestFsRequestsInRoot(t *testing.T) {
	root := t.TempDir()
	escape := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "whole.txt"), []byte("alpha\nbeta"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(escape, "secret.txt"), []byte("top secret"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var conf fakeConfirmer
	m := newManager(t, "fs", func(o *Options) {
		o.Home = root
		o.Confirmer = &conf
		o.Agents[0].Env["FAKE_AGENT_ESCAPE"] = escape
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "touch files"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(run.Report)), &got); err != nil {
		t.Fatalf("report is not the expected JSON: %v\n%s", err, run.Report)
	}
	if !strings.Contains(got["slice"], `two\r\n`) || strings.Contains(got["slice"], "one") {
		t.Fatalf("slice = %s, want 仅第二行", got["slice"])
	}
	if !strings.Contains(got["whole"], `alpha\nbeta`) {
		t.Fatalf("whole = %s", got["whole"])
	}
	if got["write"] != "{}" {
		t.Fatalf("write = %s, want 成功（空结果）", got["write"])
	}
	wantOnDisk := "written by agent\nsecond line\n"
	if b, err := os.ReadFile(filepath.Join(root, "sub", "dir", "new.txt")); err != nil || string(b) != wantOnDisk {
		t.Fatalf("落盘内容 = %q, %v；want %q（父目录应被创建）", string(b), err, wantOnDisk)
	}
	if !strings.HasPrefix(got["escape_write"], "error") {
		t.Fatalf("越界写应被拒，got %s", got["escape_write"])
	}
	if _, err := os.Stat(filepath.Join(escape, "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("越界写被拒之后盘上不该有文件")
	}
	if !strings.HasPrefix(got["escape_read"], "error") {
		t.Fatalf("越界读应被拒，got %s", got["escape_read"])
	}
	if conf.calls != 0 {
		t.Fatalf("这一版越界不走确认，calls = %d", conf.calls)
	}

	want := mustResolvePath(t, filepath.Join(root, "sub", "dir", "new.txt"))
	if len(run.Wrote) != 1 || run.Wrote[0] != want {
		t.Fatalf("wrote = %v, want [%s]", run.Wrote, want)
	}
	if len(run.Changed) != 0 {
		t.Fatalf("changed = %v, want 空（它没自称改过任何文件）", run.Changed)
	}
	text := port.DelegateReportText(task.ID, run)
	if !strings.Contains(text, "Files written through this session: "+want) {
		t.Fatalf("报告缺经手清单:\n%s", text)
	}
}

// absPathForTest 一个绝对路径（测试里判「相对路径」用）。
func absPathForTest(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "f.txt")
}

// mustResolvePath 判定侧的路径解析（断言写进报告的是解析后的落点）。
func mustResolvePath(t *testing.T, p string) string {
	t.Helper()
	r, err := pathres.Resolve(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}
