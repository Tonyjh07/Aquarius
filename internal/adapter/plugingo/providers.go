package plugingo

import (
	"context"
	"errors"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
	v1 "github.com/Tonyjh07/Aquarius/pluginapi"
)

// providers.go 供给面插件 → 内核端口的适配器（plugin-guide.md §4）。
//
// 这些适配器是内核对插件的唯一调用面：内核只认 port 接口，插件的实现藏在构造器后面。
// 每个入口都做同一件容易被忘的事——把会话身份从内核的 ctx 键搬到 v1 的键
// （toV1Session，D338）。漏一个入口不会报错，只会让插件悄悄退回全局，所以每个构造器
// 的入口方法都单独调一次，不做「顺手带上」的公共包装。
//
// 错误一律原样上抛：v1 没有错误词表，内核按「不可重试」处理（保守方向，见包注释）。

// toV1Session 会话身份换键：port → v1。插件入口的统一第一步。
func toV1Session(ctx context.Context) context.Context {
	id, ok := port.SessionIDFrom(ctx)
	if !ok || id == "" {
		return ctx
	}
	return v1.WithSession(ctx, string(id))
}

// ---------- 模型 ----------

// NewLLM 把插件的 LanguageModel 接到 port.LLM。
func NewLLM(m v1.LanguageModel) port.LLM { return llmAdapter{m: m} }

type llmAdapter struct{ m v1.LanguageModel }

func (a llmAdapter) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	s, err := a.m.Generate(toV1Session(ctx), toV1Request(req))
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errNilStream
	}
	return streamAdapter{s: s}, nil
}

func (a llmAdapter) Models(ctx context.Context) ([]port.ModelInfo, error) {
	ms, err := a.m.Models(toV1Session(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]port.ModelInfo, 0, len(ms))
	for _, m := range ms {
		out = append(out, toPortModelInfo(m))
	}
	return out, nil
}

// streamAdapter 插件的流 → 内核的流。转换发生在每一次 Recv，所以这里不许有分配之外的
// 副作用（一个请求一秒能收几十个增量）。
type streamAdapter struct{ s v1.Stream }

func (a streamAdapter) Recv() (port.Delta, error) {
	d, err := a.s.Recv()
	if err != nil {
		// io.EOF 原样返回：内核按它判正常结束，包装一层会认不出来。
		return port.Delta{}, err
	}
	return toPortDelta(d), nil
}

func (a streamAdapter) Close() error { return a.s.Close() }

// errNilStream 插件交回 nil 流且没有错误：契约要求此时恒有 stream，这属于实现缺陷，
// 宿主报明确错误而不是在后面某次 Recv 上空指针崩掉。
var errNilStream = errors.New("plugin returned a nil stream without an error")

// ---------- 工具 ----------

// T1Tool 一个插件工具在内核侧的身份：port.Tool 的实现（Spec + Execute）。
type T1Tool struct {
	p    v1.ToolProvider
	spec tool.Spec
	desc v1.Describer // nil = 插件没实现确认摘要
}

// Tools 已并入工具源（toolset.go，D340）：清单按轮给、名字按会话解析，不能再在这里
// 一次性物化成固定条目。

// Spec 工具声明（含装配期补上的确认摘要）。
func (t T1Tool) Spec() tool.Spec { return t.spec }

// Execute 把裁决后的调用交给插件。错误表示「这次根本没跑起来」；失败结论要在结果里回填。
func (t T1Tool) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	res, err := t.p.Execute(toV1Session(ctx), toV1ToolCall(call))
	if err != nil {
		return tool.Result{}, err
	}
	return toPortToolResult(call.ID, res), nil
}

// ---------- 摄取与输出 ----------

// NewIngestor 插件的摄取器 → 内核端口。
func NewIngestor(p v1.Ingestor) port.Ingestor { return ingestorAdapter{p: p} }

type ingestorAdapter struct{ p v1.Ingestor }

func (a ingestorAdapter) Accepts(raw port.RawInput) bool {
	return a.p.Accepts(toV1RawInput(raw))
}

func (a ingestorAdapter) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	rep, err := a.p.Ingest(toV1Session(ctx), toV1RawInput(raw))
	if err != nil {
		return port.IngestReport{}, err
	}
	return toPortIngestReport(rep), nil
}

