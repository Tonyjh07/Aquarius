package app

// pending_test.go 运行中的插话（D272/D273）：清单本身的增删改取，以及两个取用边界在
// 树里留下的形状——引导切开一段、排队在轮末接着起一轮。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// hookedLLM 在每次 Generate 之前跑一下钩子（模拟「一轮正在跑的时候用户插了句话」）。
type hookedLLM struct {
	inner  *scriptLLM
	onGen  func(n int)
	genNum int
}

func (h *hookedLLM) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	h.genNum++
	if h.onGen != nil {
		h.onGen(h.genNum)
	}
	return h.inner.Generate(ctx, req)
}

func (h *hookedLLM) Models(ctx context.Context) ([]port.ModelInfo, error) {
	return h.inner.Models(ctx)
}

// newSteerSession 造一个可注入插话的会话：llm 的请求记录与事件都留在返回值里。
func newSteerSession(t *testing.T, onGen func(n int), streams ...*scriptStream) (*Session, *scriptLLM, *recorder) {
	t.Helper()
	rec := &recorder{}
	inner := &scriptLLM{t: t, streams: streams}
	llm := &hookedLLM{inner: inner, onGen: onGen}
	a, err := New(Deps{LLM: llm, UI: rec, IDs: &seqIDs{}, Clock: fixedClock{testTime}}, Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: a, IDs: &seqIDs{}, Clock: fixedClock{testTime}, UI: rec,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s, inner, rec
}

// TestPendingListAddLimits 上限、空文本、超长、未知档位按排队——四种拒绝/兜底都不静默。
func TestPendingListAddLimits(t *testing.T) {
	p := newPendingList()
	if _, err := p.Add("   ", port.DeliveryQueue); !errors.Is(err, port.ErrPendingEmpty) {
		t.Fatalf("空文本 err = %v, want ErrPendingEmpty", err)
	}
	if _, err := p.Add(strings.Repeat("字", pendingMaxRunes+1), port.DeliveryQueue); !errors.Is(err, port.ErrPendingTooLong) {
		t.Fatalf("超长 err = %v, want ErrPendingTooLong", err)
	}
	it, err := p.Add("  hello  ", "bogus")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if it.Text != "hello" || it.Delivery != port.DeliveryQueue {
		t.Fatalf("item = %+v, want 去空白 + 未知档位按排队", it)
	}
	for i := 1; i < pendingMaxItems; i++ {
		if _, err := p.Add("x", port.DeliveryQueue); err != nil {
			t.Fatalf("add #%d: %v", i, err)
		}
	}
	if _, err := p.Add("x", port.DeliveryQueue); !errors.Is(err, port.ErrPendingFull) {
		t.Fatalf("第 %d 条 err = %v, want ErrPendingFull", pendingMaxItems+1, err)
	}
}

// TestPendingListMutate 撤销、改档、拉回、清空都作用在同一条上（按 ID）。
func TestPendingListMutate(t *testing.T) {
	p := newPendingList()
	a, _ := p.Add("甲", port.DeliveryQueue)
	b, _ := p.Add("乙", port.DeliveryQueue)
	if !p.SetDelivery(b.ID, port.DeliverySteer) {
		t.Fatal("改档应命中")
	}
	if got := p.List(); len(got) != 2 || got[1].Delivery != port.DeliverySteer {
		t.Fatalf("List = %+v, want 乙 为 steer", got)
	}
	if !p.Remove(a.ID) || p.Remove(a.ID) {
		t.Fatal("撤销应只在第一次命中")
	}
	got, ok := p.Take(b.ID)
	if !ok || got.Text != "乙" || p.count() != 0 {
		t.Fatalf("Take = %+v/%v, count=%d", got, ok, p.count())
	}
	if _, err := p.Add("丙", port.DeliveryQueue); err != nil {
		t.Fatalf("add: %v", err)
	}
	if n := p.Clear(); n != 1 || p.count() != 0 {
		t.Fatalf("Clear = %d, count=%d", n, p.count())
	}
}

// TestPendingSteerOrder 轮内取走全部引导（保持说出顺序），排队的一条都不动。
func TestPendingSteerOrder(t *testing.T) {
	p := newPendingList()
	_, _ = p.Add("第一条", port.DeliverySteer)
	_, _ = p.Add("排队的", port.DeliveryQueue)
	_, _ = p.Add("第二条", port.DeliverySteer)
	got := p.takeSteer()
	if len(got) != 2 || got[0].Text != "第一条" || got[1].Text != "第二条" {
		t.Fatalf("takeSteer = %+v, want 两条引导按说出顺序", got)
	}
	if left := p.List(); len(left) != 1 || left[0].Text != "排队的" {
		t.Fatalf("剩余 = %+v, want 只有排队那条", left)
	}
}

// TestSteerCutsSegment 工具批之后取引导：当前段提交成 assistant 节点、引导落成 user 节点，
// 下一次请求里模型看得见那句话，树里三个节点按序（D273）。
func TestSteerCutsSegment(t *testing.T) {
	// 第 1 次生成产出正文 + 一次工具调用；工具执行时「用户插了一句」；第 2 次生成收尾。
	runner := &steerOnToolRunner{steer: "顺便看一下日志"}
	rec := &recorder{}
	inner := &scriptLLM{t: t, streams: []*scriptStream{
		{steps: []scriptStep{
			{delta: port.Delta{Text: "前半："}},
			{delta: port.Delta{ToolCalls: []port.ToolCallDelta{{Index: 0, ID: "k1", Name: "probe"}}}},
		}},
		textStream("后半"),
	}}
	a, err := New(Deps{LLM: inner, UI: rec, IDs: &seqIDs{}, Clock: fixedClock{testTime}, Tools: runner},
		Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	s, err := NewSession(context.Background(), SessionDeps{
		Store: newMemStore(), Agent: a, IDs: &seqIDs{}, Clock: fixedClock{testTime}, UI: rec,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	runner.session = s
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "开始"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(inner.requests) != 2 {
		t.Fatalf("请求数 = %d, want 2（引导在第二次请求之前插入）", len(inner.requests))
	}
	if !requestHas(inner.requests[1], "顺便看一下日志") {
		t.Fatal("第二次请求里没有引导那句话")
	}
	path := s.Current().Path()
	var roles []conversation.Role
	var texts []string
	for _, m := range path {
		roles = append(roles, m.Role)
		texts = append(texts, pendingTestText(m))
	}
	wantRoles := []conversation.Role{
		conversation.RoleRoot, conversation.RoleSystem,
		conversation.RoleUser, conversation.RoleAssistant, conversation.RoleUser, conversation.RoleAssistant,
	}
	if len(roles) != len(wantRoles) {
		t.Fatalf("路径角色 = %v（%v）, want %v", roles, texts, wantRoles)
	}
	for i := range wantRoles {
		if roles[i] != wantRoles[i] {
			t.Fatalf("第 %d 个节点角色 = %s, want %s（全路径 %v）", i, roles[i], wantRoles[i], roles)
		}
	}
	if !strings.Contains(texts[3], "前半") || !strings.Contains(texts[5], "后半") {
		t.Fatalf("段正文 = %q / %q, want 前半 / 后半", texts[3], texts[5])
	}
	// 事件序：assistant 段 → user 节点 → 收尾段（前端照事件序追加，转写区顺序天然正确）。
	var kinds []string
	for _, ev := range rec.events {
		if c, ok := ev.(port.CommittedEvent); ok {
			kinds = append(kinds, string(c.Message.Role))
		}
	}
	if len(kinds) != 3 || kinds[0] != "assistant" || kinds[1] != "user" || kinds[2] != "assistant" {
		t.Fatalf("提交事件角色序 = %v, want [assistant user assistant]", kinds)
	}
}

// TestSteerAtEndKeepsRunning 本来要收场那一刻插进来的话也算：本轮不结束，接着跑
// （引导不带工具也能插进去——它是在流式输出期间说的）。
func TestSteerAtEndKeepsRunning(t *testing.T) {
	var s *Session
	llm := func(n int) {
		if n == 1 { // 第一次生成时「用户打下这句话」
			if _, err := s.Pending().Add("还有一件事", port.DeliverySteer); err != nil {
				t.Errorf("add steer: %v", err)
			}
		}
	}
	sess, inner, rec := newSteerSession(t, llm, textStream("先答一句"), textStream("接着答"))
	s = sess
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "开始"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(inner.requests) != 2 {
		t.Fatalf("请求数 = %d, want 2（收场前看到引导就不收场）", len(inner.requests))
	}
	if !requestHas(inner.requests[1], "还有一件事") {
		t.Fatal("第二次请求里没有引导")
	}
	if n := s.pendingQ.count(); n != 0 {
		t.Fatalf("清单剩余 %d 条, want 0", n)
	}
	var roles []string
	for _, ev := range rec.events {
		if c, ok := ev.(port.CommittedEvent); ok {
			roles = append(roles, string(c.Message.Role))
		}
	}
	if strings.Join(roles, ",") != "assistant,user,assistant" {
		t.Fatalf("提交事件角色序 = %v", roles)
	}
}

// TestQueueResumesAfterTurn 排队消息在轮末按 FIFO 取一条接着起一轮，不回到输入循环。
func TestQueueResumesAfterTurn(t *testing.T) {
	var s *Session
	llm := func(n int) {
		if n == 1 {
			for _, text := range []string{"排队的一句", "再排一句"} {
				if _, err := s.Pending().Add(text, port.DeliveryQueue); err != nil {
					t.Errorf("add queue: %v", err)
				}
			}
		}
	}
	sess, inner, _ := newSteerSession(t, llm, textStream("第一轮"), textStream("第二轮"), textStream("第三轮"))
	s = sess
	if _, err := s.Handle(context.Background(), port.UserInput{Text: "开始"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	// 一次 Handle 连跑三轮：首轮 + 两条排队各一轮，每轮末只取一条。
	if len(inner.requests) != 3 {
		t.Fatalf("请求数 = %d, want 3", len(inner.requests))
	}
	if !requestHas(inner.requests[1], "排队的一句") || requestHas(inner.requests[1], "再排一句") {
		t.Fatal("第二轮请求应只含第一条排队消息")
	}
	if !requestHas(inner.requests[2], "再排一句") {
		t.Fatal("第三轮请求里没有第二条排队消息")
	}
	if n := s.pendingQ.count(); n != 0 {
		t.Fatalf("清单剩余 %d 条, want 0", n)
	}
}

// TestStopPausesQueue 停止键取消本轮：清单原样保留、但不自动续跑；下一次真实输入解除。
func TestStopPausesQueue(t *testing.T) {
	s, inner, _ := newSteerSession(t, nil, textStream("不该被请求"))
	inner.genErr = context.Canceled // 生成前就被停止键打断
	if _, err := s.Pending().Add("留下来的一句", port.DeliveryQueue); err != nil {
		t.Fatalf("add: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 生成前就停：按取消收场
	if _, err := s.Handle(ctx, port.UserInput{Text: "开始"}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(inner.requests) != 1 {
		t.Fatalf("取消的轮只该尝试一次生成，实发 %d 次", len(inner.requests))
	}
	if n := s.pendingQ.count(); n != 1 {
		t.Fatalf("清单剩余 %d 条, want 1（停止不清空）", n)
	}
	if !s.pendingQ.isPaused() {
		t.Fatal("停止后应置 paused（轮末不自动续跑）")
	}
	if _, ok := s.pendingQ.takeFirst(); ok {
		t.Fatal("paused 期间轮末不该取走任何一条")
	}
	// 下一次真实输入解除暂停（这里只验状态位，不真的再跑一轮）。
	s.pendingQ.setPaused(false)
	if _, ok := s.pendingQ.takeFirst(); !ok {
		t.Fatal("解除暂停后应能取到那条")
	}
}

// TestSwitchDropsPending 切走会话：清单明确丢弃并留一行提示（不静默丢东西）。
func TestSwitchDropsPending(t *testing.T) {
	s, _, _ := newSteerSession(t, nil)
	if _, err := s.Pending().Add("一条", port.DeliveryQueue); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := s.execNew(context.Background(), port.Command{Name: "new"})
	if err != nil {
		t.Fatalf("execNew: %v", err)
	}
	if !strings.Contains(out, "未随会话带走") {
		t.Fatalf("/new 回执 = %q, want 含丢弃提示", out)
	}
	if n := s.pendingQ.count(); n != 0 {
		t.Fatalf("切走后剩余 %d 条, want 0", n)
	}
}

// steerOnToolRunner 工具执行时往清单里插一句引导（把「用户在我干活时说话」放进流水线）。
type steerOnToolRunner struct {
	session *Session
	steer   string
}

func (r *steerOnToolRunner) Specs(context.Context) ([]tool.Spec, error) {
	return []tool.Spec{{Name: "probe", Description: "探一下"}}, nil
}

func (r *steerOnToolRunner) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if _, err := r.session.Pending().Add(r.steer, port.DeliverySteer); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{CallID: call.ID, OK: true, Output: "done"}, nil
}

// pendingTestText 节点内正文分片的拼接（断言用，只关心纯文本分片）。
func pendingTestText(m conversation.Message) string {
	var b strings.Builder
	for _, p := range m.Content {
		if p.Kind == conversation.PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func requestHas(req port.GenerateRequest, want string) bool {
	for _, m := range req.Messages {
		for _, p := range m.Content {
			if strings.Contains(p.Text, want) {
				return true
			}
		}
	}
	return false
}
