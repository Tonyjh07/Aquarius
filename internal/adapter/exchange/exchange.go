// Package exchange 实现 port.SessionExporter 与 port.SessionImporter（§5.16，D242）：
// 把会话搬出去、把别人的会话搬进来。
//
// 无损与互通是两种东西：`aqx` 必须能原样往返（落盘 wire 形状 + 附件字节），互通格式按别人的
// 规范如实有损。两者共用一层容器与校验，但绝不互相冒充——回执里的「没有带出去」是事实，
// 不是免责声明。
package exchange

import (
	"bytes"
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
	_ port.ExchangeIndexer = (*Exchange)(nil)
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

// Export 按 format 写出一组会话（§5.16）。
func (e *Exchange) Export(ctx context.Context, cs []*conversation.Conversation, format string, w io.Writer) (port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return port.ExchangeReport{}, err
	}
	if len(cs) == 0 {
		return port.ExchangeReport{}, errors.New("exchange: 没有要导出的会话")
	}
	for _, c := range cs {
		if c == nil {
			return port.ExchangeReport{}, errors.New("exchange: 会话为空")
		}
	}
	rep := port.ExchangeReport{Format: format}
	if format != "aqx" && len(cs) > 1 {
		// 不写第一个、也不静默丢其余的：别人按那份规范读，一份半成品比没有更糟。
		return rep, fmt.Errorf("%s 这种格式一次只能写一个会话（现在给了 %d 个）：要备份多个会话请用 aqx", format, len(cs))
	}
	switch format {
	case "aqx":
		return e.exportAQX(ctx, cs, w)
	case "chatgpt":
		return e.exportChatGPT(ctx, cs[0], w)
	case "openai":
		return e.exportOpenAI(ctx, cs[0], w)
	case "sillytavern":
		return e.exportSillyTavern(ctx, cs[0], w)
	case "openwebui":
		return rep, errors.New("openwebui 只认不写：要喂 Open WebUI 用 chatgpt 那份")
	default:
		return rep, fmt.Errorf("不认识的导出格式 %q（可写：%s）", format, writableNames())
	}
}

// Import 读入一个外部文件并自检（§5.16 的「外部文件是不可信输入」）。
func (e *Exchange) Import(ctx context.Context, r io.Reader, f port.ExchangeFormat, pick []int) ([]*conversation.Conversation, port.ExchangeReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, port.ExchangeReport{}, err
	}
	rep := port.ExchangeReport{Format: f.Name}
	if f.Name != "aqx" {
		// 非容器只有一项：只认空或 {1}，其余报因（不静默当成「全导」）。
		if len(pick) > 1 || (len(pick) == 1 && pick[0] != 1) {
			return nil, rep, errors.New("这种格式只有一项，--pick 只接受 1")
		}
	}
	switch f.Name {
	case "aqx":
		return e.importAQX(ctx, r, pick)
	case "chatgpt":
		c, rep, err := e.importChatGPT(ctx, r)
		if err != nil {
			return nil, rep, err
		}
		return []*conversation.Conversation{c}, rep, nil
	case "openai":
		c, rep, err := e.importOpenAI(ctx, r)
		if err != nil {
			return nil, rep, err
		}
		return []*conversation.Conversation{c}, rep, nil
	case "sillytavern":
		c, rep, err := e.importSillyTavern(ctx, r)
		if err != nil {
			return nil, rep, err
		}
		return []*conversation.Conversation{c}, rep, nil
	case "openwebui":
		c, rep, err := e.importOpenWebUI(ctx, r)
		if err != nil {
			return nil, rep, err
		}
		return []*conversation.Conversation{c}, rep, nil
	default:
		return nil, rep, fmt.Errorf("不认识的导入格式 %q", f.Name)
	}
}

