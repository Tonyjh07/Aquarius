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

// TestDeliverSkipsThinking D42：思考分片不进通知——通知只摘正文（PartText）。
func TestDeliverSkipsThinking(t *testing.T) {
	var got string
	a := New(func(_, body string) error { got = body; return nil })
	if err := a.Deliver(context.Background(), port.OutputRequest{
		Parts: []conversation.Part{
			{Kind: conversation.PartThinking, Text: "内部推理不外发"},
			{Kind: conversation.PartText, Text: "正式回答"},
		},
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got != "正式回答" {
		t.Fatalf("body = %q, want 正式回答（思考不得外发）", got)
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

// TestDeliverFallsBackToMessageContent Parts 为空时回退消息内容（装饰器可能
// 只传 Message）。
func TestDeliverFallsBackToMessageContent(t *testing.T) {
	var got string
	a := New(func(_, body string) error { got = body; return nil })
	if err := a.Deliver(context.Background(), port.OutputRequest{
		Message: conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: []conversation.Part{{Kind: conversation.PartText, Text: "来自消息内容"}},
		},
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got != "来自消息内容" {
		t.Fatalf("body = %q", got)
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

// TestPreviewStripsControlChars §14 M3 遗留：通知正文剔除控制字符
// （ESC/BEL/DEL/C1——正文是不可信模型输出，防通知与终端注入）。
func TestPreviewStripsControlChars(t *testing.T) {
	in := "正常文本\x1b[31m红色\x07告警\x7f\x85结尾"
	got := Preview(in, 0)
	for _, bad := range []string{"\x1b", "[31m", "\x07", "\x7f", "\x85"} {
		if strings.Contains(got, bad) {
			t.Fatalf("未剔除 %q: %q", bad, got)
		}
	}
	for _, want := range []string{"正常文本", "红色", "告警", "结尾"} {
		if !strings.Contains(got, want) {
			t.Fatalf("正常文本被误删 %q: %q", want, got)
		}
	}
	// Deliver 同路径生效。
	var sent string
	a := New(func(_, body string) error { sent = body; return nil })
	if err := a.Deliver(context.Background(), port.OutputRequest{
		Parts: []conversation.Part{{Kind: conversation.PartText, Text: "前\x00后"}},
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if strings.Contains(sent, "\x00") || !strings.Contains(sent, "前后") {
		t.Fatalf("sent = %q", sent)
	}
}

// TestSanitizeControlSequences 序列级剥离（审查修复补充）：OSC 的两种收尾
// （BEL 与 ESC\）、截断序列、结尾裸 ESC、合法 UTF-8 的 C1、RuneError 字节。
func TestSanitizeControlSequences(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a\x1b[31mb", "ab"},
		{"a\x1b]0;title\x07b", "ab"},
		{"a\x1b]0;title\x1b\\b", "ab"}, // ST 收尾：反斜杠必须一并吞掉（审查修复）
		{"a\x1bMb", "ab"},
		{"截断\x1b[3", "截断"},
		{"尾部\x1b", "尾部"},
		{"ab", "ab"},      // 合法 UTF-8 的 C1（U+0085）
		{"a\xffb", "ab"},   // 非法字节 → RuneError 剔除
		{"行1\n行2", "行1行2"}, // notify 管线已由 collapseSpaces 预压空白，\n 剥除无妨
	}
	for _, tc := range cases {
		if got := sanitizeControl(tc.in); got != tc.want {
			t.Errorf("sanitizeControl(% x) = % q, want % q", []byte(tc.in), got, tc.want)
		}
	}
}
