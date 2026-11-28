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

// MemoryBlock 一次注入的记忆块（D337）：Text 原样进上下文的首条 system，宿主不解读、
// 不重排；标题由宿主加。Detail 是段标记的细目（`/context` 视图与排错用），
// 例如内置实现的 `index+session` 或接管插件的 `plugin:<name>`。
type MemoryBlock struct {
	Text   string
	Detail string
}

// MemoryQuery 一次注入请求（D345）：软预算与「当轮模型面有没有工具」。两个事实出自
// 同一次装配，打包成请求而不是让实现方各自猜——预算决定裁剪，工具信号决定插件回引导
// 还是回正文。
type MemoryQuery struct {
	// BudgetTokens 本轮记忆段的软预算（0 = 不给建议）；实现方应自己裁剪，超出由宿主硬截断。
	BudgetTokens int
	// ToolsAvailable 当轮模型面是否带工具声明。false = 模型看不见任何工具，注入引导句
	// 没有意义，实现方应直接给正文。
	ToolsAvailable bool
}

// MemorySource 记忆的唯一挂载点（D337）：每轮装配时问一次「这次要注入什么」。
//
// 回答者由装配根决定：内置文档记忆，或接管记忆的插件。宿主只认这个方法，
// 因而不需要知道记忆存在哪、怎么组织——那是回答者自己的事。
type MemorySource interface {
	Recall(ctx context.Context, id conversation.ID, query MemoryQuery) (MemoryBlock, error)
}

// MemoryStore 记忆文档端口：markdown 即记忆、检索 = 关键词（D11）；后端可插拔。
// name 寻址：全局 = GlobalMemoryDoc；会话 = SessionMemoryDoc(id)（D23）。
type MemoryStore interface {
	Index(ctx context.Context) ([]MemoryIndexEntry, error)
	Read(ctx context.Context, name string) (MemoryDoc, error)
	Write(ctx context.Context, doc MemoryDoc) error
	Remove(ctx context.Context, name string) error
	Search(ctx context.Context, query string) ([]MemoryHit, error)
}
