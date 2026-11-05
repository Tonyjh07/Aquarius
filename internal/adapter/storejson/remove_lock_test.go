package storejson

import (
	"context"
	"testing"
	"time"
)

// TestRemoveWaitsForInFlightSave Remove 与 Save 共用同一把每会话锁。
//
// 不加锁时有两个可复现的交错：Save 换入的文件落在 Remove 删完之后（刚删掉的会话复活），
// 或者 Remove 扫目录时把 Save 正在换入的临时文件一起删掉，让那次 os.Rename 报错。
// 这里用「先占住锁，再发 Remove」把第一个交错钉住。
func TestRemoveWaitsForInFlightSave(t *testing.T) {
	t.Parallel()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	c := mustConv(t)
	if err := s.Save(ctx, c); err != nil {
		t.Fatalf("save: %v", err)
	}

	unlock := s.lockSave(s.path(c.ID)) // 模拟一次还没收尾的 Save
	done := make(chan error, 1)
	go func() { done <- s.Remove(ctx, c.ID) }()

	select {
	case err := <-done:
		unlock()
		t.Fatalf("Remove 没等在途 Save 收尾（err=%v）", err)
	case <-time.After(100 * time.Millisecond):
	}

	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("放锁后 Remove 仍然没返回")
	}
}
