package repl

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// TestWakeReturnsInput 空闲唤醒经 Next 返回 Wake 标记。
func TestWakeReturnsInput(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	u := New(pr, io.Discard)
	u.Wake()
	in, err := u.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !in.Wake {
		t.Fatalf("input = %+v, want Wake", in)
	}
}

// TestConfirmIgnoresWake 唤醒不能替用户回答确认（只读输入行）。
func TestConfirmIgnoresWake(t *testing.T) {
	u := New(strings.NewReader("y\n"), &bytes.Buffer{})
	u.Wake()
	ans, err := u.Confirm(context.Background(), "确认？")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !ans.Allow {
		t.Fatal("Confirm 被唤醒误判为拒绝")
	}
}

// TestPromptIdempotent 空唤醒后 Prompt 不重复打 "> "；有输出后恢复。
func TestPromptIdempotent(t *testing.T) {
	var out bytes.Buffer
	u := New(strings.NewReader(""), &out)
	u.Prompt()
	u.Prompt()
	if got := strings.Count(out.String(), "> "); got != 1 {
		t.Fatalf("prompt 次数 = %d, want 1: %q", got, out.String())
	}
	u.Say("hello")
	u.Prompt()
	if got := strings.Count(out.String(), "> "); got != 2 {
		t.Fatalf("prompt 次数 = %d, want 2: %q", got, out.String())
	}
}
