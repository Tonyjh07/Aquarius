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
	"github.com/Tonyjh07/Aquarius/internal/i18n"
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
	if root == "" {
		if ok, why := c.allowRead(ctx, resolved); !ok {
			return acp.ReadTextFileResponse{}, fsDenied(p.Path, why)
		}
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
	// 控制文件与越界写都要问；控制文件优先（它更具体，且范围之内也会命中）。
	if rule := executionControl(resolved); rule != "" || root == "" {
		if ok, why := c.allowWrite(ctx, p.Path, resolved, rule); !ok {
			return acp.WriteTextFileResponse{}, fsDenied(p.Path, why)
		}
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

// reasonOutside 越界拒绝的原因（同一句用在读与写的拒绝里）。
const reasonOutside = "outside the session root set"

// reasonReadLimit 次数用完（或配置成不问）时的原因：说清是「额度」而不是「范围」，
// 代理换个做法（少读几次、或者请用户改配置）才有意义。
const reasonReadLimit = "the out-of-scope read limit for this delegation is used up"

// fsDenied 我们自己拒掉的 fs 请求。协议没有「权限不足」这个码，选 -32603 是因为它
// 既不是参数错也不是方法不存在——原因写在 message 里，代理能读懂也能换个做法。
func fsDenied(path, reason string) *acp.RequestError {
	return &acp.RequestError{Code: -32603, Message: reason, Data: map[string]any{"path": path}}
}

// allowOutside 越界访问的处置（D284）：读按次数问（默认 8 次），写每次都问。
//
// 拒绝落在 fail closed 那一侧；但别把这条判据说成围栏——代理自己起 shell 之后，
// 这条路根本不经过我们。
func (c *client) allowOutside(_ context.Context, op, resolved string) bool {
	c.r.note("%s outside the session root set was refused: %s", op, resolved)
	return false
}

// askUser 越界访问与控制文件的确认：与权限回落（RequestPermission）同一套口径——
// 无人值守、用户不在发起这次转交的会话、没有配置确认器，一律当场拒。
func (c *client) askUser(ctx context.Context, prompt string) (bool, string) {
	if c.r.stopKind() != stopNone || ctx.Err() != nil {
		return false, "the delegation is stopping"
	}
	if c.m.opts.CurrentSession != nil && c.r.session != "" && c.m.opts.CurrentSession() != "" &&
		c.m.opts.CurrentSession() != c.r.session {
		return false, i18n.T("acp.deny.not_in_session")
	}
	if c.m.opts.Confirmer == nil {
		return false, i18n.T("acp.deny.no_confirmer")
	}
	ans, err := c.m.opts.Confirmer.Confirm(ctx, prompt)
	switch {
	case err != nil:
		return false, err.Error()
	case ans.Allow:
		return true, ""
	}
	if reason := strings.TrimSpace(ans.Reason); reason != "" {
		return false, reason
	}
	return false, i18n.T("acp.deny.user_refused")
}

// allowRead 一次越界读的处置：同一路径只问一次（扫目录会连着问同一件事），
// 次数用完或配置成「不问」时直接拒并留档。
func (c *client) allowRead(ctx context.Context, resolved string) (bool, string) {
	if allowed, reason, asked := c.r.outsideVerdict(resolved); asked {
		if allowed {
			return true, ""
		}
		return false, reason
	}
	limit := c.m.outsideReadAsks()
	switch {
	case limit == 0:
		reason := reasonReadLimit + ": delegate.outside_read_max is 0"
		c.r.markOutsideRead(resolved, false, reason)
		return false, reason
	case limit > 0 && c.r.outsideAsksUsed() >= limit:
		reason := fmt.Sprintf("%s (%d)", reasonReadLimit, limit)
		c.r.markOutsideRead(resolved, false, reason)
		return false, reason
	}
	c.r.countOutsideAsk()
	prompt := i18n.Tf("acp.confirm.read_outside", "agent", c.r.agent.Name, "path", resolved)
	if ok, why := c.askUser(ctx, prompt); !ok {
		reason := reasonOutside + ": " + why
		c.r.markOutsideRead(resolved, false, reason)
		return false, reason
	}
	c.r.markOutsideRead(resolved, true, "")
	return true, ""
}

// allowWrite 一次越界写或控制文件写的处置：每次都问，不缓存。
func (c *client) allowWrite(ctx context.Context, path, resolved, rule string) (bool, string) {
	var prompt string
	if rule != "" {
		prompt = i18n.Tf("acp.confirm.control_file", "agent", c.r.agent.Name, "rule", rule, "path", resolved)
	} else {
		prompt = i18n.Tf("acp.confirm.write_outside", "agent", c.r.agent.Name, "path", resolved)
	}
	if ok, why := c.askUser(ctx, prompt); !ok {
		// 路径进 note 是为了让报告里能读出「它想改哪儿」，而不是只有一句「被拒了」。
		c.r.note("write was refused (%s): %s", path, why)
		if rule != "" {
			return false, "writing an execution-control file was refused: " + why
		}
		return false, reasonOutside + ": " + why
	}
	return true, ""
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

// outsideVerdict 这个路径的越界读结论：ok / 已拒（带原因）/ 没问过。
func (r *run) outsideVerdict(path string) (allowed bool, denyReason string, asked bool) {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	if r.outsideReadOK[path] {
		return true, "", true
	}
	if reason, no := r.outsideReadNo[path]; no {
		return false, reason, true
	}
	return false, "", false
}

// outsideAsksUsed 已消耗的越界读额度（放行与拒绝都算：问过就是一次）。
func (r *run) outsideAsksUsed() int {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	return r.outsideReadsAsked
}

// countOutsideAsk 记一次「问过了」：额度按问的次数算，不按放行的次数算。
func (r *run) countOutsideAsk() {
	r.cmu.Lock()
	r.outsideReadsAsked++
	r.cmu.Unlock()
}

// markOutsideRead 记一次越界读的结论。放行的进报告清单，拒掉的留一条用户面备注
// ——同一路径只记一次，免得扫目录时同一句话刷满整份报告。
func (r *run) markOutsideRead(path string, allowed bool, denyReason string) {
	r.cmu.Lock()
	defer r.cmu.Unlock()
	if allowed {
		r.outsideReadOK[path] = true
		if len(r.outsideRead) >= port.MaxDelegateChangedFiles {
			r.outsideReadOver++
			return
		}
		r.outsideRead = append(r.outsideRead, path)
		return
	}
	if _, seen := r.outsideReadNo[path]; !seen {
		r.outsideReadNo[path] = denyReason
		r.notes = append(r.notes, fmt.Sprintf("read outside the session root set was refused: %s", path))
	}
}
