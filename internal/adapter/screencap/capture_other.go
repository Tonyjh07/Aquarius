//go:build !windows

package screencap

import (
	"context"

	"github.com/Tonyjh07/Aquarius/internal/port"
)

// New 非 Windows 平台返回的捕获端口（D178）：三个方法都明确报「本平台不支持」，
// 随 linuxgate 一起编译，不引入构建标签外的桩。
func New() port.Capture { return unsupported{} }

type unsupported struct{}

func (unsupported) Windows(context.Context) ([]port.WindowInfo, error) {
	return nil, port.ErrCaptureUnsupported
}

func (unsupported) Screen(context.Context) (port.Image, error) {
	return port.Image{}, port.ErrCaptureUnsupported
}

func (unsupported) Window(context.Context, uintptr) (port.Image, error) {
	return port.Image{}, port.ErrCaptureUnsupported
}
