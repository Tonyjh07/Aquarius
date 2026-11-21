package exchange

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// deepseek.go DeepSeek 网页端「导出账号下全部会话」的整库容器（D321，只认不写）：
//
//	deepseek_data-YYYY-MM-DD.zip
//	├─ manifest.json                     {"export_format_version":3}
//	├─ chat_sessions/<uuid>.json         会话（扁平消息表 + parent_id）
//	├─ chat_sessions/<uuid>_files/<id>_<原名>  附件字节（只有图片带）
//	└─ chat_session_index.json           清单：id / title / message_count / 时间
//
// user.json（账号资料）不读、不落库、不进日志。两条按代码写死的规则：
//   - **顺序只认 parent_id 与 fragments 数组序**。assistant 的 inserted_at 比与之对应的 user
//     消息早几毫秒（生成前先落占位），按时间排会把整棵树排反。
//   - **多根 = 首条消息的版本组**（首条被反复编辑）：全部挂 Root 之下做兄弟，materialize
//     的现有规则天然接得住，不另造分支。
//
// 识别必须走路径（DetectPath）：ZIP 的条目名在中央目录（文件末尾），64 KB 文件头里只有
// PK 魔数，与 aqx 无法区分。Import 对 io.ReaderAt 断言成功即流式解包（整包不进内存），
// 失败回落读内存老路——测试里的 bytes.Reader 走后者。

// deepseek 导入侧上限（§5.16：超限报错，不静默截断）。
const (
	// maxDSEntries 清单条目上限：官方导出全库一个包，给个防呆量级即可。
	maxDSEntries = 10000
	// maxDSIndexBytes 清单上限（2274 条的实测 < 1 MB，超了说明不是官方导出）。
	maxDSIndexBytes = 8 << 20
	// maxDSSessionBytes 单个会话 JSON 上限（实测最大 3.6 MB，64 MB 已是数百倍余量）。
	maxDSSessionBytes = 64 << 20
	// maxDSFallbackBytes 非流式回落路径的包体上限（ReaderAt 拿不到时整包读进内存；377 MB
	// 的官方全量包在流式路径下不受此限）。
	maxDSFallbackBytes = 1 << 30
	// maxDSBlobBytes / maxDSBlobTotalBytes 单附件与单会话附件总量（与 aqx 同一套量级）。
	maxDSBlobBytes      = 32 << 20
	maxDSBlobTotalBytes = 512 << 20
)

// dsManifest 容器层：只认版本号，其余字段不读。
type dsManifest struct {
	ExportFormatVersion int `json:"export_format_version"`
}

// dsIndexEntry 清单的一项：只给展示与选择，导入以会话 JSON 为准。
type dsIndexEntry struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Pinned       bool   `json:"pinned"`
	MessageCount int    `json:"message_count"`
	InsertedAt   string `json:"inserted_at"`
	UpdatedAt    string `json:"updated_at"`
}

// dsSession 一个会话。
type dsSession struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	CurrentMessage int         `json:"current_message_id"`
	InsertedAt     string      `json:"inserted_at"`
	UpdatedAt      string      `json:"updated_at"`
	Messages       []dsMessage `json:"messages"`
}

// dsMessage 一条消息：ParentID 为空 = 根（首条消息的某个编辑版本）。
type dsMessage struct {
	ID         int          `json:"id"`
	ParentID   *int         `json:"parent_id"`
	Role       string       `json:"role"`
	Status     string       `json:"status"`
	InsertedAt string       `json:"inserted_at"`
	Fragments  []dsFragment `json:"fragments"`
}

// dsFragment 一个分片：content 是文本类分片的正文，files 是 FILE 分片的附件数组。
type dsFragment struct {
	Type    string   `json:"type"`
	Content string   `json:"content,omitempty"`
	Files   []dsFile `json:"files,omitempty"`
}

// dsFile 附件：只有 is_image 且 export_path 非空的才在包里有字节。
type dsFile struct {
	ID         string `json:"id"`
	FileName   string `json:"file_name"`
	FileSize   int64  `json:"file_size"`
	ExportPath string `json:"export_path"`
	IsImage    bool   `json:"is_image"`
}

