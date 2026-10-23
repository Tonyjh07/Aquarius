package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/asrchat"
	"github.com/Tonyjh07/Aquarius/internal/adapter/audioplay"
	"github.com/Tonyjh07/Aquarius/internal/adapter/blobfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/decorate"
	"github.com/Tonyjh07/Aquarius/internal/adapter/ingestmic"
	"github.com/Tonyjh07/Aquarius/internal/adapter/jobproc"
	"github.com/Tonyjh07/Aquarius/internal/adapter/llm"
	"github.com/Tonyjh07/Aquarius/internal/adapter/memoryfs"
	"github.com/Tonyjh07/Aquarius/internal/adapter/micwin"
	"github.com/Tonyjh07/Aquarius/internal/adapter/mimotts"
	"github.com/Tonyjh07/Aquarius/internal/adapter/notify"
	"github.com/Tonyjh07/Aquarius/internal/adapter/outputtts"
	"github.com/Tonyjh07/Aquarius/internal/adapter/probe"
	"github.com/Tonyjh07/Aquarius/internal/adapter/screencap"
	"github.com/Tonyjh07/Aquarius/internal/adapter/skillfs"
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
		fmt.Fprintf(w.stderr, "[llm] 服务端不支持参数 %s：已剥离并记入 config providers[%s].unsupported_params，后续不再重试\n", field, providerName)
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

// buildClient 构建 provider 客户端（D173）：base_url/tokenizer 构造期固化
// （port.LLM 不可热换）。密钥池在这里展开：一把钥匙一具 client（有的钥匙可能
// 解析失败——那种钥匙不进池，启动打一行警告而不是整体失败），多把时用 KeyPool
// 装饰器串起来（重试罩在它外面，见 stackDecorators）。
func (w *wiring) buildClient(p *providerConfig) (port.LLM, port.TokenCounter, error) {
	refs := p.keyRefs()
	keys := make([]decorate.KeyEntry, 0, len(refs))
	var counter port.TokenCounter
	var firstErr error
	for i, ref := range refs {
		key, err := resolveAPIKey(context.Background(), p.Name, ref, w.secrets, w.stderr)
		if err != nil {
			// 单把解析失败不打断启动：还有别的钥匙可用（D141 的「任一把可用即算配置完整」
			// 同一口径）。一把都没有才算失败，用第一条错误报因。
			if firstErr == nil {
				firstErr = err
			}
			fmt.Fprintf(w.stderr, "[llm] provider %q 第 %d 把密钥不可用，本次启动跳过它：%v\n",
				p.Name, i+1, err)
			continue
		}
		c, cnt, cerr := w.newLLMClient(p, key, w.noteUnsupportedFor(p.Name))
		if cerr != nil {
			return nil, nil, cerr
		}
		if counter == nil {
			counter = cnt
		}
		keys = append(keys, decorate.KeyEntry{Label: strings.TrimSpace(p.keyLabelAt(i)), LLM: c})
	}
	if len(keys) == 0 {
		if firstErr != nil {
			return nil, nil, firstErr
		}
		return nil, nil, fmt.Errorf("provider %q 没有可用的密钥", p.Name)
	}
	if len(keys) == 1 {
		return keys[0].LLM, counter, nil // 没有别的钥匙可换，轮换层没有存在意义
	}
	// 提示走 presentener 扇出：presenter 晚于 buildLLMs 才装配，故用惰性回调
	// （调用时才读 w.presenter），装配顺序不成为约束。
	pool := decorate.NewKeyPool(keys).
		OnRotate(func(ctx context.Context, from, to string) {
			w.emitNotice(ctx, fmt.Sprintf("密钥 %s 被限流或额度耗尽，本轮改用 %s", from, to))
		}).
		OnExhaust(func(ctx context.Context, until time.Time) {
			w.emitNotice(ctx, fmt.Sprintf("%s 的密钥都在冷却，最早 %s 之后恢复", p.Name, until.Format("15:04:05")))
		})
	return pool, counter, nil
}

// emitNotice 发一条面向用户的提示（presenter 未就绪时静默——装配早期没有界面可发）。
func (w *wiring) emitNotice(ctx context.Context, text string) {
	if w.presenter == nil {
		return
	}
	_ = w.presenter.Emit(ctx, port.NoticeEvent{Text: text})
}

