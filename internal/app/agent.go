// Package app 应用层：Turn 循环（唯一的编排）、上下文装配、会话命令（DESIGN §7）。
//
// 依赖方向：adapter → port ← app → domain。app 只依赖 port 与 domain，
// 具体适配器（LLM/存储/UI）由装配根 cmd/aquarius 注入。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrMaxTurns 单次 Run 的"生成 + 工具"循环次数用尽（DESIGN §7.1）。
var ErrMaxTurns = errors.New("agent: 超过最大轮次数")

// ErrNothingToCompact /compact 无可压缩历史：摘要之上没有新增内容（D21）。
var ErrNothingToCompact = errors.New("agent: 没有可压缩的历史")

// defaultMaxTurns Config.MaxTurns <= 0 时的默认值（DESIGN §8 limits.max_turns）。
const defaultMaxTurns = 8

// Deps Agent 依赖（全部端口，测试注入替身）。
type Deps struct {
	LLM   port.LLM
	UI    port.Presenter
	IDs   port.IDGen
	Clock port.Clock
	// Tools 为 nil = 无工具（M0）；Blobs 为 nil = 图片以内联占位代替（附件库 M3）。
	Tools port.ToolRunner
	Blobs port.AttachmentStore
	// Memory 为 nil = 不注入记忆索引与会话记忆（M2，DESIGN §7.1 承载）。
	Memory port.MemoryStore
}

// Config Agent 行为配置。
type Config struct {
	Model    string
	System   string // 空 = 内置默认 system 提示
	Sampling port.Sampling
	Budget   port.TokenBudget
	MaxTurns int // <=0 = 8
	// CompactThreshold 自动压缩阈值系数（×MaxContextTokens；<=0 = 0.7，D21 轨2/M2）。
	CompactThreshold float64
	// MaxContextTokens 上下文预算 tokens（<=0 = 64000，DESIGN §8）。
	MaxContextTokens int
	// Think 原生思考三态开关（D34）：nil = 键缺失（展示为"开"，但**不发**
	// enable_thinking 布尔，保持零字段变化）；true/false = 显式开关（/think 写回后生效）。
	Think *bool
	// ReasoningEffort 推理档位初值（D34；空 = 不发送）。/effort 热切换取代。
	ReasoningEffort string
}

// Agent Turn 循环：一次"模型生成 + 0..n 次工具执行"（DESIGN §7.1，内核唯一的编排）。
type Agent struct {
	llm       port.LLM
	ui        port.Presenter
	ids       port.IDGen
	clock     port.Clock
	tools     port.ToolRunner
	blobs     port.AttachmentStore
	memory    port.MemoryStore
	cfg       Config
	model     atomic.Value // string：当前生成模型（/model 热切换，D32；取代 cfg.Model 读取）
	think     atomic.Pointer[bool]
	effort    atomic.Value // string：推理档位（/effort 热切换，D34）
	system    string
	maxTurns  int
	est       *estimator // 三级 token 计数链②③（D26）
	compactAt int        // 自动压缩阈值 tokens（= CompactThreshold × MaxContextTokens）
	maxCtx    int        // 上下文预算 tokens
}

