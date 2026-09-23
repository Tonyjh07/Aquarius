package port

import (
	"context"
	"time"
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

// MemoryStore 记忆文档端口：markdown 即记忆、检索 = 关键词（D11）；后端可插拔。
type MemoryStore interface {
	Index(ctx context.Context) ([]MemoryIndexEntry, error)
	Read(ctx context.Context, name string) (MemoryDoc, error)
	Write(ctx context.Context, doc MemoryDoc) error
	Remove(ctx context.Context, name string) error
	Search(ctx context.Context, query string) ([]MemoryHit, error)
}
