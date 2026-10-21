// Package outputtts 实现 port.OutputAdapter：把一条已提交回答的正文合成语音并播放
// （D179）。它只取正文分片（PartText）——思考分片（PartThinking）不播报。
//
// 合成是网络调用（数秒），而 outputsPresenter 在会话事件循环里同步调 Deliver，
// 所以这里**立即返回**、把合成与播放放到后台；新一条回答开始播报时取消上一条
// （同一时刻只有一条在合成）。失败只记日志，不打断主流程（DESIGN §5.7）。
package outputtts

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

var _ port.OutputAdapter = (*Adapter)(nil)

// Config 装配参数：语音参数来自 audio.tts，适配器本身无状态。
type Config struct {
	Synth  port.Synthesizer
	Player port.AudioPlayer
	Blobs  port.AttachmentStore
	Voice  string
	Design string
	Style  string
	Tags   string
	// Logf 记播放/合成失败（nil = 丢弃）。
	Logf func(format string, args ...any)
}

// Adapter 语音播报输出器。
type Adapter struct {
	cfg Config

	mu     sync.Mutex
	seq    uint64
	cancel context.CancelFunc
}

// New 创建播报器；三条端口缺一不可。
func New(cfg Config) (*Adapter, error) {
	if cfg.Synth == nil {
		return nil, errors.New("tts: 未接语音合成器")
	}
	if cfg.Player == nil {
		return nil, errors.New("tts: 未接播放器")
	}
	if cfg.Blobs == nil {
		return nil, errors.New("tts: 未接附件库")
	}
	return &Adapter{cfg: cfg}, nil
}

// Name 输出器名（日志/报错用）。
func (a *Adapter) Name() string { return "语音播报" }

// Deliver 取正文、起一条后台合成任务并立即返回。新任务取消上一条。
func (a *Adapter) Deliver(ctx context.Context, req port.OutputRequest) error {
	text := bodyText(req.Parts)
	if text == "" {
		return nil
	}
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.seq++
	seq := a.seq
	// 脱钩轮次上下文：合成通常在回答提交之后才跑完，轮次 ctx 会先被取消；
	// 但「新回答开始」与 Close 仍能经这里的 cancel 停掉它。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.cancel = cancel
	a.mu.Unlock()

	go a.speak(runCtx, seq, text)
	return nil
}

// Close 停止后台任务与正在播放的音频（退出/切走时调用）。
func (a *Adapter) Close() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.mu.Unlock()
	if a.cfg.Player != nil {
		a.cfg.Player.Stop()
	}
}

func (a *Adapter) speak(ctx context.Context, seq uint64, text string) {
	defer func() {
		a.mu.Lock()
		if a.seq == seq && a.cancel != nil {
			// 只有自己仍是「最新一条」时才清掉槽位；否则后来者的 cancel 不能动。
			a.cancel()
			a.cancel = nil
		}
		a.mu.Unlock()
	}()
	ref, err := a.cfg.Synth.Synthesize(ctx, port.SpeechRequest{
		Text:   text,
		Voice:  a.cfg.Voice,
		Design: a.cfg.Design,
		Style:  a.cfg.Style,
		Tags:   a.cfg.Tags,
	})
	if err != nil {
		if ctx.Err() == nil {
			a.logf("语音播报失败：%v", err)
		}
		return
	}
	if !a.latest(seq) {
		return
	}
	rc, err := a.cfg.Blobs.Get(ctx, ref)
	if err != nil {
		if ctx.Err() == nil {
			a.logf("语音播报失败：读取音频: %v", err)
		}
		return
	}
	defer rc.Close()
	if err := a.cfg.Player.Play(ctx, ref, rc); err != nil && ctx.Err() == nil {
		a.logf("语音播报失败：%v", err)
	}
}

func (a *Adapter) latest(seq uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.seq == seq
}

func (a *Adapter) logf(format string, args ...any) {
	if a.cfg.Logf != nil {
		a.cfg.Logf(format, args...)
	}
}

// bodyText 拼正文分片（跳过思考/附件），空行折叠。
func bodyText(parts []conversation.Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind != conversation.PartText {
			continue
		}
		t := strings.TrimSpace(p.Text)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(t)
	}
	return b.String()
}
