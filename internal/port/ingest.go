package port

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// RawInput 原始输入（文本 / 文件 / 剪贴板 / 麦克风）。
type RawInput struct {
	Kind string                `json:"kind"` // "text" | "file" | "clipboard" | "mic"
	Text string                `json:"text,omitempty"`
	File string                `json:"file,omitempty"`
	Blob *conversation.BlobRef `json:"blob,omitempty"` // clipboard 图片 / mic 音频（已先入附件库）
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
type OutputRequest struct {
	Message conversation.Message
	Parts   []conversation.Part
}

// OutputAdapter 输出方式适配器（TTS 播报 / 系统通知 / 导出文件）；失败只记日志不打断主流程。
type OutputAdapter interface {
	Name() string
	Deliver(ctx context.Context, req OutputRequest) error
}
