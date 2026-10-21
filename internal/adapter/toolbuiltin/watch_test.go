package toolbuiltin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeWatches 脚本化观察器端口：记录声明、返回自增 ID。
type fakeWatches struct {
	started  []port.WatchSpec
	starts   int
	list     []port.WatchState
	stopped  []port.WatchID
	startErr error
}

func (f *fakeWatches) Start(_ context.Context, spec port.WatchSpec) (port.WatchState, error) {
	if f.startErr != nil {
		return port.WatchState{}, f.startErr
	}
	f.started = append(f.started, spec)
	f.starts++
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return port.WatchState{
		ID:        port.WatchID("w" + string(rune('0'+f.starts))),
		Spec:      spec,
		StartedAt: now,
		EndsAt:    now.Add(spec.Deadline),
	}, nil
}

func (f *fakeWatches) List(context.Context) ([]port.WatchState, error) { return f.list, nil }

func (f *fakeWatches) Stop(_ context.Context, id port.WatchID) error {
	f.stopped = append(f.stopped, id)
	return nil
}

// TestWatchStartDefaults 缺省值：30s 间隔、120 次、1h、match、match 判据为变化。
func TestWatchStartDefaults(t *testing.T) {
	w := &fakeWatches{}
	ws := &watchStart{watches: w}
	res, err := ws.Execute(context.Background(), call(`{"kind":"file","target":"/tmp/state"}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	spec := w.started[0]
	if spec.Kind != port.WatchFile || spec.Interval != 30*time.Second ||
		spec.MaxChecks != 120 || spec.Deadline != time.Hour || spec.Notify != port.WatchNotifyOnce {
		t.Fatalf("spec = %+v", spec)
	}
	for _, want := range []string{"w1 watching file", "output changes", "report once and stop"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output = %q, want 含 %q", res.Output, want)
		}
	}
}

// TestWatchStartExplicitArgs 显式参数透传，workdir 只对 command 有效。
func TestWatchStartExplicitArgs(t *testing.T) {
	w := &fakeWatches{}
	ws := &watchStart{watches: w}
	args := `{"kind":"command","target":"gh run view","match":"success","notify":"change",
		"interval_sec":15,"max_checks":20,"deadline_sec":600,"workdir":"/repo"}`
	res, err := ws.Execute(context.Background(), call(args))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	spec := w.started[0]
	if spec.Match != "success" || spec.Notify != port.WatchNotifyChange ||
		spec.Interval != 15*time.Second || spec.MaxChecks != 20 || spec.Deadline != 10*time.Minute ||
		spec.WorkDir != "/repo" {
		t.Fatalf("spec = %+v", spec)
	}
	// 非命令类型带 workdir：拒收（避免「设了却没生效」）。
	if _, err := ws.Execute(context.Background(), call(`{"kind":"file","target":"x","workdir":"/repo"}`)); err == nil ||
		!strings.Contains(err.Error(), "only applies to kind=command") {
		t.Fatalf("err = %v", err)
	}
}

// TestWatchStartValidation 参数校验：各字段越界与缺参都明确报错。
func TestWatchStartValidation(t *testing.T) {
	ws := &watchStart{watches: &fakeWatches{}}
	cases := []struct{ name, args, want string }{
		{"kind", `{"kind":"db","target":"x"}`, "kind must be"},
		{"target", `{"kind":"file"}`, "missing target"},
		{"notify", `{"kind":"file","target":"x","notify":"always"}`, "notify must be"},
		{"interval-low", `{"kind":"file","target":"x","interval_sec":1}`, "interval_sec must be"},
		{"interval-high", `{"kind":"file","target":"x","interval_sec":99999}`, "interval_sec must be"},
		{"checks", `{"kind":"file","target":"x","max_checks":-1}`, "max_checks must be"},
		{"checks-high", `{"kind":"file","target":"x","max_checks":999999}`, "max_checks must be"},
		{"deadline", `{"kind":"file","target":"x","deadline_sec":-3}`, "deadline_sec must be"},
	}
	for _, c := range cases {
		if _, err := ws.Execute(context.Background(), call(c.args)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want 含 %q", c.name, err, c.want)
		}
	}
	// 端口未装配 / 端口报错都要明确。
	if _, err := (&watchStart{}).Execute(context.Background(), call(`{"kind":"file","target":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "not available in this build") {
		t.Fatalf("err = %v", err)
	}
	bad := &fakeWatches{startErr: errors.New("watcher: shutting down")}
	if _, err := (&watchStart{watches: bad}).Execute(context.Background(), call(`{"kind":"file","target":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "watch_start") {
		t.Fatalf("err = %v", err)
	}
}

// TestWatchListAndStop 列表格式、空表提示、ID 前缀解析与缺参。
func TestWatchListAndStop(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w := &fakeWatches{list: []port.WatchState{{
		ID: "w7", Checks: 3, StartedAt: now, EndsAt: now.Add(time.Hour),
		Spec: port.WatchSpec{Kind: port.WatchHTTP, Target: "https://ci.example/x", Match: "green", Interval: 30 * time.Second},
	}}}
	res, err := (&watchList{watches: w}).Execute(context.Background(), call(`{}`))
	if err != nil || !res.OK {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	for _, want := range []string{"w7", "http", "https://ci.example/x", "matches /green/", "checks=3"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output = %q, want 含 %q", res.Output, want)
		}
	}
	empty, err := (&watchList{watches: &fakeWatches{}}).Execute(context.Background(), call(`{}`))
	if err != nil || empty.Output != "no observations running" {
		t.Fatalf("空表 = %q, err = %v", empty.Output, err)
	}

	stop := &watchStop{watches: w}
	res, err = stop.Execute(context.Background(), call(`{"id":"w7"}`))
	if err != nil || !res.OK || len(w.stopped) != 1 || w.stopped[0] != "w7" {
		t.Fatalf("stop res = %+v, stopped = %v, err = %v", res, w.stopped, err)
	}
	if _, err := stop.Execute(context.Background(), call(`{"id":"w99"}`)); err == nil ||
		!strings.Contains(err.Error(), "no watch") {
		t.Fatalf("err = %v", err)
	}
	if _, err := stop.Execute(context.Background(), call(`{}`)); err == nil {
		t.Fatal("缺 id 应报错")
	}
}

// TestWatchToolsNotAvailable 端口未装配时三个工具都明确报因（不 panic）。
func TestWatchToolsNotAvailable(t *testing.T) {
	ctx := context.Background()
	if _, err := (&watchList{}).Execute(ctx, call(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "not available in this build") {
		t.Fatalf("list err = %v", err)
	}
	if _, err := (&watchStop{}).Execute(ctx, call(`{"id":"w1"}`)); err == nil ||
		!strings.Contains(err.Error(), "not available in this build") {
		t.Fatalf("stop err = %v", err)
	}
}
