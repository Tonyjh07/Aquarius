package main

// 数据目录删除入口装配根侧测试：标记置位、退出链末尾真的删目录、未标记/空目录不动。
// 真删除的时机约束（日志句柄关闭之后）靠 defer 注册次序保证，测试锁的是行为结果。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirAdminMarksAndWipes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &wiring{dir: dir}
	a := &dataDirAdmin{w: w}
	if a.Path() != dir {
		t.Fatalf("Path = %q, want %q", a.Path(), dir)
	}
	if w.wipePending.Load() {
		t.Fatal("未请求时不应已置位")
	}
	if err := a.RequestWipe(); err != nil {
		t.Fatalf("RequestWipe: %v", err)
	}
	w.runPendingWipe()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("标记后退出应删掉整个数据目录: %v", err)
	}
}

func TestRunPendingWipeNoopWithoutMark(t *testing.T) {
	dir := t.TempDir()
	w := &wiring{dir: dir}
	w.runPendingWipe() // 未标记：no-op
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("未标记不应删目录: %v", err)
	}
	// dir 为空（如 doctorcli 这类不落 profile 的装配）也不许动到别处。
	w2 := &wiring{}
	w2.wipePending.Store(true)
	w2.runPendingWipe()
}
