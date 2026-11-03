package exchange

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// `aqx` 容器（§5.16）：一个 ZIP，里面三样东西——
//
//	manifest.json              {"format":"aqx","version":1,"conversations":[…]}
//	conversations/<i>/tree.json 第 i 个会话的落盘 wire 形状（storejson 同一份）
//	blobs/<sha256>            附件字节，内容寻址、平铺（**跨会话共用一份**）
//
// 为什么不把 format/version 塞进 tree.json：那份形状要与磁盘上的会话文件逐字段一致，
// 加两个归档专用字段就不一致了（出问题时没法拿编辑器对读）。版本门放在容器层，认不出
// 更高版本就在解包前拒绝，不去猜新字段是什么意思。
//
// `conversations/<i>` 的 i 是**清单序号**（1 起、无前导零），与 manifest 里的顺序、`--pick`
// 的序号、回执里的序号是同一个数——三处各写一套换算，迟早会错位。

// aqxVersion 当前容器版本。加需要新版才能读的东西时递增；导入遇到更高版本直接拒绝。
const aqxVersion = 1

// 导入侧上限（§5.16：超限报错，不静默截断）。
const (
	// maxArchiveBytes 归档压缩后总字节上限（先整个读进内存再解，所以必须有个上限）。
	maxArchiveBytes = 256 << 20
	// maxArchiveEntries 容器条目数上限（防「几万个小文件」）。
	maxArchiveEntries = 4000
	// maxTreeBytes 单份 tree.json 上限（每个会话各算，不是合计）。
	maxTreeBytes = 64 << 20
	// maxManifestBytes 清单上限：它只有标题与计数，超过这个数说明不是我们写的。
	maxManifestBytes = 1 << 20
	// maxNodeCount 单个会话的节点数上限（不是合计：一个会话装不下就是装不下）。
	maxNodeCount = 20000
	// maxBlobBytes 单个附件解压后上限。
	maxBlobBytes = 32 << 20
	// maxBlobTotalBytes 附件解压后总量上限（防高压缩比炸弹）。
	maxBlobTotalBytes = 512 << 20
)

// manifest 容器层：格式标识、版本与会话清单（归档专用的全部字段）。
type manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// Conversations 清单：顺序即序号（第 i 项 = conversations/<i-1>/tree.json）。
	Conversations []manifestEntry `json:"conversations"`
}

// manifestEntry 清单的一项：只给展示与选择用，导入以各自的 tree.json 为准。
type manifestEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Nodes int    `json:"nodes"`
}

// droppedBlobs 附件字节取不到时的回执行前缀（导出侧：库里没有）。
//
// 函数而不是包级常量：语言在 main 才定，包初始化时取词会永远钉在本机 locale。
func droppedBlobs(count int, names string) string {
	return i18n.Tf("exchange.io.dropped.missing_blob", "count", count, "names", names)
}

// tooLargeBlobs 附件超过单份上限时的回执行（导出侧）。
//
// 与「不在库里」分开说：前者去清库也没用，后者补进库就能带上。混成一句会让用户白忙一趟。
func tooLargeBlobs(count int, names string, mb int64) string {
	return i18n.Tf("exchange.io.dropped.blob_too_large", "count", count, "names", names, "mb", mb)
}

// legacyArchiveError 旧单树归档（根目录一个 tree.json）：本版不读。
//
// aqx 尚未正式发布，为一份没人持有的中间形状留长期兼容分支，代价大于收益：读它就得同时
// 维护两套解包逻辑，而「重新导一次」对用户只是一步。
//
// 零宽结构体：errors.Is 与 == 照旧成立，而 Error() 每次取词。
type legacyArchiveError struct{}

func (legacyArchiveError) Error() string { return i18n.T("exchange.err.legacy_archive") }

var errLegacyAQX error = legacyArchiveError{}

