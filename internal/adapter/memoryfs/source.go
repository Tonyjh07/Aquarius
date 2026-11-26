package memoryfs

import (
	"context"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// source.go 内置文档记忆的注入面（port.MemorySource）：把两篇 markdown 拼成本轮要
// 注入的块。这块形状（索引只取哪两篇、会话全文怎么放）属于内置实现，不是内核的知识
// ——插件接管记忆后换掉的就是这个实现（D337）。

var _ port.MemorySource = Source{}

// Source 内置文档记忆的注入面。零值不可用；用 NewSource 构造。
type Source struct{ store port.MemoryStore }

// NewSource 包装一个文档存储（memoryfs.Store 或它上面的合并层）。
func NewSource(store port.MemoryStore) Source { return Source{store: store} }

// 内置块的小标题（大标题由宿主加）。不点名具体工具：工具可被单独禁用，
// 写死名字会变成一句指向不存在能力的指引（D171）。
const sessionHeading = "\n\n## Session memory (this conversation's notepad)\n"

// Recall 索引（只含全局 + 当前会话两份，D23 名字白名单）+ 当前会话全文（水位无关：
// 压缩/回溯后仍在）。两段任一读取失败都跳过该段：记忆坏了不该挡住对话。
func (s Source) Recall(ctx context.Context, id conversation.ID, _ int) (port.MemoryBlock, error) {
	if s.store == nil {
		return port.MemoryBlock{}, nil
	}
	var b strings.Builder
	var parts []string
	if entries, err := s.store.Index(ctx); err == nil {
		want := map[string]bool{port.GlobalMemoryDoc: true, port.SessionMemoryDoc(id): true}
		var lines []string
		for _, e := range entries {
			if want[e.Name] {
				lines = append(lines, "- "+e.Name+": "+e.Summary)
			}
		}
		if len(lines) > 0 {
			sort.Strings(lines) // 确定性（索引顺序不承诺）
			b.WriteString("\n\n")
			b.WriteString(strings.Join(lines, "\n"))
			parts = append(parts, "index")
		}
	}
	if doc, err := s.store.Read(ctx, port.SessionMemoryDoc(id)); err == nil {
		if strings.TrimSpace(doc.Content) != "" {
			b.WriteString(sessionHeading)
			b.WriteString(doc.Content)
			parts = append(parts, "session")
		}
	}
	return port.MemoryBlock{Text: b.String(), Detail: strings.Join(parts, "+")}, nil
}
