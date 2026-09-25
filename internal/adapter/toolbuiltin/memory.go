package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// memoryList 列出记忆文档索引（全局 + 当前会话）。
type memoryList struct{ mem port.MemoryStore }

func (t *memoryList) Spec() tool.Spec {
	return tool.Spec{
		Name:        "memory_list",
		Description: "List the index of accessible memory documents (global memory and current-session memory), with each summary.",
		Schema:      jsonSchema(`{"type":"object","properties":{}}`),
		Risk:        tool.Safe,
	}
}

func (t *memoryList) Execute(ctx context.Context, _ tool.Call) (tool.Result, error) {
	entries, err := t.mem.Index(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("memory index: %w", err)
	}
	allowed := allowedDocNames(ctx)
	var b strings.Builder
	n := 0
	for _, e := range entries {
		if !allowed[e.Name] {
			continue
		}
		fmt.Fprintf(&b, "%s — %s\n", e.Name, e.Summary)
		n++
	}
	if n == 0 {
		return okResult("(no memory documents yet)"), nil
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// memoryRead 读取一篇记忆文档全文。
type memoryRead struct{ mem port.MemoryStore }

func (t *memoryRead) Spec() tool.Spec {
	return tool.Spec{
		Name:        "memory_read",
		Description: "Read a memory document in full by name. Use memory_list to see names (the global memories.md or the current session's <sessionID>.memory.md).",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "memory document name"}
			},
			"required": ["name"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *memoryRead) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	name, err := requireSessionDoc(ctx, a.Name)
	if err != nil {
		return tool.Result{}, err
	}
	doc, err := t.mem.Read(ctx, name)
	if err != nil {
		return tool.Result{}, fmt.Errorf("read memory %s: %w", name, err)
	}
	return okResult(doc.Content), nil
}

// memorySearch 关键词检索记忆（大小写不敏感）。
type memorySearch struct{ mem port.MemoryStore }

func (t *memorySearch) Spec() tool.Spec {
	return tool.Spec{
		Name:        "memory_search",
		Description: "Search accessible memories (global + current session) by keyword; returns document name, line number and snippet.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "search keyword"}
			},
			"required": ["query"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *memorySearch) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	hits, err := t.mem.Search(ctx, a.Query)
	if err != nil {
		return tool.Result{}, fmt.Errorf("search memory: %w", err)
	}
	allowed := allowedDocNames(ctx)
	var b strings.Builder
	n := 0
	for _, h := range hits {
		if !allowed[h.Name] {
			continue
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", h.Name, h.Line, h.Snippet)
		n++
	}
	if n == 0 {
		return okResult("(no hits)"), nil
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// memoryWrite 写入记忆文档（Risk=Confirm；路径格判定见 FileTarget，D22/D25）。
type memoryWrite struct {
	mem    port.MemoryStore
	pathOf PathOf
}

func (t *memoryWrite) Spec() tool.Spec {
	return tool.Spec{
		Name: "memory_write",
		Description: "Write a memory document (the global memories.md or the current session's memory). mode=append appends to the end (default); " +
			"overwrite replaces the whole document (memory_read first, then edit, then overwrite). Writing requests user confirmation.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "memory document name"},
				"content": {"type": "string", "description": "markdown content to write"},
				"mode": {"type": "string", "enum": ["append", "overwrite"], "description": "defaults to append"}
			},
			"required": ["name", "content"]
		}`),
		Risk: tool.Confirm,
	}
}

// Target 申报目标路径与写操作（D25：ToolRunner 据此查权限矩阵路径格）。
func (t *memoryWrite) Target(ctx context.Context, call tool.Call) (string, perm.Op, bool) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return "", 0, false
	}
	name, err := requireSessionDoc(ctx, a.Name)
	if err != nil {
		return "", 0, false
	}
	if t.pathOf == nil {
		return "", 0, false
	}
	path, ok := t.pathOf(name)
	if !ok {
		return "", 0, false
	}
	return path, perm.OpWrite, true
}

func (t *memoryWrite) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	var a struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	name, err := requireSessionDoc(ctx, a.Name)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Content == "" {
		return tool.Result{}, errors.New("content must not be empty")
	}
	mode := a.Mode
	if mode == "" {
		mode = "append"
	}
	content := a.Content
	switch mode {
	case "append":
		prev, err := t.mem.Read(ctx, name)
		if err == nil {
			content = strings.TrimRight(prev.Content, "\n") + "\n" + a.Content
		} else if !errors.Is(err, port.ErrMemoryNotFound) {
			return tool.Result{}, fmt.Errorf("read existing memory: %w", err)
		}
	case "overwrite":
		// 整篇覆盖
	default:
		return tool.Result{}, fmt.Errorf("unknown mode %q (append|overwrite)", mode)
	}
	if err := t.mem.Write(ctx, port.MemoryDoc{Name: name, Content: content}); err != nil {
		return tool.Result{}, fmt.Errorf("write memory: %w", err)
	}
	return okResult(fmt.Sprintf("wrote %s (%s, %d chars)", name, mode, len([]rune(content)))), nil
}