// exportAQX 无损导出：一组树 + 附件字节（附件按哈希跨会话共用一份）。
func (e *Exchange) exportAQX(ctx context.Context, cs []*conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "aqx"}
	man := manifest{Format: "aqx", Version: aqxVersion, Conversations: make([]manifestEntry, len(cs))}
	trees := make([][]byte, len(cs))
	for i, c := range cs {
		tree, err := storejson.EncodeTree(c)
		if err != nil {
			return rep, err
		}
		trees[i] = tree
		n := nodeCount(c)
		rep.Nodes += n
		man.Conversations[i] = manifestEntry{ID: string(c.ID), Title: c.Title, Nodes: n}
	}
	if len(cs) > 1 {
		// 单会话不填：回执据此走「一份会话」的老版式，不靠数量反猜。
		rep.Entries = manifestEntries(man.Conversations)
	}
	cw := &countWriter{w: w}
	zw := zip.NewWriter(cw)
	if err := writeZipEntry(zw, "manifest.json", func(w io.Writer) error {
		data, err := json.Marshal(man)
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}); err != nil {
		return rep, err
	}
	for i, tree := range trees {
		name := conversationTreeName(i + 1)
		if err := writeZipEntry(zw, name, func(w io.Writer) error {
			_, err := w.Write(tree)
			return err
		}); err != nil {
			return rep, err
		}
	}
	refs := collectBlobs(cs)
	sort.Slice(refs, func(i, j int) bool { return refs[i].Hash < refs[j].Hash }) // 同一组会话导两次字节相同
	var missing, tooLarge []string
	for _, ref := range refs {
		if e.blobs == nil {
			missing = append(missing, ref.Name)
			continue
		}
		body, err := e.readBlob(ctx, ref)
		if errors.Is(err, errBlobTooLarge) {
			tooLarge = append(tooLarge, ref.Name)
			continue
		}
		if err != nil {
			missing = append(missing, ref.Name)
			continue
		}
		entry := "blobs/" + ref.Hash
		if err := writeZipEntry(zw, entry, func(w io.Writer) error {
			_, err := w.Write(body)
			return err
		}); err != nil {
			return rep, err
		}
	}
	if err := zw.Close(); err != nil {
		return rep, fmt.Errorf("%s: %w", i18n.T("exchange.err.finalize_archive"), err)
	}
	rep.Bytes = cw.n
	if len(missing) > 0 {
		// 附件字节没带上并入树：树里的引用还在，导回去时那几张图会指向缺失的字节。
		// 回执必须说明，否则用户以为备份完整。
		rep.Dropped = append(rep.Dropped, droppedBlobs(len(missing), summarizeNames(missing)))
	}
	if len(tooLarge) > 0 {
		rep.Dropped = append(rep.Dropped, tooLargeBlobs(len(tooLarge), summarizeNames(tooLarge), maxBlobBytes>>20))
	}
	return rep, nil
}

