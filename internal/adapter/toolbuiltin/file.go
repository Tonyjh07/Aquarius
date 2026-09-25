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
		return "", errors.New("missing path")
	}
	if !filepath.IsAbs(path) {
		msg := fmt.Sprintf("path must be absolute (this product has no workspace concept), got %q", path)
		// 只在沙盒本身是绝对路径时提示——不能建议一个会被同一条规则再次
		// 拒绝的相对路径（嵌入方可能没走装配根的路径锚定）。
		if filepath.IsAbs(sandbox) {
			msg += fmt.Sprintf("; hint: use the privileged sandbox directory %q instead (writes there skip confirmation from strict upward)", sandbox)
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
		Description: "Read a text file (absolute path). Binary files are rejected; files larger than 4MB are truncated.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute path"}
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
		return tool.Result{}, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileRead+1))
	if err != nil {
		return tool.Result{}, fmt.Errorf("read file: %w", err)
	}
	if len(data) > maxFileRead {
		data = data[:maxFileRead]
		return okResult(string(data) + "\n…[file too large, only the first 4MB was read]"), nil
	}
	if isBinary(data) {
		return tool.Result{}, fmt.Errorf("%s looks like a binary file; file_read only reads text", p)
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
		Description: "List directory entries (absolute path; directories suffixed with /). At most 500 entries are returned.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute directory path"}
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
		return tool.Result{}, fmt.Errorf("list dir: %w", err)
	}
	var b strings.Builder
	for i, e := range entries {
		if i >= maxListEntries {
			fmt.Fprintf(&b, "…[total %d entries, showing the first %d]", len(entries), maxListEntries)
			return okResult(b.String()), nil
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name + "\n")
	}
	if b.Len() == 0 {
		return okResult("(empty directory)"), nil
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// fileSearch 按文件名子串递归搜索（大小写不敏感）。
type fileSearch struct{ sandbox string }

func (t *fileSearch) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_search",
		Description: "Recursively search by filename substring (case-insensitive) from a directory; returns matching absolute paths, at most 200. Start from a narrow directory — huge trees can hit the tool timeout.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute path of the directory to start from"},
				"query": {"type": "string", "description": "filename substring"}
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
		return tool.Result{}, errors.New("missing query")
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
		return tool.Result{}, fmt.Errorf("search: %w", err)
	}
	if len(hits) == 0 {
		return okResult("(no matches)"), nil
	}
	sort.Strings(hits)
	out := strings.Join(hits, "\n")
	if len(hits) == maxSearchHits {
		out += fmt.Sprintf("\n…[hit the limit of %d matches]", maxSearchHits)
	}
	return okResult(out), nil
}

// fileWrite 覆盖写文本文件（Risk=Confirm；路径格判定）。
type fileWrite struct{ sandbox string }

func (t *fileWrite) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_write",
		Description: "Overwrite a text file (absolute path; missing parent directories are created automatically). Writing requests user confirmation.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute path"},
				"content": {"type": "string", "description": "full file content (overwritten)"}
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
		return tool.Result{}, fmt.Errorf("create parent dir: %w", err)
	}
	// 唯一临时文件 + 原子换入：并发写互不踩踏，覆盖沿用既有权限位（§14 遗留）。
	if err := atomicfile.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return tool.Result{}, fmt.Errorf("write: %w", err)
	}
	return okResult(fmt.Sprintf("wrote %s (%d chars)", p, len([]rune(a.Content)))), nil
}

// fileDelete 删除文件或空目录（Risk=Confirm；路径格判定）。
type fileDelete struct{ sandbox string }

func (t *fileDelete) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_delete",
		Description: "Delete a file or empty directory (absolute path). Non-empty directories are rejected. Deleting requests user confirmation.",
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute path"}
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
			return tool.Result{}, fmt.Errorf("%s does not exist", p)
		}
		return tool.Result{}, fmt.Errorf("delete (a non-empty directory must be emptied first): %w", err)
	}
	return okResult("deleted " + p), nil
}
