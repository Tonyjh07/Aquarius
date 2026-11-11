package decorate

// 决策模型装饰器的单测（D281/D290）：三档分流的边界、失败即上交、shadow 与 off 逐条一致、
// 用户自己的命令不进模型。这几条是「它不会替你放行」的全部可执行口径。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// fakeDecider 脚本化决策模型。
type fakeDecider struct {
	dec    port.Decision
	err    error
	delay  time.Duration
	calls  int
	gotReq port.DecideRequest
}

func (f *fakeDecider) Decide(ctx context.Context, req port.DecideRequest) (port.Decision, error) {
	f.calls++
	f.gotReq = req
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return port.Decision{}, ctx.Err()
		}
	}
	return f.dec, f.err
}

// fakeSink 收集判定记录。
type fakeSink struct{ recs []DecideRecord }

func (s *fakeSink) Decide(rec DecideRecord) { s.recs = append(s.recs, rec) }

// testPolicy 默认阈值 + 够用的超时（零值超时会让 ctx 立刻过期）。
func testPolicy(mode DecideMode) DecidePolicy {
	return DecidePolicy{
		Mode: mode, Timeout: time.Second,
		AllowRiskBelow: DefaultAllowRiskBelow, AllowHumanBelow: DefaultAllowHumanBelow,
		DenyEnabled: true, DenyHumanAbove: DefaultDenyHumanAbove,
	}
}

// toolReq 一次工具确认请求。
func toolReq() port.ConfirmRequest {
	return port.ConfirmRequest{Origin: port.OriginTool, Tool: "file_write", Prompt: "write?", HasRisk: true}
}

// TestDecideAllowSkipsInner 明显该批：直接放行，不问用户，审计留一行 allow。
func TestDecideAllowSkipsInner(t *testing.T) {
	inner := &countingConf{}
	sink := &fakeSink{}
	dec := &fakeDecider{dec: port.Decision{Risk: 0.1, NeedsHuman: 0.02}}
	c := Decide(inner, dec, testPolicy(DecideOn), sink)
	ans, err := c.Confirm(context.Background(), toolReq())
	if err != nil || !ans.Allow {
		t.Fatalf("Confirm = %+v, %v", ans, err)
	}
	if inner.calls != 0 {
		t.Fatalf("内层被调用 %d 次，应为 0", inner.calls)
	}
	if len(sink.recs) != 1 || sink.recs[0].Verdict != VerdictAllow || !sink.recs[0].Scored {
		t.Fatalf("审计记录 = %+v", sink.recs)
	}
}

// TestDecideAskGoesToUser 拿不准：照旧问人。
func TestDecideAskGoesToUser(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: false, Reason: "nope"}}
	dec := &fakeDecider{dec: port.Decision{Risk: 0.9, NeedsHuman: 0.4}}
	ans, err := Decide(inner, dec, testPolicy(DecideOn), nil).
		Confirm(context.Background(), toolReq())
	if err != nil || ans.Allow || ans.Reason != "nope" {
		t.Fatalf("Confirm = %+v, %v", ans, err)
	}
	if inner.calls != 1 {
		t.Fatalf("内层调用次数 = %d, want 1", inner.calls)
	}
}

// TestDecideDenyWhenEnabled 开启拒绝档且分数越线：直接拒，原因写清「别重试」。
func TestDecideDenyWhenEnabled(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	dec := &fakeDecider{dec: port.Decision{Risk: 1.8, NeedsHuman: 0.95}}
	ans, err := Decide(inner, dec, testPolicy(DecideOn), nil).
		Confirm(context.Background(), toolReq())
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if ans.Allow || ans.Reason != DecideDenyReason {
		t.Fatalf("应拒绝并给出稳定原因: %+v", ans)
	}
	if inner.calls != 0 {
		t.Fatalf("拒绝时不该再问用户, calls=%d", inner.calls)
	}
	for _, want := range []string{"Do not retry", "permission level allows", "needs approval"} {
		if !strings.Contains(DecideDenyReason, want) {
			t.Fatalf("原因缺 %q: %q", want, DecideDenyReason)
		}
	}
}

