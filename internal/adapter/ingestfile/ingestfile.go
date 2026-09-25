// Package ingestfile 实现文件摄取（port.Ingestor，DESIGN §7.2 输入管线）：
// RawInput{Kind:"file"} → 附件先入库（AttachmentStore.Put）→ 按 MIME 分派：
//   - image/* → PartImage：装配时经附件库把字节内联进请求（§4.2）；
//   - 文本类 → PartDoc 带截断提取文本（D10 简单截断内联），截断处标注原文路径；
//   - 其余二进制（含音频，M3 无 ASR）→ PartDoc 只留引用与取用说明，
//     原文经 file_read 按路径读取。
//
// 前置动作（打开/读取/入库）都在本适配器内完成——RawInput.File 是路径，
// Session 只负责分派与入树。
package ingestfile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Ingestor = (*Ingestor)(nil)

// maxExtractText Doc 提取文本上限（约 8k tokens；D10 截断内联，全文经 file_read 取用）。
const maxExtractText = 32 << 10

// Ingestor 文件摄取器。
type Ingestor struct {
	blobs port.AttachmentStore
}

// New 创建文件摄取器（blobs 为附件库，装配根注入）。
func New(blobs port.AttachmentStore) *Ingestor { return &Ingestor{blobs: blobs} }

// Accepts 仅接受 Kind=file 且带路径的输入。
func (in *Ingestor) Accepts(raw port.RawInput) bool {
	return raw.Kind == "file" && strings.TrimSpace(raw.File) != ""
}

// Ingest 读文件 → 附件入库 → 产出 Part（见包注释的分类规则）。
func (in *Ingestor) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	if in.blobs == nil {
		return port.IngestReport{}, fmt.Errorf("ingestfile: 未配置附件库（port.AttachmentStore）")
	}
	path := strings.TrimSpace(raw.File)
	if !filepath.IsAbs(path) {
		return port.IngestReport{}, fmt.Errorf("ingestfile: 路径必须是绝对路径（本产品无工作区概念），当前为 %q", path)
	}
	path = filepath.Clean(path)
	name := filepath.Base(path)

	// 头部分类与文本提取（读 maxExtractText+1 以判定是否截断）。
	head, more, err := readHead(path)
	if err != nil {
		return port.IngestReport{}, err
	}
	mime := detectMime(head, path)

	// 附件先落库（DESIGN §7.2 输入管线第一步）。
	ref, err := in.put(ctx, path, mime, name)
	if err != nil {
		return port.IngestReport{}, err
	}

	switch {
	case strings.HasPrefix(mime, "image/"):
		return port.IngestReport{
			Parts: []conversation.Part{{Kind: conversation.PartImage, Ref: &ref}},
			Note:  fmt.Sprintf("已附图片 %s（%d 字节）", name, ref.Size),
		}, nil

	case isTextish(mime, head, path):
		text := strings.ToValidUTF8(string(head), "")
		if more {
			text += fmt.Sprintf("\n…[提取文本已截断，全文可用 file_read 读取：%s]", path)
		}
		return port.IngestReport{
			Parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: &ref, Text: text}},
			Note:  fmt.Sprintf("已附文档 %s（提取文本 %d 字符已内联）", name, len([]rune(text))),
		}, nil

	default:
		// 二进制（含音频：M3 无 ASR，不做转写）：模型侧只给取用说明。
		text := fmt.Sprintf("〔文档 %s〕二进制内容未提取，原文路径：%s（可用 file_read 读取）。", name, path)
		return port.IngestReport{
			Parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: &ref, Text: text}},
			Note:  fmt.Sprintf("已附文件 %s（二进制，%d 字节）", name, ref.Size),
		}, nil
	}
}

// put 打开文件流式入附件库。
func (in *Ingestor) put(ctx context.Context, path, mime, name string) (conversation.BlobRef, error) {
	f, err := os.Open(path)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("ingestfile: 打开文件: %w", err)
	}
	defer f.Close()
	ref, err := in.blobs.Put(ctx, f, mime, name)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("ingestfile: 附件入库: %w", err)
	}
	return ref, nil
}

// readHead 读取文件头部至多 maxExtractText+1 字节；more=是否还有更多内容。
func readHead(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("ingestfile: 打开文件: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxExtractText+1))
	if err != nil {
		return nil, false, fmt.Errorf("ingestfile: 读取文件: %w", err)
	}
	if len(data) > maxExtractText {
		return data[:maxExtractText], true, nil
	}
	return data, false, nil
}

// detectMime 判定 MIME：内容嗅探优先（首 512 字节），结构化文本扩展名校正。
func detectMime(head []byte, path string) string {
	mime := http.DetectContentType(head)
	// 嗅探把多数纯文本判为 text/plain；扩展名明确时给出更准的类型（仅影响引用元数据）。
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	}
	return mime
}

// isTextish 是否按文本提取：MIME 为 text/*（或文本型结构格式）且头部无 NUL。
func isTextish(mime string, head []byte, path string) bool {
	if len(head) > 0 && bytes.IndexByte(head, 0) >= 0 {
		return false // 含 NUL 判定为二进制（与 file_read 同口径）
	}
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	if strings.Contains(mime, "json") || strings.Contains(mime, "xml") ||
		strings.Contains(mime, "yaml") || strings.Contains(mime, "csv") {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".md", ".csv", ".tsv", ".yaml", ".yml", ".toml", ".ini",
		".log", ".go", ".py", ".js", ".ts", ".sh", ".bat", ".ps1", ".sql":
		return true
	}
	return false
}
