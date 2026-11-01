package app

// session_reconcile_test.go 启动对账的落地口径（D262）：事实注入落到发起会话，
// 目标不是当前会话就异地落地，目标已删除就丢弃。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// unfinished 造一条对账输入。
func unfinished(id string, session conversation.ID, alive bool) port.UnfinishedJob {
	return port.UnfinishedJob{
		Job: port.Job{
			ID:        port.JobID(id),
			Spec:      port.JobSpec{Command: "go", Args: []string{"test", "./..."}},
			Status:    port.JobRunning,
			PID:       4321,
			StartedAt: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC),
		},
		Session:  session,
		LogPath:  "/tmp/jobs/j003.log",
		LogTail:  "ok  example/pkg  0.2s",
		Alive:    alive,
		Deadline: time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC),
	}
}

// TestReportUnfinishedLandsInCurrentSession 目标是当前会话：挂当前 Head，正文说清事实，
// Trigger 记 false（没有起过一轮），日志段单独围栏。
func TestReportUnfinishedLandsInCurrentSession(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	ctx := context.Background()
	head := s.Current().Head

	if err := s.ReportUnfinished(ctx, []port.UnfinishedJob{unfinished("j003", s.Current().ID, true)}); err != nil {
		t.Fatalf("report: %v", err)
	}
	path := s.Current().Path()
	last := path[len(path)-1]
	if last.Role != conversation.RoleInject || last.Source != "job:j003" || last.Trigger {
		t.Fatalf("末节点 = %+v, want inject/job:j003/未触发", last)
	}
	if last.Parent != head {
		t.Fatalf("父 = %s, want %s", last.Parent, head)
	}
	text := last.Content[0].Text
	for _, want := range []string{"had not finished", "go test ./...", "PID 4321", "/tmp/jobs/j003.log", "ok  example/pkg"} {
		if !strings.Contains(text, want) {
			t.Fatalf("正文缺 %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "<injected-context") {
		t.Fatalf("日志段未围栏:\n%s", text)
	}
}

// TestReportUnfinishedLandsElsewhere 目标不是当前会话：落到它自己的树上，当前会话不动。
func TestReportUnfinishedLandsElsewhere(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	ctx := context.Background()

	other := conversation.New("other", "另一个会话")
	if err := store.Save(ctx, other); err != nil {
		t.Fatalf("save other: %v", err)
	}
	before := s.Current().NodeCount()

	if err := s.ReportUnfinished(ctx, []port.UnfinishedJob{unfinished("j004", "other", false)}); err != nil {
		t.Fatalf("report: %v", err)
	}
	if got := s.Current().NodeCount(); got != before {
		t.Fatalf("当前会话节点数 = %d, want %d", got, before)
	}
	got, err := store.Load(ctx, "other")
	if err != nil {
		t.Fatalf("load other: %v", err)
	}
	p := got.Path()
	if p[len(p)-1].Source != "job:j004" {
		t.Fatalf("目标会话末节点 = %+v, want job:j004", p[len(p)-1])
	}
	if !strings.Contains(p[len(p)-1].Content[0].Text, "already exited") {
		t.Fatalf("已结束的对账应说明退出码未知:\n%s", p[len(p)-1].Content[0].Text)
	}
}

// TestReportUnfinishedDropsDeletedSession 目标会话已删除：丢弃、不报错、不重建。
func TestReportUnfinishedDropsDeletedSession(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)
	ctx := context.Background()
	before := s.Current().NodeCount()

	if err := s.ReportUnfinished(ctx, []port.UnfinishedJob{unfinished("j005", "gone", true)}); err != nil {
		t.Fatalf("report: %v（已删除的目标只该丢弃）", err)
	}
	if got := s.Current().NodeCount(); got != before {
		t.Fatalf("当前会话节点数 = %d, want %d", got, before)
	}
	if _, err := store.Load(ctx, "gone"); err == nil {
		t.Fatal("不得重建已删除的会话")
	}
}

// TestReportUnfinishedFinishedJob 任务跑完而报告没送到（D264）：对账说的是确定的事实
// ——状态与退出码都在，只是没进过上下文——而不是「可能还在跑」。
func TestReportUnfinishedFinishedJob(t *testing.T) {
	s, _, _ := newTestSession(t, newMemStore())
	it := unfinished("j004", s.Current().ID, false)
	it.Job.Status = port.JobFailed
	it.Job.ExitCode = 1
	it.Job.EndedAt = time.Date(2026, 10, 31, 9, 5, 0, 0, time.UTC)

	if err := s.ReportUnfinished(context.Background(), []port.UnfinishedJob{it}); err != nil {
		t.Fatalf("report: %v", err)
	}
	path := s.Current().Path()
	text := path[len(path)-1].Content[0].Text
	if !strings.Contains(text, "finished (failed, exit code 1)") {
		t.Fatalf("正文没说明它已结束:\n%s", text)
	}
	if !strings.Contains(text, "never reached this session") {
		t.Fatalf("正文没说明报告未送达:\n%s", text)
	}
	if strings.Contains(text, "may still be running") {
		t.Fatalf("已结束的任务不该说可能还在跑:\n%s", text)
	}
}
