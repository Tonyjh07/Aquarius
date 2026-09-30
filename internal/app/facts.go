package app

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// factsSnapshot 会话展示事实快照（D82/§7.6，前置 B）：facts 为不可变发布值，sig 是
// 计算它时的会话签名——签名不变则跳过重算（重算 = UsageReport 一次全量装配 + 计数，
// 命令类输入不得空转）。发布后整只替换，UI 事件循环 goroutine 无锁读。
type factsSnapshot struct {
	facts port.SessionFacts
	sig   factsSig
}

// factsSig 会话事实签名：事实的全部影响因素的廉价摘要（标题/会话/Head/路径长/
// Path 实测用量和）。任一变更 → 重算；全部不变 → 快照原样保留。
type factsSig struct {
	title   string
	id      string
	head    conversation.MessageID
	pathLen int
	sumIn   int
	sumOut  int
}

func factsSigOf(c *conversation.Conversation) factsSig {
	path := c.Path()
	sig := factsSig{title: c.Title, id: string(c.ID), pathLen: len(path)}
	for _, m := range path {
		sig.sumIn += m.Usage.InputTokens
		sig.sumOut += m.Usage.OutputTokens
	}
	if len(path) > 0 {
		sig.head = path[len(path)-1].ID
	}
	return sig
}

// publishFacts 重建并原子发布会话展示事实快照（D82/§7.6）：调用点与 publishTree 同组
// （NewSession 两分支 + Handle 返回前），均在会话 goroutine。签名门控见上；计数失败
// 保留旧快照并返回（下次 Handle 自愈重试，est ③「计数失败不阻断」同款口径）。
func (s *Session) publishFacts(ctx context.Context) {
	if s.cur == nil {
		return
	}
	sig := factsSigOf(s.cur)
	if snap := s.facts.Load(); snap != nil && snap.sig == sig {
		return
	}
	rep, err := s.agent.UsageReport(ctx, s.cur)
	if err != nil {
		return
	}
	s.facts.Store(&factsSnapshot{
		facts: port.SessionFacts{
			Title:     s.cur.Title,
			ConvID:    string(s.cur.ID),
			CtxTokens: rep.Estimate,
			CtxExact:  rep.Exact,
			CtxMax:    rep.MaxCtx,
			SumIn:     rep.SumIn,
			SumOut:    rep.SumOut,
			LastIn:    rep.LastIn,
			LastOut:   rep.LastOut,
		},
		sig: sig,
	})
}

// Facts 读已发布的会话展示事实（D82/§7.6）：无锁原子读，可从 UI 事件循环 goroutine
// 直调（§15.5）；尚未发布（构造早期/计数一直失败）返回零值——消费方自行兜底。
func (s *Session) Facts() port.SessionFacts {
	if snap := s.facts.Load(); snap != nil {
		return snap.facts
	}
	return port.SessionFacts{}
}
