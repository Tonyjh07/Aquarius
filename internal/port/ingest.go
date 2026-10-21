package port

import (
	"context"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// MaxAttachments 单条消息的附件数上限（D186）。摄取端与界面共用这一个常量，
// 报错与提示文案由它生成（不各写一个数字）。
const MaxAttachments = 8

// MaxImageBytes 单张图片的字节上限（D169/D187）：读图工具与粘贴/拖拽入口共用，
// 超限一律明确报错（界面报「图片过大」），不静默降级成占位。
const MaxImageBytes = 20 << 20

// Attachment 一项待摄取附件（D186）：Path（文件路径）与 Blob（已入库字节）二选一。
// Blob 供粘贴图片走（D187）——字节已入库，不再从文件系统读第二遍。
type Attachment struct {
	Path string                `json:"path,omitempty"`
	Blob *conversation.BlobRef `json:"blob,omitempty"`
}

// RawInput 原始输入（文本 / 文件 / 剪贴板 / 麦克风）。
//
// 多附件口径（D186）：Attachments 是列表，File / Blob 保留为**单附件简写**
// （File ≡ 一个 Path 项、Blob ≡ 一个已入库项），两者与 Attachments 可以同时出现。
type RawInput struct {
	Kind        string                `json:"kind"` // "text" | "file" | "clipboard" | "mic"
	Text        string                `json:"text,omitempty"`
	File        string                `json:"file,omitempty"` // 单文件简写（等价于 Attachments 只带一项）
	Blob        *conversation.BlobRef `json:"blob,omitempty"` // clipboard 图片 / mic 音频（已先入附件库）
	Attachments []Attachment          `json:"attachments,omitempty"`
	// Seconds mic 录音时长（D179；0 = 缺省 60 秒，硬顶由采集侧夹取）。
	Seconds int `json:"seconds,omitempty"`
}

// AttachmentList 归一化附件清单（D186）：File / Blob 简写并入列表，顺序为
// 「File → Attachments → Blob」。调用方按返回序生成分片，空列表 = 无附件。
func (r RawInput) AttachmentList() []Attachment {
	if !r.HasAttachments() {
		return nil
	}
	out := make([]Attachment, 0, len(r.Attachments)+2)
	if strings.TrimSpace(r.File) != "" {
		out = append(out, Attachment{Path: r.File})
	}
	out = append(out, r.Attachments...)
	if r.Blob != nil {
		out = append(out, Attachment{Blob: r.Blob})
	}
	return out
}

// HasAttachments 是否带任何附件（含 File / Blob 简写）。
func (r RawInput) HasAttachments() bool {
	return strings.TrimSpace(r.File) != "" || r.Blob != nil || len(r.Attachments) > 0
}

// IngestReport 摄取结果：可入消息的内容分片 + 备注（如"已由 ASR 转写"）。
type IngestReport struct {
	Parts []conversation.Part `json:"parts"`
	Note  string              `json:"note,omitempty"`
}

// Ingestor 输入方式适配器。
type Ingestor interface {
	Accepts(raw RawInput) bool
	Ingest(ctx context.Context, raw RawInput) (IngestReport, error)
}

// OutputRequest 输出请求。
// Parts 取自已提交节点的 Content，**可能含思考分片（PartThinking，D42）**——
// 通知/TTS/导出属"对外播报正文"，适配器须自行筛选正文分片（notify 只取 PartText）。
type OutputRequest struct {
	Message conversation.Message
	Parts   []conversation.Part
}

// OutputAdapter 输出方式适配器（TTS 播报 / 系统通知 / 导出文件）；失败只记日志不打断主流程。
type OutputAdapter interface {
	Name() string
	Deliver(ctx context.Context, req OutputRequest) error
}
