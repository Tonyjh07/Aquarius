//go:build !windows

package audioplay

import (
	"context"
	"errors"
	"io"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.AudioPlayer = (*Player)(nil)

// Play 非 Windows 一律报因（§12：语音播报首发仅 Windows）。
func (p *Player) Play(context.Context, conversation.BlobRef, io.Reader) error {
	return errors.New(i18n.T("audio.play.err_unsupported_platform"))
}

// Stop 非 Windows 无操作。
func (p *Player) Stop() {}
