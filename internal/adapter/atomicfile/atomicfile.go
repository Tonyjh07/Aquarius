// Package atomicfile 提供"唯一临时文件 + 原子换入"的覆盖式写盘原语（DESIGN §14 遗留修复）：
//   - 临时名随机（同目录 CreateTemp），并发写同一目标互不踩踏；
//   - 目标已存在则沿用其既有权限位（覆盖不把 0600 放宽成 0644），新建才用调用方给的 perm；
//   - 任一步失败清理临时文件，不留残迹。
//
// 调用方：memoryfs 记忆写入、file_write 工具、config 写回（/permission）。
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// pathLocks 按目标路径串行化同进程写入（同路径并发 rename 在 Windows 上会
// 因瞬时共享冲突失败；跨进程与外部读者由 WriteFile 的 rename 重试兜底）。
var pathLocks sync.Map // string → *sync.Mutex

// renameAttempts / renameBackoff rename 换入的重试参数（Windows 上目标被
// 并发读者/编辑器短暂占用属瞬时错误，稍候重试即可）。
const (
	renameAttempts = 4
	renameBackoff  = 15 * time.Millisecond
)

// WriteFile 原子写入 data 到 path（父目录须已存在，由调用方 MkdirAll）。
// 临时文件形如 <base>.<随机>.tmp（后缀便于残留检测），写入 → chmod 最终权限 → rename 换入。
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	// 锁 key 归一（§14 M3 遗留：Windows 下 "a/b" 与 "a\b"、含 "." 段的等价写法
	// 必须是同一把锁，否则同目标并发写仍会撞 rename）。
	path = filepath.Clean(path)
	v, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("atomicfile: 建临时文件: %w", err)
	}
	tmp := f.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmp)
		}
	}()

	final := perm // 新建用调用方权限；既有文件沿用（权限继承）
	if fi, serr := os.Stat(path); serr == nil {
		final = fi.Mode().Perm()
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomicfile: 写入: %w", err)
	}
	if err := f.Chmod(final); err != nil {
		_ = f.Close()
		return fmt.Errorf("atomicfile: 设置权限: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("atomicfile: 关闭: %w", err)
	}
	// rename 换入：瞬时共享冲突（Windows 目标被并发占用）稍候重试。
	for attempt := 0; ; attempt++ {
		if err := os.Rename(tmp, path); err == nil {
			done = true
			return nil
		} else if attempt+1 >= renameAttempts {
			return fmt.Errorf("atomicfile: 换入 %s: %w", path, err)
		}
		time.Sleep(renameBackoff)
	}
}
