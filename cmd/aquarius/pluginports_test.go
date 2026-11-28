package main

// pluginports_test.go 六个供给面消费侧的容器与挂载测试（D356）：
// 容器单测（路由 / 顺序 / 接管还原 / 兜底档 / 扇出增删）+ setTierOne 挂载还原的
// 集成（pluginboot 模式，bootInstance 按需实现供给接口）。

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// ---------- 替身 ----------

// fakeLLM 可编程的 port.LLM：Generate 记录收到的模型名，Models 报给定清单。
type fakeLLM struct {
	models []string
	got    []string // Generate 收到的 req.Model（追加）
	err    error
}

func (f *fakeLLM) Generate(_ context.Context, req port.GenerateRequest) (port.Stream, error) {
	f.got = append(f.got, req.Model)
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}
func (f *fakeLLM) Models(context.Context) ([]port.ModelInfo, error) {
	out := make([]port.ModelInfo, 0, len(f.models))
	for _, m := range f.models {
		out = append(out, port.ModelInfo{Name: m})
	}
	return out, nil
}

// fakeIngest 按 Kind 是否命中前缀决定 Accepts；Ingest 记录接单。
type fakeIngest struct{ kinds []string }

func (f fakeIngest) Accepts(raw port.RawInput) bool {
	for _, k := range f.kinds {
		if strings.HasPrefix(raw.Kind, k) {
			return true
		}
	}
	return false
}
func (f fakeIngest) Ingest(context.Context, port.RawInput) (port.IngestReport, error) {
	return port.IngestReport{Note: "plugin"}, nil
}

// fakeVoice 可编程的转写/合成替身。
type fakeVoice struct{ hit *int }

func (f fakeVoice) Transcribe(context.Context, conversation.BlobRef, string) (string, error) {
	if f.hit != nil {
		*f.hit++
	}
	return "text", nil
}
func (f fakeVoice) Synthesize(context.Context, port.SpeechRequest) (conversation.BlobRef, error) {
	if f.hit != nil {
		*f.hit++
	}
	return conversation.BlobRef{}, nil
}

// fakeOut 记录 Deliver 的输出器。
type fakeOut struct{ name string }

func (f fakeOut) Name() string { return f.name }
func (f fakeOut) Deliver(context.Context, port.OutputRequest) error {
	return errors.New("boom")
}

// fakeSecrets 记录查询名的密钥替身。
type fakeSecrets struct{ got []string }

func (f *fakeSecrets) Get(_ context.Context, name string) (string, error) {
	f.got = append(f.got, name)
	return "v-" + name, nil
}

// ---------- 容器单测 ----------