// importAQX 无损导入：解包 → 逐树自检 → 附件字节回填。
//
// pick 是 1 起的清单序号（空 = 全部）。**只有选中的会话**参与解码与附件回填：没选中的树即使
// 坏掉也不该挡住这次导入，它们本来就不会进库。
func (e *Exchange) importAQX(ctx context.Context, r io.Reader, pick []int) ([]*conversation.Conversation, port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "aqx"}
	data, err := readLimited(r, maxArchiveBytes, i18n.T("exchange.io.what.archive"))
	if err != nil {
		return nil, rep, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, rep, fmt.Errorf("%s: %w", i18n.T("exchange.err.not_archive"), err)
	}
	if len(zr.File) > maxArchiveEntries {
		return nil, rep, errors.New(i18n.Tf("exchange.err.too_many_entries",
			"count", len(zr.File), "max", maxArchiveEntries))
	}
	var (
		man        *manifest
		trees      = map[int]*zip.File{} // 清单序号 → 树条目
		blobs      []*zip.File
		unknown    []string
		totalBlobs int64
	)
	for _, f := range zr.File {
		name, err := safeArchivePath(f.Name)
		if err != nil {
			return nil, rep, err
		}
		if name == "" {
			continue
		}
		if f.Mode()&fsSymlink != 0 {
			return nil, rep, errors.New(i18n.Tf("exchange.err.archive_symlink", "name", f.Name))
		}
		switch {
		case name == "manifest.json":
			if man != nil {
				return nil, rep, errors.New(i18n.T("exchange.err.duplicate_manifest"))
			}
			m, err := readZipJSON(f, maxManifestBytes)
			if err != nil {
				return nil, rep, err
			}
			man = m
		case name == "tree.json":
			// 旧形状：根目录的 tree.json。认下它就得再养一套解包逻辑，直接说清要重导。
			return nil, rep, errLegacyAQX
		case strings.HasPrefix(name, "conversations/"):
			i, ok := parseTreeName(name)
			if !ok {
				return nil, rep, errors.New(i18n.Tf("exchange.err.unknown_entry_tree", "name", name))
			}
			if trees[i] != nil {
				return nil, rep, errors.New(i18n.Tf("exchange.err.duplicate_tree", "index", i))
			}
			if f.UncompressedSize64 > maxTreeBytes {
				return nil, rep, errors.New(i18n.Tf("exchange.err.tree_too_large",
					"index", i, "mb", maxTreeBytes>>20))
			}
			trees[i] = f
		case strings.HasPrefix(name, "blobs/"):
			if f.UncompressedSize64 > maxBlobBytes {
				return nil, rep, errors.New(i18n.Tf("exchange.err.blob_too_large",
					"name", f.Name, "mb", maxBlobBytes>>20))
			}
			totalBlobs += int64(f.UncompressedSize64)
			if totalBlobs > maxBlobTotalBytes {
				return nil, rep, errors.New(i18n.Tf("exchange.err.blobs_total_too_large",
					"mb", maxBlobTotalBytes>>20))
			}
			blobs = append(blobs, f)
		default:
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		// aqx 是我们自己的格式：不认识的条目意味着「不是这个版本写的」，猜着读比拒绝更糟。
		return nil, rep, errors.New(i18n.Tf("exchange.err.unknown_entries",
			"names", summarizeNames(unknown)))
	}
	if man == nil {
		return nil, rep, errors.New(i18n.T("exchange.err.no_manifest_foreign"))
	}
	if man.Format != "aqx" {
		return nil, rep, errors.New(i18n.Tf("exchange.err.wrong_format", "format", man.Format))
	}
	if man.Version > aqxVersion {
		return nil, rep, errors.New(i18n.Tf("exchange.err.version_too_new",
			"version", man.Version, "supported", aqxVersion))
	}
	if len(man.Conversations) == 0 {
		return nil, rep, errors.New(i18n.T("exchange.err.empty_manifest"))
	}
	if len(man.Conversations) > maxArchiveEntries {
		return nil, rep, errors.New(i18n.Tf("exchange.err.manifest_too_many",
			"count", len(man.Conversations), "max", maxArchiveEntries))
	}
	sel, err := resolvePick(pick, len(man.Conversations), man.Conversations)
	if err != nil {
		return nil, rep, err
	}
	out := make([]*conversation.Conversation, 0, len(sel))
	for _, idx := range sel {
		f := trees[idx]
		if f == nil {
			return nil, rep, errors.New(i18n.Tf("exchange.err.missing_tree", "index", idx))
		}
		tree, err := readZipLimited(f, maxTreeBytes, conversationTreeName(idx))
		if err != nil {
			return nil, rep, err
		}
		c, err := decodeTree(tree, conversationTreeName(idx))
		if err != nil {
			return nil, rep, err
		}
		if n := c.NodeCount(); n > maxNodeCount {
			return nil, rep, errors.New(i18n.Tf("exchange.err.too_many_nodes_in",
				"index", idx, "count", n, "max", maxNodeCount))
		}
		rep.Nodes += nodeCount(c)
		out = append(out, c)
	}
	if len(man.Conversations) > 1 {
		// 回执只列**这次导入的**那几个（序号仍取清单序号）：用户关心的是搬进来了什么，
		// 不是「这份备份里还有什么」。清单本身在越界报错里给出。
		entries := make([]port.ExchangeEntry, 0, len(sel))
		for _, idx := range sel {
			e := man.Conversations[idx-1]
			entries = append(entries, port.ExchangeEntry{
				Index: idx, ID: e.ID, Title: e.Title, Nodes: e.Nodes,
			})
		}
		rep.Entries = entries
	}
	// 附件回填：只处理选中会话引用到的那些（没选中的树不会进库，其附件也不必落盘）。
	need := make(map[string]bool)
	for _, c := range out {
		for _, ref := range collectBlobs([]*conversation.Conversation{c}) {
			need[ref.Hash] = true
		}
	}
	selected := make([]*zip.File, 0, len(blobs))
	for _, f := range blobs {
		name, _ := safeArchivePath(f.Name)
		if need[strings.TrimPrefix(name, "blobs/")] {
			selected = append(selected, f)
		}
	}
	dropped, err := e.restoreBlobs(ctx, selected)
	if err != nil {
		return nil, rep, err
	}
	rep.Dropped = append(rep.Dropped, dropped...)
	// 树里引用、归档里却没有的附件：字节就是找不回来了，回执必须逐项说清——
	// 静默留下一个指向空处的引用，用户要到装配时才发现图没了。
	if missing := missingBlobs(out, selected); len(missing) > 0 {
		rep.Dropped = append(rep.Dropped, i18n.Tf("exchange.io.dropped.missing_bytes", "count", len(missing)))
	}
	return out, rep, nil
}

