package app

import (
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
)

// nodeOf 测试里取节点（会话树封装后不再直接读内部 map）。
//
// 生产代码不该这么取（该用 Find 并处理 ok），测试里到处写 `m, ok := ...; if !ok { ... }`
// 会把每个断言撑成三行，所以给一个实现 `t.Helper()` 的取值器：id 不在树中直接判失败。
func nodeOf(t *testing.T, c *conversation.Conversation, id conversation.MessageID) conversation.Message {
	t.Helper()
	m, ok := c.Find(id)
	if !ok {
		t.Fatalf("会话树中没有节点 %q", id)
	}
	return m
}
