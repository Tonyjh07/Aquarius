// Package ingestfile 实现文件摄取（port.Ingestor，DESIGN §7.2 输入管线）：
// RawInput{Kind:"file"} → 附件先入库（AttachmentStore.Put）→ 按 MIME 分派：
//   - image/* → PartImage：装配时经附件库把字节内联进请求（§4.2）；
//   - 文本类 → PartDoc 带截断提取文本（D10 简单截断内联），截断处标注原文路径；
//   - 其余二进制（含音频，M3 无 ASR）→ PartDoc 只留引用与取用说明，
//     原文经 file_read 按路径读取。
//
// 多附件（D186）：一条输入可带多项（RawInput.File 是单附件简写），逐个摄取、
// **允许部分成功**——失败项跳过并在 Note 里逐条列因，全部失败才整体报错。
//
// 前置动作（打开/读取/入库）都在本适配器内完成——附件项是路径或已入库引用，
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

// Accepts 仅接受 Kind=file 且至少带一项附件的输入。
func (in *Ingestor) Accepts(raw port.RawInput) bool {
	return raw.Kind == "file" && raw.HasAttachments()
}

// Ingest 逐个摄取附件 → 合并分片（D186：部分成功）。
// 全程每个文件单次打开：分类/提取文本与入库字节取自同一快照（防两次读取间文件被改）。
func (in *Ingestor) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	if in.blobs == nil {
		return port.IngestReport{}, fmt.Errorf("ingestfile: 未配置附件库（port.AttachmentStore）")
	}
	atts := raw.AttachmentList()
	if len(atts) > port.MaxAttachments {
		return port.IngestReport{}, fmt.Errorf("ingestfile: 一条消息最多 %d 个附件（本次 %d 个）",
			port.MaxAttachments, len(atts))
	}

	var (
		parts  []conversation.Part
		notes  []string
		failed []string
	)
	for _, att := range atts {
		rep, err := in.ingestOne(ctx, att)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s（%v）", attachmentName(att), err))
			continue
		}
		parts = append(parts, rep.parts...)
		if rep.note != "" {
			notes = append(notes, rep.note)
		}
	}
	if len(parts) == 0 {
		return port.IngestReport{}, fmt.Errorf("ingestfile: 附件全部摄取失败：%s", strings.Join(failed, "；"))
	}
	note := strings.Join(notes, "；")
	if len(failed) > 0 {
		if note != "" {
			note += "；"
		}
		note += fmt.Sprintf("已跳过 %d 个附件：%s", len(failed), strings.Join(failed, "；"))
	}
	return port.IngestReport{Parts: in.withCaption(raw, parts), Note: note}, nil
}

// ingestItem 单个附件的摄取结果（分片 + 一条回执）。
type ingestItem struct {
	parts []conversation.Part
	note  string
}

// attachmentName 附件显示名（路径取文件名、已入库项取库内名）。
func attachmentName(att port.Attachment) string {
	if att.Blob != nil && att.Blob.Name != "" {
		return att.Blob.Name
	}
	if p := strings.TrimSpace(att.Path); p != "" {
		return filepath.Base(p)
	}
	return "(未命名附件)"
}

// ingestOne 分派一项附件：已入库字节（粘贴图片，D187）或待读路径。
func (in *Ingestor) ingestOne(ctx context.Context, att port.Attachment) (ingestItem, error) {
	if att.Blob != nil {
		return in.ingestBlob(ctx, att.Blob)
	}
	return in.ingestPath(ctx, strings.TrimSpace(att.Path))
}

// ingestBlob 已入库附件的摄取：字节不重复读，只按 MIME 生成分片（D187）。
func (in *Ingestor) ingestBlob(ctx context.Context, ref *conversation.BlobRef) (ingestItem, error) {
	ok, err := in.blobs.Stat(ctx, *ref)
	if err != nil {
		return ingestItem{}, fmt.Errorf("检查附件: %w", err)
	}
	if !ok {
		return ingestItem{}, fmt.Errorf("附件不存在（须先入附件库）")
	}
	if strings.HasPrefix(ref.MIME, "image/") {
		return ingestItem{
			parts: []conversation.Part{{Kind: conversation.PartImage, Ref: ref}},
			note:  fmt.Sprintf("已附图片 %s（%d 字节）", ref.Name, ref.Size),
		}, nil
	}
	text := fmt.Sprintf("[document %s] MIME=%s, sha256=%s (stored as an attachment).",
		ref.Name, ref.MIME, ref.Hash)
	return ingestItem{
		parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: ref, Text: text}},
		note:  fmt.Sprintf("已附文件 %s（%s）", ref.Name, ref.MIME),
	}, nil
}

