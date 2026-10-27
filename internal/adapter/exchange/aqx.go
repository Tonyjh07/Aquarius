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
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// `aqx` 容器（DESIGN §5.16）：一个 ZIP，里面三样东西——
//
//	manifest.json    {"format":"aqx","version":1}
//	tree.json        落盘 wire 形状（storejson 同一份），整棵树原样
//	blobs/<sha256>   附件字节，内容寻址、平铺
//
// 为什么不把 format/version 塞进 tree.json：那份形状要与磁盘上的会话文件逐字段一致，
// 加两个归档专用字段就不一致了（出问题时没法拿编辑器对读）。版本门放在容器层，认不出
// 更高版本就在解包前拒绝，不去猜新字段是什么意思。

// aqxVersion 当前容器版本。加需要新版才能读的东西时递增；导入遇到更高版本直接拒绝。
const aqxVersion = 1

// 导入侧上限（DESIGN §5.16：超限报错，不静默截断）。
const (
	// maxArchiveBytes 归档压缩后总字节上限（先整个读进内存再解，所以必须有个上限）。
	maxArchiveBytes = 256 << 20
	// maxArchiveEntries 容器条目数上限（防「几万个小文件」）。
	maxArchiveEntries = 4000
	// maxTreeBytes tree.json 上限（正文总字节的门实际由这个守：树全在这一个文件里）。
	maxTreeBytes = 64 << 20
	// maxNodeCount 节点数上限。
	maxNodeCount = 20000
	// maxBlobBytes 单个附件解压后上限。
	maxBlobBytes = 32 << 20
	// maxBlobTotalBytes 附件解压后总量上限（防高压缩比炸弹）。
	maxBlobTotalBytes = 512 << 20
)

// manifest 容器层：格式标识与版本（唯一两个归档专用字段）。
type manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
}

// errDroppedBlob 附件字节取不到时的回执行前缀（导出侧）。
const droppedBlobFmt = "附件 %d 个（%s 不在附件库里）"

// exportAQX 无损导出：整树 + 附件字节。
func (e *Exchange) exportAQX(ctx context.Context, c *conversation.Conversation, w io.Writer) (port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "aqx", Nodes: nodeCount(c)}
	tree, err := storejson.EncodeTree(c)
	if err != nil {
		return rep, err
	}
	cw := &countWriter{w: w}
	zw := zip.NewWriter(cw)
	if err := writeZipEntry(zw, "manifest.json", func(w io.Writer) error {
		data, err := json.Marshal(manifest{Format: "aqx", Version: aqxVersion})
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}); err != nil {
		return rep, err
	}
	if err := writeZipEntry(zw, "tree.json", func(w io.Writer) error {
		_, err := w.Write(tree)
		return err
	}); err != nil {
		return rep, err
	}
	refs := collectBlobs(c)
	sort.Slice(refs, func(i, j int) bool { return refs[i].Hash < refs[j].Hash }) // 同一棵树导两次字节相同
	var missing []string
	for _, ref := range refs {
		if e.blobs == nil {
			missing = append(missing, ref.Name)
			continue
		}
		body, err := e.readBlob(ctx, ref)
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
		return rep, fmt.Errorf("收尾归档: %w", err)
	}
	rep.Bytes = cw.n
	if len(missing) > 0 {
		// 附件字节没带上并入树：树里的引用还在，导回去时那几张图会指向缺失的字节。
		// 回执必须说明，否则用户以为备份完整。
		rep.Dropped = append(rep.Dropped, fmt.Sprintf(
			droppedBlobFmt, len(missing), summarizeNames(missing)))
	}
	return rep, nil
}

