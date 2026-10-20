package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// injectQueueCap 注入队列容量（D150）：生产者在外部 goroutine（工具/MCP/轮询器），
// 排空在会话 goroutine；队列满时 Inject 立即报错——**绝不阻塞**调用方（§10 并发口径）。
const injectQueueCap = 64

// pendingInjection 队列里的一条待落地注入（触发口径已解析好，排空时只看这一个布尔）。
type pendingInjection struct {
	source  string
	trust   conversation.InjectTrust
	parts   []conversation.Part
	trigger bool
	session conversation.ID // 目标会话（D167）；空 = 落地时取当前会话
}

// Session 实现注入端口（D150）：外部生产者只经此入队，树变更仍由会话 goroutine 独占。
var _ port.Injector = (*Session)(nil)

// Inject 实现 port.Injector（D150）：**先校验、后非阻塞入队**。
// 校验在入队前做——坏包（思考/工具分片、空内容）当场报给调用方，不让它无声落进树；
// 队列满则立即返回错误（调用方自行退避重试），不在这里阻塞等空位。
func (s *Session) Inject(_ context.Context, in port.Injection) error {
	if len(in.Parts) == 0 {
		return errors.New("session: 注入内容为空")
	}
	if err := conversation.ValidateInjectParts(in.Parts); err != nil {
		return fmt.Errorf("session: 注入内容非法: %w", err)
	}
	item := pendingInjection{
		source:  strings.TrimSpace(in.Source),
		trust:   injectTrust(in.Trust),
		parts:   in.Parts,
		trigger: resolveInjectTrigger(in.Source, in.Trigger),
		session: in.Session,
	}
	select {
	case s.injectCh <- item:
		return nil
	default:
		return errors.New("session: 注入队列已满（稍后重试）")
	}
}

// injectTrust 端口信任级 → 领域信任级（D170）。两处各自定义是为了守住依赖方向
// （domain 不认识 port），映射只在这一处。未知值按外部处理，不猜。
func injectTrust(t port.TrustLevel) conversation.InjectTrust {
	if t == port.TrustHost {
		return conversation.TrustHost
	}
	return conversation.TrustExternal
}

// resolveInjectTrigger 触发口径（D150，内核唯一一处默认表）：显式覆盖优先；
// 未给定时按来源取默认——任务/轮询/手动注入默认起一轮（完成应当主动汇报），
// 工具/插件注入默认不起（Turn 内注入只补上下文，打断流程会重复计费）。
// 未知来源按不可信处理，默认不起。
func resolveInjectTrigger(source string, override *bool) bool {
	if override != nil {
		return *override
	}
	switch {
	case strings.HasPrefix(source, "job:"), strings.HasPrefix(source, "poller:"):
		return true
	case source == "user":
		return true
	default: // tool:<name> / plugin:<name> / 未知
		return false
	}
}

// drainInjections 排空注入队列并逐条落地（D150，两个排空点共用）：每条落成 `Role=inject`
// 节点（挂当前 Head，Head 随之前移）。
//
// driven 决定 Trigger 的口径：触发型注入在**驱动式**排空（`/inject`、空闲唤醒）下记为
// `true` 并把「是否该起一轮」回给调用方（由调用方在清屏回放之后决定何时跑轮，见
// session_cmd_inject）；非驱动式排空（`Handle` 入口、轮末）只**落地**、Trigger 记 `false`
// ——轮内注入不打断当前轮，用户输入优先。故记的是「本条落地时**实际**起没起响应」。
//
// announce=true 时逐条发 HistoryEvent（不清屏、非 CommittedEvent——输出装饰器不响应），
// 供转写区把注入当一条记录铺开；`/inject` 走清屏回放，故它传 announce=false。
// 落过节点即落盘（失败上抛）。
func (s *Session) drainInjections(ctx context.Context, driven, announce bool) (landed []conversation.Message, wantTurn bool, err error) {
	for {
		select {
		case item := <-s.injectCh:
			// 目标会话已不是当前会话（D167）：切换瞬间入队的报告整条丢弃，
			// 不把别的会话的后台任务塞进这里。
			if item.session != "" && item.session != s.cur.ID {
				continue
			}
			trigger := item.trigger && driven
			node, err := s.landInjection(item, trigger)
			if err != nil {
				return landed, false, err
			}
			landed = append(landed, node)
			if trigger {
				wantTurn = true
			}
			if announce && s.ui != nil {
				if err := s.ui.Emit(ctx, port.HistoryEvent{Message: node}); err != nil {
					return landed, wantTurn, fmt.Errorf("注入落地呈现: %w", err)
				}
			}
		default:
			if len(landed) > 0 {
				if err := s.persist(ctx); err != nil {
					return landed, wantTurn, err
				}
			}
			return landed, wantTurn, nil
		}
	}
}

// landInjection 把一条注入落成不可变节点（D150）：父 = 当前 Head，Head 随之前移。
// Source 只作展示与审计（不进上下文）；Trigger 记**本条落地时是否实际起了一轮响应**。
func (s *Session) landInjection(item pendingInjection, trigger bool) (conversation.Message, error) {
	node := conversation.Message{
		ID:        s.ids.MessageID(),
		Parent:    s.cur.Head,
		Role:      conversation.RoleInject,
		Content:   item.parts,
		Source:    item.source,
		Trigger:   trigger,
		Trust:     item.trust,
		CreatedAt: s.clock.Now(),
	}
	if err := s.cur.AppendCommitted(node); err != nil {
		return conversation.Message{}, fmt.Errorf("session: 注入落地: %w", err)
	}
	return node, nil
}