// Detect 按文件名与文件头认格式（§5.16）。
//
// 只看形状，不解析整份文件：认错时 Import 会报因，在这里做完整解析等于把文件读两遍。
//
// 判定顺序按「形状的特异度」排，且**先看容器前缀再看键**：
//   - ZIP 魔数最特异，扩展名 `.aqx` 兜底；
//   - 顶层是 `[` 的才可能是 chatgpt（它的导出就是数组），再看里面有没有 `"mapping"`；
//   - 顶层是 `{` 时才可能是 openwebui / openai，看 `"chat"` 是否紧跟对象、`"messages"` 是否
//     紧跟数组；
//   - `.jsonl` 连整份 JSON 都不是，单独按首行判。
//
// 不能拿 `strings.Contains(head, "\"chat\"")` 这种子串兜底：正文里出现 chat 一词的普通
// openai 文件就会先被认成 openwebui（错误文案还会说「文件里没有消息」）；反过来，标题较长
// 的 chatgpt 导出在前几百字节里既没有完整 JSON 也没有 `"mapping"`，子串判断又漏掉它。
func (e *Exchange) Detect(name string, head []byte) (port.ExchangeFormat, bool) {
	if isZip(head) || strings.EqualFold(fileExt(name), ".aqx") {
		return formatNamed("aqx")
	}
	if looksLikeSillyTavern(head) {
		return formatNamed("sillytavern")
	}
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	if len(trimmed) == 0 {
		return port.ExchangeFormat{}, false
	}
	switch trimmed[0] {
	case '[':
		// 顶层数组：chatgpt 的导出是 `[{…}]`，里面才有 mapping。
		if topKeyStarts(trimmed, "mapping", '{') {
			return formatNamed("chatgpt")
		}
	case '{':
		if topKeyStarts(trimmed, "chat", '{') {
			return formatNamed("openwebui")
		}
		if topKeyStarts(trimmed, "messages", '[') {
			return formatNamed("openai")
		}
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

// topKeyStarts 文件头的顶层对象里，键 name 的值是否以 opener（`{` 或 `[`）开头。
//
// 「顶层」= 文档最外层对象自己的成员，或顶层数组第一个元素对象的成员（chatgpt 的导出是
// 后者：数组里一项一个会话）。只扫结构、跳过字符串内容——文件头是截断的，完整 JSON 解析
// 必然失败，而退化成子串匹配会把正文里的词当成键（正文里出现一句 `"chat"` 的 openai
// 文件就会被认成 openwebui）。
//
// 截断、坏 JSON、未闭合的串都只是提前结束：扫不到就返回 false，由调用方报「认不出」。
func topKeyStarts(head []byte, name string, opener byte) bool {
	var (
		stack     []byte // 未闭合的容器：'o' 对象 / 'a' 数组
		inStr     bool
		escaped   bool
		expectKey bool   // 对象里等一个键
		pendingKe bool   // 刚读到键，等它的值
		pending   string // 待配对的键名
	)
	top := func() bool {
		// 顶层对象，或顶层数组里的第一个元素对象。
		if len(stack) == 1 {
			return stack[0] == 'o'
		}
		return len(stack) == 2 && stack[0] == 'a' && stack[1] == 'o'
	}
	for i := 0; i < len(head); i++ {
		c := head[i]
		if inStr {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c != '"' {
				continue
			}
			inStr = false
			continue
		}
		switch c {
		case ' ', '\t', '\r', '\n':
		case '"':
			start := i + 1
			j := start
			for ; j < len(head); j++ {
				if head[j] == '\\' {
					j++
					continue
				}
				if head[j] == '"' {
					break
				}
			}
			if expectKey && len(stack) > 0 && stack[len(stack)-1] == 'o' {
				expectKey = false
				pending = string(head[start:j])
				pendingKe = true
			}
			i = j
		case ',':
			pendingKe = false
			expectKey = len(stack) > 0 && stack[len(stack)-1] == 'o'
		case ':':
			// 键值分隔：待配对的键保留，等值开头的容器来认领。
		case '{', '[':
			if pendingKe && pending == name && c == opener && top() {
				return true
			}
			pendingKe = false
			if c == '{' {
				stack = append(stack, 'o')
				expectKey = true
			} else {
				stack = append(stack, 'a')
				expectKey = false
			}
		case '}', ']':
			pendingKe = false
			expectKey = false
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			// 数字、true/false/null：值到了，待配对的键作废。
			pendingKe = false
			expectKey = false
		}
	}
	return false
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
