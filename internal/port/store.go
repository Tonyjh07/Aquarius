package port

import (
	"context"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// ConversationSummary 会话列表项。
type ConversationSummary struct {
	ID        conversation.ID `json:"id"`
	Title     string          `json:"title"`
	MessageN  int             `json:"message_n"`
	UpdatedAt time.Time       `json:"updated_at"`
	// Character 绑定的角色 slug（§5.17/D243；空 = 非角色会话）。列表侧据此标注来路。
	Character string `json:"character,omitempty"`
}

// ConversationStore 会话存储端口：整树存取（一会话一 JSON），写前留一代 .bak（D7）。
type ConversationStore interface {
	Save(ctx context.Context, c *conversation.Conversation) error
	Load(ctx context.Context, id conversation.ID) (*conversation.Conversation, error)
	List(ctx context.Context) ([]ConversationSummary, error)
	Remove(ctx context.Context, id conversation.ID) error
}
