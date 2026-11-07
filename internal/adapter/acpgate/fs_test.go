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
	if !strings.HasPrefix(got["control_write"], "error") {
		t.Fatalf("控制文件写应被拒，got %s", got["control_write"])
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "hooks", "pre-commit")); !os.IsNotExist(err) {
		t.Fatal("控制文件写被拒之后盘上不该有文件")
	}
	// 三次询问：越界写、越界读、控制文件写（同一次转交里各问一次）。
	if conf.calls != 3 {
		t.Fatalf("calls = %d, want 3", conf.calls)
	}
	if !strings.Contains(strings.Join(run.Notes, "\n"), "read outside the session root set was refused") {
		t.Fatalf("被拒的越界读应留一条备注：%v", run.Notes)
	}
	if len(run.ReadOutside) != 0 {
		t.Fatalf("read outside = %v, want 空（都被拒了）", run.ReadOutside)
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

// TestFsOutsideAllowed 用户逐次批准之后：越界读写与控制文件写都放行，报告里两段都出现
// ——「经手写」与「问过才读」各一份事实。
func TestFsOutsideAllowed(t *testing.T) {
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

	conf := fakeConfirmer{allow: true}
	m := newManager(t, "fs", func(o *Options) {
		o.Home = root
		o.Confirmer = &conf
		o.Agents[0].Env["FAKE_AGENT_ESCAPE"] = escape
	})
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "reach outside"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(run.Report)), &got); err != nil {
		t.Fatalf("report not JSON: %v", err)
	}
	if got["escape_write"] != "{}" {
		t.Fatalf("批准后越界写应成功，got %s", got["escape_write"])
	}
	if b, err := os.ReadFile(filepath.Join(escape, "outside.txt")); err != nil || string(b) != "should not land\n" {
		t.Fatalf("越界写落盘 = %q, %v", string(b), err)
	}
	if !strings.Contains(got["escape_read"], "top secret") {
		t.Fatalf("批准后越界读应拿到内容，got %s", got["escape_read"])
	}
	if got["control_write"] != "{}" {
		t.Fatalf("批准后控制文件写应成功，got %s", got["control_write"])
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatalf("控制文件应落盘: %v", err)
	}

	wantRead := mustResolvePath(t, filepath.Join(escape, "secret.txt"))
	if len(run.ReadOutside) != 1 || run.ReadOutside[0] != wantRead {
		t.Fatalf("read outside = %v, want [%s]", run.ReadOutside, wantRead)
	}
	text := port.DelegateReportText(task.ID, run)
	if !strings.Contains(text, "Files it read outside the session scope, after you approved each one: "+wantRead) {
		t.Fatalf("报告缺越界读清单:\n%s", text)
	}
}

// TestAllowReadLimit 越界读的额度：问过才消耗、同一路径不重复问、额度用完直接拒。
func TestAllowReadLimit(t *testing.T) {
	conf := &fakeConfirmer{allow: true}
	m := New(Options{Confirmer: conf, OutsideReadAsks: intPtr(2)})
	t.Cleanup(m.Close)
	r := newRun("d1", Agent{Name: "fake"}, port.DelegateSpec{}, "c1")
	r.setSession("sess-1")
	c := &client{m: m, r: r}
	ctx := context.Background()

	for i, p := range []string{"/a", "/b"} {
		if ok, why := c.allowRead(ctx, p); !ok {
			t.Fatalf("第 %d 次应放行：%s", i+1, why)
		}
	}
	if conf.calls != 2 {
		t.Fatalf("calls = %d, want 2", conf.calls)
	}
	// 同一路径再读：不再问，直接放行。
	if ok, _ := c.allowRead(ctx, "/a"); !ok {
		t.Fatal("同一路径的第二次读不该再问")
	}
	if conf.calls != 2 {
		t.Fatalf("重复路径不该再问，calls = %d", conf.calls)
	}
	// 第三个路径：额度用完 → 拒，且不问。
	if ok, why := c.allowRead(ctx, "/c"); ok || !strings.Contains(why, "limit") {
		t.Fatalf("额度用完应拒，got ok=%v why=%q", ok, why)
	}
	if conf.calls != 2 {
		t.Fatalf("额度用完后不该再问，calls = %d", conf.calls)
	}
}

// TestAllowReadNever 配置成「不问」（0）：一次都不问，直接拒并留档。
func TestAllowReadNever(t *testing.T) {
	conf := &fakeConfirmer{allow: true}
	m := New(Options{Confirmer: conf, OutsideReadAsks: intPtr(0)})
	t.Cleanup(m.Close)
	r := newRun("d1", Agent{Name: "fake"}, port.DelegateSpec{}, "c1")
	r.setSession("sess-1")
	c := &client{m: m, r: r}

	if ok, _ := c.allowRead(context.Background(), "/a"); ok {
		t.Fatal("配置成不问时越界读应直接拒")
	}
	if conf.calls != 0 {
		t.Fatalf("不该问用户，calls = %d", conf.calls)
	}
	if !strings.Contains(strings.Join(r.snapshot().notes, "\n"), "read outside the session root set was refused") {
		t.Fatalf("被拒的越界读要留档：%v", r.snapshot().notes)
	}
}

// TestAllowReadDeniedCached 用户拒过一次的路径不再重复打扰（否则扫目录会连着问同一件事）。
func TestAllowReadDeniedCached(t *testing.T) {
	conf := &fakeConfirmer{allow: false, reason: "nope"}
	m := New(Options{Confirmer: conf})
	t.Cleanup(m.Close)
	r := newRun("d1", Agent{Name: "fake"}, port.DelegateSpec{}, "c1")
	r.setSession("sess-1")
	c := &client{m: m, r: r}

	if ok, _ := c.allowRead(context.Background(), "/a"); ok {
		t.Fatal("拒绝后不该放行")
	}
	if ok, _ := c.allowRead(context.Background(), "/a"); ok {
		t.Fatal("同一路径第二次也不该放行")
	}
	if conf.calls != 1 {
		t.Fatalf("同一路径只问一次，calls = %d", conf.calls)
	}
}

// intPtr 取指针（Options 里「没配」与「配成 0」是两件事）。
func intPtr(n int) *int { return &n }
