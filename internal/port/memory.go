package port

import (
	"context"
	"errors"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// MemoryDoc 一篇 markdown 记忆文档。
type MemoryDoc struct {
	Name    string    `json:"name"`
	Content string    `json:"content"`
	ModTime time.Time `json:"mod_time"`
}

// MemoryIndexEntry 记忆索引项（注入 system prompt）。
type MemoryIndexEntry struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// MemoryHit 关键词检索命中。
type MemoryHit struct {
	Name    string `json:"name"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// ErrMemoryNotFound 记忆文档不存在（memoryfs 等实现共同包装本哨兵，供消费方 errors.Is 判定）。
var ErrMemoryNotFound = errors.New("memory doc not found")

// 全局/会话记忆文档名（D23 布局的端口侧约定，app 与 memoryfs 共同消费）。
const (
	GlobalMemoryDoc = "memories.md" // 全局记忆：<dataDir>/memories.md
	// SessionMemorySuffix 会话记忆后缀：<dataDir>/conversations/<会话ID><本后缀>。
	SessionMemorySuffix = ".memory.md"
)

// SessionMemoryDoc 返回会话 ID 对应的记忆文档名。
func SessionMemoryDoc(id conversation.ID) string { return string(id) + SessionMemorySuffix }

// MemoryStore 记忆文档端口：markdown 即记忆、检索 = 关键词（D11）；后端可插拔。
// name 寻址：全局 = GlobalMemoryDoc；会话 = SessionMemoryDoc(id)（D23）。
type MemoryStore interface {
	Index(ctx context.Context) ([]MemoryIndexEntry, error)
	Read(ctx context.Context, name string) (MemoryDoc, error)
	Write(ctx context.Context, doc MemoryDoc) error
	Remove(ctx context.Context, name string) error
	Search(ctx context.Context, query string) ([]MemoryHit, error)
}
