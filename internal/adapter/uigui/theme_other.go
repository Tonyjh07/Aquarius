//go:build !windows

package uigui

// systemDark GUI 未适配非 Windows（§15.1 平台边界）：恒浅色，仅桩接住构建。
func systemDark() bool { return false }
