// Package ingestclip 实现剪贴板摄取（port.Ingestor，DESIGN §7.2）：
// RawInput{Kind:"clipboard"}——图片/二进制已先入附件库（Blob，管线第一步由
// 产出方完成），或纯文本（Text）。剪贴板的抓取属 UI 职责（M4 TUI 的
// 粘贴入口走同一 UserInput.Raw），本包只做管线端：校验 + 产出 Part。
package ingestclip

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
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
		return port.IngestReport{}, fmt.Errorf("ingestclip: 未配置附件库（port.AttachmentStore）")
	}
	ok, err := in.blobs.Stat(ctx, *raw.Blob)
	if err != nil {
		return port.IngestReport{}, fmt.Errorf("ingestclip: 检查附件: %w", err)
	}
	if !ok {
		return port.IngestReport{}, fmt.Errorf("ingestclip: 附件不存在（须先入附件库）: %.12s… %s",
			raw.Blob.Hash, raw.Blob.Name)
	}
	if strings.HasPrefix(raw.Blob.MIME, "image/") {
		return port.IngestReport{
			Parts: []conversation.Part{{Kind: conversation.PartImage, Ref: raw.Blob}},
			Note:  fmt.Sprintf("已附剪贴板图片 %s（%d 字节）", raw.Blob.Name, raw.Blob.Size),
		}, nil
	}
	// 非图片附件（如剪贴板带的文件）：按文档处理并给取用说明。
	text := fmt.Sprintf("〔文档 %s〕MIME=%s，哈希=%s（附件已入库，可经装配查看）。",
		raw.Blob.Name, raw.Blob.MIME, raw.Blob.Hash)
	return port.IngestReport{
		Parts: []conversation.Part{{Kind: conversation.PartDoc, Ref: raw.Blob, Text: text}},
		Note:  fmt.Sprintf("已附剪贴板文件 %s（%s）", raw.Blob.Name, raw.Blob.MIME),
	}, nil
}
