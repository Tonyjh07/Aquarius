package plugingo

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// host_test.go Host 包装与供给面适配器的行为：转发是否保真、可选扩展是不是真的
// 「实现即被发现」、以及每个入口有没有把会话身份带进去。

type fakeConfirmer struct {
	got  port.ConfirmRequest
	sess conversation.ID
	err  error
}

func (f *fakeConfirmer) Confirm(ctx context.Context, req port.ConfirmRequest) (port.ConfirmAnswer, error) {
	f.got = req
	f.sess, _ = port.SessionIDFrom(ctx)
	if f.err != nil {
		return port.ConfirmAnswer{}, f.err
	}
	return port.ConfirmAnswer{Allow: true, Level: "full"}, nil
}

func TestOptionalExtensionsAreDiscoverable(t *testing.T) {
	plain := NewHost(HostOptions{Plugin: "p"})
	if _, ok := plain.(v1.NoticePoster); ok {
		t.Fatal("没给 Notice 时不该被断言到 NoticePoster")
	}
	if _, ok := plain.(v1.LegacyMemoryHost); ok {
		t.Fatal("没给 LegacyMemory 时不该被断言到 LegacyMemoryHost")
	}
	if _, ok := plain.(v1.UIEnviron); ok {
		t.Fatal("没给 Environ 时不该被断言到 UIEnviron")
	}
	rich := NewHost(HostOptions{
		Plugin: "p",
		Notice: func(string, string, ...any) {},
		LegacyMemory: func(context.Context) (v1.LegacyMemorySnapshot, error) {
			return v1.LegacyMemorySnapshot{Global: v1.LegacyGlobalMemory{Name: "memories.md", Content: "g"}}, nil
		},
	})
	if _, ok := rich.(v1.NoticePoster); !ok {
		t.Fatal("给了 Notice 就该命中 NoticePoster")
	}
	lm, ok := rich.(v1.LegacyMemoryHost)
	if !ok {
		t.Fatal("给了 LegacyMemory 就该命中 LegacyMemoryHost")
	}
	snap, err := lm.LegacyMemory(context.Background())
	if err != nil || snap.Global.Content != "g" {
		t.Fatalf("快照没转发：%+v %v", snap, err)
	}
}

// TestUIEnvironIsLive 环境快照是「现问」而不是「造 Host 时冻结」：深浅色会在运行中变，
// 存值的话插件永远拿到第一眼那个。
func TestUIEnvironIsLive(t *testing.T) {
	dark := false
	h := NewHost(HostOptions{
		Plugin: "p",
		Environ: func() v1.UIEnvironment {
			return v1.UIEnvironment{Frontend: v1.FrontendGUI, Dark: dark, Scale: 1.5}
		},
	})
	env, ok := h.(v1.UIEnviron)
	if !ok {
		t.Fatal("给了 Environ 就该命中 UIEnviron")
	}
	if got := env.Environ(); got.Dark || got.Scale != 1.5 || got.Frontend != v1.FrontendGUI {
		t.Fatalf("快照没转发：%+v", got)
	}
	dark = true // 模拟设置窗切深色
	if !env.Environ().Dark {
		t.Fatal("第二次调用没看到变化：存的是值不是函数")
	}
}

func TestHostLoggerFillsContextSegment(t *testing.T) {
	var gotCtx string
	h := NewHost(HostOptions{Plugin: "myp", Log: func(_ port.LogLevel, ctx, _, _ string, _ ...any) {
		gotCtx = ctx
	}})
	h.Logger().Log(v1.LogWarn, "自己写的一句话", "也一样")
	if gotCtx != "plugin:myp" {
		t.Fatalf("上下文段=%q，想要 plugin:myp", gotCtx)
	}
}

