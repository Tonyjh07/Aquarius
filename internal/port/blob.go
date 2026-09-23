package port

import (
	"context"
	"io"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// AttachmentStore 附件库端口：sha256 内容寻址、同内容只存一份、GC 按引用清扫（DESIGN §4.2）。
// 引用值对象为 domain/conversation.BlobRef（D17）。
type AttachmentStore interface {
	Put(ctx context.Context, r io.Reader, mime, name string) (conversation.BlobRef, error)
	Get(ctx context.Context, ref conversation.BlobRef) (io.ReadCloser, error)
	Stat(ctx context.Context, ref conversation.BlobRef) (bool, error)
	GC(ctx context.Context, keep map[string]bool) error
}
