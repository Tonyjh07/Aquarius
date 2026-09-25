//go:build windows

package toolrun

import (
	"os"
	"syscall"
)

// fileAttributeReparsePoint FILE_ATTRIBUTE_REPARSE_POINT：符号链接与目录
// junction（mount point）等重解析点均置位。junction 不被 Lstat 归类为
// ModeSymlink，且 mklink /J 无需管理员权限——必须按属性位识别（D22 绕过面）。
const fileAttributeReparsePoint = 0x400

// isSymlink 判定路径最后一级是否为链接（Windows：symlink 与 junction）；
// 组件不存在时返回含 fs.ErrNotExist 的错误（evalPath 据此按字面拼回尾部）。
func isSymlink(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if data, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return data.FileAttributes&fileAttributeReparsePoint != 0, nil
	}
	return fi.Mode()&os.ModeSymlink != 0, nil
}
