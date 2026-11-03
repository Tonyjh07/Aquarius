// Package ingestclip 实现剪贴板摄取（port.Ingestor，§7.2）：
// RawInput{Kind:"clipboard"}——图片/二进制已先入附件库（Blob，管线第一步由
// 产出方完成），或纯文本（Text）。剪贴板的抓取属 UI 职责（M4 TUI 的
// 粘贴入口走同一 UserInput.Raw），本包只做管线端：校验 + 产出 Part。
package ingestclip

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Ingestor = (*Ingestor)(nil)

// Ingestor 剪贴板摄取器。
type Ingestor struct {
	blobs port.AttachmentStore
}

// New 创建剪贴板摄取器（blobs 为附件库，校验 Blob 用；可为 nil——此时仅文本可用）。
func New(blobs port.AttachmentStore) *Ingestor { return &Ingestor{blobs: blobs} }

// Accepts 仅接受 Kind=clipboard 且带 Blob 或文本的输入。
func (in *Ingestor) Accepts(raw port.RawInput) bool {
	return raw.Kind == "clipboard" && (raw.Blob != nil || strings.TrimSpace(raw.Text) != "")
}

// Ingest 产出 Part：Blob 按 MIME 分类为图片/文档；纯文本直接内联。
func (in *Ingestor) Ingest(ctx context.Context, raw port.RawInput) (port.IngestReport, error) {
	if raw.Blob == nil {
		return port.IngestReport{
			Parts: []conversation.Part{{Kind: conversation.PartText, Text: strings.TrimSpace(raw.Text)}},
		}, nil
	}
	if in.blobs == nil {
		return port.IngestReport{}, errors.New(i18n.T("audio.clip.err_no_blobs"))
	}
	ok, err := in.blobs.Stat(ctx, *raw.Blob)
	if err != nil {
		return port.IngestReport{}, fmt.Errorf("%s: %w", i18n.T("audio.clip.err_stat"), err)
	}
	if !ok {
		return port.IngestReport{}, errors.New(i18n.Tf("audio.clip.err_missing",
			"hash", shortHash(raw.Blob.Hash), "name", raw.Blob.Name))
	}
	if strings.HasPrefix(raw.Blob.MIME, "image/") {
		return port.IngestReport{
			Parts: []conversation.Part{{Kind: conversation.PartImage, Ref: raw.Blob}},
			Note:  i18n.Tf("audio.clip.note_image", "name", raw.Blob.Name, "size", raw.Blob.Size),
		}, nil
	}
	// 非图片附件（如剪贴板带的文件）：按文档处理并给取用说明。
	text := fmt.Sprintf("[document %s] MIME=%s, sha256=%s (stored as an attachment).",
		raw.Blob.Name, raw.Blob.MIME, raw.Blob.Hash)
	return port.IngestReport{
		Parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: raw.Blob, Text: text}},
		Note:  i18n.Tf("audio.clip.note_file", "name", raw.Blob.Name, "mime", raw.Blob.MIME),
	}, nil
}

// shortHash 报因里只露哈希的前 12 个字符（够定位，又不至于把回执写成一行噪声）。
func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
