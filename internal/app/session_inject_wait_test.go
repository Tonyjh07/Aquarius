package app

// session_inject_wait_test.go 注入落地回执（D264）：报「已落地并落盘」才回话，
// 任务报告的台账销账靠它判，不再靠「入队成功」。

import (
	"context"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestInjectAndWaitLandsBeforeReturning 返回 nil 时节点已经在盘上：
// 进程下一秒退出，切回来仍看得见（这正是台账能销账的依据）。
func TestInjectAndWaitLandsBeforeReturning(t *testing.T) {
	store := newMemStore()
	s, _, _ := injectableSession(t, store)
	s.wake = func() { _, _, _ = s.drainInjections(context.Background(), drainOnWake) }
	id := s.Current().ID

	if err := s.InjectAndWait(context.Background(), port.Injection{
		Source: "job:j1", Session: id, Parts: injectPart("构建完成"),
	}); err != nil {
		t.Fatalf("InjectAndWait: %v", err)
	}
	stored, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	path := stored.Path()
	if last := path[len(path)-1]; last.Source != "job:j1" {
		t.Fatalf("末节点 = %+v, want job:j1（返回前应已落盘）", last)
	}
}

// TestInjectAndWaitElsewherePersisted 目标不是当前会话：返回时它的树上已经有这条。
func TestInjectAndWaitElsewherePersisted(t *testing.T) {
	store := newMemStore()
	s, _, _ := injectableSession(t, store)
	s.wake = func() { _, _, _ = s.drainInjections(context.Background(), drainOnWake) }
	idA := s.Current().ID

	if _, err := handleCmd(s, "new", "乙"); err != nil {
		t.Fatalf("/new: %v", err)
	}
	if err := s.InjectAndWait(context.Background(), port.Injection{
		Source: "job:j2", Session: idA, Parts: injectPart("任务完成：测试通过"),
	}); err != nil {
		t.Fatalf("InjectAndWait: %v", err)
	}
	stored, err := store.Load(context.Background(), idA)
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	path := stored.Path()
	if last := path[len(path)-1]; last.Source != "job:j2" {
		t.Fatalf("A 的末节点 = %+v, want job:j2", last)
	}
	// 当前会话（乙）不该多出这条：异地落地只碰目标会话的盘。
	for _, n := range s.Current().Path() {
		if n.Source == "job:j2" {
			t.Fatal("异地报告落进了当前会话")
		}
	}
}

// TestInjectAndWaitGivesUpOnCancel 没人排空时等不到就返回 ctx.Err，不假装送到了。
func TestInjectAndWaitGivesUpOnCancel(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore()) // wake 未接线：条目只躺在队列里
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := s.InjectAndWait(ctx, port.Injection{Source: "job:j3", Parts: injectPart("x")})
	if err == nil {
		t.Fatal("没人排空时应返回错误，不能假装已落地")
	}
}
