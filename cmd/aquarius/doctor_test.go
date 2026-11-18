package main

// doctor_test.go `/doctor` 的装配根一半（§7.14/D306）：七项各自把「正常」与「坏了」
// 两面都走到——体检的价值全在坏的那一面说得准不准。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/adapter/secretbin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/plugin"
)

// TestCheckConfig 配置一项读的是磁盘现状：文件好着就正常，改坏了就报出解析错误与路径。
func TestCheckConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	w := &wiring{profileDir: dir, cfgPath: path}

	if err := os.WriteFile(path, []byte(`{"model":{"providers":[{"name":"p","models":[{"name":"m"}]}]}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := checkConfig(w); got.State != app.HealthOK || !strings.Contains(got.Detail, "1") {
		t.Fatalf("正常配置 = %+v", got)
	}

	if err := os.WriteFile(path, []byte(`{"model":`), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	got := checkConfig(w)
	if got.State != app.HealthFail {
		t.Fatalf("坏配置应当异常：%+v", got)
	}
	if !strings.Contains(got.Detail, path) || !strings.Contains(got.Fix, path) {
		t.Fatalf("原因与修法都要指到文件：%+v", got)
	}
}

// TestCheckSecrets 密钥库一项真的解一次：没存过 = 正常空库，密文坏了 = 异常且修法
// 走「带钥匙回来」那条，不是笼统的「看一下权限」。
func TestCheckSecrets(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := secretbin.New(dir)
	if err != nil {
		t.Fatalf("secretbin: %v", err)
	}

	if got := checkSecrets(ctx, &wiring{secretsBin: nil}); got.State != app.HealthWarn {
		t.Fatalf("未接入应当注意：%+v", got)
	}
	if got := checkSecrets(ctx, &wiring{secretsBin: store}); got.State != app.HealthOK {
		t.Fatalf("空库应当正常：%+v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "secrets.bin"), []byte("not-an-envelope"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := checkSecrets(ctx, &wiring{secretsBin: store})
	if got.State != app.HealthFail {
		t.Fatalf("坏库应当异常：%+v", got)
	}
	if got.Fix != i18n.T("cmd.doctor.secrets_unreadable_fix") {
		t.Fatalf("修法没走「钥匙不对」那条：%q", got.Fix)
	}
}

// TestCheckSessions 会话一项报出被 List 静默跳过的坏文件，正常时给文件数。
func TestCheckSessions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storejson.New(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	w := &wiring{sessionDir: dir}

	if got := checkSessions(ctx, w); got.State != app.HealthOK {
		t.Fatalf("空目录应当正常：%+v", got)
	}

	c := conversation.New(conversation.ID("conv-1"), "t")
	if _, err := c.Append(conversation.RoleUser, []conversation.Part{{Kind: conversation.PartText, Text: "hi"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken-1.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := checkSessions(ctx, w)
	if got.State != app.HealthFail {
		t.Fatalf("坏文件应当异常：%+v", got)
	}
	more := strings.TrimSpace(strings.SplitN(i18n.T("cmd.doctor.sessions_more"), "{", 2)[0])
	if !strings.Contains(got.Detail, "broken-1.json") || strings.Contains(got.Detail, more) {
		t.Fatalf("只有一个坏文件时要点名且不提「另有」：%q", got.Detail)
	}
}

// TestCheckMCPSnapshotOnly 只看状态快照：没有声明不是问题，宿主缺失记注意。
func TestCheckMCP(t *testing.T) {
	if got := checkMCP(&wiring{host: nil}); got.State != app.HealthWarn {
		t.Fatalf("没有宿主应当注意：%+v", got)
	}
	got := checkMCP(&wiring{host: &plugin.Host{}})
	if got.State != app.HealthOK || got.Detail != i18n.T("cmd.doctor.mcp_none") {
		t.Fatalf("没有声明应当正常：%+v", got)
	}
}

// TestCheckDir 目录一项：不存在 = 0 个文件不是错误；越过阈值升为注意并给清法。
func TestCheckDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if got := checkDir("logs", dir, logsWarnBytes); got.State != app.HealthOK {
		t.Fatalf("不存在的目录应当正常：%+v", got)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.log"), []byte("0123456789"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := checkDir("logs", dir, logsWarnBytes); got.State != app.HealthOK || !strings.Contains(got.Detail, "1") {
		t.Fatalf("小额占用应当正常：%+v", got)
	}
	got := checkDir("logs", dir, 4) // 阈值压到 4 字节，同一个目录立刻越线
	if got.State != app.HealthWarn || got.Fix == "" {
		t.Fatalf("越线应当注意并给修法：%+v", got)
	}
}

// TestCheckDisk 卷余量：拿得到就给两个数（不断言具体大小，只断言不是异常态）。
func TestCheckDisk(t *testing.T) {
	got := checkDisk(t.TempDir())
	if got.State == app.HealthFail {
		t.Skipf("这台机器磁盘真的告急：%+v", got)
	}
	if !strings.Contains(got.Detail, "B") {
		t.Fatalf("余量没印出字节数：%q", got.Detail)
	}
}

// TestHumanSize 三档写法：整数、KB/MB、TB。
func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{3 << 30, "3.0 GB"},
		{2 << 40, "2.0 TB"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Fatalf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestProbeOrder 七项的顺序固定：从「坏了一点都动不了」到「只影响体验」。
func TestProbeOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	checks := healthProbe{w: &wiring{profileDir: dir, cfgPath: filepath.Join(dir, "config.json")}}.Probe(context.Background())
	want := []string{"config", "secrets", "sessions", "mcp", "logs", "exports", "disk"}
	if len(checks) != len(want) {
		t.Fatalf("项数 = %d, want %d", len(checks), len(want))
	}
	for i, id := range want {
		if checks[i].ID != id {
			t.Fatalf("第 %d 项 = %q, want %q", i, checks[i].ID, id)
		}
	}
}
