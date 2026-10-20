package toolbuiltin

import (
	"bufio"
	"bytes"
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
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// 单页读取预算（D169）：行数、字符、单行长度三重上限。
//
// 字符预算（16000）刻意略小于缺省的 tool_output_chars（20000），与 term_exec 的
// 保头尾预算同口径——这样 ToolRunner 的统一裁剪不会二次触发，页尾那条
// 「continue with offset」才不会被裁掉；分页是 file_read 唯一的截断方式。
const (
	maxReadLines     = 2000
	maxReadPageChars = 16000
	maxReadLineChars = 2000
)

// maxImageBytes 单张图片上限（D169）：超过就报错并给出实际大小，不静默降级成文本。
const maxImageBytes = 20 << 20

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

// fileRead 读取文本文件（按行分页），或把图片作为附件交给模型（D169）。
type fileRead struct {
	sandbox string
	blobs   port.AttachmentStore
	vision  bool
}

func (t *fileRead) Spec() tool.Spec {
	return tool.Spec{
		Name: "file_read",
		Description: fmt.Sprintf("Read a text file (absolute path), or an image which is attached to the conversation for you to look at "+
			"(png, jpeg, gif or webp, up to %s). Other binary files are rejected. "+
			"Text files are read in pages: offset is the 1-based first line, limit is how many lines to return "+
			"(default and maximum %d, one page is also capped at %d characters). Every returned line is prefixed with "+
			"its line number; the prefix is for orientation only and is not part of the file content. "+
			"When a page ends before the file does, the last line says which offset to continue from. "+
			"Reading never needs confirmation.", humanBytes(maxImageBytes), maxReadLines, maxReadPageChars),
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute path"},
				"offset": {"type": "integer", "description": "1-based line number to start from (default 1)"},
				"limit": {"type": "integer", "description": "maximum number of lines to return (default 2000)"}
			},
			"required": ["path"]
		}`),
		Risk: tool.Safe,
	}
}

// humanBytes 把字节上限写成描述里的可读单位（4MB / 512KB）：描述里的数字必须与常量同源，
// 手写数字在常量改动后不会报错，只会悄悄说谎（D171）。
func humanBytes(n int64) string {
	const mb = 1 << 20
	const kb = 1 << 10
	switch {
	case n >= mb && n%mb == 0:
		return fmt.Sprintf("%dMB", n/mb)
	case n >= kb && n%kb == 0:
		return fmt.Sprintf("%dKB", n/kb)
	default:
		return fmt.Sprintf("%dB", n)
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
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Offset < 0 {
		return tool.Result{}, fmt.Errorf("offset must be 1 or greater, got %d", a.Offset)
	}
	if a.Limit < 0 {
		return tool.Result{}, fmt.Errorf("limit must be positive, got %d", a.Limit)
	}
	offset := a.Offset
	if offset == 0 {
		offset = 1 // 缺省从第一行开始
	}
	limit := a.Limit
	if limit == 0 || limit > maxReadLines {
		limit = maxReadLines
	}

	f, err := os.Open(p)
	if err != nil {
		return tool.Result{}, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// 先嗅探头部再决定怎么读（D169）：图片、二进制与文本都在这里分流，
	// 不能先按大小截断再看——截断会把二进制切成一堆"看起来合法"的文本。
	head, err := sniffHead(f)
	if err != nil {
		return tool.Result{}, err
	}
	if mime := imageMIME(head); mime != "" {
		return t.readImage(ctx, f, p, mime)
	}
	if isBinary(head) {
		return tool.Result{}, fmt.Errorf("%s looks like a binary file; file_read only reads text", p)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return tool.Result{}, fmt.Errorf("rewind file: %w", err)
	}

	page, empty, err := readPage(f, offset, limit)
	if err != nil {
		return tool.Result{}, err
	}
	if empty {
		return okResult(fmt.Sprintf("(empty page: the file ends before line %d)", offset)), nil
	}
	return okResult(page), nil
}

// sniffHead 读前 8KB 用于二进制/媒体判定（不改文件偏移，调用方自行 Seek）。
func sniffHead(f *os.File) ([]byte, error) {
	head := make([]byte, 8192)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("read file head: %w", err)
	}
	return head[:n], nil
}

// imageMIME 按文件头魔数判定图片类型（D169）：只看内容，不看扩展名——
// 扩展名是用户随便写的，模型拿到"后缀像图片、内容不是"的文件只会更困惑。
// 返回空串 = 不是受支持的图片格式。
func imageMIME(head []byte) string {
	switch {
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		return "image/jpeg"
	case len(head) >= 6 && (bytes.Equal(head[:6], []byte("GIF87a")) || bytes.Equal(head[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// readImage 把图片写进附件库并回填引用（D169）。
//
// 字节不进 tool 结果 JSON：工具结果会落盘进会话树，图片按内容寻址单独存一份，
// 装配时再投影成紧随 tool 应答之后的一条 user 消息（OpenAI 兼容端点不接受
// tool 角色带图片块）。模型不支持看图时**明确报因**，不静默转成文本占位。
func (t *fileRead) readImage(ctx context.Context, f *os.File, path, mime string) (tool.Result, error) {
	if !t.vision {
		return tool.Result{}, fmt.Errorf("%s is an image; the current model does not accept image input", path)
	}
	if t.blobs == nil {
		return tool.Result{}, errors.New("image input is not available in this build")
	}
	fi, err := f.Stat()
	if err != nil {
		return tool.Result{}, fmt.Errorf("stat file: %w", err)
	}
	if fi.Size() > maxImageBytes {
		return tool.Result{}, fmt.Errorf("%s is %s; the image limit is %s", path, humanBytes(fi.Size()), humanBytes(maxImageBytes))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return tool.Result{}, fmt.Errorf("rewind file: %w", err)
	}
	ref, err := t.blobs.Put(ctx, f, mime, filepath.Base(path))
	if err != nil {
		return tool.Result{}, fmt.Errorf("store image: %w", err)
	}
	return tool.Result{
		OK:     true,
		Output: fmt.Sprintf("attached %s (%s, %s); the image itself is provided with this result.", filepath.Base(path), mime, humanBytes(ref.Size)),
		Images: []tool.ImageRef{{Hash: ref.Hash, MIME: ref.MIME, Name: ref.Name, Size: ref.Size}},
	}, nil
}

// readPage 从 1-based offset 开始读取至多 limit 行，返回带行号的页面。
//
// 三重上限：行数（limit）、整页字符（maxReadPageChars）、单行字符（maxReadLineChars）。
// 任何一档先到就收尾，并在页尾写一条续读指引——**这条指引必须留在页面里**，
// 被通用裁剪吃掉就等于把「只给开头、无法续读」重新装了回来。所以这里不统计总行数：
// 数一遍要把整个文件读完，而续读只需要知道下一页从哪一行开始。
func readPage(f *os.File, offset, limit int) (page string, empty bool, err error) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20) // 单行最长 1MB，超长行按扫描错误处理

	var b strings.Builder
	lineNo := 0
	start, end := 0, 0
	chars := 0
	more := false
	for sc.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		line := sc.Text()
		if r := []rune(line); len(r) > maxReadLineChars {
			line = string(r[:maxReadLineChars]) + fmt.Sprintf("...[line clamped at %d chars]", maxReadLineChars)
		}
		entries := len([]rune(line)) + len(itoa(lineNo)) + 2
		if start > 0 && (end-start+1 >= limit || chars+entries > maxReadPageChars) {
			more = true
			continue
		}
		b.WriteString(itoa(lineNo))
		b.WriteString(": ")
		b.WriteString(line)
		b.WriteString("\n")
		chars += entries
		if start == 0 {
			start = lineNo
		}
		end = lineNo
	}
	if err := sc.Err(); err != nil {
		return "", false, fmt.Errorf("read file: %w", err)
	}
	if start == 0 {
		return "", true, nil // 空文件，或 offset 已越过末行
	}
	if more {
		b.WriteString(fmt.Sprintf("...[truncated; continue with offset: %d]", end+1))
	}
	return strings.TrimRight(b.String(), "\n"), false, nil
}

// itoa 行号转十进制（热点路径，避免 fmt 的反射开销与分配）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
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
		Name: "file_list",
		Description: fmt.Sprintf("List directory entries (absolute path; directories suffixed with /). "+
			"Read in pages: offset is the 0-based index of the first entry, limit is how many to return "+
			"(default and maximum %d). When a page ends before the directory does, the last line says which offset to continue from.",
			maxListEntries),
		Schema: jsonSchema(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "absolute directory path"},
				"offset": {"type": "integer", "description": "0-based index of the first entry (default 0)"},
				"limit": {"type": "integer", "description": "maximum number of entries to return (default 500)"}
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
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeArgs(call, &a); err != nil {
		return tool.Result{}, err
	}
	p, err := requireAbs(t.sandbox, a.Path)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Offset < 0 {
		return tool.Result{}, fmt.Errorf("offset must be 0 or greater, got %d", a.Offset)
	}
	if a.Limit < 0 {
		return tool.Result{}, fmt.Errorf("limit must be positive, got %d", a.Limit)
	}
	limit := a.Limit
	if limit == 0 || limit > maxListEntries {
		limit = maxListEntries
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return tool.Result{}, fmt.Errorf("list dir: %w", err)
	}
	if len(entries) == 0 {
		return okResult(emptyEntries), nil
	}
	if a.Offset >= len(entries) {
		return okResult(fmt.Sprintf("(empty page: the directory has %d entries; offset %d is past the end)", len(entries), a.Offset)), nil
	}
	var b strings.Builder
	end := a.Offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	for _, e := range entries[a.Offset:end] {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name + "\n")
	}
	if end < len(entries) {
		fmt.Fprintf(&b, "...[truncated; continue with offset: %d]", end)
	}
	return okResult(strings.TrimRight(b.String(), "\n")), nil
}

// fileSearch 按文件名子串递归搜索（大小写不敏感）。
type fileSearch struct{ sandbox string }

func (t *fileSearch) Spec() tool.Spec {
	return tool.Spec{
		Name: "file_search",
		Description: fmt.Sprintf("Recursively search by filename substring (case-insensitive) from a directory; returns matching absolute paths, "+
			"at most %d. Start from a narrow directory: huge trees can hit the tool timeout.", maxSearchHits),
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
		return okResult(emptyMatches), nil
	}
	sort.Strings(hits)
	out := strings.Join(hits, "\n")
	if len(hits) == maxSearchHits {
		out += fmt.Sprintf("\n...[hit the limit of %d matches]", maxSearchHits)
	}
	return okResult(out), nil
}

// fileWrite 覆盖写文本文件（Risk=Confirm；路径格判定）。
type fileWrite struct{ sandbox string }

func (t *fileWrite) Spec() tool.Spec {
	return tool.Spec{
		Name:        "file_write",
		Description: "Overwrite a text file (absolute path; missing parent directories are created automatically). May require user confirmation depending on the permission level and the target path.",
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
		Description: "Delete a file or empty directory (absolute path). Non-empty directories are rejected. May require user confirmation depending on the permission level and the target path.",
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