func TestLLMRouteRoutingAndUnion(t *testing.T) {
	t.Parallel()
	builtin := &fakeLLM{models: []string{"m0"}}
	r := newLLMRoute(builtin)
	ctx := context.Background()

	// 未挂插件：全走内置，清单 = 内置。
	_, err := r.Generate(ctx, port.GenerateRequest{Model: "m0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(builtin.got) != 1 {
		t.Fatal("未挂插件应回落内置")
	}

	plug := &fakeLLM{models: []string{"p1", "p2"}}
	r.attach("plug", plug, []string{"p1", "p2"})
	if _, err := r.Generate(ctx, port.GenerateRequest{Model: "p1"}); err != nil || len(plug.got) != 1 {
		t.Fatalf("命中插件模型应路由到插件: %v %v", err, plug.got)
	}
	if _, err := r.Generate(ctx, port.GenerateRequest{Model: "m0"}); err != nil || len(builtin.got) != 2 {
		t.Fatal("内置模型名仍应走内置")
	}

	// 清单并集：内置在前、插件按启用序追加。
	ms, err := r.Models(ctx)
	if err != nil || len(ms) != 3 || ms[0].Name != "m0" || ms[1].Name != "p1" || ms[2].Name != "p2" {
		t.Fatalf("Models 并集 = %v err=%v", ms, err)
	}

	// 同名后到覆盖先到（名字冲突的裁决是 /model 能选到）。
	plug2 := &fakeLLM{models: []string{"p1"}}
	r.attach("plug2", plug2, []string{"p1"})
	if _, err := r.Generate(ctx, port.GenerateRequest{Model: "p1"}); err != nil || len(plug2.got) != 1 || len(plug.got) != 1 {
		t.Fatal("同名模型应归后启用者")
	}
	if ms, _ := r.Models(ctx); len(ms) != 3 {
		t.Fatalf("去重后并集 = %d", len(ms))
	}

	// 摘除：插件模型名回到内置。
	r.detach("plug")
	r.detach("plug2")
	if _, err := r.Generate(ctx, port.GenerateRequest{Model: "p1"}); err != nil || len(builtin.got) != 3 {
		t.Fatal("摘除后插件模型名应回落内置")
	}
	if ms, _ := r.Models(ctx); len(ms) != 1 {
		t.Fatalf("摘除后清单 = %d", len(ms))
	}
}

func TestIngestRouteOrderAndRemove(t *testing.T) {
	t.Parallel()
	r := newIngestRoute()
	a := fakeIngest{kinds: []string{"kind-a"}}
	b := fakeIngest{kinds: []string{"kind-a", "kind-b"}} // 与 a 抢 kind-a：先启用者赢
	r.put("b", b)
	r.put("a", a)

	if !r.Accepts(port.RawInput{Kind: "kind-a"}) || !r.Accepts(port.RawInput{Kind: "kind-b"}) {
		t.Fatal("任一插件认即 Accepts")
	}
	if rep, _ := r.Ingest(context.Background(), port.RawInput{Kind: "kind-a"}); rep.Note != "plugin" {
		t.Fatal("应分派给启用的插件")
	}

	r.remove("b")
	if !r.Accepts(port.RawInput{Kind: "kind-a"}) {
		t.Fatal("摘除后剩下的插件仍要接得住")
	}
	if r.Accepts(port.RawInput{Kind: "kind-b"}) {
		t.Fatal("摘除后它的 kind 不再被认")
	}
	r.remove("a")
	if r.Accepts(port.RawInput{Kind: "kind-a"}) || r.Accepts(port.RawInput{Kind: "kind-b"}) {
		t.Fatal("全摘后不再 Accepts")
	}
}

func TestVoiceSlotTakeoverRestore(t *testing.T) {
	t.Parallel()
	builtinHit, pluginHit := 0, 0
	builtin := fakeVoice{hit: &builtinHit}
	plugin := fakeVoice{hit: &pluginHit}
	r := newTranscribeRoute(builtin)

	// 第一个接管生效：走插件、内置不动。
	if owner := r.take("a", plugin); owner != "" {
		t.Fatalf("首接管应成功, got %q", owner)
	}
	if _, err := r.Transcribe(context.Background(), conversation.BlobRef{}, "zh"); err != nil || pluginHit != 1 || builtinHit != 0 {
		t.Fatalf("接管后应走插件: builtin=%d plugin=%d err=%v", builtinHit, pluginHit, err)
	}
	// 第二个被拒，报出占用者。
	if owner := r.take("b", fakeVoice{}); owner != "a" {
		t.Fatalf("第二接管应被拒并报占用者, got %q", owner)
	}
	// 停用还原内置。
	r.release("a")
	if _, err := r.Transcribe(context.Background(), conversation.BlobRef{}, "zh"); err != nil || builtinHit != 1 {
		t.Fatal("还原后应走回内置")
	}
	r.release("b") // 非占用者的 release 是 no-op
	if _, err := r.Transcribe(context.Background(), conversation.BlobRef{}, "zh"); err != nil || builtinHit != 2 {
		t.Fatal("release 应只认占用者")
	}

	// 内置没配（nil）：接管可用、还原清空。
	empty := newTranscribeRoute(nil)
	if empty.builtinWired() {
		t.Fatal("nil builtin 应报未接线")
	}
	empty.take("p", fakeVoice{})
	if _, err := empty.Transcribe(context.Background(), conversation.BlobRef{}, ""); err != nil {
		t.Fatal("接管后即便内置没配也应可用")
	}
	empty.release("p")
	if _, err := empty.Transcribe(context.Background(), conversation.BlobRef{}, ""); err == nil {
		t.Fatal("还原后无内置应报因（不再走插件）")
	}

	// 合成槽位同规则。
	ttsBuiltin, ttsPlugin := 0, 0
	s := newSynthesizeRoute(fakeVoice{hit: &ttsBuiltin})
	if owner := s.take("a", fakeVoice{hit: &ttsPlugin}); owner != "" {
		t.Fatal("合成首接管应成功")
	}
	if _, err := s.Synthesize(context.Background(), port.SpeechRequest{}); err != nil || ttsPlugin != 1 || ttsBuiltin != 0 {
		t.Fatalf("接管后应走插件: builtin=%d plugin=%d", ttsBuiltin, ttsPlugin)
	}
	s.release("a")
	if _, err := s.Synthesize(context.Background(), port.SpeechRequest{}); err != nil || ttsBuiltin != 1 {
		t.Fatal("合成还原应走回内置")
	}
}

func TestSecretChainPluginFallback(t *testing.T) {
	t.Parallel()
	plug := &fakeSecrets{}
	c := &secretChain{env: envSecrets{}, warnf: nil}
	c.SetExtra(plug)
	ctx := context.Background()

	// 库与 env 都没有 → 插件兜底命中。
	if v, err := c.Get(ctx, "PLUGIN_KEY"); err != nil || v != "v-PLUGIN_KEY" {
		t.Fatalf("插件兜底应命中: %q %v", v, err)
	}
	if len(plug.got) != 1 {
		t.Fatal("内置命中时不应问插件")
	}

	// 摘除后报因（两处都没有的原报因）。
	c.SetExtra(nil)
	if _, err := c.Get(ctx, "PLUGIN_KEY"); err == nil {
		t.Fatal("摘除兜底后应报因")
	}
}

func TestOutputsPresenterAddRemove(t *testing.T) {
	t.Parallel()
	p := &outputsPresenter{}
	p.AddOutput(fakeOut{name: "x"})
	p.AddOutput(fakeOut{name: "y"})
	p.AddOutput(fakeOut{name: "x"}) // 重名先摘后挂
	p.mu.Lock()
	n := len(p.outs)
	p.mu.Unlock()
	if n != 2 {
		t.Fatalf("重名去重后应剩 2, got %d", n)
	}
	if !p.RemoveOutput("x") || p.RemoveOutput("x") {
		t.Fatal("RemoveOutput 应幂等且报告是否摘到")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.outs) != 1 || p.outs[0].Name() != "y" {
		t.Fatalf("只剩 y: %v", p.outs)
	}
}

// ---------- setTierOne 挂载/还原集成 ----------

// portsBootInstance 按需实现六个供给接口的启动实例：字段为 nil 即不实现。
type portsBootInstance struct {
	models   []string
	accepts  string
	asrHit   *int
	secretOn bool
}

func (i portsBootInstance) Start() error                   { return nil }
func (i portsBootInstance) Shutdown(context.Context) error { return nil }
func (i portsBootInstance) Recall(context.Context, v1.MemoryRequest) (v1.MemoryContext, error) {
	return v1.MemoryContext{}, nil
}
func (i portsBootInstance) Tools(context.Context) []v1.ToolSpec { return nil }
func (i portsBootInstance) Execute(context.Context, v1.ToolCall) (v1.ToolResult, error) {
	return v1.ToolResult{}, nil
}
func (i portsBootInstance) Generate(context.Context, v1.GenerateRequest) (v1.Stream, error) {
	return portsStream{}, nil
}

// portsStream 最小流替身：立即 EOF（适配器收口即回，路由测试只看通不通）。
type portsStream struct{}

func (portsStream) Recv() (v1.Delta, error) { return v1.Delta{}, io.EOF }
func (portsStream) Close() error            { return nil }
func (i portsBootInstance) Models(context.Context) ([]v1.ModelInfo, error) {
	out := make([]v1.ModelInfo, 0, len(i.models))
	for _, m := range i.models {
		out = append(out, v1.ModelInfo{Name: m})
	}
	return out, nil
}
func (i portsBootInstance) Accepts(raw v1.RawInput) bool { return raw.Kind == i.accepts }
func (i portsBootInstance) Ingest(context.Context, v1.RawInput) (v1.IngestReport, error) {
	return v1.IngestReport{}, nil
}
func (i portsBootInstance) Name() string { return "ports-boot" }
func (i portsBootInstance) Deliver(context.Context, v1.OutputRequest) error {
	return errors.New("unused")
}
func (i portsBootInstance) Transcribe(context.Context, v1.BlobRef, string) (string, error) {
	if i.asrHit != nil {
		*i.asrHit++
	}
	return "text", nil
}
func (i portsBootInstance) Synthesize(context.Context, v1.SpeechRequest) (v1.BlobRef, error) {
	return v1.BlobRef{}, nil
}
func (i portsBootInstance) Get(context.Context, string) (string, error) {
	if !i.secretOn {
		return "", errors.New("nope")
	}
	return "sv", nil
}

// portsBootFactory 声明给定供给面的工厂，实例由构造函数给。
type portsBootFactory struct {
	name     string
	provides []string
	instance func() v1.Instance
}

func (f portsBootFactory) Manifest() v1.Manifest {
	return v1.Manifest{Name: f.name, API: v1.APIVersion, Provides: f.provides}
}
func (f portsBootFactory) Create(v1.Host) (v1.Instance, error) { return f.instance(), nil }

func TestSetTierOnePortsMountAndRestore(t *testing.T) {
	asrHit := 0
	inst := portsBootInstance{models: []string{"p-model"}, accepts: "kind-x", asrHit: &asrHit, secretOn: true}
	w, _ := newBootWiring(t, `{}`, portsBootFactory{
		name: "ports", provides: []string{"llm", "ingest", "output", "transcribe", "synthesize", "secrets"},
		instance: func() v1.Instance { return inst },
	})
	// 容器与槽位由测试预立（真装配在 assembleAudio/buildLLMs；boot 接线不跑那段）。
	w.llmRoute = newLLMRoute(&fakeLLM{models: []string{"m0"}})
	w.asrRoute = newTranscribeRoute(nil)
	w.ttsRoute = newSynthesizeRoute(nil)
	w.secrets = &secretChain{env: envSecrets{}}
	ctx := context.Background()

	if err := w.setTierOne(ctx, "ports", true, false); err != nil {
		t.Fatal(err)
	}
	// llm：模型清单进路由。
	if _, err := w.llmRoute.Generate(ctx, port.GenerateRequest{Model: "p-model"}); err != nil {
		t.Fatal("插件模型应可路由")
	}
	// ingest：集合里接得住。
	if !w.pluginIngest().Accepts(port.RawInput{Kind: "kind-x"}) {
		t.Fatal("插件摄取器应已接入")
	}
	// transcribe：接管槽位（内置没配，w.asr 被指到容器）。
	if _, err := w.asrRoute.Transcribe(ctx, conversation.BlobRef{}, ""); err != nil || asrHit != 1 {
		t.Fatal("识别应接管到插件")
	}
	if w.asr != w.asrRoute {
		t.Fatal("内置没配时接管应把 w.asr 指到容器")
	}
	// secrets：兜底档就位。
	if w.secretExtraOwner != "ports" {
		t.Fatalf("密钥兜底接管者 = %q", w.secretExtraOwner)
	}

	if err := w.setTierOne(ctx, "ports", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := w.llmRoute.Generate(ctx, port.GenerateRequest{Model: "p-model"}); err != nil {
		t.Fatal("摘除后插件模型名应回落内置")
	}
	if w.pluginIngest().Accepts(port.RawInput{Kind: "kind-x"}) {
		t.Fatal("停用后摄取器应摘除")
	}
	if w.asr != nil {
		t.Fatal("内置没配时停用应把 w.asr 还原为 nil")
	}
	if w.secretExtraOwner != "" {
		t.Fatal("密钥兜底档应摘除")
	}
	if w.hasAttachedPort("ports") {
		t.Fatal("还原后不应再有挂载记账")
	}
}
