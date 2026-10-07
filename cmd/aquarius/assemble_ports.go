package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/jobproc"
	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/notify"
	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolbuiltin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/toolrun"
	"github.com/Tonyjh07/Aquarius/internal/app"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// assembleStores 装配存储面：会话树 store、附件库与记忆（全部落 profile 内，
// D110 修订①）。
func (w *wiring) assembleStores(ctx context.Context) error {
	store, err := storejson.New(filepath.Join(w.profileDir, "conversations"))
	if err != nil {
		return err
	}
	w.store = store
	// 附件库（DESIGN §4.2）：sha256 内容寻址存 <profile>/attachments（随 profile
	// 隔离，D110 修订①）；启动 GC 按全量会话引用清扫（引用收集不完整则跳过，宁可漏清不误删）。
	blobs, err := blobfs.New(filepath.Join(w.profileDir, "attachments"))
	if err != nil {
		return err
	}
	w.blobs = blobs
	runAttachmentGC(ctx, w.store, blobs, w.stderr)
	// 记忆（D23 布局）：全局 memories.md + 会话 <id>.memory.md（与会话树同目录）。
	mem, err := memoryfs.New(filepath.Join(w.profileDir, port.GlobalMemoryDoc), filepath.Join(w.profileDir, "conversations"))
	if err != nil {
		return err
	}
	w.mem = mem
	// 合并记忆（§6.3）：主存储 + 插件 resources 只读投影；宿主建好前 extras 为空。
	// 写/删与 /memory 编辑器仍走主存储（mem.Path 是 memoryfs 专有面）。
	w.merged = &mergedMemory{
		primary: mem,
		extras: func() []port.MemoryStore {
			if w.host == nil {
				return nil
			}
			ready := w.host.Ready()
			out := make([]port.MemoryStore, 0, len(ready))
			for _, name := range sortedStoreNames(ready) {
				out = append(out, ready[name].Memory())
			}
			return out
		},
		logf:    func(format string, args ...any) { fmt.Fprintf(w.stderr, format+"\n", args...) },
		timeout: 5 * time.Second, // 插件投影单次上限（卡死的插件不得拖垮 Turn）
	}
	return nil
}

// noteUnsupportedFor 返回服务端不认参数的记录回调（D34/D110②）：LLM 适配器剥离
// 重试成功后写回 config providers[<name>].unsupported_params（此后启动直接省略）；
// 写回失败只记日志（下次仍会剥离）。按 provider 名定位条目——fallback（D110②）
// 会按 provider 各建 client，回调须记到产生剥离的那一条。
func (w *wiring) noteUnsupportedFor(providerName string) func(string) {
	return func(field string) {
		fmt.Fprintf(w.stderr, "[llm] 服务端不支持参数 %s：已剥离重试，记入 config providers[%s].unsupported_params（D34）\n", field, providerName)
		if err := persistConfig(w.cfgPath, func(generic map[string]any) {
			p := providerSection(generic, providerName)
			if p == nil {
				return // providers 结构被外部改动：放弃记录，下次启动仍会剥离
			}
			list, _ := p["unsupported_params"].([]any)
			for _, v := range list {
				if v == field {
					return // 已记录
				}
			}
			p["unsupported_params"] = append(list, field)
		}); err != nil {
			fmt.Fprintf(w.stderr, "[llm] 记录 unsupported_params 失败: %v（下次启动仍会先试发该字段）\n", err)
		}
	}
}

// buildClient 构建 provider 客户端（D110②/修订③）：base_url/api_key/tokenizer
// 构造期固化（port.LLM 不可热换）。密钥逐 provider 解析（D35）。
func (w *wiring) buildClient(p *providerConfig) (*llm.Client, port.TokenCounter, error) {
	key, kerr := resolveAPIKey(context.Background(), p.Name, p.APIKey, w.stderr)
	if kerr != nil {
		return nil, nil, kerr
	}
	c, cerr := llm.New(llm.Config{
		BaseURL: p.BaseURL, APIKey: key, Tokenizer: p.Tokenizer,
		UnsupportedParams: p.UnsupportedParams,
		NoteUnsupported:   w.noteUnsupportedFor(p.Name),
	})
	if cerr != nil {
		return nil, nil, cerr
	}
	var counter port.TokenCounter
	if cc, ok := any(c).(port.TokenCounter); ok {
		counter = cc
	}
	return c, counter, nil
}

// buildLLMs 为 primary 与 fallback[] 各建一具客户端（含 fallback——避免降级时才
// 发现缺配置），填好降级链条目与逐 provider 计数器表。
func (w *wiring) buildLLMs() error {
	client, primaryCounter, err := w.buildClient(w.primary)
	if err != nil {
		return err
	}
	w.client = client
	// 降级链条目（D110 修订③）：Model 空 = 透传 req.Model（primary——agent 每轮
	// 注入当前模型，热切换不失效）；降级目标 = 其 models[0]。
	w.fallbackEntries = []decorate.FallbackEntry{{Name: w.primary.Name, LLM: client}}
	w.fallbackCounters = map[string]port.TokenCounter{w.primary.Name: primaryCounter}
	for _, fbName := range w.cfg.Model.Fallback {
		p := w.cfg.Model.byName(fbName)
		c, counter, ferr := w.buildClient(p)
		if ferr != nil {
			return ferr
		}
		w.fallbackEntries = append(w.fallbackEntries, decorate.FallbackEntry{
			Name: fbName, LLM: c, Model: p.Models[0],
		})
		w.fallbackCounters[fbName] = counter
	}
	return nil
}

