//go:build !windows

package micwin

import (
	"context"
	"errors"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.Recorder = (*Recorder)(nil)

// Recorder 非 Windows 占位：编译期保持同一契约，运行期明确报因。
type Recorder struct{}

// New 创建录音器（非 Windows 上不可用）。
func New() *Recorder { return &Recorder{} }

// Record 非 Windows 一律报因（§12：采集仅 Windows）。
func (r *Recorder) Record(context.Context, time.Duration, port.FrameSink) error {
	return errors.New(i18n.T("audio.rec.err_unsupported_platform"))
}
