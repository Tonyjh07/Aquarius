package port

import (
	"context"
	"errors"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// ErrConversationNotFound 目标会话不存在（Load/Remove 找不到时由实现方包装返回）。
// 内核据此区分「会话已被删除」与「读取故障」：前者丢弃注入，后者上抛。
var ErrConversationNotFound = errors.New("store: conversation not found")

// ConversationSummary 会话列表项。
type ConversationSummary struct {
	ID        conversation.ID `json:"id"`
	Title     string          `json:"title"`
	MessageN  int             `json:"message_n"`
	UpdatedAt time.Time       `json:"updated_at"`
	// Character 绑定的角色 slug（§5.17/D243；空 = 非角色会话）。列表侧据此标注来路。
	Character string `json:"character,omitempty"`
	// Tags 会话标签（D313；空 = 没打标签）。/list --tag 据此过滤。
	Tags []string `json:"tags,omitempty"`
	// Pinned 置顶（D313）。列表排序置顶在前。
	Pinned bool `json:"pinned,omitempty"`
}

// TrashEntry 回收站条目摘要（D311）：与 ConversationSummary 同用途，多一个删除时间。
// 删除时间的取法（主文件改动时间）是各实现自己的事，端口只收发这个结果。
type TrashEntry struct {
	ID        conversation.ID `json:"id"`
	Title     string          `json:"title"`
	MessageN  int             `json:"message_n"`
	UpdatedAt time.Time       `json:"updated_at"`
	DeletedAt time.Time       `json:"deleted_at"`
}

// ConversationStore 会话存储端口：整树存取（一会话一 JSON），写前把旧文件轮成备份代
// （代数由实现定，内置 storejson 默认保留 3 代、可配，D7/D254）。
type ConversationStore interface {
	Save(ctx context.Context, c *conversation.Conversation) error
	Load(ctx context.Context, id conversation.ID) (*conversation.Conversation, error)
	List(ctx context.Context) ([]ConversationSummary, error)
	Remove(ctx context.Context, id conversation.ID) error
}
