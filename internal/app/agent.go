// Package app 应用层：Turn 循环（唯一的编排）、上下文装配、会话命令（DESIGN §7）。
//
// 依赖方向：adapter → port ← app → domain。app 只依赖 port 与 domain，
// 具体适配器（LLM/存储/UI）由装配根 cmd/aquarius 注入。
package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// ErrMaxTurns 单次 Run 的"生成 + 工具"循环次数用尽（DESIGN §7.1）。
var ErrMaxTurns = errors.New("agent: 超过最大轮次数")

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
	// EchoThinking 思考回传开关（D42）：true = 树内 PartThinking 置入 PromptMessage.Reasoning
	// 由适配器映射 reasoning_content；false = 装配时过滤。config `model.echo_thinking` 键缺失 =
	// 回传、显式 false 才关（缺省解析在装配根完成，app 只见 bool）。
	EchoThinking bool
	// Env 运行环境块（D37）：附加到 system 提示（含 config 自定义提示）之后；
	// 全空 = 不附加。
	Env RuntimeEnv
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
	system = systemWithEnv(system, cfg.Env) // D37：兜底注入的 system 同样带环境块
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
//   - 流式增量只经 Presenter 进 UI；**一次 Turn（可含多轮"生成+工具"循环）只提交一个
//     不可变节点**（D3/D95 Turn 粒度）：多轮的思考/正文/工具分片按到达序交错累积，
//     Turn 收场（正常/取消/失败/MaxTurns 用尽）时一次性 AppendCommitted。
//   - 节点 ID 在 Turn 开始时预分配，作全部流事件的关联 ID；第 2 轮起构造请求时把
//     未提交缓冲投影为合成消息追加在 Path 之后（模型能看到前几轮的调用与结果）。
//   - 工具级失败转 OK=false 照常回填，不中断 Turn；装配级错误（确认器缺失/报错）
//     为剩余调用补失败结果后随 Turn 节点提交并中止。
//   - 取消 = 已生成部分以 Outcome: cancelled 随 Turn 节点提交后返回 nil（可 /edit 重试）；
//     工具阶段取消 = 补齐中断结果后同样收场（树可装配，主循环经 ctx 收尾）。
//     模型/网络错误 = Outcome: error 提交并返回错误（ErrorEvent 由装配根统一上抛）。
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

	mid := a.ids.MessageID() // Turn 预分配关联 ID（一 Turn 一节点，D95）
	buf := newTurnBuffer(mid)

	for turn := 0; turn < a.maxTurns; turn++ {
		req, err := a.buildRequest(ctx, c, buf)
		if err != nil {
			if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeError); cerr != nil {
				return fmt.Errorf("装配上下文: %w (commit failed: %v)", err, cerr)
			}
			return fmt.Errorf("装配上下文: %w", err)
		}
		// 三级计数链（D26）：估算当前请求；达阈值 → 自动压缩（轨2）。
		sentEstimate, _ := a.est.Estimate(ctx, req)
		if !autoTried && sentEstimate >= a.compactAt {
			autoTried = true
			req, sentEstimate = a.autoCompact(ctx, c, req, sentEstimate, buf)
		}
		stream, err := a.llm.Generate(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				// 发起阶段被取消：按取消提交（Outcome: cancelled，返回 nil），
				// 与断流取消一致（§10）——主循环经 ctx 状态收尾，不报 error 行。
				if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeCancelled); cerr != nil {
					return fmt.Errorf("提交取消节点: %w", cerr)
				}
				return nil
			}
			if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeError); cerr != nil {
				return fmt.Errorf("发起生成: %w (commit failed: %v)", err, cerr)
			}
			return fmt.Errorf("发起生成: %w", err)
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
		// 服务端实测 usage 回校估算（D26①→③）：raw 与 actual 必须同请求，逐轮校准。
		if ru := buf.round; ru.InputTokens > 0 {
			a.est.Calibrate(sentEstimate, ru.InputTokens)
		}
		// 本轮思考/正文定稿进 Turn 缓冲（幂等；工具分片随后按到达序追加）。
		buf.sealRound()

		// 工具执行（D95 先执行后提交）：结果分片入缓冲，随 Turn 节点一次性入树。
		// 非 done 终态不执行——半截调用不进缓冲（§10 取消提交语义）。
		var toolErr error
		if recvErr == nil {
			toolErr = a.runTools(ctx, buf, mid, calls)
		}

		switch {
		case recvErr != nil:
			if outcome == conversation.OutcomeCancelled {
				if _, cerr := a.finishTurn(ctx, c, buf, outcome); cerr != nil {
					return fmt.Errorf("提交取消节点: %w", cerr)
				}
				return nil // 取消已提交，不视为失败
			}
			if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeError); cerr != nil {
				return fmt.Errorf("生成失败: %w (commit failed: %v)", recvErr, cerr)
			}
			return fmt.Errorf("生成失败: %w", recvErr)
		case toolErr != nil:
			// 装配级故障：失败结果已填进分片，提交 Turn 节点后上抛；
			// 取消（Ctrl+C/超时）按取消收场，不作为错误呈现（§10）。
			if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeDone); cerr != nil {
				return fmt.Errorf("%w (commit failed: %v)", toolErr, cerr)
			}
			if errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded) {
				return nil
			}
			return toolErr
		case len(calls) == 0:
			// Turn 正常收场：一次性提交（D95）。
			if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeDone); cerr != nil {
				return fmt.Errorf("提交节点: %w", cerr)
			}
			return nil
		}
		// 仍有调用 → 下一轮：请求构造会把未提交缓冲投影进上下文。
	}
	// MaxTurns 用尽：先提交已累积的 Turn 节点再报错。
	if _, cerr := a.finishTurn(ctx, c, buf, conversation.OutcomeDone); cerr != nil {
		return fmt.Errorf("%w (commit failed: %v)", ErrMaxTurns, cerr)
	}
	return fmt.Errorf("%w（%d）", ErrMaxTurns, a.maxTurns)
}

