package exchange

// helper_test.go 单会话进出的测试便捷入口。
//
// 端口签名是「一组会话」（§5.16 的 aqx 容器），但绝大多数格式只装得下一条链；这些用例关心
// 的是那一条链本身，逐处写 `[]*conversation.Conversation{c}` 只会把断言埋进样板里。
// 多会话口径由 aqx 的多会话用例单列断言。

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// trHead 文案里第一个占位符之前的那截。
//
// 断言跟着 key 走：文案改了测试跟着改；实参（计数、名字）不在这里重复一遍，那条由功能用例
// 自己盯着。只用在这类「回执里提到了某一类东西」的宽断言上。
func trHead(key string) string {
	t := i18n.T(key)
	if i := strings.Index(t, "{"); i >= 0 {
		return t[:i]
	}
	return t
}

// exportOne 单会话导出。
func exportOne(ctx context.Context, ex port.SessionExporter, c *conversation.Conversation, format string, w io.Writer) (port.ExchangeReport, error) {
	return ex.Export(ctx, []*conversation.Conversation{c}, format, w)
}

// importOne 单会话导入：取回来的第一棵树（`--pick` 留给多会话用例）。
func importOne(ctx context.Context, im port.SessionImporter, r io.Reader, f port.ExchangeFormat) (*conversation.Conversation, port.ExchangeReport, error) {
	cs, rep, err := im.Import(ctx, r, f, nil)
	if err != nil {
		return nil, rep, err
	}
	if len(cs) == 0 {
		return nil, rep, errors.New("没有导入任何会话")
	}
	return cs[0], rep, nil
}
