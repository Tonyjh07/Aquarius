//go:build !windows

package main

// doctor_disk_other.go 卷余量（D306）：statfs 的 Bsize × 块数。
//
// 用 stdlib 的 syscall 而不是 x/sys/unix：这里只要两个数，且 syscall.Statfs 在 Linux
// 与 macOS 上都在（把 x/sys 引进 go.mod 的直接依赖只为这点事不值）。字段类型在两套系统
// 上不一致（Bavail 一边 uint64 一边 int64），统一经 uint64 转一次即可。

import (
	"fmt"
	"syscall"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// diskSpace 返回 dir 所在文件系统的可用字节与总字节。
func diskSpace(dir string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if serr := syscall.Statfs(dir, &st); serr != nil {
		return 0, 0, fmt.Errorf("%s: %w", i18n.Tf("cmd.doctor.disk_err", "path", dir), serr)
	}
	bs := uint64(st.Bsize)
	return uint64(st.Bavail) * bs, uint64(st.Blocks) * bs, nil
}
