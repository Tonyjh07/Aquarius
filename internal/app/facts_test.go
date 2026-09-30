package app

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestFactsPublishOnConstruct 构造即发布事实快照（D82/§7.6）：标题/会话 ID 就绪，
// 上下文预算取默认（Cfg 零值 64000），persona 装配非空故占用 > 0（无 TokenCounter →
// ③估算非精确），尚无实测用量。
func TestFactsPublishOnConstruct(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	f := s.Facts()
	if f.Title != defaultTitle {
		t.Fatalf("Title = %q, want %q", f.Title, defaultTitle)
	}
	if f.ConvID != string(s.Current().ID) {
		t.Fatalf("ConvID = %q, want %q", f.ConvID, s.Current().ID)
	}
	if f.CtxMax != DefaultMaxContextTokens {
		t.Fatalf("CtxMax = %d, want %d", f.CtxMax, DefaultMaxContextTokens)
	}
	if f.CtxTokens <= 0 {
		t.Fatalf("CtxTokens = %d, want > 0（persona 装配非空）", f.CtxTokens)
	}
	if f.CtxExact {
		t.Fatal("CtxExact = true, want false（无 TokenCounter → ③估算）")
	}
	if f.SumIn != 0 || f.SumOut != 0 || f.LastIn != 0 || f.LastOut != 0 {
		t.Fatalf("用量应全零: %+v", f)
	}
}

// TestFactsRecomputedAfterTurn Turn 后重算（D82/§7.6）：签名随标题/Head/用量变更，
// 重算后实测用量与上轮就位、上下文占用增长（est 链计入新节点）。
func TestFactsRecomputedAfterTurn(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, withUsage(textStream("答"),
		conversation.Usage{InputTokens: 100, OutputTokens: 20}))

	before := s.Facts()
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "你好"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	after := s.Facts()
	if after.SumIn != 100 || after.SumOut != 20 {
		t.Fatalf("Sum = %+v, want in=100 out=20（Path 实测累计）", after)
	}
	if after.LastIn != 100 || after.LastOut != 20 {
		t.Fatalf("Last = %d/%d, want 100/20（上轮实测）", after.LastIn, after.LastOut)
	}
	if after.Title == before.Title {
		t.Fatalf("Title 未随首条消息改写: %q", after.Title)
	}
	if after.CtxTokens <= before.CtxTokens {
		t.Fatalf("CtxTokens = %d, want > %d（计入新节点）", after.CtxTokens, before.CtxTokens)
	}
}

// TestFactsGateKeepsValues 门控行为等价（D82/§7.6）：无变更的命令输入（/help）
// 签名不变 → 保留快照原值；仅有空输入不炸不发布。
func TestFactsGateKeepsValues(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store)

	before := s.Facts()
	if _, err := s.Handle(context.Background(),
		port.UserInput{Command: &port.Command{Name: "help"}}); err != nil {
		t.Fatalf("help: %v", err)
	}
	after := s.Facts()
	if after != before {
		t.Fatalf("门控应保留快照: before=%+v after=%+v", before, after)
	}
}

// TestFactsSigOf 签名区分度（D82/§7.6 门控的纯函数单元）：标题/Head/用量任一变更
// 签名即变；同一会话未动签名稳定。
func TestFactsSigOf(t *testing.T) {
	store := newMemStore()
	s, _, _ := newTestSession(t, store, withUsage(textStream("答"),
		conversation.Usage{InputTokens: 100, OutputTokens: 20}))

	c := s.Current()
	sig0 := factsSigOf(c)

	if _, err := s.Handle(context.Background(), port.UserInput{Text: "你好"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	sig1 := factsSigOf(s.Current())
	if sig1 == sig0 {
		t.Fatal("标题/Head/用量均变更，签名应不同")
	}

	// 只动 Head 不动用量：/goto 移 Head 后签名仍变（上下文装配随 Head 变化）。
	if _, err := s.Handle(context.Background(),
		port.UserInput{Command: &port.Command{Name: "help"}}); err != nil {
		t.Fatalf("help: %v", err)
	}
	if sig2 := factsSigOf(s.Current()); sig2 != sig1 {
		t.Fatal("无变更输入后签名应稳定")
	}
}

// TestFactsUnpublishedZeroValue 未发布（构造早期）读零值不炸（D82/§7.6）。
func TestFactsUnpublishedZeroValue(t *testing.T) {
	s := &Session{}
	if f := s.Facts(); f != (port.SessionFacts{}) {
		t.Fatalf("Facts = %+v, want 零值", f)
	}
}
