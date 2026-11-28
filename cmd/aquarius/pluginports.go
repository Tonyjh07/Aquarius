package main

// 插件供给面的消费侧容器（D356）：六个面里除 output（扇出直挂 outputsPresenter）外的
// 五个各有一个「装配期插入、启用期换内里」的路由容器——装配根把容器插进既有链路，
// 插件启停只换容器内里，链路本身不重建。
//
//   - llmRoute：按请求模型名路由到插件模型，没命中回落内置主链；Models 并集。作为
//     primary 链的最内层插入（stackDecorators 的截断/审计/重试对插件模型照常生效）。
//   - ingestRoute：插件摄取器集合，排在内置摄取链之后（内置优先，D356）。
//   - transcribeRoute / synthesizeRoute：语音两槽位，启用即接管（Store 插件实现），
//     停用还原内置选型（Store 回原值）。
//   - secretChain.extra：密钥读链的插件兜底档（bin → env → extra），见 secrets.go。

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ---------- llmRoute：插件模型按名路由（链中一环） ----------

// llmRoute port.LLM 的插件路由层：primary 链的最内一环。请求模型名命中插件模型 →
// 走插件；否则回落内置主链。命中与否对上层不可见——截断/审计/重试照常包在外面，
// 降级链也照常工作（插件 Generate 报瞬时错误同样落到下一条降级链）。
type llmRoute struct {
	mu       sync.RWMutex
	inner    port.LLM            // 内置主链裸客户端（未命中模型名时回落）
	byMdl    map[string]port.LLM // 模型名 → 插件适配器（并集路由表）
	owners   map[string][]string // 插件名 → 它注册过的模型名（摘除用）
	adapters map[string]port.LLM // 插件名 → 适配器（Models 并集遍历用）
	order    []string            // 插件启用序（Models 并集的稳定顺序）
}

func newLLMRoute(inner port.LLM) *llmRoute {
	return &llmRoute{inner: inner, byMdl: map[string]port.LLM{},
		owners: map[string][]string{}, adapters: map[string]port.LLM{}}
}

var _ port.LLM = (*llmRoute)(nil)

// attach 注册一个插件模型的全部名字；同名后到覆盖先到（后启用的插件赢，与语音
// 槽位的「第一个赢」相反——这里是名字冲突，后到者顶掉前者才能被 /model 选到）。
func (r *llmRoute) attach(plugin string, l port.LLM, models []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detachLocked(plugin)
	for _, m := range models {
		r.byMdl[m] = l
	}
	r.owners[plugin] = models
	r.adapters[plugin] = l
	r.order = append(r.order, plugin)
}

// detachLocked 摘掉一个插件注册的全部模型名（调用方持锁）。
func (r *llmRoute) detachLocked(plugin string) {
	models, ok := r.owners[plugin]
	if !ok {
		return
	}
	for _, m := range models {
		delete(r.byMdl, m)
	}
	delete(r.owners, plugin)
	delete(r.adapters, plugin)
	if i := slices.Index(r.order, plugin); i >= 0 {
		r.order = slices.Delete(r.order, i, i+1)
	}
}

func (r *llmRoute) detach(plugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.detachLocked(plugin)
}

func (r *llmRoute) Generate(ctx context.Context, req port.GenerateRequest) (port.Stream, error) {
	r.mu.RLock()
	inner, hit := r.byMdl[req.Model]
	r.mu.RUnlock()
	if hit {
		return inner.Generate(ctx, req)
	}
	return r.inner.Generate(ctx, req)
}

