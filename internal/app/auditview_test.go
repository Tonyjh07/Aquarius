package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// writeAuditLog 造一份审计文件（测试数据全 ASCII，避开 CJK 锁）。
func writeAuditLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write audit log: %v", err)
	}
	return path
}

// TestReadAuditSkipsGarbage 坏行与空行跳过，总行数只数非空行。
func TestReadAuditSkipsGarbage(t *testing.T) {
	path := writeAuditLog(t,
		`{"ts":"2026-11-16T10:00:00Z","kind":"llm","model":"m1","dur_ms":1200,"ok":true,"in":10,"out":5}`,
		`not json at all`,
		``,
		`{"ts":"2026-11-16T10:00:01Z","kind":"tool","name":"file_read","dur_ms":24,"ok":true}`,
	)
	rows, total, err := readAudit(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 (non-empty lines)", total)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (garbage skipped)", len(rows))
	}
}

// TestReadAuditMissingFile 没有文件不是错误：首次启动时本来就是空。
func TestReadAuditMissingFile(t *testing.T) {
	rows, total, err := readAudit(filepath.Join(t.TempDir(), "nope.log"))
	if err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("rows=%d total=%d err=%v, want empty and nil", len(rows), total, err)
	}
}

// TestPickAuditRowsTail 取尾部且保持文件顺序；过滤只按名字。
func TestPickAuditRowsTail(t *testing.T) {
	var rows []auditRow
	for _, n := range []string{"a", "b", "c", "d"} {
		rows = append(rows, auditRow{Kind: "tool", Name: n, OK: true})
	}
	got := pickAuditRows(rows, 2, "")
	if len(got) != 2 || got[0].Name != "c" || got[1].Name != "d" {
		t.Fatalf("tail = %v, want c,d in order", got)
	}
	got = pickAuditRows(rows, 10, "b")
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("filtered = %v, want just b", got)
	}
}

// TestRenderAuditShowsDetail 失败行带原因、llm 行带 usage、decide 行带结论。
func TestRenderAuditShowsDetail(t *testing.T) {
	rows := []auditRow{
		{TS: "2026-11-16T10:00:00Z", Kind: "llm", Model: "m1", DurMS: 1200, OK: true, In: 10, Out: 5},
		{TS: "2026-11-16T10:00:01Z", Kind: "tool", Name: "file_write", DurMS: 8, OK: false, Err: "denied by policy"},
		{TS: "2026-11-16T10:00:02Z", Kind: "decide", Name: "term_exec", OK: true,
			Decide: &auditDecide{Verdict: "ask", Scored: true}},
	}
	out := renderAudit(rows, len(rows), "")
	for _, want := range []string{"m1", "in 10", "out 5", "denied by policy", "ask"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q in:\n%s", want, out)
		}
	}
	// 参数内容不得出现在视图里：审计行本来就不记，视图也不该凭空长出来。
	if strings.Contains(out, "{\"") {
		t.Fatalf("render leaked raw JSON:\n%s", out)
	}
}

// TestExecAuditNoPath 未接入审计时明确报错，不返回空表。
func TestExecAuditNoPath(t *testing.T) {
	s := &Session{}
	if _, err := s.execAudit(context.Background(), port.Command{}); err == nil {
		t.Fatal("want an error when no audit path is configured")
	}
}

// TestExecAuditEmptyAndFilter 没记录与过滤空结果给出不同的话。
func TestExecAuditEmptyAndFilter(t *testing.T) {
	s := &Session{auditPath: filepath.Join(t.TempDir(), "none.log")}
	out, err := s.execAudit(context.Background(), port.Command{})
	if err != nil {
		t.Fatalf("exec audit: %v", err)
	}
	if !strings.Contains(out, i18n.T("session.audit.empty")) {
		t.Fatalf("empty output = %q", out)
	}

	s.auditPath = writeAuditLog(t, `{"ts":"2026-11-16T10:00:00Z","kind":"tool","name":"file_read","ok":true}`)
	out, err = s.execAudit(context.Background(), port.Command{Args: []string{"--tool", "file_write"}})
	if err != nil {
		t.Fatalf("exec audit: %v", err)
	}
	if !strings.Contains(out, "file_write") {
		t.Fatalf("no-match output = %q", out)
	}
}

// TestExecAuditArgs 行数与工具两个开关都能用，非法写法报用法。
func TestExecAuditArgs(t *testing.T) {
	path := writeAuditLog(t,
		`{"ts":"2026-11-16T10:00:00Z","kind":"tool","name":"a","ok":true}`,
		`{"ts":"2026-11-16T10:00:01Z","kind":"tool","name":"b","ok":true}`,
		`{"ts":"2026-11-16T10:00:02Z","kind":"tool","name":"c","ok":true}`,
	)
	s := &Session{auditPath: path}
	out, err := s.execAudit(context.Background(), port.Command{Args: []string{"-n", "1"}})
	if err != nil {
		t.Fatalf("exec audit: %v", err)
	}
	if !strings.Contains(out, "  c  ") || strings.Contains(out, "  a  ") {
		t.Fatalf("-n 1 output = %q", out)
	}
	if _, err := s.execAudit(context.Background(), port.Command{Args: []string{"-n", "0"}}); err == nil {
		t.Fatal("want usage error for -n 0")
	}
	if _, err := s.execAudit(context.Background(), port.Command{Args: []string{"--tool"}}); err == nil {
		t.Fatal("want usage error for a dangling --tool")
	}
}