// buildPresenter 装配输出器扇出（D28/D14）：Presenter 装饰器包住实际 UI，仅
// output.notify 开启时装配；app 与 repl 均不感知输出器，Deliver 失败只记日志（§5.7）。
func (w *wiring) buildPresenter() {
	var outs []port.OutputAdapter
	if w.cfg.Output.Notify {
		outs = append(outs, notify.New(notifySenderImpl(runtime.GOOS)))
	}
	w.presenter = &outputsPresenter{
		inner: w.ui,
		outs:  outs,
		log:   func(err error) { fmt.Fprintf(w.stderr, "[输出器] %v\n", err) },
	}
}

// assembleTools 装配内置工具 + ToolRunner 门面（D25：查找/权限判定/确认/超时/裁剪）。
// 后台任务（DESIGN §5.8/D8）：日志落盘 <profile>/jobs，任务表内存态。
func (w *wiring) assembleTools(confirmer port.Confirmer) error {
	jobs, err := jobproc.New(filepath.Join(w.profileDir, "jobs"))
	if err != nil {
		return err
	}
	w.jobs = jobs
	toolTimeout := time.Duration(w.cfg.Limits.ToolTimeoutSec) * time.Second
	if w.cfg.Limits.ToolTimeoutSec <= 0 {
		toolTimeout = 60 * time.Second
	}
	w.toolTimeout = toolTimeout
	maxOutput := w.cfg.Limits.ToolOutputChars
	if maxOutput <= 0 {
		maxOutput = 20000
	}
	builtinTools := toolbuiltin.New(w.merged, func(name string) (string, bool) {
		p, err := w.mem.Path(name)
		return p, err == nil
	}, jobs, w.sandboxDir)
	// think 草稿工具可见性（D34）：默认隐藏（model.think_tool 缺省 false），
	// 配置启用后重启生效——装配期摘除，不做能力探测、运行时零开销。
	if !w.cfg.Model.ThinkTool {
		builtinTools = dropTool(builtinTools, "think")
	}
	w.runner = toolrun.New(toolrun.Options{
		Tools:       builtinTools,
		Confirmer:   confirmer,
		Level:       w.lvl.Get,
		SandboxPath: w.sandboxDir,
		Timeout:     toolTimeout,
		MaxOutput:   maxOutput,
	})
	return nil
}

// stackDecorators 叠横切装饰器链（D14/§10，装配根叠加，铁律 9）：审计记每次真实
// 调用，重试包在审计之外（一次用户可见的 Generate = 多条审计行），硬保底截断最内
// （发给服务端前裁到预算内）；跨 provider 降级（D110②/Q5/修订③）罩全体最外——
// 重试在内、降级在外。审计句柄存 wiring（run 据此登记退出时关闭）。
func (w *wiring) stackDecorators(ctx context.Context) (port.LLM, error) {
	audit, err := decorate.NewAudit(filepath.Join(w.profileDir, "audit.log"), 0)
	if err != nil {
		return nil, err
	}
	w.audit = audit
	maxCtx := w.cfg.Limits.MaxContextTokens
	if maxCtx <= 0 {
		maxCtx = app.DefaultMaxContextTokens
	}
	// fit 闭包工厂：逐 provider 用各自 tokenizer 计数（fallback client 可配不同
	// tokenizer；est 链恒取 primary）。
	fitFor := func(cnt port.TokenCounter) func(context.Context, port.GenerateRequest) (port.GenerateRequest, int) {
		return func(fctx context.Context, req port.GenerateRequest) (port.GenerateRequest, int) {
			reserve := req.Budget.MaxOutputTokens
			if reserve < minOutputReserve {
				reserve = minOutputReserve
			}
			kept, omitted := app.TrimOldest(fctx, cnt, req.Messages, maxCtx-reserve, req.Tools...)
			req.Messages = kept
			return req, omitted
		}
	}
	truncateNotice := func(omitted int) {
		_ = w.presenter.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("硬保底截断：上下文超预算，已省略 %d 条最旧消息", omitted),
		})
	}
	// 每 provider 一条内链：硬保底截断最内（发给服务端前裁到预算内）→ 审计（记每次
	// 真实调用）→ 同 provider 重试（外）；跨 provider 降级（D110②/Q5/修订③）罩全体
	// 最外——重试在内、降级在外（铁律 9：装饰器只在装配根叠加）。
	stacked := make([]decorate.FallbackEntry, 0, len(w.fallbackEntries))
	for _, e := range w.fallbackEntries {
		var inner port.LLM = e.LLM
		inner = decorate.NewTruncate(inner, fitFor(w.fallbackCounters[e.Name]), truncateNotice)
		inner = decorate.AuditLLM(inner, audit)
		inner = decorate.NewRetry(inner)
		stacked = append(stacked, decorate.FallbackEntry{Name: e.Name, LLM: inner, Model: e.Model})
	}
	var gen port.LLM = stacked[0].LLM
	if len(stacked) > 1 {
		gen = decorate.NewFallback(stacked, func(from, to string) {
			_ = w.presenter.Emit(ctx, port.NoticeEvent{
				Text: fmt.Sprintf("模型服务 %s 网络类错误，本轮已降级到 %s（Q5：状态行一次性提示）", from, to),
			})
		})
	}
	return gen, nil
}