func TestHostConfirmCarriesSession(t *testing.T) {
	fc := &fakeConfirmer{}
	h := NewHost(HostOptions{Plugin: "myp", Confirmer: fc})
	ctx := v1.WithSession(context.Background(), "s-9")
	ans, err := h.Confirm(ctx, v1.ConfirmRequest{
		Tool: "write", Op: v1.OpWrite, HasOp: true, Risk: v1.RiskConfirm, HasRisk: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Allow || ans.Level != "full" {
		t.Fatalf("应答没转回来：%+v", ans)
	}
	if fc.sess != conversation.ID("s-9") {
		t.Fatalf("确认链没拿到会话：%q", fc.sess)
	}
	if fc.got.Op != 2 || !fc.got.HasOp { // perm.OpWrite 的枚举值不写死，这里只验「不是 read」
		if fc.got.Op == 0 {
			t.Fatalf("Op 没搬过去：%+v", fc.got)
		}
	}
}

func TestHostConfirmWithoutConfirmer(t *testing.T) {
	h := NewHost(HostOptions{Plugin: "myp"})
	if _, err := h.Confirm(context.Background(), v1.ConfirmRequest{}); err == nil {
		t.Fatal("没接确认链时应报错，不是静默放行")
	}
}

func TestHostBlobsAndSecretsErrors(t *testing.T) {
	h := NewHost(HostOptions{Plugin: "myp"})
	if _, err := h.Secrets().Resolve(context.Background(), "k"); err == nil {
		t.Fatal("没接密钥服务时应报错")
	}
	if _, err := h.Blobs().Stat(context.Background(), v1.BlobRef{Hash: "h"}); err == nil {
		t.Fatal("没接附件库时应报错")
	}
	if h.WorkDir() != "" {
		t.Fatalf("未给目录时应为空串，得到 %q", h.WorkDir())
	}
}

// fakeTools 一个工具供给面插件：记录收到的 ctx 与调用。
type fakeTools struct {
	sawSession      string
	sawToolsSession string
	lastCall        v1.ToolCall
}

func (f *fakeTools) Tools(ctx context.Context) []v1.ToolSpec {
	f.sawToolsSession, _ = v1.SessionFrom(ctx)
	return []v1.ToolSpec{{Name: "remember", Description: "记一条", Schema: []byte(`{"type":"object"}`), Risk: v1.RiskSafe}}
}

func (f *fakeTools) Execute(ctx context.Context, call v1.ToolCall) (v1.ToolResult, error) {
	f.lastCall = call
	f.sawSession, _ = v1.SessionFrom(ctx)
	return v1.ToolResult{OK: true, Output: "ok"}, nil
}

func (f *fakeTools) Describe(call v1.ToolCall) string {
	return "记下：" + string(call.Args)
}

func TestToolSetAdapterWiresDescriberAndSession(t *testing.T) {
	p := &fakeTools{}
	set := NewToolSet("demo", p)
	if set.Name() != "demo" {
		t.Fatalf("源名=%q", set.Name())
	}
	ctx := port.WithSessionID(context.Background(), conversation.ID("s-2"))

	specs, err := set.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("工具数=%d", len(specs))
	}
	spec := specs[0]
	if spec.Name != "remember" || spec.Risk != tool.Safe {
		t.Fatalf("声明没转对：%+v", spec)
	}
	// 未声明风险（空串）按 confirm 处理：写错一个词的代价是多问一次，不是默认放行。
	if r := toPortRisk(""); r != tool.Confirm {
		t.Fatalf("空风险档应落到 confirm，得到 %v", r)
	}
	if spec.Describe == nil {
		t.Fatal("插件实现了 Describer，声明里该有确认摘要")
	}
	if got := spec.Describe([]byte(`{"k":"v"}`)); !strings.Contains(got, `{"k":"v"}`) {
		t.Fatalf("确认摘要没走插件：%q", got)
	}
	if p.sawToolsSession != "s-2" {
		t.Fatalf("声明入口没带会话：%q", p.sawToolsSession)
	}

	ts, err := set.Lookup(ctx, "remember")
	if err != nil || ts == nil {
		t.Fatalf("按名找不到自己的工具：%v", err)
	}
	res, err := ts.Execute(ctx, tool.Call{ID: "c1", Name: "remember", Args: []byte(`{"k":"v"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Output != "ok" || res.CallID != "c1" {
		t.Fatalf("结果没转回来：%+v", res)
	}
	if p.sawSession != "s-2" {
		t.Fatalf("工具入口没带会话：%q", p.sawSession)
	}
	if p.lastCall.ID != "c1" || string(p.lastCall.Args) != `{"k":"v"}` {
		t.Fatalf("调用没搬过去：%+v", p.lastCall)
	}
}

// fakeLLM 一个模型插件：把收到的会话记下来，流里给一段文本与一次用量。
type fakeLLM struct{ sawSession string }

func (f *fakeLLM) Generate(ctx context.Context, req v1.GenerateRequest) (v1.Stream, error) {
	f.sawSession, _ = v1.SessionFrom(ctx)
	if req.Model == "" {
		return nil, errors.New("no model")
	}
	return &fakeStream{}, nil
}

func (f *fakeLLM) Models(context.Context) ([]v1.ModelInfo, error) {
	return []v1.ModelInfo{{Name: "m", Caps: v1.ModelCaps{Vision: true}, ContextWindow: 8000}}, nil
}

type fakeStream struct{ sent bool }

func (s *fakeStream) Recv() (v1.Delta, error) {
	if s.sent {
		return v1.Delta{}, io.EOF
	}
	s.sent = true
	return v1.Delta{Text: "hi", Usage: &v1.Usage{InputTokens: 3, OutputTokens: 1}}, nil
}

func (s *fakeStream) Close() error { return nil }

func TestLLMAdapter(t *testing.T) {
	p := &fakeLLM{}
	l := NewLLM(p)
	ctx := port.WithSessionID(context.Background(), conversation.ID("s-3"))
	s, err := l.Generate(ctx, port.GenerateRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if p.sawSession != "s-3" {
		t.Fatalf("模型入口没带会话：%q", p.sawSession)
	}
	d, err := s.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != "hi" || d.Usage == nil || d.Usage.InputTokens != 3 {
		t.Fatalf("增量没转对：%+v", d)
	}
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("结束信号应原样是 io.EOF，得到 %v", err)
	}

	ms, err := l.Models(context.Background())
	if err != nil || len(ms) != 1 || !ms[0].Caps.Vision || ms[0].ContextWindow != 8000 {
		t.Fatalf("模型清单没转对：%+v %v", ms, err)
	}
}

func TestLLMAdapterRejectsNilStream(t *testing.T) {
	l := NewLLM(nilStreamLLM{})
	if _, err := l.Generate(context.Background(), port.GenerateRequest{Model: "m"}); err == nil {
		t.Fatal("插件交回 nil 流时要报错")
	}
}

type nilStreamLLM struct{}

func (nilStreamLLM) Generate(context.Context, v1.GenerateRequest) (v1.Stream, error) {
	return nil, nil
}
func (nilStreamLLM) Models(context.Context) ([]v1.ModelInfo, error) { return nil, nil }

// 两个目录与语言都是纯转发：插件拿到什么就是宿主填了什么，不在这里做任何加工。
func TestHostPathsAndLocaleArePassThrough(t *testing.T) {
	h := NewHost(HostOptions{Plugin: "p", DataDir: "/persist/p", WorkDir: "/tmp/p", Locale: "en"})
	if h.DataDir() != "/persist/p" || h.WorkDir() != "/tmp/p" {
		t.Fatalf("目录没转发：data=%q work=%q", h.DataDir(), h.WorkDir())
	}
	if h.Locale() != "en" {
		t.Fatalf("语言没转发：%q", h.Locale())
	}
	// 没接的时候是空串而不是报错：目录与语言都是「有就有」的能力，插件据此降级。
	bare := NewHost(HostOptions{Plugin: "p"})
	if bare.DataDir() != "" || bare.WorkDir() != "" || bare.Locale() != "" {
		t.Fatalf("未接线时应为空串：data=%q work=%q locale=%q", bare.DataDir(), bare.WorkDir(), bare.Locale())
	}
}
