package main

import (
	"sync"
	"testing"

	"github.com/Tonyjh07/Aquarius/internal/domain/perm"
)

// TestLevelHolderConcurrent 审查修复：TUI 状态行（事件循环 goroutine）读、
// /permission（REPL 主 goroutine）写——并发读写下 -race 必须干净。
func TestLevelHolderConcurrent(t *testing.T) {
	h := newLevelHolder(perm.DefaultLevel)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = h.Get()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				h.Set(perm.FullAccess)
			}
		}()
	}
	wg.Wait()
	if h.Get() != perm.FullAccess {
		t.Fatalf("final = %s, want full-access", h.Get())
	}
	// 初值语义：构造即存。
	if got := newLevelHolder(perm.ReadOnly).Get(); got != perm.ReadOnly {
		t.Fatalf("初值 = %s", got)
	}
}
