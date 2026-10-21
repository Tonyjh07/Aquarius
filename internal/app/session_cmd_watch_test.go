package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeWatchManager 脚本化观察器端口（/watch 命令测试）。
type fakeWatchManager struct {
	list    []port.WatchState
	stopped []port.WatchID
	listErr error
	stopErr error
}

func (f *fakeWatchManager) Start(context.Context, port.WatchSpec) (port.WatchState, error) {
	return port.WatchState{}, errors.New("not scripted")
}

func (f *fakeWatchManager) List(context.Context) ([]port.WatchState, error) {
	return f.list, f.listErr
}

func (f *fakeWatchManager) Stop(_ context.Context, id port.WatchID) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, id)
	return nil
}

// TestSessionWatchCommand /watch list|stop：列表带判据与节奏，stop 走唯一前缀。
func TestSessionWatchCommand(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fw := &fakeWatchManager{list: []port.WatchState{{
		ID: "w3", Checks: 4, StartedAt: now, EndsAt: now.Add(time.Hour),
		Spec: port.WatchSpec{Kind: port.WatchCommand, Target: "gh run view", Interval: 30 * time.Second},
	}, {
		ID: "w4", Checks: 1, StartedAt: now, EndsAt: now.Add(time.Hour),
		Spec: port.WatchSpec{Kind: port.WatchHTTP, Target: "https://ci.example", Match: "green", Interval: 30 * time.Second},
	}}}
	s := &Session{watches: fw}
	out, err := s.watchReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"w3", "gh run view", "判据：输出有变化", "已查 4 次", "w4", "判据：输出匹配 /green/"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want 含 %q", out, want)
		}
	}
	if msg, err := s.watchReport(context.Background(), []string{"stop", "w3"}); err != nil || !strings.Contains(msg, "已停止观察 w3") {
		t.Fatalf("stop = %q, err = %v", msg, err)
	}
	if len(fw.stopped) != 1 || fw.stopped[0] != "w3" {
		t.Fatalf("stopped = %v", fw.stopped)
	}
	// 空表、未知 ID、用法错误。
	if msg, err := (&Session{watches: &fakeWatchManager{}}).watchReport(context.Background(), nil); err != nil ||
		!strings.Contains(msg, "暂无观察器") {
		t.Fatalf("空表 = %q, err = %v", msg, err)
	}
	if _, err := s.watchReport(context.Background(), []string{"stop", "w9"}); err == nil ||
		!strings.Contains(err.Error(), "no watch") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.watchReport(context.Background(), []string{"stop"}); err == nil {
		t.Fatal("缺 id 应报错")
	}
	if _, err := s.watchReport(context.Background(), []string{"bogus"}); err == nil {
		t.Fatal("未知子命令应报错")
	}
	if _, err := (&Session{}).watchReport(context.Background(), nil); err == nil ||
		!strings.Contains(err.Error(), "未配置观察器") {
		t.Fatalf("err = %v", err)
	}
}
