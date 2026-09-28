package uigui

// 窗口注册表 headless 测试（§15.5：注册表/生命周期抽纯逻辑 + 假开窗器，
// 不创建真实窗口、不进 CI 图形路径）。runSecondary/secondaryFrame 的真实窗口
// 路径由手工验收覆盖（GUI 不进 CI）。

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor 轮询等待条件成立（watcher 异步摘除登记；≤1s、步 5ms）。
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("条件未在时限内满足: " + msg)
}

// TestWinHostSingleInstance 单实例防重开：同 kind 重开不产生第二实例，
// 不同 kind 各自开窗；isOpen 与登记一致。
func TestWinHostSingleInstance(t *testing.T) {
	var mu sync.Mutex
	var spawned []winKind
	h := newWinHost(func(k winKind) *winHandle {
		mu.Lock()
		spawned = append(spawned, k)
		mu.Unlock()
		return &winHandle{done: make(chan struct{})}
	})
	h.openWin(winHistory)
	h.openWin(winHistory) // 已开 = 聚焦，不开第二实例
	h.openWin(winWelcome)

	mu.Lock()
	n := len(spawned)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("开窗数 = %d, want 2（history 单实例 + welcome）", n)
	}
	if !h.isOpen(winHistory) || !h.isOpen(winWelcome) {
		t.Error("isOpen 应为 true（history/welcome 已开）")
	}
	if h.isOpen(winSettings) {
		t.Error("isOpen 应为 false（settings 未开）")
	}
}

// TestWinHostReopenAfterClose 用户关窗（X → DestroyEvent → done）后 watcher
// 摘除登记，允许重开。
func TestWinHostReopenAfterClose(t *testing.T) {
	var mu sync.Mutex
	var spawns int
	var handles []*winHandle
	h := newWinHost(func(k winKind) *winHandle {
		mu.Lock()
		spawns++
		hd := &winHandle{done: make(chan struct{})}
		handles = append(handles, hd)
		mu.Unlock()
		return hd
	})

	h.openWin(winWelcome)
	mu.Lock()
	first := handles[0]
	mu.Unlock()
	close(first.done) // 模拟用户关窗：次窗事件循环退出

	waitFor(t, func() bool { return !h.isOpen(winWelcome) }, "watcher 摘除登记")
	h.openWin(winWelcome)

	mu.Lock()
	n := spawns
	mu.Unlock()
	if n != 2 {
		t.Fatalf("开窗数 = %d, want 2（关窗后可重开）", n)
	}
}

// TestWinHostCloseAll 退出收编：全部实例收到关闭请求、二次收编幂等为 0。
func TestWinHostCloseAll(t *testing.T) {
	var mu sync.Mutex
	var handles []*winHandle
	h := newWinHost(func(k winKind) *winHandle {
		mu.Lock()
		hd := &winHandle{done: make(chan struct{})}
		handles = append(handles, hd)
		mu.Unlock()
		return hd
	})
	h.openWin(winSettings)
	h.openWin(winHistory)
	h.openWin(winWelcome)

	if n := h.closeAll(); n != 3 {
		t.Fatalf("收编数 = %d, want 3", n)
	}
	mu.Lock()
	for i, hd := range handles {
		if !hd.closePending.Load() {
			t.Errorf("句柄 %d 未收到关闭请求", i)
		}
	}
	mu.Unlock()
	if h.isOpen(winSettings) || h.isOpen(winHistory) || h.isOpen(winWelcome) {
		t.Error("收编后 isOpen 应全为 false")
	}
	if n := h.closeAll(); n != 0 {
		t.Fatalf("二次收编 = %d, want 0（幂等）", n)
	}
}

// TestWinHostConcurrentOpen 并发开窗（托盘线程 × 多入口）只产生一个实例
// （-race 门禁同时覆盖注册表竞态）。
func TestWinHostConcurrentOpen(t *testing.T) {
	var spawns atomic.Int32
	h := newWinHost(func(k winKind) *winHandle {
		spawns.Add(1)
		return &winHandle{done: make(chan struct{})}
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.openWin(winSettings)
		}()
	}
	wg.Wait()
	if got := spawns.Load(); got != 1 {
		t.Fatalf("并发开窗数 = %d, want 1", got)
	}
}

// TestWinHostHeadlessNoSpawn 无开窗器（headless 构造）：openWin 为 no-op。
func TestWinHostHeadlessNoSpawn(t *testing.T) {
	h := newWinHost(nil)
	h.openWin(winHistory)
	if h.isOpen(winHistory) {
		t.Error("headless 下 openWin 应为 no-op")
	}
	if n := h.closeAll(); n != 0 {
		t.Fatalf("headless 收编 = %d, want 0", n)
	}
}

// TestWinHostPropagatePalette 主题广播（§15.7/D61）：全部在开句柄换快照 +
// 逐窗请求重绘；未装配重绘器（nil）的句柄安全跳过。
func TestWinHostPropagatePalette(t *testing.T) {
	var mu sync.Mutex
	var handles []*winHandle
	h := newWinHost(func(k winKind) *winHandle {
		mu.Lock()
		hd := &winHandle{done: make(chan struct{})}
		handles = append(handles, hd)
		mu.Unlock()
		return hd
	})
	h.openWin(winHistory)
	h.openWin(winWelcome)
	h.openWin(winSettings)
	invokes := 0
	mu.Lock()
	for i, hd := range handles {
		f := func() { invokes++ }
		hd.invalidate.Store(&f)
		if i == 0 {
			hd.invalidate.Store(nil) // 未就绪句柄：nil 守卫
		}
	}
	mu.Unlock()

	p := darkPalette()
	h.propagatePalette(&p)

	mu.Lock()
	defer mu.Unlock()
	for i, hd := range handles {
		if got := hd.pal.Load(); got != &p {
			t.Errorf("句柄 %d 主题快照未更新", i)
		}
	}
	if invokes != 2 {
		t.Errorf("重绘请求数 = %d, want 2（nil 守卫跳过 1 个）", invokes)
	}
}
