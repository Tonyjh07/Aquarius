//go:build windows

package main

// doctor_disk_windows.go 卷余量（D306）：Win32 的 GetDiskFreeSpaceEx，按**卷**问，不按路径问。
//
// 走 x/sys/windows 而不是 syscall：后者没有这个函数。参数里第一个是「调用者可用的空闲
// 字节」（磁盘配额下小于卷空闲），体检要的是「这个程序还能写多少」，取它。

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
)

// diskSpace 返回 dir 所在卷的可用字节与总字节。
func diskSpace(dir string) (free, total uint64, err error) {
	vol := filepath.VolumeName(dir)
	if vol == "" {
		return 0, 0, fmt.Errorf("%s", i18n.Tf("cmd.doctor.disk_err", "path", dir))
	}
	root, perr := windows.UTF16PtrFromString(vol + `\`)
	if perr != nil {
		return 0, 0, fmt.Errorf("%s: %w", i18n.Tf("cmd.doctor.disk_err", "path", dir), perr)
	}
	var freeToCaller, totalBytes, totalFree uint64
	if werr := windows.GetDiskFreeSpaceEx(root, &freeToCaller, &totalBytes, &totalFree); werr != nil {
		return 0, 0, fmt.Errorf("%s: %w", i18n.Tf("cmd.doctor.disk_err", "path", dir), werr)
	}
	return freeToCaller, totalBytes, nil
}
