package main

// heap_test.go 堆软上限的装配口径：默认装 256MiB；GOMEMLIMIT 显式给出时不覆盖。
// 两者都是进程级全局状态，测试各自还原原值，避免影响同进程的其它用例。

import (
	"runtime/debug"
	"testing"
)

// restoreMemoryLimit 还原调用前的堆上限（SetMemoryLimit 没有「取消」语义，只能回写）。
func restoreMemoryLimit(t *testing.T) {
	t.Helper()
	prev := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
}

// TestApplyHeapLimitSetsDefault 没有 GOMEMLIMIT 时装默认上限；原来的值（无论是什么）
// 都被覆盖。
func TestApplyHeapLimitSetsDefault(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "")
	restoreMemoryLimit(t)
	debug.SetMemoryLimit(1 << 40) // 先放一个明显不同的值
	applyHeapLimit()
	if got := debug.SetMemoryLimit(-1); got != heapLimitBytes {
		t.Fatalf("limit = %d, want %d", got, heapLimitBytes)
	}
}

// TestApplyHeapLimitHonorsEnv GOMEMLIMIT 存在时不动运行时设置——用户/部署方的口径优先，
// 代码里的默认值不该盖掉它。
func TestApplyHeapLimitHonorsEnv(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "512MiB")
	restoreMemoryLimit(t)
	const keep = 1 << 40
	debug.SetMemoryLimit(keep)
	applyHeapLimit()
	if got := debug.SetMemoryLimit(-1); got != keep {
		t.Fatalf("limit = %d, want %d（GOMEMLIMIT 已设置时不得覆盖）", got, keep)
	}
}
