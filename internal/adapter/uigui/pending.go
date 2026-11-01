package uigui

// pending.go 待发清单的界面侧（D272）：生成中键入的消息去哪、怎么拉回来。
//
// 这一层只做三件事：把编辑器里的话按档位送进清单、把清单里的条目变回编辑器里的字、
// 以及把内核的三种拒绝理由翻成一句人话。清单本身归会话（app.Session）持有——界面在
// 自己的事件循环 goroutine 里直接读写它，不经过 256 深的输入通道（那条通道要等本轮
// 结束才有人取，正是这个毛病逼出了待发清单）。

import (
	"errors"
	"strings"

	"gioui.org/widget"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// pendingCtl 待发条一条目的点击态（按条目 ID 缓存，帧间复用——Clickable 靠它认「同一个
// 按钮」；条目消失时一并丢弃）。
type pendingCtl struct {
	drag   widget.Clickable // 拉回输入框
	drop   widget.Clickable // 撤销
	toggle widget.Clickable // 改档（排队 ↔ 引导）
}

// pending 取当前清单（nil = 本构建没接清单）。
func (u *UI) pending() port.Pending {
	if u.pendingFn == nil {
		return nil
	}
	return u.pendingFn()
}

// pendingItems 当前待发条目（插入序）；没有清单/没有条目 = nil。
// 每帧调一次：清单一共 8 条封顶，这份复制可以忽略不计。
func (u *UI) pendingItems() []port.PendingInput {
	p := u.pending()
	if p == nil {
		return nil
	}
	return p.List()
}

// ctlFor 取（必要时新建）某条目的点击态；顺手把已消失条目的点击态清掉。
func (u *UI) ctlFor(id string) *pendingCtl {
	if u.pendCtl == nil {
		u.pendCtl = map[string]*pendingCtl{}
	}
	c, ok := u.pendCtl[id]
	if !ok {
		c = &pendingCtl{}
		u.pendCtl[id] = c
	}
	return c
}

// prunePendingCtl 丢掉清单里已不存在条目的点击态（每帧一次）。
func (u *UI) prunePendingCtl(items []port.PendingInput) {
	if len(u.pendCtl) == 0 {
		return
	}
	live := make(map[string]bool, len(items))
	for _, it := range items {
		live[it.ID] = true
	}
	for id := range u.pendCtl {
		if !live[id] {
			delete(u.pendCtl, id)
		}
	}
}

// addPending 把一段文字按档位放进清单；收不下就明确说原因（不静默丢弃）。
// 返回 true = 已进清单（调用方据此清编辑器）。
func (u *UI) addPending(text string, d port.Delivery) bool {
	p := u.pending()
	if p == nil {
		return false
	}
	if _, err := p.Add(text, d); err != nil {
		u.m.add(blockNotice, pendingErrText(err))
		return false
	}
	return true
}

// pendingErrText 三种拒绝理由的用户面说法（前端不该把 errors.New 的中文原样甩出来，
// 但也别另造一套口径——这里只是把它说完整）。
func pendingErrText(err error) string {
	switch {
	case errors.Is(err, port.ErrPendingFull):
		return "待发已满 8 条，先撤销一条再发"
	case errors.Is(err, port.ErrPendingEmpty):
		return "待发内容为空，这一行没有发出去"
	case errors.Is(err, port.ErrPendingTooLong):
		return "这一行太长了（上限 8000 字符），没有进待发"
	default:
		return "没能放进待发：" + err.Error()
	}
}

// recallPending 把一条待发拉回编辑器（此后不再被取用）。编辑器里已有的字不丢——
// 拉回来的接在后面，两段之间补一个换行。
func (u *UI) recallPending(id string) {
	p := u.pending()
	if p == nil {
		return
	}
	it, ok := p.Take(id)
	if !ok {
		return
	}
	text := it.Text
	if cur := u.editor.Text(); strings.TrimSpace(cur) != "" {
		sep := "\n"
		if u.editor.SingleLine {
			sep = " " // 单行编辑器里换行会被压平成空格；直接给空格，别绕一圈
		}
		text = cur + sep + text
	}
	u.editor.SetText(text)
	n := len([]rune(text))
	u.editor.SetCaret(n, n)
	u.focusPending = true
}

// dropPending 撤销一条待发。
func (u *UI) dropPending(id string) {
	if p := u.pending(); p != nil {
		p.Remove(id)
	}
}

// togglePendingDelivery 改档（排队 ↔ 引导）：同一条消息想早一点或晚一点到模型眼前，
// 不必撤了重打。
func (u *UI) togglePendingDelivery(id string) {
	p := u.pending()
	if p == nil {
		return
	}
	for _, it := range p.List() {
		if it.ID != id {
			continue
		}
		next := port.DeliveryQueue
		if it.Delivery == port.DeliveryQueue {
			next = port.DeliverySteer
		}
		p.SetDelivery(id, next)
		return
	}
}
