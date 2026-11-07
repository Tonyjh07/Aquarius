package acpgate

// 申报 fs 之后的两个 handler（A2）：代理的读与写都经 Aquarius 过手。
//
// 为什么接：`Changed` 现在是**代理自称**（`tool_call_update` 里的 path），它不报就漏、
// 报错就脏，而那份清单是报告里最容易被当成事实的一段。读写过手之后，「写过哪些文件」
// 变成我们经手的事实，与它自称的分两段并列（D283）。
//
// 边界要说清：这不是沙箱。代理自己起 shell 之后写盘不经过这里，所以闸门的性质与
// `terminal`、根集那两条一致——**我们用来自拒的判据，不是内核围栏**。

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/Tonyjh07/Aquarius/internal/adapter/atomicfile"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	// maxReadBytes 单次读的上限：再大就不是「读一段文本」，而是把整块数据塞进代理的
	// 上下文。要读大文件的尾部/片段，协议本来就有 line/limit。
	maxReadBytes = 8 << 20
	// maxWriteBytes 单次写的上限（同上）。
	maxWriteBytes = 16 << 20
)

var errReadTooLarge = errors.New("read exceeds the size limit")

// ReadTextFile 替代理读一段文本（`fs/read_text_file`）。
//
// line/limit 是 **1 基行号切片**，不是字节偏移：只给 line = 读到尾；只给 limit = 从第一行起；
// 都不给 = 整个文件；limit <= 0 按「不限」处理（协议没定义 0 的含义，按最小惊讶取）。
// 行终止符原样保留（CRLF 不转 LF）：改写回来时逐字节对得上，才算没动过它没要求动的东西。
func (c *client) ReadTextFile(ctx context.Context, p acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	if err := c.fsGate(ctx, p.SessionId, p.Path); err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	resolved, root, err := c.r.rootSet().Locate(p.Path)
	if err != nil {
		// 解析不出来（链接环、权限）就说不清它要读哪儿 —— fail closed。
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, "cannot resolve the path: "+err.Error())
	}
	if root == "" && !c.allowOutside(ctx, opRead, resolved) {
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, reasonOutside)
	}
	f, err := os.Open(resolved)
	if err != nil {
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, "cannot open: "+err.Error())
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, "cannot stat: "+err.Error())
	}
	if st.IsDir() {
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, "it is a directory")
	}
	line, limit := 1, 0
	if p.Line != nil {
		line = *p.Line
	}
	if p.Limit != nil {
		limit = *p.Limit
	}
	content, err := readTextSlice(f, line, limit)
	switch {
	case errors.Is(err, errReadTooLarge):
		return acp.ReadTextFileResponse{}, fsDenied(p.Path,
			fmt.Sprintf("more than %d bytes were requested; use line and limit to read a part", maxReadBytes))
	case err != nil:
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, "read failed: "+err.Error())
	}
	if strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content) {
		// 二进制/非 UTF-8 不是「文本文件」：协议给的方法名就叫 read_text_file。
		return acp.ReadTextFileResponse{}, fsDenied(p.Path, "not a UTF-8 text file")
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

// WriteTextFile 替代理写一整份文本（`fs/write_text_file`）。
//
// 文件不存在必须创建（协议 MUST）；父目录不存在按「建」处理（协议没写，我们定成建，
// 用户文档写死）；按字节写、不做换行转换；已存在的文件沿用既有权限位（atomicfile 保证）。
func (c *client) WriteTextFile(ctx context.Context, p acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if err := c.fsGate(ctx, p.SessionId, p.Path); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	if len(p.Content) > maxWriteBytes {
		return acp.WriteTextFileResponse{}, fsDenied(p.Path,
			fmt.Sprintf("content is too large (%d bytes, at most %d)", len(p.Content), maxWriteBytes))
	}
	resolved, root, err := c.r.rootSet().Locate(p.Path)
	if err != nil {
		return acp.WriteTextFileResponse{}, fsDenied(p.Path, "cannot resolve the path: "+err.Error())
	}
	if root == "" && !c.allowOutside(ctx, opWrite, resolved) {
		return acp.WriteTextFileResponse{}, fsDenied(p.Path, reasonOutside)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return acp.WriteTextFileResponse{}, fsDenied(p.Path, "cannot create the parent directory: "+err.Error())
	}
	if err := atomicfile.WriteFile(resolved, []byte(p.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, fsDenied(p.Path, "write failed: "+err.Error())
	}
	c.r.addWrote(resolved)
	return acp.WriteTextFileResponse{}, nil
}

// fsGate fs 请求的共同前置：转交正在停 → 取消；会话不是本次的 → 参数错；
// 相对路径 → 参数错（相对谁？子进程的 cwd 与我们的进程 cwd 不是一回事，猜错就是写到别处）。
func (c *client) fsGate(ctx context.Context, sid acp.SessionId, path string) error {
	if c.r.stopKind() != stopNone || ctx.Err() != nil {
		return acp.NewRequestCancelled(map[string]any{"error": "the delegation is stopping"})
	}
	if cur := c.r.sessionID(); cur == "" || string(sid) != cur {
		return acp.NewInvalidParams(map[string]any{"error": "sessionId does not belong to this delegation"})
	}
	if !port.AbsPath(path) {
		return acp.NewInvalidParams(map[string]any{"error": "path must be absolute", "path": path})
	}
	return nil
}

// opRead / opWrite 越界处置里的操作名（确认句与报告都用它）。
const (
	opRead  = "read"
	opWrite = "write"
)

// reasonOutside 越界拒绝的原因（同一句用在读与写的拒绝里）。
const reasonOutside = "outside the session root set"

// fsDenied 我们自己拒掉的 fs 请求。协议没有「权限不足」这个码，选 -32603 是因为它
// 既不是参数错也不是方法不存在——原因写在 message 里，代理能读懂也能换个做法。
func fsDenied(path, reason string) *acp.RequestError {
	return &acp.RequestError{Code: -32603, Message: reason, Data: map[string]any{"path": path}}
}

// allowOutside 越界访问的处置：现在一律拒（下一笔接确认与次数上限）。
//
// 拒绝是 fail closed 的那一侧；但别把这条判据说成围栏——代理自己起 shell 之后，
// 这条路根本不经过我们。
func (c *client) allowOutside(_ context.Context, op, resolved string) bool {
	c.r.note("%s outside the session root set was refused: %s", op, resolved)
	return false
}

// readTextSlice 按 1 基行号切片读文本。流式推进：越界的那几行只丢弃不加载，
// 所以「从第 10 万行读 3 行」不会先把整个文件读进内存。
func readTextSlice(f *os.File, line, limit int) (string, error) {
	if line < 1 {
		line = 1
	}
	stop := 0
	if limit > 0 {
		stop = line + limit - 1
	}
	br := bufio.NewReaderSize(f, 64<<10)
	var b strings.Builder
	n := 0
	for {
		chunk, err := br.ReadString('\n')
		if chunk != "" {
			n++
			if n >= line {
				if stop > 0 && n > stop {
					break
				}
				if b.Len()+len(chunk) > maxReadBytes {
					return "", errReadTooLarge
				}
				b.WriteString(chunk)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// addWrote 记一次**经我们之手**写下的文件（去重；超出上限只记数）。
func (r *run) addWrote(path string) {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	if r.wroteSeen[path] {
		return
	}
	r.wroteSeen[path] = true
	if len(r.wrote) >= port.MaxDelegateChangedFiles {
		r.wroteOver++
		return
	}
	r.wrote = append(r.wrote, path)
}
