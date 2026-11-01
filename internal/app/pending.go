package app

// pending.go 待发清单的实现（D272）：会话级、内存态、有序、有上限。
//
// 调用方有两个：前端（自己的事件循环里增删改，见 port.Pending）与会话 goroutine
// （两个取用边界：轮内取全部引导、轮末取一条）。所以全程一把互斥锁——这里没有「只在
// 一个 goroutine 里改」的前提，前端和内核是两条真并发的手。

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	// pendingMaxItems 清单上限（条）。8 条够覆盖「边看边想边补几句」，又能在一条窄条里
	// 显示完；满了 Add 报错由前端明确提示，不静默丢弃。
	pendingMaxItems = 8
	// pendingMaxRunes 单条上限（字符）。正常对话远远用不到，防的是一次误粘。
	pendingMaxRunes = 8000
)

// pendingList 待发清单（port.Pending 的实现）。
type pendingList struct {
	mu    sync.Mutex
	seq   int
	items []port.PendingInput
	// paused 停止键按过之后为真：本轮收尾不再自动续跑（清单原样保留）。下一次真实用户输入
	// 到来时清掉——停止是停手，不是「这些都不要了」，也不能一停就接着跑队列里那条。
	paused bool
}

func newPendingList() *pendingList { return &pendingList{} }

var _ port.Pending = (*pendingList)(nil)

// Add 追加一条（插入序即取用序）。
func (p *pendingList) Add(text string, d port.Delivery) (port.PendingInput, error) {
	t := strings.TrimSpace(text)
	if t == "" {
		return port.PendingInput{}, port.ErrPendingEmpty
	}
	if utf8.RuneCountInString(t) > pendingMaxRunes {
		return port.PendingInput{}, port.ErrPendingTooLong
	}
	if !d.Valid() {
		d = port.DeliveryQueue // 未知档位按排队：宁可晚一轮，不擅自插话
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) >= pendingMaxItems {
		return port.PendingInput{}, port.ErrPendingFull
	}
	p.seq++
	it := port.PendingInput{
		ID:       fmt.Sprintf("p%d", p.seq),
		Text:     t,
		Delivery: d,
		AddedAt:  time.Now(),
	}
	p.items = append(p.items, it)
	return it, nil
}

// List 按插入序复制一份。
func (p *pendingList) List() []port.PendingInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]port.PendingInput, len(p.items))
	copy(out, p.items)
	return out
}

// Remove 撤销一条。
func (p *pendingList) Remove(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, it := range p.items {
		if it.ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return true
		}
	}
	return false
}

// SetDelivery 改档（排队 ↔ 引导）。
func (p *pendingList) SetDelivery(id string, d port.Delivery) bool {
	if !d.Valid() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.items {
		if p.items[i].ID == id {
			p.items[i].Delivery = d
			return true
		}
	}
	return false
}

// Take 取走一条（拉回输入框）：此后不再被取用。
func (p *pendingList) Take(id string) (port.PendingInput, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, it := range p.items {
		if it.ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return it, true
		}
	}
	return port.PendingInput{}, false
}

// Clear 清空（切会话、关停）；返回清掉的条数。
func (p *pendingList) Clear() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.items)
	p.items = nil
	p.paused = false
	return n
}

// takeSteer 取走全部引导（轮内边界；保持插入序，与它们被说出来的顺序一致）。
func (p *pendingList) takeSteer() []port.PendingInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 {
		return nil
	}
	var out, rest []port.PendingInput
	for _, it := range p.items {
		if it.Delivery == port.DeliverySteer {
			out = append(out, it)
			continue
		}
		rest = append(rest, it)
	}
	if len(out) == 0 {
		return nil
	}
	p.items = rest
	return out
}

// takeFirst 取走最早的一条（轮末边界，不分档位）：引导错过窗口就只是「一句话」，
// 按下一轮发出去是对的——留在清单里发霉才是错的。
func (p *pendingList) takeFirst() (port.PendingInput, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 || p.paused {
		return port.PendingInput{}, false
	}
	it := p.items[0]
	p.items = p.items[1:]
	return it, true
}

// takeOneSteer 取走**第一条引导**（停止键中断后立即重新请求用）。paused 不拦：这不是
// 「自动续跑」，是用户刚按停止时明确要求「把这句话插进去重来」。
func (p *pendingList) takeOneSteer() (port.PendingInput, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, it := range p.items {
		if it.Delivery == port.DeliverySteer {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return it, true
		}
	}
	return port.PendingInput{}, false
}

// setPaused 置/清「停止之后不再自动续跑」。
func (p *pendingList) setPaused(on bool) {
	p.mu.Lock()
	p.paused = on
	p.mu.Unlock()
}

// isPaused 读当前暂停位（测试与提示文案用）。
func (p *pendingList) isPaused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// count 当前条数（状态行与「切会话丢弃」提示用；List 也够，但这条不必分配）。
func (p *pendingList) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items)
}

// Pending 待发清单（前端直接读写；port.Pending 的会话级实现）。
func (s *Session) Pending() port.Pending { return s.pendingQ }

// dropPending 清空待发清单并返回清掉的条数（切会话、关停共用）。
func (s *Session) dropPending() int {
	if s.pendingQ == nil {
		return 0
	}
	return s.pendingQ.Clear()
}

// dropPendingNote 切走会话时的那行提示（不静默丢东西，D272）：「3 条待发消息未随会话带走」。
// 没清掉任何东西时返回空串——没有东西可丢就不该多一行话。
func (s *Session) dropPendingNote() string {
	n := s.dropPending()
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("（%d 条待发消息未随会话带走）", n)
}