// NewOutput 插件的输出镜像 → 内核端口。
func NewOutput(p v1.OutputAdapter) port.OutputAdapter { return outputAdapter{p: p} }

type outputAdapter struct{ p v1.OutputAdapter }

func (a outputAdapter) Name() string { return a.p.Name() }

func (a outputAdapter) Deliver(ctx context.Context, req port.OutputRequest) error {
	return a.p.Deliver(toV1Session(ctx), toV1OutputRequest(req))
}

// ---------- 语音 ----------

// NewTranscriber 插件的 ASR → 内核端口。
func NewTranscriber(p v1.Transcriber) port.Transcriber { return transcriberAdapter{p: p} }

type transcriberAdapter struct{ p v1.Transcriber }

func (a transcriberAdapter) Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error) {
	return a.p.Transcribe(toV1Session(ctx), *toV1BlobRef(&audio), lang)
}

// NewSynthesizer 插件的 TTS → 内核端口。返回的引用落回内核附件库的寻址空间。
func NewSynthesizer(p v1.Synthesizer) port.Synthesizer { return synthesizerAdapter{p: p} }

type synthesizerAdapter struct{ p v1.Synthesizer }

func (a synthesizerAdapter) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	ref, err := a.p.Synthesize(toV1Session(ctx), toV1SpeechRequest(req))
	if err != nil {
		return conversation.BlobRef{}, err
	}
	return *toPortBlobRef(&ref), nil
}

// NewSegmenter 插件的语音分段器 → 内核端口。一个采集流一个实例（插件负责不自相污染）。
func NewSegmenter(p v1.SpeechSegmenter) port.SpeechSegmenter { return segmenterAdapter{p: p} }

type segmenterAdapter struct{ p v1.SpeechSegmenter }

func (a segmenterAdapter) Feed(frame []byte) ([]port.SpeechSegment, error) {
	segs, err := a.p.Feed(frame)
	if err != nil {
		return nil, err
	}
	return toPortSpeechSegments(segs), nil
}

func (a segmenterAdapter) Flush() ([]port.SpeechSegment, error) {
	segs, err := a.p.Flush()
	if err != nil {
		return nil, err
	}
	return toPortSpeechSegments(segs), nil
}

// ---------- 记忆 ----------

// NewMemory 插件的 Memory → 内核的记忆挂载点（D337）。会话身份经 ctx 递进去：装配面
// 按 conversation.ID 调用，这里换成 port 的键再换成 v1 的键（两段都要，插件读 v1 那个）。
func NewMemory(m v1.Memory) port.MemorySource { return memoryAdapter{m: m} }

type memoryAdapter struct{ m v1.Memory }

func (a memoryAdapter) Recall(ctx context.Context, id conversation.ID, q port.MemoryQuery) (port.MemoryBlock, error) {
	if id != "" {
		ctx = port.WithSessionID(ctx, id)
	}
	req := v1.MemoryRequest{BudgetTokens: q.BudgetTokens, ToolsAvailable: q.ToolsAvailable}
	mc, err := a.m.Recall(toV1Session(ctx), req)
	if err != nil {
		return port.MemoryBlock{}, err
	}
	return port.MemoryBlock{Text: mc.Text}, nil
}

// ---------- 密钥 ----------

// NewSecrets 插件的密钥后端 → 内核端口。
func NewSecrets(p v1.SecretProvider) port.Secrets { return secretsAdapter{p: p} }

type secretsAdapter struct{ p v1.SecretProvider }

func (a secretsAdapter) Get(ctx context.Context, name string) (string, error) {
	return a.p.Get(toV1Session(ctx), name)
}

// 编译期断言：接口形状一变，这里先红，不用等到某个装配点。
var (
	_ port.LLM             = llmAdapter{}
	_ port.Stream          = streamAdapter{}
	_ port.Tool            = T1Tool{}
	_ port.Ingestor        = ingestorAdapter{}
	_ port.OutputAdapter   = outputAdapter{}
	_ port.Transcriber     = transcriberAdapter{}
	_ port.Synthesizer     = synthesizerAdapter{}
	_ port.SpeechSegmenter = segmenterAdapter{}
	_ port.Secrets         = secretsAdapter{}
	_ port.MemorySource    = memoryAdapter{}
)
