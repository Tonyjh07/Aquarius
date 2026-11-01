package jobproc

// close_test.go 关停语义（§5.8）：Close 返回之后不再往 jobs 目录写东西。

import (
	"context"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestCloseWaitsForFinishedReaper Close 等已结束任务的收割者收尾：返回时台账终态已经落盘。
//
// 不等就会出现「关停返回之后还有一次写盘」——测试里是 TempDir 清理撞上「目录非空」，
// 真机上则是把台账写进一个已经在收尾的目录。
func TestCloseWaitsForFinishedReaper(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, dir, "echo", "done"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// 等到终态已经置上、但收割者不一定写完了台账（Close 要等的正是这一段）。
	waitFor(t, "任务进入终态", func() bool {
		j, err := m.Status(context.Background(), job.ID)
		return err == nil && j.Status != port.JobRunning
	})
	m.Close()
	// Close 返回即可断言：终态条目已落盘（markFinished 在 done 关闭之前）。
	d := readLedgerFile(t, dir)
	if len(d.Entries) != 1 {
		t.Fatalf("台账条目 = %d, want 1", len(d.Entries))
	}
	if e := d.Entries[0]; e.Status == "" || e.EndedAt.IsZero() {
		t.Fatalf("关闭后台账仍是未标终态: %+v", e)
	}
}

// TestCloseDoesNotWaitForRunningJob 仍在跑的任务不等（§10：退出不杀任务）：
// 等它等于把退出拖成任务时长；台账里那条 running 就是下次启动要说的事实。
func TestCloseDoesNotWaitForRunningJob(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	job, err := m.Start(context.Background(), helperSpec(t, dir, "sleep"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	start := time.Now()
	m.Close()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("Close 等了 %v，不该等仍在跑的任务", took)
	}
	if d := readLedgerFile(t, dir); len(d.Entries) != 1 || d.Entries[0].Status != "" {
		t.Fatalf("在跑任务应留一条未标终态的台账: %+v", d.Entries)
	}
	// 收尾：杀掉再等，避免这条任务在测试退出后还挂着。
	if err := m.Kill(context.Background(), job.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := m.Wait(context.Background(), job.ID); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// TestCloseIsIdempotentAndStopsNewJobs Close 幂等；关停之后不再接新任务。
func TestCloseIsIdempotentAndStopsNewJobs(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	m.Close()
	m.Close()
	if _, err := m.Start(context.Background(), helperSpec(t, dir, "echo")); err == nil {
		t.Fatal("关闭后 Start 应该报错")
	}
}