// Index 只读归档清单（界面勾选面板用）：不解树、不回填附件。
func (e *Exchange) Index(ctx context.Context, path string) ([]port.ExchangeEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("exchange.err.open_archive"), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("exchange.err.stat_archive"), err)
	}
	if fi.Size() > maxArchiveBytes {
		return nil, errors.New(i18n.Tf("exchange.err.archive_too_large", "mb", maxArchiveBytes>>20))
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("exchange.err.not_archive"), err)
	}
	for _, zf := range zr.File {
		name, err := safeArchivePath(zf.Name)
		if err != nil {
			return nil, err
		}
		if name != "manifest.json" {
			continue
		}
		m, err := readZipJSON(zf, maxManifestBytes)
		if err != nil {
			return nil, err
		}
		if m.Format != "aqx" {
			return nil, errors.New(i18n.Tf("exchange.err.wrong_format", "format", m.Format))
		}
		if m.Version > aqxVersion {
			return nil, errors.New(i18n.Tf("exchange.err.version_too_new",
				"version", m.Version, "supported", aqxVersion))
		}
		return manifestEntries(m.Conversations), nil
	}
	return nil, errors.New(i18n.T("exchange.err.no_manifest"))
}

// conversationTreeName 清单序号 → 归档条目名（1 起、无前导零）。
func conversationTreeName(i int) string {
	return fmt.Sprintf("conversations/%d/tree.json", i)
}

// parseTreeName 认 `conversations/<正数>/tree.json`，返回清单序号。
func parseTreeName(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "conversations/")
	if !ok {
		return 0, false
	}
	num, tail, ok := strings.Cut(rest, "/")
	if !ok || tail != "tree.json" || num == "" {
		return 0, false
	}
	i, err := strconv.Atoi(num)
	if err != nil || i < 1 || strconv.Itoa(i) != num {
		return 0, false // 前导零、负号、非数字都不认
	}
	return i, true
}