// ingestPath 路径附件的摄取（打开 → 分类 → 入库 → 分片）。
func (in *Ingestor) ingestPath(ctx context.Context, path string) (ingestItem, error) {
	if path == "" {
		return ingestItem{}, fmt.Errorf("附件路径为空")
	}
	if !filepath.IsAbs(path) {
		return ingestItem{}, fmt.Errorf("路径必须是绝对路径（本产品无工作区概念），当前为 %q", path)
	}
	path = filepath.Clean(path)
	name := filepath.Base(path)

	f, err := os.Open(path)
	if err != nil {
		return ingestItem{}, fmt.Errorf("打开文件: %w", err)
	}
	defer f.Close()

	// 头部分类与文本提取（读 maxExtractText+1 以判定是否截断）。
	head, more, err := readHead(f)
	if err != nil {
		return ingestItem{}, err
	}
	mime := detectMime(head, path)

	// 回到文件头，同一 fd 的完整内容入附件库（DESIGN §7.2 输入管线第一步）。
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return ingestItem{}, fmt.Errorf("回到文件头: %w", err)
	}
	ref, err := in.blobs.Put(ctx, f, mime, name)
	if err != nil {
		return ingestItem{}, fmt.Errorf("附件入库: %w", err)
	}

	switch {
	case strings.HasPrefix(mime, "image/"):
		return ingestItem{
			parts: []conversation.Part{{Kind: conversation.PartImage, Ref: &ref}},
			note:  fmt.Sprintf("已附图片 %s（%d 字节）", name, ref.Size),
		}, nil

	case structuredKind(path) != "": // D105：常见文档转文本（解析失败回落占位）
		kind := structuredKind(path)
		text, truncated, perr := extractStructured(kind, f, path)
		if perr != nil {
			// 解析失败（加密/损坏/超大）：占位并注明原因——提取尽力而为（D105④）。
			text = fmt.Sprintf("[document %s] failed to parse (%v); source file: %s (use file_read to read it).", name, perr, path)
			return ingestItem{
				parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: &ref, Text: text}},
				note:  fmt.Sprintf("已附文件 %s（解析失败，回落占位：%v）", name, perr),
			}, nil
		}
		if truncated {
			text += fmt.Sprintf("\n...[truncated; use file_read: %s]", path)
		}
		what := map[string]string{"pdf": "PDF", "docx": "Word", "xlsx": "Excel", "html": "HTML"}[kind]
		return ingestItem{
			parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: &ref, Text: text}},
			note:  fmt.Sprintf("已附%s文档 %s（提取文本 %d 字符已内联）", what, name, len([]rune(text))),
		}, nil

	case isTextish(mime, head, path):
		text := strings.ToValidUTF8(string(head), "")
		if more {
			text += fmt.Sprintf("\n...[truncated; use file_read: %s]", path)
		}
		return ingestItem{
			parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: &ref, Text: text}},
			note:  fmt.Sprintf("已附文档 %s（提取文本 %d 字符已内联）", name, len([]rune(text))),
		}, nil

	default:
		// 二进制（含音频：M3 无 ASR，不做转写）：模型侧只给取用说明。
		text := fmt.Sprintf("[document %s] binary content not extracted; source file: %s (use file_read to read it).", name, path)
		return ingestItem{
			parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: &ref, Text: text}},
			note:  fmt.Sprintf("已附文件 %s（二进制，%d 字节）", name, ref.Size),
		}, nil
	}
}

// withCaption 把随附文本（raw.Text，D104② 暂存随文发）并入首分片：file kind 现状
// 无视 RawInput.Text——GUI 暂存随文发后该字段承载已输入文本，须进树（标题优先取
// 文本分片由 ingestTitle 既有口径承接）。空文本原样返回。
func (in *Ingestor) withCaption(raw port.RawInput, parts []conversation.Part) []conversation.Part {
	if strings.TrimSpace(raw.Text) == "" {
		return parts
	}
	out := make([]conversation.Part, 0, len(parts)+1)
	out = append(out, conversation.Part{Kind: conversation.PartText, Text: raw.Text})
	return append(out, parts...)
}

// readHead 从已打开的文件读取头部至多 maxExtractText+1 字节；more=是否还有更多内容。
func readHead(f *os.File) ([]byte, bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false, fmt.Errorf("ingestfile: 定位文件头: %w", err)
	}
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
	// 嗅探把多数纯文本判为 text/plain、OOXML 判为 zip——扩展名明确时给出更准的类型
	//（仅影响引用元数据；解析分派走 structuredKind 的独立扩展名判定，D105）。
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".pdf":
		return "application/pdf"
	case ".html", ".htm":
		return "text/html"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx", ".xlsm":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	}
	return mime
}

// isTextish 是否按文本提取：MIME 为 text/*（或文本型结构格式）且头部无 NUL。
func isTextish(mime string, head []byte, path string) bool {
	// D105：rtf 控制词会污染提取文本（无成熟纯 Go 解析库）——显式排除、回落二进制占位。
	if strings.EqualFold(filepath.Ext(path), ".rtf") {
		return false
	}
	// D105：OOXML 容器（mime 扩展名校正带 openxmlformats——其中含 "xml" 会被下方
	// Contains 误判为文本）不按文本提取。
	if strings.Contains(mime, "openxmlformats") {
		return false
	}
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
