package port

import (
	"context"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// Clock 时间源（测试可注入固定时钟）。
type Clock interface {
	Now() time.Time
}

// IDGen 标识生成器（ULID）；测试可注入确定性实现。
type IDGen interface {
	ConversationID() conversation.ID
	MessageID() conversation.MessageID
	CallID() tool.CallID
}

// Secrets 按名取用密钥；返回值不进会话树、不进上下文、不落日志（DESIGN §9）。
type Secrets interface {
	Get(ctx context.Context, name string) (string, error)
}
