package uigui

import (
	"context"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// TestWakeReturnsInput 空闲唤醒经 Next 返回 Wake 标记（不混输入队列，Confirm 不读它）。
func TestWakeReturnsInput(t *testing.T) {
	u := &UI{
		inCh:   make(chan port.UserInput, 1),
		wakeCh: make(chan struct{}, 1),
		eofCh:  make(chan struct{}),
	}
	u.Wake()
	in, err := u.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !in.Wake {
		t.Fatalf("input = %+v, want Wake", in)
	}
}