// finishTurn 提交 Turn 节点并广播（每 Turn 恰一次，D95）：parent 取提交时的 Head
// （mid-turn 的 compact 摘要可能已前移 Head，节点挂到最新 Head 之下保持路径线性）。
func (a *Agent) finishTurn(ctx context.Context, c *conversation.Conversation, buf *turnBuffer, outcome conversation.Outcome) (conversation.Message, error) {
	node := buf.commit(c.Head, a.clock.Now(), outcome, a.modelName())
	if err := c.AppendCommitted(node); err != nil {
		return conversation.Message{}, err
	}
	_ = a.ui.Emit(ctx, port.CommittedEvent{Message: node})
	return node, nil
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
	req, err := a.buildRequest(ctx, c, nil)
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
// Turn 粒度（D95）：buf 非空时把未提交缓冲投影为合成 assistant 消息追加在 Path 尾部——
// 第 2 轮起模型能看到本 Turn 前几轮的调用与结果（合成节点不进树，仅本次请求可见）。
func (a *Agent) buildRequest(ctx context.Context, c *conversation.Conversation, buf *turnBuffer) (port.GenerateRequest, error) {
	treePath := c.Path()
	if buf != nil && !buf.empty() {
		treePath = append(treePath, buf.inFlight())
	}
	path, err := assemblePath(ctx, treePath, a.blobs, a.cfg.EchoThinking)
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
			b.WriteString("\n\n# Memory index (use memory_read/memory_search for details)\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}
	if doc, err := a.memory.Read(ctx, port.SessionMemoryDoc(id)); err == nil {
		if strings.TrimSpace(doc.Content) != "" {
			b.WriteString("\n\n# Session memory (this conversation's notepad)\n")
			b.WriteString(doc.Content)
		}
	}
	return b.String()
}