// newLLMClient 建客户端本体（协议分派的唯一落点，D175 口径一）：config 里的 `kind`
// 词表与适配器的 `llm.Protocol` 是同一套，这里做一次映射就够，两处不会再各定义一遍。
// key 由调用方解析（密钥池逐把解析后再进来）。note 为 nil = 不记录服务端不认的参数
// （能力探测用：探测刻意发可能不被支持的参数，不该把结论记成正式请求的剥离清单）。
func (w *wiring) newLLMClient(p *providerConfig, key string, note func(string)) (*llm.Client, port.TokenCounter, error) {
	c, cerr := llm.New(llm.Config{
		BaseURL: p.BaseURL, APIKey: key, Tokenizer: p.Tokenizer,
		Protocol:          llmProtocol(p.protocolKind()),
		UnsupportedParams: p.UnsupportedParams,
		NoteUnsupported:   note,
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

// llmProtocol config 的 kind → 适配器协议标识（D175）。认得的词在这里一一对上；
// 不认得的词原样传给适配器，由它统一 fail-fast 报因——启动时的配置错误该由校验拦下
// （config.normalize 已拦），这里是第二道保险。
func llmProtocol(kind string) llm.Protocol {
	switch kind {
	case kindOpenAIResponses:
		return llm.ProtoResponses
	case kindAnthropic:
		return llm.ProtoAnthropic
	default:
		return llm.ProtoChat
	}
}

// buildLLMs 为 primary 与 fallback[] 各建一具客户端（含 fallback——避免降级时才
// 发现缺配置），填好降级链条目与逐 provider 计数器表。
func (w *wiring) buildLLMs() error {
	client, primaryCounter, err := w.buildClient(w.primary)
	if err != nil {
		return err
	}
	w.client = client
	// 降级链条目（D173）：Model 空 = 透传 req.Model（primary——agent 每轮注入当前
	// 模型，热切换不失效）；降级目标 = 该项声明的模型，省略时取该 provider 的 models[0]。
	w.fallbackEntries = []decorate.FallbackEntry{{Name: w.primary.Name, LLM: client}}
	w.fallbackCounters = map[string]port.TokenCounter{w.primary.Name: primaryCounter}
	for _, fb := range w.cfg.Model.Fallback {
		p := w.cfg.Model.byName(fb.Provider)
		if p == nil {
			return fmt.Errorf("model.fallback 引用的 provider %q 不存在", fb.Provider)
		}
		c, counter, ferr := w.buildClient(p)
		if ferr != nil {
			return ferr
		}
		target := fb.Model
		if target == "" {
			target = p.models0()
		}
		// D204：降级目标带自己那份合并后的采样参数（model.sampling 叠该模型的覆盖），
		// 由降级装饰器在改写模型名时一并换上——模型名在装饰器里才确定，参数得跟着走。
		sampling := w.cfg.Model.samplingFor(fb.Provider, target)
		w.fallbackEntries = append(w.fallbackEntries, decorate.FallbackEntry{
			Name: fb.Provider, LLM: c, Model: target, Sampling: &sampling,
		})
		w.fallbackCounters[fb.Provider] = counter
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
	if w.cfg.Output.TTS && w.ttsSynth != nil {
		out, err := outputtts.New(outputtts.Config{
			Synth:  w.ttsSynth,
			Player: w.player,
			Blobs:  w.blobs,
			Voice:  w.cfg.Audio.TTS.Voice,
			Design: w.cfg.Audio.TTS.Design,
			Style:  w.cfg.Audio.TTS.Style,
			Tags:   w.cfg.Audio.TTS.Tags,
			Logf: func(format string, args ...any) {
				if w.stderr != nil {
					fmt.Fprintf(w.stderr, format+"\n", args...)
				}
			},
		})
		if err != nil {
			fmt.Fprintf(w.stderr, "警告：语音播报不可用：%v\n", err)
		} else {
			w.ttsOut = out
			outs = append(outs, out)
		}
	}
	w.presenter = &outputsPresenter{
		inner: w.ui,
		outs:  outs,
		log:   func(err error) { fmt.Fprintf(w.stderr, "[输出器] %v\n", err) },
	}
}

// assembleAudio 装配语音端口（D179）：录音/播放恒在（未配置时执行报因），识别与
// 合成按 audio.* 引用的 provider 现建。引用的 provider 不存在只让该能力不可用——
// 启动时已提示，不阻断对话。
func (w *wiring) assembleAudio(ctx context.Context) {
	w.recorder = micwin.New()
	w.player = audioplay.New()

	if p, why := w.cfg.audioProvider("asr"); p != nil {
		key, err := p.resolveKeyAt(ctx, 0, w.secrets, w.stderr)
		if err != nil {
			fmt.Fprintf(w.stderr, "警告：语音识别不可用：%v\n", err)
		} else if c, err := asrchat.New(asrchat.Config{
			BaseURL: p.BaseURL, APIKey: key, Model: w.cfg.Audio.ASR.Model, Blobs: w.blobs,
		}); err != nil {
			fmt.Fprintf(w.stderr, "警告：语音识别不可用：%v\n", err)
		} else {
			w.asr = c
		}
	} else if w.cfg.Audio.ASR.Provider != "" {
		fmt.Fprintf(w.stderr, "警告：语音识别不可用：%s\n", why)
	}

	if p, why := w.cfg.audioProvider("tts"); p != nil {
		key, err := p.resolveKeyAt(ctx, 0, w.secrets, w.stderr)
		if err != nil {
			fmt.Fprintf(w.stderr, "警告：语音合成不可用：%v\n", err)
		} else if c, err := mimotts.New(mimotts.Config{
			BaseURL: p.BaseURL, APIKey: key, Model: w.cfg.Audio.TTS.Model, Blobs: w.blobs,
		}); err != nil {
			fmt.Fprintf(w.stderr, "警告：语音合成不可用：%v\n", err)
		} else {
			w.ttsSynth = c
		}
	} else if w.cfg.Audio.TTS.Provider != "" {
		fmt.Fprintf(w.stderr, "警告：语音合成不可用：%s\n", why)
	}

	// 语音键与 /mic 共用同一摄取器：录 → 附件库 → 转写。
	w.mic = ingestmic.New(w.recorder, w.blobs, w.asr, w.cfg.Input.Language)
}

// closeAudio 收尾：停播放并清临时文件。
func (w *wiring) closeAudio() {
	if w.ttsOut != nil {
		w.ttsOut.Close()
	}
	w.silence()
}

// silence 停掉正在播放的语音（停止键与退出共用）。
func (w *wiring) silence() {
	if w.player != nil {
		w.player.Stop()
	}
}

// assembleTools 装配内置工具 + ToolRunner 门面（D25：查找/权限判定/确认/超时/裁剪）。
// 后台任务（DESIGN §5.8/D8）：日志落盘 <profile>/jobs，任务表内存态。
func (w *wiring) assembleTools(ctx context.Context, confirmer port.Confirmer, asker port.Asker) error {
	jobs, err := jobproc.New(filepath.Join(w.profileDir, "jobs"))
	if err != nil {
		return err
	}
	// D189：任务收尾观察者阻塞在 JobManager.Wait 上（完成事件，不轮询），终态注入 + 唤醒；
	// 注入端口延迟取（会话在工具装配之后才建好），Wake 走 UI 的空闲唤醒通道。
	w.jobWatch = app.NewJobWatcher(app.JobWatcherOptions{
		Jobs: jobs,
		Injector: func() port.Injector {
			if s := w.sessPtr.Load(); s != nil {
				return s
			}
			return nil
		},
		Wake: func() {
			if w.ui != nil {
				w.ui.Wake()
			}
		},
		Logf: func(format string, args ...any) {
			if w.stderr != nil {
				fmt.Fprintf(w.stderr, format+"\n", args...)
			}
		},
	})
	w.jobs = app.NewWatchingJobs(jobs, w.jobWatch)
	// D190：观察器与任务收尾是两件事——任务有终态（等完成事件），观察器盯不确定的
	// 外部状态（按模型给的节奏取样）。探针走端口，注入与唤醒复用同一条通道。
	w.watcher = app.NewWatcher(app.WatcherOptions{
		Prober: probe.New(),
		Injector: func() port.Injector {
			if s := w.sessPtr.Load(); s != nil {
				return s
			}
			return nil
		},
		Wake: func() {
			if w.ui != nil {
				w.ui.Wake()
			}
		},
		Logf: func(format string, args ...any) {
			if w.stderr != nil {
				fmt.Fprintf(w.stderr, format+"\n", args...)
			}
		},
	})
	toolTimeout := time.Duration(w.cfg.Limits.ToolTimeoutSec) * time.Second
	if w.cfg.Limits.ToolTimeoutSec <= 0 {
		toolTimeout = 60 * time.Second
	}
	w.toolTimeout = toolTimeout
	maxOutput := w.cfg.Limits.ToolOutputChars
	if maxOutput <= 0 {
		maxOutput = 20000
	}
	// D197：联网能力按配置顺序装配（缺密钥只警告并跳过该家）；未配置 = nil，工具
	// 照常注册但执行时明确报因。
	webSearch, webFetch := w.buildWeb(ctx)
	// D201：技能清单（config 追加根在前，<profile>/skills 恒垫底）。坏技能只记警告，
	// 不阻断启动——先扫一次，把跳过的条目打到 stderr（/skill 里也能再看）。
	skills := skillfs.New(skillfs.Options{Dirs: append(append([]string{}, w.cfg.Skills.Dirs...),
		filepath.Join(w.profileDir, "skills"))})
	for _, warn := range skills.Warnings(ctx) {
		fmt.Fprintf(w.stderr, "技能：%s\n", warn)
	}
	w.skills = skills
	// D202：安装面内核（副本落 `<profile>/skills`、git 检出进 `skill-cache`、台账
	// `skill-installs.json`）。装配根在 cmd/aquarius/skillsadmin.go 把它适配成
	// uigui.SkillsAdmin / app.SkillAdmin 两套消费方接口。
	installer, err := skillfs.NewInstaller(skillfs.InstallOptions{
		Root:       filepath.Join(w.profileDir, "skills"),
		CacheDir:   filepath.Join(w.profileDir, "skill-cache"),
		LedgerPath: filepath.Join(w.profileDir, "skill-installs.json"),
		Catalog:    skills,
		Logf:       func(format string, args ...any) { fmt.Fprintf(w.stderr, "技能："+format+"\n", args...) },
	})
	if err != nil {
		return fmt.Errorf("技能安装面: %w", err)
	}
	w.skillInstall = installer
	builtinTools := toolbuiltin.New(toolbuiltin.Deps{
		Memory: w.merged,
		PathOf: func(name string) (string, bool) {
			p, err := w.mem.Path(name)
			return p, err == nil
		},
		Jobs:        w.jobs,
		Sandbox:     w.sandboxDir,
		Blobs:       w.blobs,
		Vision:      w.cfg.Model.chainCaps().Vision,
		Capture:     screencap.New(),
		Asker:       asker,
		Watches:     w.watcher,
		WebSearch:   webSearch,
		WebFetch:    webFetch,
		WebCount:    w.cfg.Web.Search.Count,
		WebMaxChars: w.cfg.Web.Fetch.MaxChars,
		Skills:      skills, // D201：skill 工具（nil 时工具明确报「本构建未提供」）
	}) // S6/D143：内置工具禁用集（config tools.disabled 推导；启动期定值，运行期经
	// toolsAdmin.SetBuiltinEnabled 热更——不再装配期摘 think）。
	w.disabledTools = newDisabledHolder(w.builtinDisabled())
	w.runner = toolrun.New(toolrun.Options{
		Tools:       builtinTools,
		Disabled:    w.disabledTools.snapshot(),
		Confirmer:   confirmer,
		Level:       w.lvl.Get,
		SandboxPath: w.sandboxDir,
		Timeout:     toolTimeout,
		MaxOutput:   maxOutput,
	})
	return nil
}

// maxContextBudget 装配与截断共用的上下文预算（D177）：min(当前模型窗口, 全局上限)。
// 压缩阈值（Agent）、/usage 占用、装饰器硬保底截断三处必须同源——各算各的就会出现
// 「界面说还有空间，发出去却被服务端拒」这种最难查的分叉。全局值 <= 0 时用内置缺省。
func (w *wiring) maxContextBudget() int {
	global := w.cfg.Limits.MaxContextTokens
	if global <= 0 {
		global = app.DefaultMaxContextTokens
	}
	return w.cfg.Model.effectiveMaxContext(global)
}

// contextBudget /model 展示用的预算口径（D177）：生效值 + 这个值是谁决定的。
func (w *wiring) contextBudget() app.ContextBudget {
	global := w.cfg.Limits.MaxContextTokens
	if global <= 0 {
		global = app.DefaultMaxContextTokens
	}
	return app.ContextBudget{
		Effective:   w.cfg.Model.effectiveMaxContext(global),
		ModelWindow: w.cfg.Model.currentContextWindow(),
		GlobalLimit: global,
	}
}

// listModels /model 无参的可用清单：服务端名字表 + 配置里的能力位/窗口（D177）。
func (w *wiring) listModels(ctx context.Context) ([]port.ModelInfo, error) {
	infos, err := w.client.Models(ctx)
	if err != nil {
		return nil, err
	}
	return w.cfg.Model.enrichModels(infos), nil
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
	maxCtx := w.maxContextBudget()
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
		// D205：费用折算贴在最内（最靠近适配器）——只加工流末的 Usage，不改请求；放在
		// 降级之内，改写后的模型名才能取到正确单价。单价按**本条链自己那家 provider**
		// 查：不同 provider 可能给同名模型标不同价。
		provider := e.Name
		price := func(model string) (float64, float64) {
			return w.cfg.Model.costFor(provider, model)
		}
		inner = decorate.NewCost(inner, price)
		inner = decorate.NewTruncate(inner, fitFor(w.fallbackCounters[e.Name]), truncateNotice)
		inner = decorate.AuditLLM(inner, audit)
		inner = decorate.NewRetry(inner)
		stacked = append(stacked, decorate.FallbackEntry{Name: e.Name, LLM: inner, Model: e.Model, Sampling: e.Sampling})
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