// New 创建 Agent 并校验必需依赖。
func New(d Deps, cfg Config) (*Agent, error) {
	switch {
	case d.LLM == nil:
		return nil, errors.New("agent: LLM 依赖为空")
	case d.UI == nil:
		return nil, errors.New("agent: Presenter 依赖为空")
	case d.IDs == nil:
		return nil, errors.New("agent: IDGen 依赖为空")
	case d.Clock == nil:
		return nil, errors.New("agent: Clock 依赖为空")
	case strings.TrimSpace(cfg.Model) == "":
		return nil, errors.New("agent: model 为空")
	}
	system := strings.TrimSpace(cfg.System)
	if system == "" {
		system = defaultSystem
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	threshold := cfg.CompactThreshold
	if threshold <= 0 {
		threshold = defaultCompactThreshold
	}
	maxCtx := cfg.MaxContextTokens
	if maxCtx <= 0 {
		maxCtx = DefaultMaxContextTokens
	}
	// 三级计数链②：LLM 适配器可选实现 TokenCounter（D26），实现即覆盖通用估算③。
	var counter port.TokenCounter
	if c, ok := d.LLM.(port.TokenCounter); ok {
		counter = c
	}
	ag := &Agent{
		llm:       d.LLM,
		ui:        d.UI,
		ids:       d.IDs,
		clock:     d.Clock,
		tools:     d.Tools,
		blobs:     d.Blobs,
		memory:    d.Memory,
		cfg:       cfg,
		system:    system,
		maxTurns:  maxTurns,
		est:       newEstimator(counter),
		compactAt: int(threshold*float64(maxCtx) + 0.5),
		maxCtx:    maxCtx,
	}
	ag.model.Store(cfg.Model) // 生成模型初值（/model 热切换，D32）
	ag.think.Store(cfg.Think) // 思考三态初值（nil = 键缺失，D34）
	ag.effort.Store(cfg.ReasoningEffort)
	return ag, nil
}

// setModel 切换生成模型（/model，D32）：只改模型名，请求参数其余部分不受影响。
func (a *Agent) setModel(name string) { a.model.Store(name) }

// setThink 切换原生思考三态（/think，D34）：nil = 键缺失态。
func (a *Agent) setThink(v *bool) { a.think.Store(v) }

// thinkState 取三态开关（wire 口径）：nil = 未显式设置（不发 enable_thinking）。
func (a *Agent) thinkState() *bool { return a.think.Load() }

// ThinkOn 展示口径的开关状态（D34：键缺失视为"开"）。
func (a *Agent) ThinkOn() bool {
	v := a.think.Load()
	return v == nil || *v
}

// setEffort 切换推理档位（/effort，D34）；空串 = 清除（不发送）。
func (a *Agent) setEffort(v string) { a.effort.Store(v) }

// Effort 当前推理档位（"" = 未设，交服务端默认）。
func (a *Agent) Effort() string {
	v, _ := a.effort.Load().(string)
	return v
}

// CurrentEffort 状态行口径（D34）：think off 时 effort 不发送，故显示空。
func (a *Agent) CurrentEffort() string {
	if v := a.think.Load(); v != nil && !*v {
		return ""
	}
	return a.Effort()
}

// modelName 当前生成模型。
func (a *Agent) modelName() string {
	v, _ := a.model.Load().(string)
	return v
}

// CurrentModel 当前生成模型（/model 热切换后的运行值；TUI 状态行经装配根回调读取）。
func (a *Agent) CurrentModel() string { return a.modelName() }

// Run 从当前 Head 出发执行一轮 Turn（DESIGN §7.1 / §10）：
//   - 流式增量只经 Presenter 进 UI，每次生成结束一次性 Commit 不可变节点（D3）；
//     节点 ID 在 Turn 开始时预分配，作流事件关联 ID。
//   - 工具级失败转 OK=false 照常回填，不中断 Turn；
//     装配级错误（确认器缺失/报错）为剩余调用补失败结果后中止本轮。
//   - 取消 = 已生成部分以 Outcome: cancelled 提交后返回 nil（可 /edit 重试）；
//     工具阶段取消 = 补齐中断结果后同样返回 nil（树可装配，主循环经 ctx 收尾）。
//     模型/网络错误 = Outcome: error 提交并返回错误（ErrorEvent 由装配根统一上抛，避免重复呈现）。
func (a *Agent) Run(ctx context.Context, c *conversation.Conversation) error {
	if c == nil {
		return errors.New("agent: nil conversation")
	}
	if len(c.Path()) <= 1 { // 仅 Root = 空会话
		return errors.New("agent: 会话为空，无可生成的上下文")
	}
	// 工具执行上下文：会话 ID（port，memory_* 会话作用域）+ 会话对象（app 内部，context_compact）。
	ctx = port.WithSessionID(ctx, c.ID)
	ctx = withConversation(ctx, c)
	autoTried := false // 自动压缩轨每次 Run 至多尝试一次（D21 轨2）

	for turn := 0; turn < a.maxTurns; turn++ {
		req, err := a.buildRequest(ctx, c)
		if err != nil {
			return fmt.Errorf("装配上下文: %w", err)
		}
		// 三级计数链（D26）：估算当前请求；达阈值 → 自动压缩（轨2）。
		sentEstimate, _ := a.est.Estimate(ctx, req)
		if !autoTried && sentEstimate >= a.compactAt {
			autoTried = true
			req, sentEstimate = a.autoCompact(ctx, c, req, sentEstimate)
		}
		mid := a.ids.MessageID() // 预分配关联 ID
		buf := &commitBuffer{id: mid, parent: c.Head}

		stream, err := a.llm.Generate(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				// 发起阶段被取消：按取消提交（Outcome: cancelled，返回 nil），
				// 与断流取消一致（§10）——主循环经 ctx 状态收尾，不报 error 行。
				return a.commitCancelled(ctx, c, buf)
			}
			return a.commitFailure(ctx, c, buf, fmt.Errorf("发起生成: %w", err))
		}
		calls, recvErr := a.consume(ctx, stream, buf, mid)
		calls = a.normalizeCalls(calls)
		if recvErr == nil && ctx.Err() != nil {
			recvErr = ctx.Err() // 断流恰好落在取消瞬间：以取消为准
		}
		outcome := conversation.OutcomeDone
		switch {
		case recvErr == nil:
		case errors.Is(recvErr, context.Canceled), errors.Is(recvErr, context.DeadlineExceeded):
			outcome = conversation.OutcomeCancelled
		default:
			outcome = conversation.OutcomeError
		}
		node := buf.commit(a.clock.Now(), outcome, a.modelName(), calls)
		if err := c.AppendCommitted(node); err != nil {
			return fmt.Errorf("提交节点: %w", err)
		}
		_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
		// 服务端实测 usage 回校估算（D26①→③：已发生的精确值修正未发送的估算）。
		if node.Usage.InputTokens > 0 {
			a.est.Calibrate(sentEstimate, node.Usage.InputTokens)
		}

		if recvErr != nil {
			if outcome == conversation.OutcomeCancelled {
				return nil // 取消已提交，不视为失败
			}
			return fmt.Errorf("生成失败: %w", recvErr)
		}
		if len(node.ToolCalls) == 0 {
			return nil
		}

		for i, call := range node.ToolCalls {
			_ = a.ui.Emit(ctx, port.ToolCallEvent{MessageID: mid, Call: call})
			res, err := a.execTool(ctx, call)
			if err != nil {
				// 装配级错误（确认器缺失/报错、父 ctx 取消）：先补公告尚未呈现的
				// 调用，再为剩余调用补失败结果保持 tool_calls 一一配对（树下次
				// 仍可装配），随后中止本轮，不回填空转 MaxTurns（§14 遗留修复）。
				for _, rest := range node.ToolCalls[i+1:] {
					_ = a.ui.Emit(ctx, port.ToolCallEvent{MessageID: mid, Call: rest})
				}
				cause := fmt.Errorf("执行工具 %s: %w", call.Name, err)
				if aerr := a.abortToolCalls(ctx, c, node.ToolCalls[i:], cause); aerr != nil {
					return aerr
				}
				// 取消（Ctrl+C/超时）补齐中断结果后按取消收场，与生成阶段一致（§10）：
				// 不作为错误呈现，主循环经 ctx 状态干净退出。
				if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
					return nil
				}
				return cause
			}
			tnode := conversation.Message{
				ID:         a.ids.MessageID(),
				Parent:     c.Head,
				Role:       conversation.RoleTool,
				ToolResult: &res,
				CreatedAt:  a.clock.Now(),
			}
			if err := c.AppendCommitted(tnode); err != nil {
				return fmt.Errorf("提交工具结果: %w", err)
			}
			_ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
		}
	}
	return fmt.Errorf("%w（%d）", ErrMaxTurns, a.maxTurns)
}