// resolvePick 把 `--pick` 的序号解成「按清单顺序排好、去重」的下标（1 起）。
//
// 空 = 全部。重复不报错（用户勾重了就按一次算）；越界与非法值报因并**附上清单**——
// 只说「序号不对」，用户还得回去自己数列第几项。
func resolvePick(pick []int, total int, entries []manifestEntry) ([]int, error) {
	if len(pick) == 0 {
		out := make([]int, total)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	seen := make(map[int]bool, len(pick))
	out := make([]int, 0, len(pick))
	for _, i := range pick {
		if i < 1 || i > total {
			return nil, errors.New(i18n.Tf("exchange.err.pick_out_of_range",
				"index", i, "total", total, "listing", manifestListing(entries)))
		}
		if seen[i] {
			continue
		}
		seen[i] = true
		out = append(out, i)
	}
	sort.Ints(out)
	return out, nil
}

// manifestListing 清单的可读列法（序号 + 标题），附在「序号不对」的错误后面。
func manifestListing(entries []manifestEntry) string {
	const show = 20
	var b strings.Builder
	b.WriteString("\n" + i18n.T("exchange.io.manifest_listing.head"))
	for i, e := range entries {
		if i >= show {
			b.WriteString("\n" + i18n.Tf("exchange.io.manifest_listing.more", "count", len(entries)))
			break
		}
		b.WriteString("\n" + i18n.Tf("exchange.io.manifest_listing.item",
			"index", i+1, "title", e.Title, "count", e.Nodes))
	}
	return b.String()
}

// manifestEntries 清单 → 端口条目（序号在此统一分配：1 起、按清单顺序）。
func manifestEntries(items []manifestEntry) []port.ExchangeEntry {
	out := make([]port.ExchangeEntry, len(items))
	for i, e := range items {
		out[i] = port.ExchangeEntry{Index: i + 1, ID: e.ID, Title: e.Title, Nodes: e.Nodes}
	}
	return out
}

// missingBlobs 树里引用、归档里没有的附件哈希（按引用去重；多棵树一起算）。
func missingBlobs(cs []*conversation.Conversation, files []*zip.File) []string {
	have := make(map[string]bool, len(files))
	for _, f := range files {
		if name, err := safeArchivePath(f.Name); err == nil {
			have[strings.TrimPrefix(name, "blobs/")] = true
		}
	}
	var out []string
	for _, ref := range collectBlobs(cs) {
		if !have[ref.Hash] {
			out = append(out, ref.Hash)
		}
	}
	return out
}

// restoreBlobs 把归档里的附件字节放回附件库；返回要进回执的说明行。
//
// 边校验边写：先把全部附件读进内存再逐个入库，峰值 = 归档字节 + 解压总量（可达几百 MB）。
// 逐份读、校验、入库的峰值只有一份附件（单份上限 32 MB）。
func (e *Exchange) restoreBlobs(ctx context.Context, files []*zip.File) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if e.blobs == nil {
		return []string{i18n.Tf("exchange.io.dropped.no_blob_store", "count", len(files))}, nil
	}
	var total int64
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name, _ := safeArchivePath(f.Name)
		want := strings.TrimPrefix(name, "blobs/")
		body, err := readZipLimited(f, maxBlobBytes, f.Name)
		if err != nil {
			return nil, err
		}
		// 按实际解压字节累计，不信条目声明的大小：压缩包可以谎报 UncompressedSize。
		total += int64(len(body))
		if total > maxBlobTotalBytes {
			return nil, errors.New(i18n.Tf("exchange.err.blobs_total_too_large", "mb", maxBlobTotalBytes>>20))
		}
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != want {
			return nil, errors.New(i18n.Tf("exchange.err.blob_hash_mismatch",
				"name", f.Name, "want", shortHash(want), "got", shortHash(got)))
		}
		// 名字与 MIME 不在附件库里（它们随 Part.Ref 存在树里，D17），这里只放字节。
		if _, err := e.blobs.Put(ctx, bytes.NewReader(body), "", ""); err != nil {
			return nil, fmt.Errorf("%s: %w",
				i18n.Tf("exchange.err.store_blob", "hash", shortHash(got)), err)
		}
	}
	return nil, nil
}

// errBlobTooLarge 附件超过单份上限（导出侧的丢项分类：与「库里没有」分开说）。
//
// 两者都是「这次没带上」，但原因是两回事：前者去清附件库也没用，后者补进库就能带上。
// 混成一句「不在附件库里」会让用户白忙一趟。
type blobTooLargeError struct{}

func (blobTooLargeError) Error() string { return i18n.T("exchange.err.blob_over_limit") }

var errBlobTooLarge error = blobTooLargeError{}

