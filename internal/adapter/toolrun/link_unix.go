//go:build !windows

package toolrun

import "os"

// isSymlink 判定路径最后一级是否为符号链接（unix：Lstat 模式位）；
// 组件不存在时返回含 fs.ErrNotExist 的错误（evalPath 据此按字面拼回尾部）。
func isSymlink(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode()&os.ModeSymlink != 0, nil
}