// Compact 生成上下文压缩摘要（D21 手动轨，/compact）：
// 把当前上下文（persona + 现有摘要 + 其后历史）交给模型转写为一条 system 摘要节点入树
// （记 Model/Usage，链式吸收旧摘要），其上历史此后装配不再回传。
// 摘要按英文结构化模板生成（compact_prompt.go，D21/§7.1）：已有旧摘要走合并更新提示词；
// 输出缺模板小节则发 NoticeEvent 并带提醒重试一次（两次生成的 Usage 累计入节点），
// 两次都不合格按失败返回。
// 失败只报错、树无损；无可压缩历史（摘要之上无新增）返回 ErrNothingToCompact。
// 返回 (摘要节点, 被吸收的历史节点数（persona 恒回传不计）, nil)。
func (a *Agent) Compact(ctx context.Context, c *conversation.Conversation) (conversation.Message, int, error) {
	if c == nil {
		return conversation.Message{}, 0, errors.New("agent: nil conversation")
	}
	path := c.Path()
	if len(path) <= 1 { // 仅 Root
		return conversation.Message{}, 0, ErrNothingToCompact
	}
	personaIdx, watermarkIdx := waterline(path)
	start := 1
	if watermarkIdx >= 0 {
		start = watermarkIdx
	}
	absorbed := len(path) - start
	if personaIdx >= start {
		absorbed-- // persona 恒回传，不计入被吸收的历史
	}
	// 可压缩增量 = 水位之后的非摘要节点；为 0 表示上下文没有新内容（重复压缩短路）。
	fresh := 0
	for i := start; i < len(path); i++ {
		if path[i].Role != conversation.RoleSystem {
			fresh++
		}
	}
	if fresh == 0 {
		return conversation.Message{}, 0, ErrNothingToCompact
	}

	// 压缩输入 = 当前上下文（已水位化：链式吸收只吞旧摘要与其后的新历史）。
	history, err := assemblePath(ctx, path, a.blobs)
	if err != nil {
		return conversation.Message{}, 0, fmt.Errorf("装配压缩输入: %w", err)
	}
	// 提示词两态：水位存在 = 已有旧摘要 → 合并更新，否则首次压缩（compact_prompt.go）。
	base := make([]port.PromptMessage, 0, len(history)+2)
	base = append(base, port.PromptMessage{
		Role:    "system",
		Content: []port.PromptPart{{Kind: "text", Text: compactPrompt(watermarkIdx >= 0)}},
	})
	base = append(base, history...)
	base = append(base, port.PromptMessage{
		Role:    "user",
		Content: []port.PromptPart{{Kind: "text", Text: compactNudge}},
	})

	mid := a.ids.MessageID() // 预分配关联 ID（重试复用，作流事件关联 ID）
	var (
		usage conversation.Usage
		text  string
	)
	// 至多两次生成：输出缺模板小节（或为空）→ NoticeEvent + 带提醒重试一次；
	// 两次都不合格按失败返回（树无损），失败原因区分"为空"与"未匹配模板"。
	for attempt := 0; attempt < 2; attempt++ {
		msgs := make([]port.PromptMessage, 0, len(base)+1)
		msgs = append(msgs, base...)
		if attempt > 0 {
			_ = a.ui.Emit(ctx, port.NoticeEvent{Text: "摘要输出未匹配模板，正在带提醒重试"})
			msgs = append(msgs, port.PromptMessage{
				Role:    "user",
				Content: []port.PromptPart{{Kind: "text", Text: compactRetryReminder}},
			})
		}
		buf := &commitBuffer{id: mid, parent: c.Head}
		stream, err := a.llm.Generate(ctx, port.GenerateRequest{
			Model:    a.modelName(),
			Messages: msgs,
			Budget:   a.cfg.Budget,
		})
		if err != nil {
			return conversation.Message{}, 0, fmt.Errorf("生成摘要: %w", err)
		}
		// 流式过程只进 UI；失败不提交（树无损）。
		if _, err := a.consume(ctx, stream, buf, mid); err != nil {
			return conversation.Message{}, 0, fmt.Errorf("生成摘要: %w", err)
		}
		usage.InputTokens += buf.usage.InputTokens
		usage.OutputTokens += buf.usage.OutputTokens
		usage.CostUSD += buf.usage.CostUSD
		text = strings.TrimSpace(buf.text.String())
		if text != "" && hasSummarySection(text) {
			break
		}
		if attempt == 1 {
			if text == "" {
				return conversation.Message{}, 0, errors.New("生成摘要: 模型返回为空")
			}
			return conversation.Message{}, 0, errors.New("生成摘要: 输出未匹配摘要模板")
		}
	}
	node := conversation.Message{
		ID:        mid,
		Parent:    c.Head,
		Role:      conversation.RoleSystem,
		Content:   []conversation.Part{{Kind: conversation.PartText, Text: text}},
		Outcome:   conversation.OutcomeDone,
		Model:     a.modelName(),
		Usage:     usage,
		CreatedAt: a.clock.Now(),
	}
	if err := c.AppendCommitted(node); err != nil {
		return conversation.Message{}, 0, fmt.Errorf("提交摘要节点: %w", err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return node, absorbed, nil
}

// autoCompact 自动压缩轨（D21 轨2 / M2）：估算达阈值时先尝试 Compact——
// 成功则重建请求（水位生效）并发 NoticeEvent；无可压缩内容则维持原请求；
// 失败回退"最旧裁剪"（D21：保 leading system 与最近、丢中间）并提示省略条数。
// 返回（执行后的请求, 用于 usage 校准的该请求估算）。
func (a *Agent) autoCompact(ctx context.Context, c *conversation.Conversation, req port.GenerateRequest, est int) (port.GenerateRequest, int) {
	_, _, err := a.Compact(ctx, c)
	switch {
	case err == nil:
		_ = a.ui.Emit(ctx, port.NoticeEvent{
			Text: fmt.Sprintf("上下文 ≈%d tokens 达自动压缩阈值 %d，已生成摘要", est, a.compactAt),
		})
		next, berr := a.buildRequest(ctx, c)
		if berr != nil {
			return req, est // 重建失败：沿用原请求（下一轮生成会暴露同因错误）
		}
		ne, _ := a.est.Estimate(ctx, next)
		return next, ne
	case errors.Is(err, ErrNothingToCompact):
		return req, est // 摘要之上无新内容（如 persona 巨大）：无可压，维持原请求
	default:
		kept, omitted := a.est.trimOldest(ctx, req.Messages, a.compactAt, req.Tools...)
		req.Messages = kept
		text := fmt.Sprintf("自动压缩失败（%v），已回退最旧裁剪", err)
		if omitted > 0 {
			text += fmt.Sprintf("，已省略 %d 条较早消息", omitted)
		}
		_ = a.ui.Emit(ctx, port.NoticeEvent{Text: text})
		ne, _ := a.est.Estimate(ctx, req)
		return req, ne
	}
}

// UsageReport /usage 的用量汇总（DESIGN §7.3，三级计数链 D26 的展示面）。
type UsageReport struct {
	Estimate  int     // 当前上下文占用（②精确 / ③估算）
	Exact     bool    // 是否精确计数（适配器 TokenCounter 生效）
	MaxCtx    int     // 上下文预算 tokens
	CompactAt int     // 自动压缩阈值 tokens
	Ratio     float64 // ③的服务端 usage 校准比值
	SumIn     int     // Path 内累计输入 tokens（服务端实测）
	SumOut    int     // Path 内累计输出 tokens（服务端实测）
	Gen       int     // Path 内带用量的生成次数
	LastIn    int     // 最近一次实测输入 tokens
	LastOut   int     // 最近一次实测输出 tokens
}

// UsageReport 汇总当前上下文估算与树上实测用量（估算走三级链，实测直接读节点）。
func (a *Agent) UsageReport(ctx context.Context, c *conversation.Conversation) (UsageReport, error) {
	if c == nil {
		return UsageReport{}, errors.New("agent: nil conversation")
	}
	req, err := a.buildRequest(ctx, c)
	if err != nil {
		return UsageReport{}, fmt.Errorf("装配上下文: %w", err)
	}
	est, exact := a.est.Estimate(ctx, req)
	rep := UsageReport{
		Estimate:  est,
		Exact:     exact,
		MaxCtx:    a.maxCtx,
		CompactAt: a.compactAt,
		Ratio:     a.est.ratio,
	}
	for _, m := range c.Path() {
		if m.Usage.InputTokens == 0 && m.Usage.OutputTokens == 0 {
			continue
		}
		rep.SumIn += m.Usage.InputTokens
		rep.SumOut += m.Usage.OutputTokens
		rep.Gen++
		rep.LastIn, rep.LastOut = m.Usage.InputTokens, m.Usage.OutputTokens
	}
	return rep, nil
}

// buildRequest 装配本轮请求：一条 system 提示 + Path 全量 + 记忆 + 工具清单（DESIGN §7.1）。
func (a *Agent) buildRequest(ctx context.Context, c *conversation.Conversation) (port.GenerateRequest, error) {
	treePath := c.Path()
	path, err := assemblePath(ctx, treePath, a.blobs)
	if err != nil {
		return port.GenerateRequest{}, err
	}
	msgs := make([]port.PromptMessage, 0, len(path)+1)
	// persona 恒回传（D20）：树内 persona 在位则由它充当 system；否则 config/内置提示兜底注入。
	if !(len(treePath) > 1 && treePath[1].Role == conversation.RoleSystem) {
		msgs = append(msgs, port.PromptMessage{
			Role:    "system",
			Content: []port.PromptPart{{Kind: "text", Text: a.system}},
		})
	}
	msgs = append(msgs, path...)
	// 记忆注入（DESIGN §7.1 承载 / D23）：索引（全局+当前会话）与会话记忆全文追加进
	// 首条 system；msgs[0] 恒为 system（persona 或兜底）。读取失败静默跳过，不阻断对话。
	if block := a.memoryBlock(ctx, c.ID); block != "" && len(msgs) > 0 && msgs[0].Role == "system" {
		msgs[0].Content = append(msgs[0].Content, port.PromptPart{Kind: "text", Text: block})
	}

	req := port.GenerateRequest{
		Model:    a.modelName(),
		Messages: msgs,
		Params:   a.cfg.Sampling,
		Budget:   a.cfg.Budget,
	}
	// 思考参数（D34）：/think 是总开关，off 时 reasoning_effort 与 enable_thinking
	// 一律不发（覆盖 /effort）；on/缺省时 effort 空则不发（交服务端默认）。
	think := a.thinkState()
	if think != nil { // 键缺失（nil）不发布尔：缺省零字段变化
		req.Params.Thinking = think
	}
	if think != nil && !*think {
		req.Params.ReasoningEffort = ""
	} else {
		req.Params.ReasoningEffort = a.Effort()
	}
	if a.tools != nil {
		specs, err := a.tools.Specs(ctx)
		if err != nil {
			return port.GenerateRequest{}, fmt.Errorf("工具清单: %w", err)
		}
		req.Tools = specs
	}
	return req, nil
}

// memoryBlock 构造记忆注入块（DESIGN §7.1 承载）：记忆索引（只含全局 + 当前会话两份，
// D23 名字白名单）+ 当前会话记忆全文（水位无关：压缩/回溯后仍在）。任一读取失败静默跳过。
func (a *Agent) memoryBlock(ctx context.Context, id conversation.ID) string {
	if a.memory == nil {
		return ""
	}
	var b strings.Builder
	if entries, err := a.memory.Index(ctx); err == nil {
		want := map[string]bool{port.GlobalMemoryDoc: true, port.SessionMemoryDoc(id): true}
		var lines []string
		for _, e := range entries {
			if want[e.Name] {
				lines = append(lines, "- "+e.Name+" — "+e.Summary)
			}
		}
		if len(lines) > 0 {
			sort.Strings(lines) // 确定性（索引顺序不承诺）
			b.WriteString("\n\n# 记忆索引（memory_read/memory_search 可取详情）\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}
	if doc, err := a.memory.Read(ctx, port.SessionMemoryDoc(id)); err == nil {
		if strings.TrimSpace(doc.Content) != "" {
			b.WriteString("\n\n# 会话记忆（本会话专用便签）\n")
			b.WriteString(doc.Content)
		}
	}
	return b.String()
}

// consume 边收边发 DeltaEvent（只进 UI），返回聚合后的工具调用。
func (a *Agent) consume(ctx context.Context, stream port.Stream, buf *commitBuffer, mid conversation.MessageID) ([]tool.Call, error) {
	defer stream.Close()
	for {
		d, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err // 半截工具调用不带出（未完成调用不能进树）
		}
		buf.add(d)
		_ = a.ui.Emit(ctx, port.DeltaEvent{MessageID: mid, Delta: d})
	}
	return buf.finalize(), nil
}

// execTool 执行工具：工具级失败由 Runner 转为 OK=false 结果照常回填（§10：不中断 Turn）；
// 返回 error 仅限装配级错误（确认器缺失/报错、父 ctx 取消等基础设施故障），
// 由 Run 快速失败上抛（§14：不让模型空转到 MaxTurns）。
func (a *Agent) execTool(ctx context.Context, call tool.Call) (tool.Result, error) {
	if a.tools == nil {
		// 未声明工具却收到 tool_calls（嵌入方/配置缺陷）：按装配级错误中止，
		// 不让空指针 panic、也不回填让模型空转。
		return tool.Result{}, errors.New("未配置工具执行器（Deps.Tools）")
	}
	res, err := a.tools.Execute(ctx, call)
	if err != nil {
		return tool.Result{}, err
	}
	if res.CallID == "" {
		res.CallID = call.ID
	}
	return res, nil
}

// abortToolCalls 装配级工具故障的收尾：为剩余未执行的调用补 OK=false 结果节点
// （保持 assistant.tool_calls 与 tool 结果一一配对，树下次装配仍可发送）。
// 只在补录自身失败时返回错误；成功返回 nil（原错误由调用方处置）。
// 与 §10 的"工具失败不中断 Turn"不同——那指的是工具级失败。
func (a *Agent) abortToolCalls(ctx context.Context, c *conversation.Conversation, calls []tool.Call, cause error) error {
	for _, call := range calls {
		res := tool.Result{CallID: call.ID, OK: false, Err: cause.Error()}
		tnode := conversation.Message{
			ID:         a.ids.MessageID(),
			Parent:     c.Head,
			Role:       conversation.RoleTool,
			ToolResult: &res,
			CreatedAt:  a.clock.Now(),
		}
		if err := c.AppendCommitted(tnode); err != nil {
			return fmt.Errorf("%w（补齐中断结果失败: %v）", cause, err)
		}
		_ = a.ui.Emit(ctx, port.ToolResultEvent{Result: res})
	}
	return nil
}

// normalizeCalls 补齐/去重调用 ID（个别兼容服务不回传 ID），并给空参数补 {}。
func (a *Agent) normalizeCalls(calls []tool.Call) []tool.Call {
	if len(calls) == 0 {
		return nil
	}
	seen := map[tool.CallID]bool{}
	for i := range calls {
		if calls[i].ID == "" || seen[calls[i].ID] {
			calls[i].ID = a.ids.CallID()
		}
		seen[calls[i].ID] = true
		if len(calls[i].Args) == 0 {
			calls[i].Args = json.RawMessage(`{}`)
		}
	}
	return calls
}

// commitFailure 以 Outcome: error 提交节点后返回原错误（§10）；
// ErrorEvent 由装配根统一上抛，避免重复呈现。
func (a *Agent) commitFailure(ctx context.Context, c *conversation.Conversation, buf *commitBuffer, cause error) error {
	node := buf.commit(a.clock.Now(), conversation.OutcomeError, a.modelName(), nil)
	if err := c.AppendCommitted(node); err != nil {
		return fmt.Errorf("%w（提交失败: %v）", cause, err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return cause
}

// commitCancelled 发起阶段取消的收场：以 Outcome: cancelled 提交占位节点并返回
// nil（与断流取消一致，§10"取消提交"——可 /edit 重试，不作为错误呈现）。
func (a *Agent) commitCancelled(ctx context.Context, c *conversation.Conversation, buf *commitBuffer) error {
	node := buf.commit(a.clock.Now(), conversation.OutcomeCancelled, a.modelName(), nil)
	if err := c.AppendCommitted(node); err != nil {
		return fmt.Errorf("提交取消节点: %w", err)
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return nil
}

// commitBuffer 流式缓冲：只进 UI，Turn 结束一次性 Commit（D3）。
type commitBuffer struct {
	id     conversation.MessageID
	parent conversation.MessageID
	text   strings.Builder
	calls  callAssembler
	usage  conversation.Usage
}

// add 累积一个增量。
func (b *commitBuffer) add(d port.Delta) {
	b.text.WriteString(d.Text)
	b.calls.add(d.ToolCalls)
	if d.Usage != nil {
		b.usage = *d.Usage
	}
}

// finalize 返回聚合后的工具调用。
func (b *commitBuffer) finalize() []tool.Call { return b.calls.finalize() }

// commit 组装终态节点。非 done 终态丢弃半截工具调用——未完成的调用若进树，
// 后续装配会产出"无应答的 tool_calls"被服务端拒（DESIGN §10 取消提交语义）。
func (b *commitBuffer) commit(now time.Time, outcome conversation.Outcome, model string, calls []tool.Call) conversation.Message {
	var content []conversation.Part
	if b.text.Len() > 0 {
		content = []conversation.Part{{Kind: conversation.PartText, Text: b.text.String()}}
	}
	if outcome != conversation.OutcomeDone {
		calls = nil
	}
	return conversation.Message{
		ID:        b.id,
		Parent:    b.parent,
		Role:      conversation.RoleAssistant,
		Content:   content,
		ToolCalls: calls,
		Outcome:   outcome,
		Model:     model,
		Usage:     b.usage,
		CreatedAt: now,
	}
}

// callAssembler 按 Index 聚合流式工具调用分片（port.ToolCallDelta → tool.Call）。
type callAssembler struct {
	order   []int
	byIndex map[int]*tool.Call
}

// add 累积一批分片。
func (a *callAssembler) add(deltas []port.ToolCallDelta) {
	if len(deltas) == 0 {
		return
	}
	if a.byIndex == nil {
		a.byIndex = map[int]*tool.Call{}
	}
	for _, d := range deltas {
		c, ok := a.byIndex[d.Index]
		if !ok {
			c = &tool.Call{}
			a.byIndex[d.Index] = c
			a.order = append(a.order, d.Index)
		}
		if d.ID != "" {
			c.ID = tool.CallID(d.ID)
		}
		if d.Name != "" {
			c.Name = d.Name
		}
		if d.ArgsDelta != "" {
			c.Args = append(c.Args, d.ArgsDelta...)
		}
	}
}

// finalize 按 Index 升序返回聚合结果。
func (a *callAssembler) finalize() []tool.Call {
	if len(a.order) == 0 {
		return nil
	}
	idx := append([]int(nil), a.order...)
	sort.Ints(idx)
	out := make([]tool.Call, 0, len(idx))
	for _, i := range idx {
		out = append(out, *a.byIndex[i])
	}
	return out
}
