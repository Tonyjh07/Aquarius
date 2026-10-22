package llm

// 端点工具名的可移植性（D195）：严格端点（OpenAI / Anthropic / DeepSeek 等）
// 只接受 `^[a-zA-Z0-9_-]+$` 且长度 ≤64 的函数名；内核的 MCP 命名是
// `mcp:<server>:<tool>`（日志/审计/权限矩阵按冒号可解析）。
//
// 两者在**出站边界**换算：声明与历史调用发端点安全别名，回传的别名再换回内部名。
// 别名按本次请求的工具集生成，因此同名清洗碰撞也能稳定消歧（追加短哈希）。

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/tool"
)

// wireNameLimit 端点函数名长度上限（OpenAI 系为 64）。
const wireNameLimit = 64

// wireNames 一次请求内的内部名 ⇄ 端点别名双向表。
type wireNames struct {
	toWire  map[string]string
	toLocal map[string]string
}

// newWireNames 按本次声明的工具集生成别名表。
func newWireNames(specs []tool.Spec) *wireNames {
	w := &wireNames{toWire: make(map[string]string, len(specs)), toLocal: make(map[string]string, len(specs))}
	if len(specs) == 0 {
		return w
	}
	groups := make(map[string][]string, len(specs))
	for _, s := range specs {
		b := wireBaseName(s.Name)
		groups[b] = append(groups[b], s.Name)
	}
	for base, names := range groups {
		if len(names) == 1 {
			w.toWire[names[0]] = base
			w.toLocal[base] = names[0]
			continue
		}
		// 清洗后碰撞（如 mcp:a:b 与 mcp_a_b）：同组每个都带原名的短哈希，
		// 结果与声明顺序无关，跨轮稳定。
		for _, n := range names {
			alias := wireShorten(base, n)
			w.toWire[n] = alias
			w.toLocal[alias] = n
		}
	}
	return w
}

// Wire 内部名 → 端点别名。未登记的名字（已下线的历史工具）按同一规则现算。
func (w *wireNames) Wire(name string) string {
	if w != nil {
		if alias, ok := w.toWire[name]; ok {
			return alias
		}
	}
	return wireBaseName(name)
}

// Local 端点别名 → 内部名。未知名字原样返回：模型臆造的名字交给 runner 报未知工具。
func (w *wireNames) Local(name string) string {
	if w != nil {
		if local, ok := w.toLocal[name]; ok {
			return local
		}
	}
	return name
}

// wireBaseName 单个名字的端点安全形态：非法字符（含 MCP 命名空间的冒号）换成下划线；
// 超长时按原名哈希截断，保证确定性。
func wireBaseName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_' || c == '-',
			c >= '0' && c <= '9',
			c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z':
			b.WriteByte(c)
		default:
			// 多字节 UTF-8 逐字节落成多个下划线也无妨：只为可移植，不要求可逆。
			b.WriteByte('_')
		}
	}
	base := b.String()
	if base == "" {
		base = "tool"
	}
	if len(base) <= wireNameLimit {
		return base
	}
	return wireShorten(base, name)
}

// wireShorten 截断 + 8 位短哈希（按原名），长度不超过上限。
func wireShorten(base, name string) string {
	sum := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:4])
	cut := wireNameLimit - len(suffix)
	if cut < 1 {
		cut = 1
	}
	if len(base) > cut {
		base = base[:cut]
	}
	return base + suffix
}
