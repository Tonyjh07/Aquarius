package app

// session_drain_test.go 排空点的口径表（D150/D167）：四个时机各自的两条口径——触发型注入
// 是否真起一轮、是否逐条呈现。此前这两条由调用点传两个布尔，四个组合里有一个（只驱动不
// 呈现之外的「既不驱动也不呈现」）从不出现却没有东西拦住；收成具名时机后由下面这张表锁定。

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestDrainPlanTable 口径表：① 入口与 ② 轮末只落地且逐条呈现；③ 唤醒驱动且呈现；
// ③' 命令点驱动但不呈现（调用方清屏回放）。未知取值按最保守的一档（只落地、呈现）。
func TestDrainPlanTable(t *testing.T) {
	cases := []struct {
		at       drainPoint
		driven   bool
		announce bool
		name     string
	}{
		{drainBeforeInput, false, true, "① Handle 入口"},
		{drainAfterTurn, false, true, "② 轮末"},
		{drainOnWake, true, true, "③ 空闲唤醒"},
		{drainBeforeReplay, true, false, "③' 命令点"},
		{drainPoint(99), false, true, "未知取值"},
	}
	for _, tc := range cases {
		p := tc.at.plan()
		if p.driven != tc.driven || p.announce != tc.announce {
			t.Fatalf("%s: plan = %+v, want driven=%v announce=%v", tc.name, p, tc.driven, tc.announce)
		}
	}
}

// TestDrainBeforeReplayKeepsTrigger 命令点排空：触发型注入落地时 Trigger 记为 true（本条确实
// 会起一轮），但 Drain 不逐条呈现——呈现交给调用方的清屏回放。
func TestDrainBeforeReplayKeepsTrigger(t *testing.T) {
	s, _, rec := injectableSession(t, newMemStore(), textStream("收到"))
	before := len(rec.events)

	if err := s.Inject(context.Background(), port.Injection{
		Source: "user",
		Parts:  injectPart("注入正文"),
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	landed, wantTurn, err := s.drainInjections(context.Background(), drainBeforeReplay)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(landed) != 1 || !landed[0].Trigger || !wantTurn {
		t.Fatalf("landed = %+v, wantTurn = %v（命令点应驱动起一轮）", landed, wantTurn)
	}
	if len(rec.events) != before {
		t.Fatal("命令点排空不得逐条呈现——由调用方清屏回放统一铺开")
	}
}

// TestDrainAfterTurnDoesNotTrigger 轮末排空：触发型注入只落地、Trigger 记 false（记的是
// 实际发生），也不要求跑轮。
func TestDrainAfterTurnDoesNotTrigger(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore())
	if err := s.Inject(context.Background(), port.Injection{
		Source: "job:j1", // 默认该触发
		Parts:  injectPart("任务完成"),
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	landed, wantTurn, err := s.drainInjections(context.Background(), drainAfterTurn)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(landed) != 1 || landed[0].Trigger || wantTurn {
		t.Fatalf("landed = %+v, wantTurn = %v（轮末只落地）", landed, wantTurn)
	}
	if landed[0].Role != conversation.RoleInject {
		t.Fatalf("role = %s, want inject", landed[0].Role)
	}
}

// TestDrainOnWakeTriggersAndAnnounces 唤醒点排空：触发型注入 Trigger 记 true、要求跑轮，
// 并逐条呈现（唤醒没有清屏回放，靠 HistoryEvent 补上这条记录）。
func TestDrainOnWakeTriggersAndAnnounces(t *testing.T) {
	s, _, rec := injectableSession(t, newMemStore())
	before := len(rec.events)
	if err := s.Inject(context.Background(), port.Injection{
		Source: "poller:j1",
		Parts:  injectPart("轮询命中"),
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	landed, wantTurn, err := s.drainInjections(context.Background(), drainOnWake)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(landed) != 1 || !landed[0].Trigger || !wantTurn {
		t.Fatalf("landed = %+v, wantTurn = %v", landed, wantTurn)
	}
	if len(rec.events) == before {
		t.Fatal("唤醒点应逐条呈现注入（无清屏回放兜底）")
	}
}

// TestDrainMisroutedSessionDropped 目标会话不是当前会话的注入整条丢弃（D167）：切换瞬间的
// 竞态不能把别的会话的报告塞进这里；丢弃不算「落地过」，故不落盘、也不要求跑轮。
func TestDrainMisroutedSessionDropped(t *testing.T) {
	s, _, _ := injectableSession(t, newMemStore())
	if err := s.Inject(context.Background(), port.Injection{
		Source:  "job:other",
		Parts:   injectPart("别的会话"),
		Session: conversation.ID("convOther"),
	}); err != nil {
		t.Fatalf("inject: %v", err)
	}
	landed, wantTurn, err := s.drainInjections(context.Background(), drainOnWake)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(landed) != 0 || wantTurn {
		t.Fatalf("landed = %+v, wantTurn = %v（错投的注入应整条丢弃）", landed, wantTurn)
	}
}
