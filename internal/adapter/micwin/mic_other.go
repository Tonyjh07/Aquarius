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
type Recorder struct {
	max time.Duration // 未用（Record 恒报因）；与 Windows 侧同形
}

// Options 构造选项：MaxDuration 空 = 缺省硬顶（240s）；识别后端限段时传小值（D316）。
type Options struct {
	MaxDuration time.Duration
}

// New 创建录音器（不打开设备；Record 时才检查有没有设备）。
func New(opts Options) *Recorder { return &Recorder{max: maxDurationFor(opts.MaxDuration)} }

// Record 非 Windows 一律报因（§12：采集仅 Windows）。
func (r *Recorder) Record(context.Context, time.Duration, port.FrameSink) error {
	return errors.New(i18n.T("audio.rec.err_unsupported_platform"))
}
