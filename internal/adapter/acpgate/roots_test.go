package acpgate

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestAdditionalDirectories 额外根的两种情形：对方申报了支持就发出去；没申报就不发，
// 但本地照旧认这几个根，并在报告里说清「只在我们这边生效」。
//
// 「本地照拦照放」这一半由 fs_test 的根集用例覆盖（根集与发不发无关）。
func TestAdditionalDirectories(t *testing.T) {
	extra := t.TempDir()

	m := newManager(t, "additional", func(o *Options) { o.Home = t.TempDir() })
	task, err := m.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x", Roots: []string{extra}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run, err := m.Wait(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("wait: %v（这一模式要求 session/new 带上 additionalDirectories）", err)
	}
	if strings.Contains(strings.Join(run.Notes, "\n"), "does not accept additional directories") {
		t.Fatalf("对方已申报支持，不该记「不接受」：%v", run.Notes)
	}

	m2 := newManager(t, "ok", func(o *Options) { o.Home = t.TempDir() })
	task2, err := m2.Start(ctxWithSession("c1"), port.DelegateSpec{Task: "x", Roots: []string{extra}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run2, err := m2.Wait(context.Background(), task2.ID)
	if err != nil {
		t.Fatalf("wait: %v（对方不申报支持也不该失败）", err)
	}
	if !strings.Contains(strings.Join(run2.Notes, "\n"), "does not accept additional directories") {
		t.Fatalf("对方没申报时该记一条 note：%v", run2.Notes)
	}
}