// TestDecideDenyDisabledByDefault 拒绝档关着时同样分数只问人——默认不做硬拒（D290 决策 4）。
func TestDecideDenyDisabledByDefault(t *testing.T) {
	p := testPolicy(DecideOn)
	p.DenyEnabled = false
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	dec := &fakeDecider{dec: port.Decision{Risk: 1.8, NeedsHuman: 0.95}}
	ans, err := Decide(inner, dec, p, nil).Confirm(context.Background(), toolReq())
	if err != nil || !ans.Allow || inner.calls != 1 {
		t.Fatalf("应照旧问人: %+v, %v, calls=%d", ans, err, inner.calls)
	}
}

// TestDecideFailsToHuman 失败、超时、取消三条路：前两条交回确认，第三条按取消上抛。
func TestDecideFailsToHuman(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: false}}
	sink := &fakeSink{}
	dec := &fakeDecider{err: errors.New("endpoint down")}
	if _, err := Decide(inner, dec, testPolicy(DecideOn), sink).
		Confirm(context.Background(), toolReq()); err != nil {
		t.Fatalf("判定失败不该上抛: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("失败应回到内层确认, calls=%d", inner.calls)
	}
	if len(sink.recs) != 1 || sink.recs[0].Verdict != VerdictError || sink.recs[0].Scored {
		t.Fatalf("审计应记一次 error: %+v", sink.recs)
	}

	// 超时：模型比总闸慢，ctx 超时后照样回到内层。
	p := testPolicy(DecideOn)
	p.Timeout = 20 * time.Millisecond
	slow := &fakeDecider{dec: port.Decision{Risk: 0.1}, delay: time.Second}
	inner2 := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	if _, err := Decide(inner2, slow, p, nil).Confirm(context.Background(), toolReq()); err != nil {
		t.Fatalf("超时不该上抛: %v", err)
	}
	if inner2.calls != 1 {
		t.Fatalf("超时应回到内层确认, calls=%d", inner2.calls)
	}

	// 父 ctx 取消：不记「没判成」，直接按取消返回，也不去问用户。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Decide(inner, dec, testPolicy(DecideOn), nil).Confirm(ctx, toolReq()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestDecideShadowIsOffWithAudit shadow 的返回值与 off 逐条一致，只多一行审计。
func TestDecideShadowIsOffWithAudit(t *testing.T) {
	sink := &fakeSink{}
	cases := []port.Decision{
		{Risk: 0.05, NeedsHuman: 0.01}, // 本该放行
		{Risk: 1.9, NeedsHuman: 0.99},  // 本该拒绝
		{Risk: 0.9, NeedsHuman: 0.5},   // 该问
	}
	want := []Verdict{VerdictAllow, VerdictDeny, VerdictAsk}
	for i, d := range cases {
		inner := &countingConf{answer: port.ConfirmAnswer{Allow: false, Reason: "asked"}}
		ans, err := Decide(inner, &fakeDecider{dec: d}, testPolicy(DecideShadow), sink).
			Confirm(context.Background(), toolReq())
		if err != nil || ans.Allow || ans.Reason != "asked" || inner.calls != 1 {
			t.Fatalf("case %d: shadow 必须照旧问人: %+v, %v, calls=%d", i, ans, err, inner.calls)
		}
	}
	if len(sink.recs) != len(cases) {
		t.Fatalf("审计记录 %d 条, want %d", len(sink.recs), len(cases))
	}
	for i, rec := range sink.recs {
		if rec.Verdict != want[i] {
			t.Fatalf("case %d verdict = %q, want %q", i, rec.Verdict, want[i])
		}
		if rec.Mode != DecideShadow {
			t.Fatalf("case %d mode = %q", i, rec.Mode)
		}
	}
}

// TestDecideSkipsCommandOrigin 用户自己敲的命令不送模型：那不是模型行为，也没有上下文。
func TestDecideSkipsCommandOrigin(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	dec := &fakeDecider{dec: port.Decision{Risk: 0.1, NeedsHuman: 0.01}}
	req := toolReq()
	req.Origin = port.OriginCommand
	if _, err := Decide(inner, dec, testPolicy(DecideOn), nil).Confirm(context.Background(), req); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if dec.calls != 0 {
		t.Fatalf("命令来源不该问模型, calls=%d", dec.calls)
	}
	if inner.calls != 1 {
		t.Fatalf("应照旧问人, calls=%d", inner.calls)
	}
}

// TestDecideRequestShape 折给端口的字段：op 只在申报时给，风险档来自工具规格。
func TestDecideRequestShape(t *testing.T) {
	dec := &fakeDecider{dec: port.Decision{Risk: 0.1}}
	req := port.ConfirmRequest{
		Origin: port.OriginAgent, Tool: "shell", Op: perm.OpWrite, HasOp: true,
		Target: "go build ./...", HasRisk: false, Task: "verify the refactor",
	}
	if _, err := Decide(&countingConf{answer: port.ConfirmAnswer{Allow: true}}, dec,
		testPolicy(DecideOn), nil).Confirm(context.Background(), req); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	got := dec.gotReq
	if got.Origin != "acpgate" || got.Tool != "shell" || got.Op != "write" ||
		got.Target != "go build ./..." || got.Task != "verify the refactor" {
		t.Fatalf("请求字段 = %+v", got)
	}
	// 没申报风险档时按 medium 交出去。
	if got.Risk != "medium" {
		t.Fatalf("risk = %q, want medium", got.Risk)
	}

	// 没申报操作时 op 留空，不能落成默认的 read。
	dec2 := &fakeDecider{dec: port.Decision{Risk: 0.1}}
	req2 := port.ConfirmRequest{Origin: port.OriginTool, Tool: "job_wait"}
	if _, err := Decide(&countingConf{answer: port.ConfirmAnswer{Allow: true}}, dec2,
		testPolicy(DecideOn), nil).Confirm(context.Background(), req2); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if dec2.gotReq.Op != "" {
		t.Fatalf("未申报操作时 op = %q, want empty", dec2.gotReq.Op)
	}
}

// TestDecideJudgement 阈值边界：放行要两个条件同时成立，拒绝档在其后。
func TestDecideJudgement(t *testing.T) {
	p := testPolicy(DecideOn)
	cases := []struct {
		name string
		dec  port.Decision
		want Verdict
	}{
		{"两条都过", port.Decision{Risk: 0.49, NeedsHuman: 0.14}, VerdictAllow},
		{"风险刚好压线", port.Decision{Risk: 0.5, NeedsHuman: 0.0}, VerdictAsk},
		{"人意愿刚好压线", port.Decision{Risk: 0.0, NeedsHuman: 0.15}, VerdictAsk},
		{"风险高但没有该拒的依据", port.Decision{Risk: 0.6, NeedsHuman: 0.2}, VerdictAsk},
		{"该拒", port.Decision{Risk: 1.2, NeedsHuman: 0.85}, VerdictDeny},
	}
	for _, c := range cases {
		if got := p.Judge(c.dec); got != c.want {
			t.Fatalf("%s: Judge(%+v) = %q, want %q", c.name, c.dec, got, c.want)
		}
	}
}

// TestDecideNoDeciderPassThrough 没建出客户端时原样透传（装配根的兜底）。
func TestDecideNoDeciderPassThrough(t *testing.T) {
	inner := &countingConf{answer: port.ConfirmAnswer{Allow: true}}
	if _, err := Decide(inner, nil, testPolicy(DecideOn), nil).
		Confirm(context.Background(), toolReq()); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1", inner.calls)
	}
}
