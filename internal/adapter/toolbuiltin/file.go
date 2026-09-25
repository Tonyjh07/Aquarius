package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// maxFileRead 单文件读取上限（超出截断并标注；最终视图仍按 tool_output_chars 裁剪）。
const maxFileRead = 4 << 20 // 4MB

// 目录列举与搜索结果上限（防爆上下文）。
const (
	maxListEntries = 500
	maxSearchHits  = 200
)

// requireAbs 要求绝对路径（DESIGN：无 cwd 工作区概念，硬性规则 7）。
// sandbox 非空时附特权沙盒目录提示——相对路径多半是"想写进当前工作区"，
// 给调用模型一个立刻可用的绝对路径（审查/反馈：报错里缺少可行动的替代）。
func requireAbs(sandbox, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("缺少 path")
	}
	if !filepath.IsAbs(path) {
		msg := fmt.Sprintf("path 必须是绝对路径（本产品无工作区概念），当前为 %q", path)
		if sandbox != "" {
			msg += fmt.Sprintf("；可改用特权沙盒目录 %q（strict 起写入免确认）", sandbox)
		}
		return "", errors.New(msg)
	}
	return filepath.Clean(path), nil
}

// fileRead 读取文本文件。
type fileRead struct{ sandbox string }

func (t *fileRead) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_read",
		Description: "读取文本文件内容（绝对路径）。二进制文件会拒绝；超大文件截断到 4MB。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "绝对路径"}
			},
			"required": ["path"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *fileRead) Target(_ context.Context, call tool.Call) (string, perm.Op, bool) {
	p, ok := targetPath(call, t.sandbox)
	if !ok {
		return "", 0, false
	}
	return p, perm.OpRead, true
}

func (t *fileRead) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err // 入口即响应取消（DESIGN §10：工具执行同受 ctx 约束）
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	f, err := os.Open(p)
	if err != nil {
		return tool.Result{}, fmt.Errorf("打开文件: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileRead+1))
	if err != nil {
		return tool.Result{}, fmt.Errorf("读取文件: %w", err)
	}
	if len(data) > maxFileRead {
		data = data[:maxFileRead]
		return okResult(string(data) + "\n…[文件过大，仅读取前 4MB]"), nil
	}
	if isBinary(data) {
		return tool.Result{}, fmt.Errorf("%s 疑似二进制文件，file_read 只读文本", p)
	}
	return okResult(string(data)), nil
}

// isBinary 前 8KB 含 NUL 判定为二进制。
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// fileList 列目录。
type fileList struct{ sandbox string }

func (t *fileList) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_list",
		Description: "列出目录条目（绝对路径；目录名以 / 结尾）。最多返回 500 项。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "目录绝对路径"}
			},
			"required": ["path"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *fileList) Target(_ context.Context, call tool.Call) (string, perm.Op, bool) {
	p, ok := targetPath(call, t.sandbox)
	if !ok {
		return "", 0, false
	}
	return p, perm.OpRead, true
}

func (t *fileList) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return tool.Result{}, fmt.Errorf("列目录: %w", err)
	}
	var b strings.Builder
	for i, e := range entries {
		if i >= maxListEntries {
			fmt.Fprintf(&b, "…[共 %d 项，仅列出前 %d]", len(entries), maxListEntries)
			return okResult(b.String()), nil
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name + "\n")
	}
	if b.Len() == 0 {
		return okResult("（空目录）"), nil
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// fileSearch 按文件名子串递归搜索（大小写不敏感）。
type fileSearch struct{ sandbox string }

func (t *fileSearch) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_search",
		Description: "从指定目录递归按文件名子串搜索（大小写不敏感），返回匹配的绝对路径，最多 200 条。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "起搜目录绝对路径"},
				"query": {"type": "string", "description": "文件名子串"}
			},
			"required": ["path", "query"]
		}`),
		Risk: tool.Safe,
	}
}

func (t *fileSearch) Target(_ context.Context, call tool.Call) (string, perm.Op, bool) {
	p, ok := targetPath(call, t.sandbox)
	if !ok {
		return "", 0, false
	}
	return p, perm.OpRead, true
}

func (t *fileSearch) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	var a struct {
		Path  string `json:"path"`
		Query string `json:"query"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	q := strings.ToLower(strings.TrimSpace(a.Query))
	if q == "" {
		return tool.Result{}, errors.New("缺少 query")
	}
	var hits []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil // 无权限/悬空项跳过，不中断整体搜索
		}
		if !d.IsDir() && strings.Contains(strings.ToLower(d.Name()), q) {
			hits = append(hits, path)
			if len(hits) >= maxSearchHits {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("搜索: %w", err)
	}
	if len(hits) == 0 {
		return okResult("（无匹配）"), nil
	}
	sort.Strings(hits)
	out := strings.Join(hits, "\n")
	if len(hits) == maxSearchHits {
		out += fmt.Sprintf("\n…[命中达到上限 %d 条]", maxSearchHits)
	}
	return okResult(out), nil
}

// fileWrite 覆盖写文本文件（Risk=Confirm；路径格判定）。
type fileWrite struct{ sandbox string }

func (t *fileWrite) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_write",
		Description: "覆盖写入文本文件（绝对路径；父目录不存在会自动创建）。写入会请求用户确认。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "绝对路径"},
				"content": {"type": "string", "description": "文件全文（覆盖）"}
			},
			"required": ["path", "content"]
		}`),
		Risk: tool.Confirm,
	}
}

func (t *fileWrite) Target(_ context.Context, call tool.Call) (string, perm.Op, bool) {
	p, ok := targetPath(call, t.sandbox)
	if !ok {
		return "", 0, false
	}
	return p, perm.OpWrite, true
}

func (t *fileWrite) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return tool.Result{}, fmt.Errorf("创建父目录: %w", err)
	}
	// 唯一临时文件 + 原子换入：并发写互不踩踏，覆盖沿用既有权限位（§14 遗留）。
	if err := atomicfile.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return tool.Result{}, fmt.Errorf("写入: %w", err)
	}
	return okResult(fmt.Sprintf("已写入 %s（%d 字符）", p, len([]rune(a.Content)))), nil
}

// fileDelete 删除文件或空目录（Risk=Confirm；路径格判定）。
type fileDelete struct{ sandbox string }

func (t *fileDelete) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_delete",
		Description: "删除文件或空目录（绝对路径）。非空目录会被拒绝。删除会请求用户确认。",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "绝对路径"}
			},
			"required": ["path"]
		}`),
		Risk: tool.Confirm,
	}
}

func (t *fileDelete) Target(_ context.Context, call tool.Call) (string, perm.Op, bool) {
	p, ok := targetPath(call, t.sandbox)
	if !ok {
		return "", 0, false
	}
	return p, perm.OpWrite, true
}

func (t *fileDelete) Execute(ctx context.Context, call tool.Call) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return tool.Result{}, fmt.Errorf("%s 不存在", p)
		}
		return tool.Result{}, fmt.Errorf("删除（非空目录需先清空）: %w", err)
	}
	return okResult("已删除 " + p), nil
}
