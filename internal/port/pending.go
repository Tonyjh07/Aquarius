package port

// pending.go 待发消息端口（D272）：一轮在跑的时候用户说的话停在哪、什么时候到模型眼前。
//
// 两档按「什么时候到模型眼前」分，不按语气分：排队（queue）在本轮收尾后接着起下一轮；
// 引导（steer）在下一次模型请求之前插入、本轮不结束。
//
// 清单**由一个前端直接读写**，不经 Prompter.Next 的输入通道：主循环此刻还卡在 Handle 里
// （那一轮没结束），走通道就等于「等本轮结束才看得到」——那正是这条端口要修的毛病。
// 因此这几个方法都不带 ctx：它们只是内存读写，收尾路径上调它们时不该被一个已取消的 ctx 拦住。

import (
	"errors"
	"time"
)

// 待发清单的拒绝理由（前端据 errors.Is 给不同的提示）。
var (
	// ErrPendingFull 已满（8 条）：要加就得先撤销一条。
	ErrPendingFull = errors.New("pending: 待发清单已满")
	// ErrPendingEmpty 空文本：没有可发的。
	ErrPendingEmpty = errors.New("pending: 待发内容为空")
	// ErrPendingTooLong 单条超长（8000 字符）。
	ErrPendingTooLong = errors.New("pending: 待发内容过长")
)

// Delivery 待发消息的到模型口径。
type Delivery string

const (
	// DeliveryQueue 本轮收尾后接着起下一轮（缺省档）。
	DeliveryQueue Delivery = "queue"
	// DeliverySteer 下一次模型请求之前插入本轮（本轮不结束，assistant 消息被切开）。
	DeliverySteer Delivery = "steer"
)

// Valid 是否是已知档位（未知值一律按排队处理，不静默变引导）。
func (d Delivery) Valid() bool {
	return d == DeliveryQueue || d == DeliverySteer
}

// PendingInput 待发清单里的一条。
type PendingInput struct {
	// ID 清单内稳定 ID（前端按它撤销、改档、拉回）。
	ID string `json:"id"`
	// Text 正文（纯文本；带附件/语音的不进清单，那些走输入通道在本轮之后执行）。
	Text string `json:"text"`
	// Delivery 档位。
	Delivery Delivery `json:"delivery"`
	// AddedAt 入清单时刻（界面显示「刚刚/N 分钟前」用；排序仍是插入序，不按时间重排）。
	AddedAt time.Time `json:"added_at"`
}

// Pending 待发清单：会话级、内存态、有序、有上限。
//
// 实现方是 app.Session（会话级一份，切会话即清）；前端在自己的事件循环里直接调用。
type Pending interface {
	// Add 追加一条；满 / 空文本 / 超长 → 错误（前端据此提示，不静默丢弃）。
	Add(text string, d Delivery) (PendingInput, error)
	// List 按插入序返回一份副本（调用方随便读，改动不影响清单）。
	List() []PendingInput
	// Remove 撤销一条。
	Remove(id string) bool
	// SetDelivery 改档（排队 ↔ 引导）。
	SetDelivery(id string, d Delivery) bool
	// Take 取走一条（「拉回输入框」）：此后不再被取用，正文交给调用方。
	Take(id string) (PendingInput, bool)
	// Clear 清空（切会话、关停）；返回清掉的条数（0 = 本来就没有，前端不必提示）。
	Clear() int
}