// importAQX 无损导入：解包 → 树自检 → 附件字节回填。
func (e *Exchange) importAQX(ctx context.Context, r io.Reader) (*conversation.Conversation, port.ExchangeReport, error) {
	rep := port.ExchangeReport{Format: "aqx"}
	data, err := readLimited(r, maxArchiveBytes, "归档")
	if err != nil {
		return nil, rep, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, rep, fmt.Errorf("不是可用的归档文件: %w", err)
	}
	if len(zr.File) > maxArchiveEntries {
		return nil, rep, fmt.Errorf("归档条目数 %d 超过 %d 上限", len(zr.File), maxArchiveEntries)
	}
	var (
		man        *manifest
		tree       []byte
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
			return nil, rep, fmt.Errorf("归档里有符号链接 %q：拒绝解包", f.Name)
		}
		switch {
		case name == "manifest.json":
			if man != nil {
				return nil, rep, errors.New("归档里有重复的 manifest.json")
			}
			m, err := readZipJSON(f, 1<<20)
			if err != nil {
				return nil, rep, err
			}
			man = m
		case name == "tree.json":
			if tree != nil {
				return nil, rep, errors.New("归档里有重复的 tree.json")
			}
			if f.UncompressedSize64 > maxTreeBytes {
				return nil, rep, fmt.Errorf("tree.json 超过 %d MB 上限", maxTreeBytes>>20)
			}
			tree, err = readZipLimited(f, maxTreeBytes, "tree.json")
			if err != nil {
				return nil, rep, err
			}
		case strings.HasPrefix(name, "blobs/"):
			if f.UncompressedSize64 > maxBlobBytes {
				return nil, rep, fmt.Errorf("归档内附件 %q 超过 %d MB 上限", f.Name, maxBlobBytes>>20)
			}
			totalBlobs += int64(f.UncompressedSize64)
			if totalBlobs > maxBlobTotalBytes {
				return nil, rep, fmt.Errorf("归档内附件总量超过 %d MB 上限", maxBlobTotalBytes>>20)
			}
			blobs = append(blobs, f)
		default:
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		// aqx 是我们自己的格式：不认识的条目意味着「不是这个版本写的」，猜着读比拒绝更糟。
		return nil, rep, fmt.Errorf("归档里有不认识的条目 %s（本版只认 manifest.json / tree.json / blobs/）",
			summarizeNames(unknown))
	}
	if man == nil {
		return nil, rep, errors.New("归档缺 manifest.json：这不是 Aquarius 归档")
	}
	if man.Format != "aqx" {
		return nil, rep, fmt.Errorf("归档声明的格式是 %q，不是 aqx", man.Format)
	}
	if man.Version > aqxVersion {
		return nil, rep, fmt.Errorf("归档版本 %d 高于本程序支持的 %d：请升级后再导入", man.Version, aqxVersion)
	}
	if tree == nil {
		return nil, rep, errors.New("归档缺 tree.json")
	}
	c, err := decodeTree(tree, "tree.json")
	if err != nil {
		return nil, rep, err
	}
	if n := c.NodeCount(); n > maxNodeCount {
		return nil, rep, fmt.Errorf("会话节点数 %d 超过 %d 上限", n, maxNodeCount)
	}
	rep.Nodes = nodeCount(c)
	// 附件回填：按内容寻址放回附件库；哈希对不上的归档拒绝（内容与名字不符意味着损坏）。
	dropped, err := e.restoreBlobs(ctx, blobs)
	if err != nil {
		return nil, rep, err
	}
	rep.Dropped = append(rep.Dropped, dropped...)
	// 树里引用、归档里却没有的附件：字节就是找不回来了，回执必须逐项说清——
	// 静默留下一个指向空处的引用，用户要到装配时才发现图没了。
	if missing := missingBlobs(c, blobs); len(missing) > 0 {
		rep.Dropped = append(rep.Dropped, fmt.Sprintf("附件 %d 个（归档里没有它们的字节）", len(missing)))
	}
	return c, rep, nil
}

// missingBlobs 树里引用、归档里没有的附件哈希（按引用去重）。
func missingBlobs(c *conversation.Conversation, files []*zip.File) []string {
	have := make(map[string]bool, len(files))
	for _, f := range files {
		if name, err := safeArchivePath(f.Name); err == nil {
			have[strings.TrimPrefix(name, "blobs/")] = true
		}
	}
	var out []string
	for _, ref := range collectBlobs(c) {
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
		return []string{fmt.Sprintf("附件 %d 个（本构建未配置附件库）", len(files))}, nil
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
			return nil, fmt.Errorf("归档内附件总量超过 %d MB 上限", maxBlobTotalBytes>>20)
		}
		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		if got != want {
			return nil, fmt.Errorf("归档内附件 %q 的内容哈希不符（声明 %s，实为 %s）：拒绝导入",
				f.Name, shortHash(want), shortHash(got))
		}
		// 名字与 MIME 不在附件库里（它们随 Part.Ref 存在树里，D17），这里只放字节。
		if _, err := e.blobs.Put(ctx, bytes.NewReader(body), "", ""); err != nil {
			return nil, fmt.Errorf("存回附件 %s: %w", shortHash(got), err)
		}
	}
	return nil, nil
}

// readBlob 从附件库取一份字节。
func (e *Exchange) readBlob(ctx context.Context, ref conversation.BlobRef) ([]byte, error) {
	rc, err := e.blobs.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readLimited(rc, maxBlobBytes, "附件 "+ref.Name)
}

// collectBlobs 树里被引用的附件（按哈希去重，名字取首个引用）。
func collectBlobs(c *conversation.Conversation) []conversation.BlobRef {
	seen := map[string]bool{}
	var out []conversation.BlobRef
	for _, m := range c.Snapshot().Nodes {
		for _, p := range m.Content {
			if p.Ref == nil || seen[p.Ref.Hash] {
				continue
			}
			seen[p.Ref.Hash] = true
			out = append(out, *p.Ref)
		}
	}
	return out
}

// writeZipEntry 写一个条目（函数式写法省掉「打开、写、关」三行样板）。
func writeZipEntry(zw *zip.Writer, name string, fn func(io.Writer) error) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("建归档条目 %s: %w", name, err)
	}
	if err := fn(w); err != nil {
		return fmt.Errorf("写归档条目 %s: %w", name, err)
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
		return nil, fmt.Errorf("解析 %s: %w", f.Name, err)
	}
	return &m, nil
}

// readZipLimited 读一个条目的全部内容，超限报错（不按声明的大小信任压缩包）。
func readZipLimited(f *zip.File, limit int64, what string) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", what, err)
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
		return nil, fmt.Errorf("读 %s: %w", what, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s 超过 %d MB 上限", what, limit>>20)
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
		return "", fmt.Errorf("归档条目 %q 是绝对路径：拒绝解包", name)
	}
	if len(n) >= 2 && n[1] == ':' {
		return "", fmt.Errorf("归档条目 %q 带盘符：拒绝解包", name)
	}
	clean := path.Clean(n)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("归档条目 %q 越出解包目录：拒绝解包", name)
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
	return fmt.Sprintf("%s 等 %d 项", strings.Join(names[:show], ", "), len(names))
}