// readBlob 从附件库取一份字节。
func (e *Exchange) readBlob(ctx context.Context, ref conversation.BlobRef) ([]byte, error) {
	// 先按引用里记的大小拦一道：超限时不必读进内存，也让回执能说清是「太大」而不是「没有」。
	if ref.Size > maxBlobBytes {
		return nil, errBlobTooLarge
	}
	rc, err := e.blobs.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readLimited(rc, maxBlobBytes, i18n.Tf("exchange.io.what.attachment", "name", ref.Name))
}

// collectBlobs 树里被引用的附件（按哈希去重，名字取首个引用；多棵树一起算，共用一份字节）。
func collectBlobs(cs []*conversation.Conversation) []conversation.BlobRef {
	seen := map[string]bool{}
	var out []conversation.BlobRef
	for _, c := range cs {
		for _, m := range c.Snapshot().Nodes {
			for _, p := range m.Content {
				if p.Ref == nil || seen[p.Ref.Hash] {
					continue
				}
				seen[p.Ref.Hash] = true
				out = append(out, *p.Ref)
			}
		}
	}
	return out
}

// writeZipEntry 写一个条目（函数式写法省掉「打开、写、关」三行样板）。
func writeZipEntry(zw *zip.Writer, name string, fn func(io.Writer) error) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.Tf("exchange.err.create_entry", "name", name), err)
	}
	if err := fn(w); err != nil {
		return fmt.Errorf("%s: %w", i18n.Tf("exchange.err.write_entry", "name", name), err)
	}
	return nil
}

// readZipJSON 读一个小条目的 JSON。
func readZipJSON(f *zip.File, limit int64) (*manifest, error) {
	body, err := readZipLimited(f, limit, f.Name)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Tf("exchange.err.parse_entry", "name", f.Name), err)
	}
	return &m, nil
}

// readZipLimited 读一个条目的全部内容，超限报错（不按声明的大小信任压缩包）。
func readZipLimited(f *zip.File, limit int64, what string) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Tf("exchange.err.open_what", "what", what), err)
	}
	defer rc.Close()
	return readLimited(rc, limit, what)
}

// readLimited 读到 EOF，超过 limit 即报错。
//
// 不信任「声明的大小」：压缩包可以谎报 UncompressedSize，读时按实际字节数截。
func readLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Tf("exchange.err.read_what", "what", what), err)
	}
	if int64(len(body)) > limit {
		return nil, errors.New(i18n.Tf("exchange.err.what_too_large", "what", what, "mb", limit>>20))
	}
	return body, nil
}

// fsSymlink 符号链接条目：归档里出现符号链接一律拒绝（与技能安装的 zip-slip 口径同源）。
const fsSymlink = os.ModeSymlink

// safeArchivePath 校验并归一归档条目路径（绝对路径、盘符、`..`、空名一律按口径处理）。
//
// 与技能安装的 zip-slip 口径同源：归档同样是不可信输入。返回空串 = 无意义的条目（跳过）。
//
// 独立的目录条目（名字以 `/` 结尾，或模式位带目录）也返回空串：第三方重新打包时常带上
// `blobs/` 这样的一条纯目录记录，它不是「不认识的条目」，拒绝整份归档太严。
func safeArchivePath(name string) (string, error) {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.TrimSpace(n) == "" {
		return "", nil
	}
	if strings.HasSuffix(n, "/") {
		return "", nil // 目录条目：归档里不需要建目录，逐条解到库里
	}
	if strings.HasPrefix(n, "/") {
		return "", errors.New(i18n.Tf("exchange.err.entry_abs_path", "name", name))
	}
	if len(n) >= 2 && n[1] == ':' {
		return "", errors.New(i18n.Tf("exchange.err.entry_drive", "name", name))
	}
	clean := path.Clean(n)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New(i18n.Tf("exchange.err.entry_escape", "name", name))
	}
	return clean, nil
}

// shortHash 哈希的用户面前缀（错误文案里不必印 64 位）。
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

// summarizeNames 名字清单的用户面写法（最多列 3 个，其余记数）。
func summarizeNames(names []string) string {
	const show = 3
	if len(names) <= show {
		return strings.Join(names, ", ")
	}
	return i18n.Tf("exchange.io.names_more", "names", strings.Join(names[:show], ", "), "count", len(names))
}