// Models 内置清单在前、插件模型按启用序追加在后（同名去重）。
func (r *llmRoute) Models(ctx context.Context) ([]port.ModelInfo, error) {
	base, err := r.inner.Models(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := slices.Clone(base)
	seen := map[string]bool{}
	for _, m := range base {
		seen[m.Name] = true
	}
	for _, p := range r.order {
		l := r.adapters[p]
		if l == nil {
			continue
		}
		ms, merr := l.Models(ctx)
		if merr != nil {
			continue // 插件清单拉不到不影响内置清单
		}
		for _, m := range ms {
			if !seen[m.Name] {
				seen[m.Name] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// ---------- ingestRoute：插件摄取器集合（内置优先，排尾位） ----------

// ingestRoute port.Ingestor 的插件聚合：Accepts = 任一插件认；Ingest 分派首个认的。
// 容器自身排在内置摄取链之后（SessionDeps.Ingestors 尾位），内置 kind 永远先被内置接走。
type ingestRoute struct {
	mu    sync.RWMutex
	byP   map[string]port.Ingestor
	order []string // 启用序（稳定分派顺序）
}

func newIngestRoute() *ingestRoute {
	return &ingestRoute{byP: map[string]port.Ingestor{}}
}

var _ port.Ingestor = (*ingestRoute)(nil)

func (r *ingestRoute) put(plugin string, ing port.Ingestor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byP[plugin]; !ok {
		r.order = append(r.order, plugin)
	}
	r.byP[plugin] = ing
}

func (r *ingestRoute) remove(plugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byP, plugin)
	if i := slices.Index(r.order, plugin); i >= 0 {
		r.order = slices.Delete(r.order, i, i+1)
	}
}

func (r *ingestRoute) Accepts(raw port.RawInput) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.order {
		if r.byP[p].Accepts(raw) {
			return true
		}
	}
	return false
}

func (r *ingestRoute) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.order {
		ing := r.byP[p]
		if ing.Accepts(raw) {
			return ing.Ingest(ctx, raw)
		}
	}
	return port.IngestReport{}, errors.New("ingestRoute: no plugin accepts the input")
}

// pluginIngest 摄取集合的取用口（nil = 现建）：sessionDeps 与 setTierOne 共用同一实例。
func (w *wiring) pluginIngest() *ingestRoute {
	if w.ingRoute == nil {
		w.ingRoute = newIngestRoute()
	}
	return w.ingRoute
}

// ---------- transcribeRoute / synthesizeRoute：语音两槽位（启用即接管） ----------

// transcribeRoute 识别槽位：装配期存内置选型，插件启用时 Store 插件实现、停用时
// Store 回内置值。持有者（ingestmic、voiceSlot）只见容器，接管与还原对它们透明。
type transcribeRoute struct {
	mu      sync.Mutex
	builtin port.Transcriber // 内置选型（可为 nil = 本来就没配，停用还原即清空）
	cur     port.Transcriber // 当前生效（接管 = 插件；否则 = builtin）
	owner   string           // 接管者插件名（"" = 无）
}

func newTranscribeRoute(builtin port.Transcriber) *transcribeRoute {
	return &transcribeRoute{builtin: builtin, cur: builtin}
}

var _ port.Transcriber = (*transcribeRoute)(nil)

// setBuiltin 装配期填内置选型（assembleAudio 建出来后再挂；nil = 内置没配）。
func (r *transcribeRoute) setBuiltin(t port.Transcriber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builtin = t
	if r.owner == "" {
		r.cur = t
	}
}

// builtinWired 内置是否配置过：没配时接管方还要负责把槽位消费者（w.asr）指到容器，
// 停用还原时指回 nil（语音键显隐随 w.asr 是否为 nil）。
func (r *transcribeRoute) builtinWired() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.builtin != nil
}

// take 接管；已有接管者时返回其名（调用方 warn 后不挂），否则记名生效。
func (r *transcribeRoute) take(plugin string, t port.Transcriber) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != "" {
		return r.owner
	}
	r.owner, r.cur = plugin, t
	return ""
}

func (r *transcribeRoute) release(plugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == plugin {
		r.owner, r.cur = "", r.builtin
	}
}

func (r *transcribeRoute) Transcribe(ctx context.Context, blob conversation.BlobRef, lang string) (string, error) {
	r.mu.Lock()
	t := r.cur
	r.mu.Unlock()
	if t == nil {
		return "", errors.New("transcribeRoute: no transcriber wired")
	}
	return t.Transcribe(ctx, blob, lang)
}

// synthesizeRoute 合成槽位：同 transcribeRoute，槽位消费者是 outputtts.Adapter。
type synthesizeRoute struct {
	mu      sync.Mutex
	builtin port.Synthesizer
	cur     port.Synthesizer
	owner   string
}

func newSynthesizeRoute(builtin port.Synthesizer) *synthesizeRoute {
	return &synthesizeRoute{builtin: builtin, cur: builtin}
}

var _ port.Synthesizer = (*synthesizeRoute)(nil)

func (r *synthesizeRoute) setBuiltin(s port.Synthesizer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builtin = s
	if r.owner == "" {
		r.cur = s
	}
}

func (r *synthesizeRoute) builtinWired() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.builtin != nil
}

func (r *synthesizeRoute) take(plugin string, s port.Synthesizer) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != "" {
		return r.owner
	}
	r.owner, r.cur = plugin, s
	return ""
}

func (r *synthesizeRoute) release(plugin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == plugin {
		r.owner, r.cur = "", r.builtin
	}
}

func (r *synthesizeRoute) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	r.mu.Lock()
	s := r.cur
	r.mu.Unlock()
	if s == nil {
		return conversation.BlobRef{}, errors.New("synthesizeRoute: no synthesizer wired")
	}
	return s.Synthesize(ctx, req)
}
