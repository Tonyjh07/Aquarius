package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

func TestName(t *testing.T) {
	if got := New(nil).Name(); got != "notify" {
		t.Fatalf("name = %q", got)
	}
}

// TestDeliverSendsPreview 提取文本、压平换行、截断预览后送出。
func TestDeliverSendsPreview(t *testing.T) {
	var gotTitle, gotBody string
	a := New(func(title, body string) error {
		gotTitle, gotBody = title, body
		return nil
	})
	req := port.OutputRequest{
		Message: conversation.Message{Role: conversation.RoleAssistant},
		Parts: []conversation.Part{
			{Kind: conversation.PartText, Text: "第一段回答\n带换行"},
			{Kind: conversation.PartText, Text: "第二段"},
		},
	}
	if err := a.Deliver(context.Background(), req); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if gotTitle != "Aquarius" {
		t.Fatalf("title = %q", gotTitle)
	}
	if gotBody != "第一段回答 带换行 第二段" {
		t.Fatalf("body = %q", gotBody)
	}
}

// TestDeliverPreviewTruncates 超长文本截断加省略号。
func TestDeliverPreviewTruncates(t *testing.T) {
	var got string
	a := New(func(_, body string) error { got = body; return nil })
	long := strings.Repeat("字", 300)
	if err := a.Deliver(context.Background(), port.OutputRequest{
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: long}},
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if n := len([]rune(got)); n != defaultPreview+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len = %d, suffix…: %v", n, strings.HasSuffix(got, "…"))
	}
}

// TestDeliverSkipsEmpty 无文本不打扰；未配置发送器/取消均报因。
func TestDeliverSkipsEmpty(t *testing.T) {
	called := false
	a := New(func(string, string) error { called = true; return nil })
	if err := a.Deliver(context.Background(), port.OutputRequest{}); err != nil {
		t.Fatalf("empty deliver: %v", err)
	}
	if called {
		t.Fatal("空内容不应发送")
	}
	if err := New(nil).Deliver(context.Background(), port.OutputRequest{
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: "x"}},
	}); err == nil {
		t.Fatal("nil send 应报错")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Deliver(ctx, port.OutputRequest{
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: "x"}},
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
}

// TestDeliverPropagatesSendError 发送失败原样返回（装饰器据此记日志）。
func TestDeliverPropagatesSendError(t *testing.T) {
	a := New(func(string, string) error { return errors.New("无桌面会话") })
	err := a.Deliver(context.Background(), port.OutputRequest{
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "无桌面会话") {
		t.Fatalf("err = %v", err)
	}
}
