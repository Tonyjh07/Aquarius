package app

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// Session 实现会话列表只读端口（D120⑥，S3 左栏）；与 TreeView 同款反向端口：
// 消费方是 UI 适配器、实现方是 app（依赖方向不变，§5 铁律 1）。
var _ port.SessionLister = (*Session)(nil)

// sessionSnapshot 会话列表只读快照（D120⑥）：一次整体重建、整体发布的**不可变**值，
// 发布后不再改写；UI 事件循环 goroutine 无锁读（§15.5），故无竞态（-race 干净）。
type sessionSnapshot struct {
	items []port.SessionSummary
}

// publishSessions 重建并原子发布会话列表快照：由 store.List 汇总，标出当前会话。
// 发布点与 tree/facts 同组（构造完成 + 每次 Handle 返回前）——命令与 Turn 都可能改变
// 列表（新建/切换/改名/删除/追加消息与更新时间）。读失败时**保留上一版**（不空转，同
// facts 降级口径）；UI 侧不做任何 store I/O。
//
// 成本注：store.List 会读并解码全部会话 JSON；个人助手会话数有限，每次输入一次可接受。
// 若日后成为热路径，可按树/事实快照同款签名门控（D82）收敛重算。
func (s *Session) publishSessions(ctx context.Context) {
	if s.cur == nil {
		return
	}
	sums, err := s.store.List(ctx)
	if err != nil {
		return
	}
	items := make([]port.SessionSummary, 0, len(sums))
	for _, sm := range sums {
		items = append(items, port.SessionSummary{
			ID:        sm.ID,
			Title:     sm.Title,
			UpdatedAt: sm.UpdatedAt,
			Messages:  sm.MessageN,
			Current:   sm.ID == s.cur.ID,
		})
	}
	s.sessions.Store(&sessionSnapshot{items: items})
}

// List 实现 port.SessionLister（D120⑥）：读已发布的不可变快照，无锁——UI 每帧可调。
// 返回切片为快照内部数据，调用方只读、不得改写；尚未发布时返回 nil。
func (s *Session) List() []port.SessionSummary {
	snap := s.sessions.Load()
	if snap == nil {
		return nil
	}
	return snap.items
}
