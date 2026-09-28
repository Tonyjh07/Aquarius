//go:build !windows

package uigui

// postClose GUI 未适配非 Windows（§15.1 平台边界）：仅桩，接住构建。
func (h *winHandle) postClose() {}

// focus 同上桩。
func (h *winHandle) focus() {}
