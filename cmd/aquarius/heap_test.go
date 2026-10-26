package main

// heap_test.go 常驻内存策略的装配口径：两个旋钮各自「默认装、环境变量优先」。
// GOMEMLIMIT / GOGC 都是进程级全局状态，测试各自还原原值，避免影响同进程的其它用例。

import (
	"runtime/debug"
	"testing"
)

// restoreMemoryPolicy 还原调用前的堆上限与 GC 百分比（两者都没有「取消」语义，只能回写）。
func restoreMemoryPolicy(t *testing.T) {
	t.Helper()
	prevLimit := debug.SetMemoryLimit(-1)
	prevPercent := debug.SetGCPercent(-1)
	t.Cleanup(func() {
		debug.SetMemoryLimit(prevLimit)
		debug.SetGCPercent(prevPercent)
	})
}

// readGCPercent 读当前 GC 百分比而不改变它（SetGCPercent 返回的是调用前的值）。
func readGCPercent() int {
	prev := debug.SetGCPercent(-1)
	debug.SetGCPercent(prev)
	return prev
}

// TestApplyMemoryPolicySetsDefaults 两个环境变量都没有时装默认值；原来的值（无论是什么）
// 都被覆盖。
func TestApplyMemoryPolicySetsDefaults(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("GOGC", "")
	restoreMemoryPolicy(t)
	debug.SetMemoryLimit(1 << 40) // 先放明显不同的值，确保断言不会碰巧成立
	debug.SetGCPercent(100)
	applyMemoryPolicy()
	if got := debug.SetMemoryLimit(-1); got != heapLimitBytes {
		t.Fatalf("limit = %d, want %d", got, heapLimitBytes)
	}
	if got := readGCPercent(); got != gcPercent {
		t.Fatalf("gc percent = %d, want %d", got, gcPercent)
	}
}

// TestApplyMemoryPolicyHonorsEnv 环境变量显式给出时对应的旋钮一律不动——用户/部署方的口径
// 优先，代码里的默认值不该盖掉它。另一个旋钮照装。
func TestApplyMemoryPolicyHonorsEnv(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "512MiB")
	t.Setenv("GOGC", "80")
	restoreMemoryPolicy(t)
	const keepLimit = 1 << 40
	const keepPercent = 100
	debug.SetMemoryLimit(keepLimit)
	debug.SetGCPercent(keepPercent)
	applyMemoryPolicy()
	if got := debug.SetMemoryLimit(-1); got != keepLimit {
		t.Fatalf("limit = %d, want %d（GOMEMLIMIT 已设置时不得覆盖）", got, keepLimit)
	}
	if got := readGCPercent(); got != keepPercent {
		t.Fatalf("gc percent = %d, want %d（GOGC 已设置时不得覆盖）", got, keepPercent)
	}
}

// TestApplyMemoryPolicyKnobsAreIndependent 只设一个环境变量时，只跳过那一个旋钮。
func TestApplyMemoryPolicyKnobsAreIndependent(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "512MiB")
	t.Setenv("GOGC", "")
	restoreMemoryPolicy(t)
	const keepLimit = 1 << 40
	debug.SetMemoryLimit(keepLimit)
	debug.SetGCPercent(100)
	applyMemoryPolicy()
	if got := debug.SetMemoryLimit(-1); got != keepLimit {
		t.Fatalf("limit = %d, want %d（GOMEMLIMIT 已设置时不得覆盖）", got, keepLimit)
	}
	if got := readGCPercent(); got != gcPercent {
		t.Fatalf("gc percent = %d, want %d（GOGC 未设置时应装默认值）", got, gcPercent)
	}
}