// DetectPath 按路径认 deepseek 的整库容器（D321）。
//
// ZIP 的条目名在中央目录（文件末尾），文件头只有 PK 魔数——与 aqx 无法区分，所以这一步
// 必须拿路径开 ReaderAt 读中央目录。判据两条同时成立：manifest.json 里 export_format_version
// > 0，且有 chat_sessions/*.json 条目。aqx 的 manifest.json 没有 export_format_version，
// 天然不撞。
func (e *Exchange) DetectPath(path string) (port.ExchangeFormat, bool) {
	f, err := os.Open(path)
	if err != nil {
		return port.ExchangeFormat{}, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return port.ExchangeFormat{}, false
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return port.ExchangeFormat{}, false
	}
	manifestOK, sessionsOK := false, false
	for _, zf := range zr.File {
		name := strings.ReplaceAll(zf.Name, `\`, "/")
		if !manifestOK && name == "manifest.json" {
			if m := dsPeekManifest(zf); m != nil && m.ExportFormatVersion > 0 {
				manifestOK = true
			}
		}
		if strings.HasPrefix(name, "chat_sessions/") && strings.HasSuffix(name, ".json") {
			sessionsOK = true
		}
		if manifestOK && sessionsOK {
			return formatNamed("deepseek")
		}
	}
	return port.ExchangeFormat{}, false
}

// dsPeekManifest 读 manifest.json 的前几 KB 解版本号；坏 JSON / 缺键都返回 nil（这里只判形状）。
func dsPeekManifest(f *zip.File) *dsManifest {
	body, err := readZipLimited(f, 4<<10, f.Name)
	if err != nil {
		return nil
	}
	var m dsManifest
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	return &m
}

// dsDeepSeekZip 打开容器：ReaderAt+Seeker 走流式（官方全量包 377 MB 不进内存），否则
// 整包读进内存（测试替身与无 Seek 的读侧；给个防呆上限）。
func dsOpenZip(r io.Reader) (*zip.Reader, func() error, error) {
	type sized interface {
		io.ReaderAt
		io.Seeker
	}
	if s, ok := r.(sized); ok {
		size, err := s.Seek(0, io.SeekEnd)
		if err == nil {
			if _, err := s.Seek(0, io.SeekStart); err == nil {
				zr, zerr := zip.NewReader(s, size)
				return zr, func() error { return nil }, zerr
			}
		}
		// Seek 失败继续走内存回落，不让通路断在这里。
	}
	data, err := readLimited(r, maxDSFallbackBytes, i18n.T("exchange.io.what.archive"))
	if err != nil {
		return nil, nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", i18n.T("exchange.err.not_archive"), err)
	}
	return zr, func() error { return nil }, nil
}

// dsZipIndex 条目名 → 条目（deepseek 的包按名字取，不做 aqx 那套结构校验——第三方形状
// 不是我们的规范，识别已经过了 DetectPath，条目缺失按「清单有、文件缺」逐条报）。
func dsZipIndex(zr *zip.Reader) map[string]*zip.File {
	out := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		name, err := safeArchivePath(f.Name)
		if err != nil || name == "" {
			continue // zip-slip 形状的条目不取：导入只按清单里的名字拿文件
		}
		out[name] = f
	}
	return out
}

// dsSortedIndex 清单按 updated_at 倒序（与网页端一致）：清单序即 --pick 的序号。
func dsSortedIndex(items []dsIndexEntry) []dsIndexEntry {
	out := append([]dsIndexEntry(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := dsTime(out[i].UpdatedAt), dsTime(out[j].UpdatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].ID < out[j].ID // 同刻给个稳定序（同一份文件两次清单相同）
	})
	return out
}

// dsTime RFC3339（官方导出的形状）；解析失败返回零值，排序时沉底。
func dsTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// dsIndexEntries 清单 → aqx 同款的 manifestEntry（resolvePick / 错误清单共用一份）。
func dsIndexEntries(items []dsIndexEntry) []manifestEntry {
	out := make([]manifestEntry, len(items))
	for i, e := range items {
		out[i] = manifestEntry{ID: e.ID, Title: e.Title, Nodes: e.MessageCount}
	}
	return out
}

// importDeepSeek 整库导入：读清单 → resolvePick → 逐会话流式解包 → materialize。
//
// 分块由调用方做（app 按 100 条一块挑 pick 再调进来）：这块只管「pick 里选了哪几个」，
// 不管进度与落盘节奏。
func (e *Exchange) importDeepSeek(ctx context.Context, r io.Reader, pick []int, opts port.ImportOptions) ([]*conversation.Conversation, port.ExchangeReport, error) {
	// 逐会话失败即整份报错（选中的那份读不动就是读不动）；缺文件见 dsImportOne（计丢弃）。
	rep := port.ExchangeReport{Format: "deepseek"}
	zr, closer, err := dsOpenZip(r)
	if err != nil {
		return nil, rep, err
	}
	defer func() { _ = closer() }()
	zipBy := dsZipIndex(zr)
	idx, err := dsReadIndex(zipBy)
	if err != nil {
		return nil, rep, err
	}
	if len(idx) > maxDSEntries {
		return nil, rep, errors.New(i18n.Tf("exchange.err.ds.index_too_many", "count", len(idx), "max", maxDSEntries))
	}
	sorted := dsSortedIndex(idx)
	sel, err := resolvePick(pick, len(sorted), dsIndexEntries(sorted))
	if err != nil {
		return nil, rep, err
	}
	run := newDSRun(e, zr, zipBy, opts)
	out := make([]*conversation.Conversation, 0, len(sel))
	for _, idx := range sel {
		if err := ctx.Err(); err != nil {
			return nil, rep, err
		}
		entry := sorted[idx-1]
		c, notes, err := e.dsImportOne(ctx, run, entry)
		if err != nil {
			return nil, rep, err
		}
		rep.Dropped = append(rep.Dropped, notes...)
		if c == nil {
			continue // 缺文件的清单条目：已计丢弃，不占导入结果
		}
		rep.Nodes += nodeCount(c)
		out = append(out, c)
		// 深度容器恒填清单（分块导入时调用方按块聚合）；回执自己按条数选版式。
		rep.Entries = append(rep.Entries, port.ExchangeEntry{
			Index: idx, ID: entry.ID, Title: entry.Title, Nodes: entry.MessageCount,
			UpdatedAt: dsTime(entry.UpdatedAt),
		})
	}
	rep.Dropped = append(rep.Dropped, run.droppedLines()...)
	rep.Counts = run.counts
	return out, rep, nil
}

// dsRun 一次导入运行的聚合状态：跨会话的丢弃计数与总量上限都在这里。
type dsRun struct {
	e      *Exchange
	zipBy  map[string]*zip.File
	opts   port.ImportOptions
	counts map[string]int
}

func newDSRun(e *Exchange, _ *zip.Reader, zipBy map[string]*zip.File, opts port.ImportOptions) *dsRun {
	return &dsRun{e: e, zipBy: zipBy, opts: opts, counts: map[string]int{}}
}

// droppedLines 计数表 → 回执行（复用 chatgpt 的排序写法）。
func (run *dsRun) droppedLines() []string { return droppedLines(run.counts) }

// dsReadIndex 找到并解析 chat_session_index.json；没有清单就不是 deepseek 的包。
func dsReadIndex(zipBy map[string]*zip.File) ([]dsIndexEntry, error) {
	f, ok := zipBy["chat_session_index.json"]
	if !ok {
		return nil, errors.New(i18n.T("exchange.err.ds.no_index"))
	}
	body, err := readZipLimited(f, maxDSIndexBytes, f.Name)
	if err != nil {
		return nil, err
	}
	var items []dsIndexEntry
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Tf("exchange.err.parse_entry", "name", f.Name), err)
	}
	return items, nil
}

// dsImportOne 导入一个会话：读 JSON → 片段映射 → materialize。
// notes 是 materialize 自己发现的事实（如 Head 回退），调用方并进回执。
func (e *Exchange) dsImportOne(ctx context.Context, run *dsRun, entry dsIndexEntry) (*conversation.Conversation, []string, error) {
	name := "chat_sessions/" + entry.ID + ".json"
	f, ok := run.zipBy[name]
	if !ok {
		// 清单有、包里缺：官方导出偶有这种（样例子集导出就是全清单+部分文件），逐条报不当
		// 整份失败——缺的那份本来也不会进库。
		run.counts[i18n.T("exchange.io.drop.ds_session_missing")]++
		return nil, nil, nil
	}
	body, err := readZipLimited(f, maxDSSessionBytes, name)
	if err != nil {
		return nil, nil, err
	}
	var s dsSession
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", i18n.Tf("exchange.err.parse_entry", "name", name), err)
	}
	in, err := e.dsParseTree(ctx, run, &s)
	if err != nil {
		return nil, nil, err
	}
	c, err := materialize(in)
	return c, in.Dropped, err
}

// dsParseTree 会话 JSON → importedTree：片段映射、空节点跳过、版本组挂 Root。
func (e *Exchange) dsParseTree(ctx context.Context, run *dsRun, s *dsSession) (*importedTree, error) {
	in := importedTree{
		Title:     s.Title,
		Head:      strconv.Itoa(s.CurrentMessage),
		CreatedAt: dsTime(s.InsertedAt),
		UpdatedAt: dsTime(s.UpdatedAt),
	}
	msgs := make(map[int]dsMessage, len(s.Messages))
	for _, m := range s.Messages {
		msgs[m.ID] = m
	}
	// ParentOf 给 resolveHead 用（Head 可能落在被跳过的空节点上）。
	in.ParentOf = func(id string) (string, bool) {
		n, err := strconv.Atoi(id)
		if err != nil {
			return "", false
		}
		m, ok := msgs[n]
		if !ok || m.ParentID == nil {
			return "", ok // 根的父是挂点（""），对 resolveHead 等价于「最近收下」
		}
		return strconv.Itoa(*m.ParentID), true
	}
	var blobTotal int64
	for _, m := range s.Messages {
		role, ok := dsRole(m.Role)
		if !ok {
			run.counts[i18n.Tf("exchange.io.drop.unknown_role", "role", m.Role)]++
			continue
		}
		parts, err := e.dsParts(ctx, run, m, &blobTotal)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue // 空节点（无任何可收分片）不建空壳；子节点由 reparent 接到最近祖先
		}
		node := importedNode{
			SrcID:   strconv.Itoa(m.ID),
			Role:    role,
			Parts:   parts,
			Created: dsTime(m.InsertedAt),
		}
		if m.ParentID != nil {
			node.Parent = strconv.Itoa(*m.ParentID)
		}
		if role == conversation.RoleAssistant {
			node.Outcome = dsOutcome(m.Status)
		}
		in.Nodes = append(in.Nodes, node)
	}
	dsReparent(msgs, in.Nodes)
	return &in, nil
}

// dsRole 源角色 → 本程序角色（只有 USER / ASSISTANT 两档）。
func dsRole(role string) (conversation.Role, bool) {
	switch role {
	case "USER":
		return conversation.RoleUser, true
	case "ASSISTANT":
		return conversation.RoleAssistant, true
	default:
		return "", false
	}
}

// dsOutcome status → 节点终态（D321 拍板的四态映射；其余留空不猜）。
func dsOutcome(status string) conversation.Outcome {
	switch status {
	case "FINISHED":
		return conversation.OutcomeDone
	case "INCOMPLETE":
		return conversation.OutcomeCancelled
	case "TIMEOUT", "CONTENT_FILTER":
		return conversation.OutcomeError
	default:
		return ""
	}
}

// dsParts 一个消息的 fragments → 本程序的分片。文件字节按 export_path 从包里流式取
// （单份 32 MB、单会话合计 512 MB，按实际解压字节累计）；--no-files 或非图片计入丢弃。
func (e *Exchange) dsParts(ctx context.Context, run *dsRun, m dsMessage, blobTotal *int64) ([]conversation.Part, error) {
	var parts []conversation.Part
	for _, fr := range m.Fragments {
		switch fr.Type {
		case "REQUEST", "RESPONSE", "TEMPLATE_RESPONSE":
			if fr.Content != "" {
				parts = append(parts, conversation.Part{Kind: conversation.PartText, Text: fr.Content})
			}
		case "THINK":
			if fr.Content != "" {
				parts = append(parts, conversation.Part{Kind: conversation.PartThinking, Text: fr.Content})
			}
		case "FILE":
			for _, file := range fr.Files {
				if run.opts.OmitFiles || !file.IsImage || file.ExportPath == "" {
					// 非图片文件源包不含字节（export_path 为 null）；--no-files 是用户主动不要。
					run.counts[i18n.T("exchange.io.drop.ds_file_no_bytes")]++
					continue
				}
				ref, err := e.dsStoreBlob(ctx, run, file, blobTotal)
				if err != nil {
					return nil, err
				}
				if ref != nil {
					parts = append(parts, conversation.Part{Kind: conversation.PartImage, Ref: ref})
				}
			}
		case "CONTENT_RISK":
			run.counts[i18n.T("exchange.io.drop.ds_risk_banner")]++
		case "TOOL_SEARCH", "TOOL_FIND":
			run.counts[i18n.T("exchange.io.drop.ds_searches")]++
		case "TOOL_OPEN", "READ_LINK", "PARTIAL_FILE_READ":
			run.counts[i18n.T("exchange.io.drop.ds_page_reads")]++
		default:
			run.counts[i18n.Tf("exchange.io.drop.ds_fragment", "kind", fr.Type)]++
		}
	}
	return parts, nil
}

// dsStoreBlob 取一个附件条目的字节放回附件库；条目缺失 / 超限计入丢弃（不当整份失败——
// 官方导出自身偶有 export_path 指不到的条目，逐条报比拒收整个会话合理）。
func (e *Exchange) dsStoreBlob(ctx context.Context, run *dsRun, file dsFile, blobTotal *int64) (*conversation.BlobRef, error) {
	if e.blobs == nil {
		run.counts[i18n.T("exchange.io.drop.ds_file_no_bytes")]++
		return nil, nil
	}
	f, ok := run.zipBy[file.ExportPath]
	if !ok || f.UncompressedSize64 > maxDSBlobBytes {
		run.counts[i18n.T("exchange.io.drop.ds_file_no_bytes")]++
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Tf("exchange.err.open_what", "what", file.ExportPath), err)
	}
	defer rc.Close()
	// 边读边入库：峰值只有一份附件；总量按实际解压字节累计（压缩包可以谎报声明大小）。
	lr := io.LimitReader(rc, maxDSBlobBytes+1)
	cr := &countReader{r: lr}
	ref, err := e.blobs.Put(ctx, cr, mimeByExt(file.FileName), file.FileName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w",
			i18n.Tf("exchange.err.store_blob", "hash", shortHash(file.ID)), err)
	}
	if cr.n > maxDSBlobBytes {
		return nil, errors.New(i18n.Tf("exchange.err.what_too_large", "what", file.ExportPath, "mb", maxDSBlobBytes>>20))
	}
	*blobTotal += cr.n
	if *blobTotal > maxDSBlobTotalBytes {
		return nil, errors.New(i18n.Tf("exchange.err.blobs_total_too_large", "mb", maxDSBlobTotalBytes>>20))
	}
	return &ref, nil
}

// countReader 边入库边计字节的装饰器（Put 内部读完才知实际大小）。
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// mimeByExt 扩展名 → MIME（只认图片那几类；其余的条目根本走不到这里）。
func mimeByExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return ""
	}
}

// dsReparent 把「父节点没被收下」的节点接到最近一个收下的祖先上（与 chatgpt 同一套规则）：
// 空节点（无分片）与纯附件消息不建空壳，但它们的子节点不能因此失去位置。
func dsReparent(msgs map[int]dsMessage, nodes []importedNode) {
	kept := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		kept[n.SrcID] = true
	}
	for i := range nodes {
		p := nodes[i].Parent
		for hops := 0; p != "" && !kept[p] && hops < len(msgs); hops++ {
			id, err := strconv.Atoi(p)
			if err != nil {
				break // 非数字 ID：留给 materialize 报「父节点不在文件里」
			}
			m, ok := msgs[id]
			if !ok {
				break // 悬空父：不改写，交给自检明确报因
			}
			if m.ParentID == nil {
				p = "" // 祖先链走到根：挂到 Root 之下（父节点是被跳过的空壳根消息）
				break
			}
			p = strconv.Itoa(*m.ParentID)
		}
		nodes[i].Parent = p
	}
}

// Index 只读容器清单。deepseek 一档走同一入口（界面勾选面板与命令面共用）：
// 有 aqx 的 manifest.json 按 aqx 读，否则找 chat_session_index.json 按 deepseek 读。
func (e *Exchange) dsIndex(ctx context.Context, path string) ([]port.ExchangeEntry, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", i18n.T("exchange.err.open_archive"), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", i18n.T("exchange.err.stat_archive"), err)
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", i18n.T("exchange.err.not_archive"), err)
	}
	zipBy := dsZipIndex(zr)
	if _, ok := zipBy["chat_session_index.json"]; !ok {
		return nil, false, nil // 不是 deepseek 的包：调用方继续走 aqx / 报因
	}
	items, err := dsReadIndex(zipBy)
	if err != nil {
		return nil, true, err
	}
	if len(items) > maxDSEntries {
		return nil, true, errors.New(i18n.Tf("exchange.err.ds.index_too_many", "count", len(items), "max", maxDSEntries))
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	sorted := dsSortedIndex(items)
	out := make([]port.ExchangeEntry, len(sorted))
	for i, e := range sorted {
		out[i] = port.ExchangeEntry{
			Index: i + 1, ID: e.ID, Title: e.Title, Nodes: e.MessageCount,
			UpdatedAt: dsTime(e.UpdatedAt),
		}
	}
	return out, true, nil
}
