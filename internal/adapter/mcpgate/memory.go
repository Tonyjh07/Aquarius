package mcpgate

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// resourceStore resources 只读投影（§6.3：资源进上下文索引，写仍走自有记忆——
// 与记忆系统的边界见 DESIGN §3）。name 寻址用资源 URI。
type resourceStore struct {
	s *Server
}

var _ port.MemoryStore = resourceStore{}

// Index 逐项列出资源：Name = URI，Summary = 名称 + 描述 + MIME。
func (r resourceStore) Index(ctx context.Context) ([]port.MemoryIndexEntry, error) {
	list, err := r.s.cs.ListResources(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("resources/list（插件 %s）: %w", r.s.name, err)
	}
	out := make([]port.MemoryIndexEntry, 0, len(list.Resources))
	for _, res := range list.Resources {
		if res.URI == "" {
			continue
		}
		summary := res.Name
		if summary == "" {
			summary = res.URI
		}
		if res.Description != "" {
			summary += " — " + res.Description
		}
		if res.MIMEType != "" {
			summary += "（" + res.MIMEType + "）"
		}
		out = append(out, port.MemoryIndexEntry{Name: res.URI, Summary: summary})
	}
	return out, nil
}

// Read 按 URI 取文本；二进制内容拒绝（v1 只内联文本，同 D10 口径）。
func (r resourceStore) Read(ctx context.Context, name string) (port.MemoryDoc, error) {
	res, err := r.s.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: name})
	if err != nil {
		return port.MemoryDoc{}, fmt.Errorf("read resource %s (plugin %s): %w", name, r.s.name, err)
	}
	if len(res.Contents) == 0 {
		return port.MemoryDoc{}, fmt.Errorf("resource %s has no content: %w", name, port.ErrMemoryNotFound)
	}
	var b strings.Builder
	for _, c := range res.Contents {
		if c == nil || len(c.Blob) > 0 {
			return port.MemoryDoc{}, fmt.Errorf("resource %s contains binary content (v1 reads text only)", name)
		}
		// 空文本是合法内容（审查修复：曾与二进制一并拒收）。
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(c.Text)
	}
	return port.MemoryDoc{Name: name, Content: b.String()}, nil
}

// Write 只读投影：拒绝（§6.3 写走自有记忆）。
func (r resourceStore) Write(context.Context, port.MemoryDoc) error {
	return fmt.Errorf("MCP resources (plugin %s) are a read-only projection; write to your own memory instead (DESIGN 6.3)", r.s.name)
}

// Remove 只读投影：拒绝。
func (r resourceStore) Remove(context.Context, string) error {
	return fmt.Errorf("MCP resources (plugin %s) are a read-only projection; delete from your own memory instead (DESIGN 6.3)", r.s.name)
}

// Search 关键词检索：对索引的 URI 与 Summary 做包含匹配（内容级检索先 Read 再查，
// 个人规模资源量小，不引入倒排/缓存）。
func (r resourceStore) Search(ctx context.Context, query string) ([]port.MemoryHit, error) {
	entries, err := r.Index(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	var hits []port.MemoryHit
	for _, e := range entries {
		hay := strings.ToLower(e.Name + "\n" + e.Summary)
		if idx := strings.Index(hay, q); idx >= 0 {
			hits = append(hits, port.MemoryHit{Name: e.Name, Line: 0, Snippet: e.Summary})
		}
	}
	return hits, nil
}
