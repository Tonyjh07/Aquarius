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
		Description: "列出可访问的记忆文档索引（全局记忆与当前会话记忆），含每篇摘要。",
		Schema:      jsonSchema(`{"type":"object","properties":{}}`),
		Risk:        tool.Safe,
	}
}

func (t *memoryList) Execute(ctx context.Context, _ tool.Call) (tool.Result, error) {
	entries, err := t.mem.Index(ctx)
	if err != nil {
		return tool.Result{}, fmt.Errorf("记忆索引: %w", err)
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
		return okResult("（暂无记忆文档）"), nil
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// memoryRead 读取一篇记忆文档全文。
type memoryRead struct{ mem port.MemoryStore }

func (t *memoryRead) Spec() tool.Spec {
	return tool.Spec{
		Name:        "memory_read",
		Description: "按文档名读取记忆全文。名字用 memory_list 查看（全局 memories.md 或当前会话的 <会话ID>.memory.md）。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "记忆文档名"}
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
		return tool.Result{}, fmt.Errorf("读取记忆 %s: %w", name, err)
	}
	return okResult(doc.Content), nil
}

// memorySearch 关键词检索记忆（大小写不敏感）。
type memorySearch struct{ mem port.MemoryStore }

func (t *memorySearch) Spec() tool.Spec {
	return tool.Spec{
		Name:        "memory_search",
		Description: "在可访问的记忆（全局 + 当前会话）中按关键词检索，返回文档名、行号与片段。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "检索关键词"}
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
		return tool.Result{}, fmt.Errorf("检索记忆: %w", err)
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
		return okResult("（无命中）"), nil
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
		Description: "写入记忆文档（全局 memories.md 或当前会话记忆）。mode=append 追加到文末（缺省）；" +
			"overwrite 整篇覆盖（先 memory_read 再改再覆盖）。写入会请求用户确认。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "记忆文档名"},
				"content": {"type": "string", "description": "要写入的 markdown 内容"},
				"mode": {"type": "string", "enum": ["append", "overwrite"], "description": "缺省 append"}
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
		return tool.Result{}, errors.New("content 不可为空")
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
			return tool.Result{}, fmt.Errorf("读取既有记忆: %w", err)
		}
	case "overwrite":
		// 整篇覆盖
	default:
		return tool.Result{}, fmt.Errorf("未知 mode %q（append|overwrite）", mode)
	}
	if err := t.mem.Write(ctx, port.MemoryDoc{Name: name, Content: content}); err != nil {
		return tool.Result{}, fmt.Errorf("写入记忆: %w", err)
	}
	return okResult(fmt.Sprintf("已写入 %s（%s，%d 字符）", name, mode, len([]rune(content)))), nil
}
