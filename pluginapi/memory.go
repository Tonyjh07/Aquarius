package pluginapi

import "context"

// memory.go 记忆供给面（plugin-design.md §4）：换掉记忆后端，或接一个向量检索。
//
// 寻址只有一个维度——文档名。全局记忆是一个固定名字的文档，会话记忆按会话 ID 派生，
// 这两条约定留在内核；插件只按名字读写，不需要知道谁是谁。

// MemoryStore 记忆文档后端。索引、读取、写入、删除、检索五件事构成完整替换面。
// 兼容承诺：改方法集会破坏所有声明 provides: memory 的插件，以及记忆装配的转换。
type MemoryStore interface {
	// Index 列出进上下文的索引条目（名字 + 摘要）。顺序自由，内核按名字归组。
	Index(ctx context.Context) ([]MemoryIndexEntry, error)
	// Read 读一篇文档。不存在时返回的错误必须能被 errors.Is(err, ErrMemoryNotFound) 命中。
	Read(ctx context.Context, name string) (MemoryDoc, error)
	// Write 整篇覆盖写入（内容是 markdown 原文，插件不解读）。
	Write(ctx context.Context, doc MemoryDoc) error
	// Remove 删除一篇文档；不存在不算错误（删除是幂等的）。
	Remove(ctx context.Context, name string) error
	// Search 关键词检索，返回命中行号与片段。空查询 = 无命中，不是错误。
	Search(ctx context.Context, query string) ([]MemoryHit, error)
}
