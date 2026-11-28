package memoryfs

import (
	"context"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestSourceWhitelist 注入只取全局 + 当前会话两篇：别的会话的文档不能进上下文
// （D23 名字白名单）。这条规则属内置实现的形状，所以测在 memoryfs 而不是 app。
func TestSourceWhitelist(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	docs := map[string]string{
		port.GlobalMemoryDoc:                            "## 用户偏好\n喜欢简洁回答",
		port.SessionMemoryDoc(conversation.ID("conv")):  "本会话要点：先给结论",
		port.SessionMemoryDoc(conversation.ID("other")): "other-session-only",
	}
	for name, body := range docs {
		if err := s.Write(ctx, port.MemoryDoc{Name: name, Content: body}); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	blk, err := NewSource(s).Recall(ctx, conversation.ID("conv"), port.MemoryQuery{})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, want := range []string{port.GlobalMemoryDoc, "用户偏好",
		port.SessionMemoryDoc(conversation.ID("conv")), "本会话要点"} {
		if !strings.Contains(blk.Text, want) {
			t.Fatalf("块缺 %q:\n%s", want, blk.Text)
		}
	}
	if strings.Contains(blk.Text, "other-session-only") {
		t.Fatalf("泄漏其他会话记忆:\n%s", blk.Text)
	}
	if blk.Detail != "index+session" {
		t.Fatalf("细目 = %q, want index+session", blk.Detail)
	}
}

// TestSourceEmpty 什么都没有时给空块（不是错误）：调用方据此不注入这一段。
func TestSourceEmpty(t *testing.T) {
	blk, err := NewSource(newStore(t)).Recall(context.Background(), conversation.ID("conv"), port.MemoryQuery{})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if blk.Text != "" || blk.Detail != "" {
		t.Fatalf("空库应给空块：%+v", blk)
	}
	// nil 存储（装配失败时的降级）同样给空块，不 panic。
	if blk, err := (Source{}).Recall(context.Background(), conversation.ID("conv"), port.MemoryQuery{}); err != nil || blk.Text != "" {
		t.Fatalf("nil 存储：%+v / %v", blk, err)
	}
}
