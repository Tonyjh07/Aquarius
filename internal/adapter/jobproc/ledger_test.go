package jobproc

// ledger_test.go 未收尾台账（D262）：在跑的任务留痕、终态抹掉、启动对账读即清。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestLedgerTracksRunningJob 起任务即留痕（含发起会话）；终态只标状态，报告确认送达才销账。
func TestLedgerTracksRunningJob(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := port.WithSessionID(context.Background(), conversation.ID("convA"))
	job, err := m.Start(ctx, helperSpec(t, dir, "sleep"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	d := readLedgerFile(t, dir)
	if len(d.Entries) != 1 {
		t.Fatalf("台账条目 = %d, want 1", len(d.Entries))
	}
	e := d.Entries[0]
	if e.ID != job.ID || e.Session != "convA" || e.PID != job.PID || e.StartedAt.IsZero() {
		t.Fatalf("条目 = %+v, want id/session/pid/start 对齐 %+v", e, job)
	}
	if e.LogPath == "" || !strings.HasSuffix(e.LogPath, string(job.ID)+".log") {
		t.Fatalf("log path = %q", e.LogPath)
	}

	if err := m.Kill(context.Background(), job.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := m.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// 终态不删（D264）：这段窗口里如果退出程序，下次启动还得靠这条说清「跑完了、报告没送到」。
	d = readLedgerFile(t, dir)
	if len(d.Entries) != 1 {
		t.Fatalf("终态后台账条目 = %d, want 1", len(d.Entries))
	}
	if got := d.Entries[0]; got.Status != port.JobKilled || got.EndedAt.IsZero() {
		t.Fatalf("终态条目 = %+v, want status=killed 且带结束时刻", got)
	}
	// 报告确认送达后才销账。
	if err := m.Settle(context.Background(), job.ID); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if d := readLedgerFile(t, dir); len(d.Entries) != 0 {
		t.Fatalf("销账后条目 = %d, want 0", len(d.Entries))
	}
}

// TestUnfinishedReportsFinishedJob 跑完而报告没送到的条目（D264）：对账拿到的状态是终态、
// 退出码是确定的，且不再按 pid 探活（进程早没了，问了只会撞上复用）。
func TestUnfinishedReportsFinishedJob(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, dir, "fail"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
	items, err := m.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("unfinished: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("条目 = %d, want 1（未销账的终态条目）", len(items))
	}
	it := items[0]
	if it.Job.Status != port.JobFailed || it.Job.ExitCode == 0 {
		t.Fatalf("item = %+v, want failed 且退出码非 0", it.Job)
	}
	if it.Alive {
		t.Fatal("已结束的条目不该再探活")
	}
}

// TestUnfinishedTakesAndClears 对账读即清：一次取走，第二次为空（否则每次启动都重报）。
func TestUnfinishedTakesAndClears(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := port.WithSessionID(context.Background(), conversation.ID("convA"))
	job, err := m.Start(ctx, helperSpec(t, dir, "sleep"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = m.Kill(context.Background(), job.ID) }()

	items, err := m.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("unfinished: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("条目 = %d, want 1", len(items))
	}
	it := items[0]
	if it.Job.ID != job.ID || it.Session != "convA" || !it.Alive {
		t.Fatalf("item = %+v, want 在跑的 %s（会话 convA）", it, job.ID)
	}
	if it.LogPath == "" {
		t.Fatal("台账条目应带日志路径")
	}

	again, err := m.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("unfinished again: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("第二次取到 %d 条, want 0（读即清）", len(again))
	}
}

// TestUnfinishedCarriesLogTail 对账带上日志末几行（重启后进程不在了，只能从盘上读）。
func TestUnfinishedCarriesLogTail(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, dir, "lines", "40"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// 模拟「上次退出时未收尾」：换成一条只剩在跑信息的残留（终态字段缺省）。
	writeLedgerFile(t, dir, ledgerDoc{Version: ledgerVersion, Entries: []ledgerEntry{{
		ID:        job.ID,
		Session:   "convA",
		Command:   "lines",
		StartedAt: time.Now().Add(-time.Minute),
		PID:       job.PID,
		LogPath:   filepath.Join(dir, string(job.ID)+".log"),
	}}})

	items, err := m.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("unfinished: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("条目 = %d, want 1", len(items))
	}
	tail := items[0].LogTail
	if !strings.Contains(tail, "line-40") {
		t.Fatalf("日志尾 = %q, want 含 line-40", tail)
	}
	if strings.Contains(tail, "line-1\n") {
		t.Fatalf("日志尾含首行，取多了: %q", tail)
	}
	if n := len(strings.Split(strings.TrimSpace(tail), "\n")); n > ledgerTailLines {
		t.Fatalf("日志尾 %d 行, want ≤ %d", n, ledgerTailLines)
	}
}

// TestUnfinishedIgnoresBrokenLedger 坏台账当作没有：对账不该被一份读不动的文件挡住启动。
func TestUnfinishedIgnoresBrokenLedger(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ledgerName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	items, err := m.Unfinished(context.Background())
	if err != nil {
		t.Fatalf("unfinished: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("条目 = %d, want 0", len(items))
	}
}

// readLedgerFile 读磁盘上的台账（断言用）。
func readLedgerFile(t *testing.T, dir string) ledgerDoc {
	t.Helper()
	m := &Manager{ledgerPath: filepath.Join(dir, ledgerName)}
	d, err := m.readLedger()
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return d
}

// writeLedgerFile 直接写台账（模拟上一进程留下的残留）。
func writeLedgerFile(t *testing.T, dir string, d ledgerDoc) {
	t.Helper()
	m := &Manager{ledgerPath: filepath.Join(dir, ledgerName)}
	if err := m.writeLedger(d); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
}
