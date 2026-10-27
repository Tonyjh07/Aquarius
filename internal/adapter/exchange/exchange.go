// Package exchange 实现 port.SessionExporter 与 port.SessionImporter（DESIGN §5.16，D242）：
// 把会话搬出去、把别人的会话搬进来。
//
// 无损与互通是两种东西：`aqx` 必须能原样往返（落盘 wire 形状 + 附件字节），互通格式按别人的
// 规范如实有损。两者共用一层容器与校验，但绝不互相冒充——回执里的「没有带出去」是事实，
// 不是免责声明。
package exchange

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/adapter/storejson"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var (
	_ port.SessionExporter = (*Exchange)(nil)
	_ port.SessionImporter = (*Exchange)(nil)
)

// Options 装配参数。
type Options struct {
	// Blobs 附件库：导出时按引用取字节，导入时把归档里的字节放回去。
	// 为空时 `aqx` 仍能导出/导入树，只是附件字节缺席（回执里如实记一条「没有带」）。
	Blobs port.AttachmentStore
}

// Exchange 会话进出适配器。
//
// 所有格式共用一份格式表（port.ExchangeFormats），命令面的取值与回执文案因此不会各说一套。
type Exchange struct {
	blobs port.AttachmentStore
}

// New 构造适配器。
func New(opts Options) *Exchange {
	return &Exchange{blobs: opts.Blobs}
}

// Formats 全部格式（表序即用法提示的顺序）。
func (e *Exchange) Formats() []port.ExchangeFormat { return port.ExchangeFormats() }

// Export 按 format 写出整棵树（DESIGN §5.16）。
func (e *Exchange) Export(ctx context.Context, c *conversation.Conversation, format string, w io.Writer) (port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return port.ExchangeReport{}, err
	}
	if c == nil {
		return port.ExchangeReport{}, errors.New("exchange: 会话为空")
	}
	rep := port.ExchangeReport{Format: format}
	switch format {
	case "aqx":
		return e.exportAQX(ctx, c, w)
	case "chatgpt":
		return e.exportChatGPT(ctx, c, w)
	case "openai":
		return e.exportOpenAI(ctx, c, w)
	case "sillytavern":
		return e.exportSillyTavern(ctx, c, w)
	case "openwebui":
		return rep, errors.New("openwebui 只认不写：要喂 Open WebUI 用 chatgpt 那份")
	default:
		return rep, fmt.Errorf("不认识的导出格式 %q（可写：%s）", format, writableNames())
	}
}

// Import 读入一个外部文件并自检（DESIGN §5.16 的「外部文件是不可信输入」）。
func (e *Exchange) Import(ctx context.Context, r io.Reader, f port.ExchangeFormat) (*conversation.Conversation, port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, port.ExchangeReport{}, err
	}
	rep := port.ExchangeReport{Format: f.Name}
	switch f.Name {
	case "aqx":
		c, rep, err := e.importAQX(ctx, r)
		if err != nil {
			return nil, rep, err
		}
		return c, rep, nil
	case "chatgpt":
		return e.importChatGPT(ctx, r)
	case "openai":
		return e.importOpenAI(ctx, r)
	case "sillytavern":
		return e.importSillyTavern(ctx, r)
	case "openwebui":
		return nil, rep, fmt.Errorf("导入格式 %q 本版未接入", f.Name)
	default:
		return nil, rep, fmt.Errorf("不认识的导入格式 %q", f.Name)
	}
}

// Detect 按文件名与文件头认格式（DESIGN §5.16）。
//
// 只做形状判断，不解析内容：认错时 Import 会报因，而在这里做完整解析等于把文件读两遍。
// 顺序按「形状的特异度」排：ZIP 最特异，其次是带 `mapping` 的树，再是 `.jsonl`
// （它连 JSON 整体都不是），最后才是宽泛的 `messages` 数组。
func (e *Exchange) Detect(name string, head []byte) (port.ExchangeFormat, bool) {
	if isZip(head) || strings.EqualFold(fileExt(name), ".aqx") {
		return formatNamed("aqx")
	}
	if looksLikeChatGPT(head) {
		return formatNamed("chatgpt")
	}
	if looksLikeSillyTavern(head) {
		return formatNamed("sillytavern")
	}
	if looksLikeOpenAI(head) {
		return formatNamed("openai")
	}
	return port.ExchangeFormat{}, false
}

// formatNamed 按名取格式事实（格式表里没有就不算认出来）。
func formatNamed(name string) (port.ExchangeFormat, bool) {
	for _, f := range port.ExchangeFormats() {
		if f.Name == name {
			return f, true
		}
	}
	return port.ExchangeFormat{}, false
}

// isZip ZIP 魔数（`PK\x03\x04` 等四种块类型）：aqx 是唯一装在 ZIP 里的格式。
func isZip(head []byte) bool {
	return len(head) >= 4 && head[0] == 'P' && head[1] == 'K' &&
		(head[2] == 3 || head[2] == 5 || head[2] == 7) && head[3] == 4
}

// fileExt 文件名的小写扩展名（含点；没有则空）。
func fileExt(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return strings.ToLower(name[i:])
}

// writableNames 可写格式名的用户面清单（错误文案用）。
func writableNames() string {
	var names []string
	for _, f := range port.ExchangeFormats() {
		if !f.NoExport {
			names = append(names, f.Name)
		}
	}
	return strings.Join(names, " / ")
}

// nodeCount 用户眼里的节点数：Root 是会话自身的挂点，不算「一条消息」。
func nodeCount(c *conversation.Conversation) int {
	if n := c.NodeCount() - 1; n > 0 {
		return n
	}
	return 0
}

// countWriter 累计写入量（回执里的字节数；ZIP 是流式写，无法事后回看）。
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// decodeTree 解析落盘 wire 形状的树（`sheet` 参数只为错误文案带上来源）。
func decodeTree(data []byte, source string) (*conversation.Conversation, error) {
	c, err := storejson.DecodeTree(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return c, nil
}
