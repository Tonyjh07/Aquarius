package app

// diagview_test.go `/diag`（§7.1）：三部分必须落在同一次请求上。
//
// 这一组测试守两件事：窗口只捞那一次请求的审计行（多一行少一行都会让「同一次请求」
// 变成一句空话）；包里的三节与回执、次窗内容逐字同源。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestPickDiagAudit 只取窗口内的行、保持文件顺序、时间解析不出的行丢掉。
func TestPickDiagAudit(t *testing.T) {
	anchor := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	from, to := diagWindow(anchor, 3*time.Second)
	rows := []auditRow{
		{TS: anchor.Add(-time.Hour).Format(time.RFC3339Nano), Name: "too-old"},
		{TS: anchor.Add(-2 * time.Second).Format(time.RFC3339Nano), Name: "early"},
		{TS: "not-a-time", Name: "broken"},
		{TS: anchor.Add(3 * time.Second).Format(time.RFC3339Nano), Name: "inside"},
		{TS: anchor.Add(time.Minute).Format(time.RFC3339Nano), Name: "too-late"},
	}
	got := pickDiagAudit(rows, from, to)
	if len(got) != 2 || got[0].Name != "early" || got[1].Name != "inside" {
		t.Fatalf("pick = %+v, want [early inside]", got)
	}
}

// TestExecDiag 命令面：写入磁盘一份、回执带路径与大小、三节齐全、时间窗只含那一行，
// 次窗与文件逐字节相同。
func TestExecDiag(t *testing.T) {
	s, _, rec := newTestSession(t, newMemStore())
	s.exportsDir = t.TempDir()
	s.version = "v-test"
	s.auditPath = filepath.Join(t.TempDir(), "audit.log")
	ctx := context.Background()

	if _, err := s.execDiag(ctx, port.Command{Name: "diag"}); err == nil {
		t.Fatal("没有请求时应当报错")
	}

	appendUserText(t, s, "hi")
	anchor := time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local)
	req, marks, err := s.agent.buildRequestTracked(ctx, s.cur, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s.agent.saveLast(anchor, "conv-1", req, marks)
	s.agent.RecordWire(port.WireTrace{
		Meta: port.WireMeta{
			Proto: "openai-chat", Model: "m", Status: 200, End: port.WireEndDone,
			Started: anchor, Elapsed: 2 * time.Second, Bytes: len(wireScript),
		},
		Raw: []byte(wireScript),
	})
	writeAuditLines(t, s.auditPath, []string{
		`{"ts":"` + anchor.Add(-time.Hour).Format(time.RFC3339Nano) + `","kind":"tool","name":"old_tool","ok":true}`,
		`{"ts":"` + anchor.Add(time.Second).Format(time.RFC3339Nano) + `","kind":"llm","name":"m","ok":true,"in":11,"out":22}`,
		// 收流 2s + 尾部 2s：这一行在尾巴里、但在收流区间之外，用来看 Elapsed 是否真的进了窗口。
		`{"ts":"` + anchor.Add(3*time.Second).Format(time.RFC3339Nano) + `","kind":"tool","name":"mid_tool","ok":true}`,
		`{"ts":"` + anchor.Add(time.Minute).Format(time.RFC3339Nano) + `","kind":"tool","name":"late_tool","ok":false}`,
	})

	out, err := s.execDiag(ctx, port.Command{Name: "diag"})
	if err != nil {
		t.Fatalf("execDiag: %v", err)
	}
	entries, err := os.ReadDir(s.exportsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("导出目录应有 1 个文件，实际 %v（err=%v）", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(s.exportsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	body := string(data)

	if !strings.Contains(body, "v-test") {
		t.Fatalf("包头没有版本：\n%s", body)
	}
	if !strings.Contains(body, i18n.T("session.diag.warn")) {
		t.Fatal("包头缺少外发提醒")
	}
	// 三节标题都在。
	for _, want := range []string{"一、", "二、", "三、"} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少第 %s 节：\n%s", want, body)
		}
	}
	// 上下文那一节确实取的是这一次实发的账。
	if !strings.Contains(body, "hi") {
		t.Fatalf("上下文分块账里没有本轮内容：\n%s", body)
	}
	// 原始返回逐字在里面。
	if !strings.Contains(body, "Hello") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("原始返回不完整：\n%s", body)
	}
	// 审计行只含窗口内的那些：锚点那一行、收流区间内的，以及尾部宽限内的。
	if !strings.Contains(body, "llm") || !strings.Contains(body, "mid_tool") ||
		strings.Contains(body, "old_tool") || strings.Contains(body, "late_tool") {
		t.Fatalf("审计窗口不对：\n%s", body)
	}
	if strings.Contains(body, "Authorization") || strings.Contains(body, "api_key") {
		t.Fatalf("包里出现了不该有的东西：\n%s", body)
	}
	if !strings.Contains(out, entries[0].Name()) || !strings.Contains(out, humanBytes(int64(len(body)))) {
		t.Fatalf("回执缺路径或大小：%q", out)
	}
	// 次窗与文件同源（逐字节相同）。
	var view *port.TextViewEvent
	for _, ev := range rec.events {
		if tv, ok := ev.(port.TextViewEvent); ok {
			v := tv
			view = &v
		}
	}
	if view == nil || view.Text != body {
		t.Fatal("次窗内容与写入磁盘文件不一致")
	}
}

// TestExecDiagWithoutAudit 没有审计日志时照发：那一节写「窗口内没有」，其余两节不缺。
func TestExecDiagWithoutAudit(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	s.exportsDir = t.TempDir()
	ctx := context.Background()
	appendUserText(t, s, "hi")
	anchor := time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local)
	req, marks, err := s.agent.buildRequestTracked(ctx, s.cur, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s.agent.saveLast(anchor, "conv-1", req, marks)
	s.agent.RecordWire(port.WireTrace{Meta: port.WireMeta{Proto: "openai-chat", Status: 200}, Raw: []byte("data: x\n\n")})
	if _, err := s.execDiag(ctx, port.Command{Name: "diag"}); err != nil {
		t.Fatalf("execDiag: %v", err)
	}
	entries, err := os.ReadDir(s.exportsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("导出目录应有 1 个文件，实际 %v（err=%v）", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(s.exportsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	body := string(data)
	// 缺审计一节不算失败：那一节写明「窗口内没有」，另外两节照发。
	if !strings.Contains(body, i18n.T("session.diag.audit_none")) {
		t.Fatalf("缺「窗口内没有」：\n%s", body)
	}
	for _, want := range []string{"一、", "三、"} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少第 %s 节：\n%s", want, body)
		}
	}
}

// TestExecDiagRejectsArgs 无参数是刻意的：多一个参数就报用法，不猜。
func TestExecDiagRejectsArgs(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	if _, err := s.execDiag(context.Background(), port.Command{Args: []string{"last"}}); err == nil {
		t.Fatal("带参数应当报用法")
	}
}

// writeAuditLines 铺一份审计日志（每行一条 JSON）。
func writeAuditLines(t *testing.T, path string, lines []string) {
	t.Helper()
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("写审计日志: %v", err)
	}
}
