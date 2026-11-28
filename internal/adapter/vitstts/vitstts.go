// Package vitstts 实现 port.Synthesizer：走 vits-simple-api
// （github.com/Artrajz/vits-simple-api）的 HTTP 接口。
//
//	GET  {base_url}/voice/vits?text=…&id=<说话人>&format=wav&lang=zh&length=<语速>
//	→ WAV 字节流（Content-Type: audio/wav）
//
// 同一个服务端还托管 Bert-VITS2、W2V2-VITS、GPT-SoVITS 等模型，这里只覆盖
// VITS 那条路径——GPT-SoVITS 有自己的原生接口（见 gptsovitstts）。
//
// 服务端是 AGPL-3.0：只能通过 HTTP 调用，不能链接服务端的代码。本适配器是独立的
// HTTP 客户端，不含任何服务端代码。
package vitstts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/adapter/audiohttp"
	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	vitsPath = "/voice/vits"
	// maxAudioBytes 单次合成的音频上限（4 分钟 22kHz WAV ≈ 21MB，留到 32MB
	// 覆盖长段播报）。
	maxAudioBytes = 32 << 20
	// maxTextBytes 单次合成的文本上限：GET 的查询串有长度限制，长文要分批。
	maxTextBytes = 2000
)

// errKeys 本适配器的报因词表（key 与既有口径一致）。
var errKeys = audiohttp.ErrKeys{
	BaseURLEmpty:   "tts.err.base_url_empty",
	BaseURLInvalid: "tts.err.base_url_invalid",
	ServerStatus:   "tts.err.server_status",
	RequestFailed:  "tts.err.request_failed",
	ReadAudio:      "tts.err.read_audio",
	AudioEmpty:     "tts.err.empty_audio",
	AudioTooLarge:  "tts.err.audio_too_large",
	SaveAudio:      "tts.err.save_audio",
}

var _ port.Synthesizer = (*Client)(nil)

// Config 适配器配置。
type Config struct {
	BaseURL string
	// APIKey 服务端开了 api_key_enabled 才需要（走查询参数 api_key）。
	APIKey string
	// Speaker 缺省说话人 ID（配置里的 audio.tts.voice 优先）。
	Speaker string
	// Language 文本语种（auto | zh | ja | mix）；空 = auto。
	Language string
	// Blobs 合成结果落这里（可重播、随 GC）。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 3 分钟整体超时的默认客户端。
	HTTP *http.Client
}

// Client 合成客户端。
type Client struct {
	base    string
	key     string
	speaker string
	lang    string
	blobs   port.AttachmentStore
	hc      *http.Client
}

// New 创建客户端并校验配置（fail-fast，装配期报因）。
func New(cfg Config) (*Client, error) {
	base, hc, err := audiohttp.ValidateBaseURL(cfg.BaseURL, 3*time.Minute, errKeys)
	if err != nil {
		return nil, err
	}
	if cfg.HTTP != nil {
		hc = cfg.HTTP
	}
	if cfg.Blobs == nil {
		return nil, errors.New(i18n.T("tts.err.no_blobs"))
	}
	lang := strings.ToLower(strings.TrimSpace(cfg.Language))
	if lang == "" {
		lang = "auto"
	}
	return &Client{
		base:    base,
		key:     strings.TrimSpace(cfg.APIKey),
		speaker: strings.TrimSpace(cfg.Speaker),
		lang:    lang,
		blobs:   cfg.Blobs,
		hc:      hc,
	}, nil
}

// Synthesize 合成一段语音并落附件库，返回引用。
func (c *Client) Synthesize(ctx context.Context, req port.SpeechRequest) (conversation.BlobRef, error) {
	if ctx == nil {
		return conversation.BlobRef{}, errors.New(i18n.T("tts.err.nil_context"))
	}
	if err := ctx.Err(); err != nil {
		return conversation.BlobRef{}, err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return conversation.BlobRef{}, errors.New(i18n.T("tts.err.text_empty"))
	}
	// 风格标签拼在正文前（与其它合成器同规则）：VITS 没有独立的风格参数，
	// 但标点与停顿会跟着正文走，效果仍成立。
	if tags := strings.TrimSpace(req.Tags); tags != "" {
		text = "(" + tags + ")" + text
	}
	if len(text) > maxTextBytes {
		return conversation.BlobRef{}, errors.New(i18n.Tf("tts.err.text_too_long", "bytes", maxTextBytes))
	}
	raw, err := c.fetch(ctx, text, speakerOf(req.Voice, c.speaker))
	if err != nil {
		return conversation.BlobRef{}, err
	}
	return audiohttp.SaveWAV(ctx, c.blobs, raw, errKeys)
}

// fetch 发一次合成请求，返回音频字节。
func (c *Client) fetch(ctx context.Context, text, speaker string) ([]byte, error) {
	q := url.Values{}
	q.Set("text", text)
	q.Set("format", "wav")
	q.Set("lang", c.lang)
	if speaker != "" {
		q.Set("id", speaker)
	}
	if c.key != "" {
		q.Set("api_key", c.key)
	}
	endpoint := c.base + vitsPath + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("tts.err.build_request"), err)
	}
	resp, err := audiohttp.Do(ctx, c.hc, req, errKeys)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, audiohttp.StatusError(resp.StatusCode, resp.Body, errKeys)
	}
	raw, err := audiohttp.ReadAudio(resp.Body, maxAudioBytes, errKeys)
	if err != nil {
		return nil, err
	}
	if !audiohttp.IsWAV(raw) {
		// 服务端出错时也可能回 200 + 一段 JSON/HTML，直接当音频存下去会让
		// 播放器莫名其妙地没声音。
		return nil, errors.New(i18n.Tf("tts.err.not_wav", "sniff", audiohttp.Sniff(raw)))
	}
	return raw, nil
}

// speakerOf 说话人 ID：配置里填的优先，其次用适配器的缺省值。
// 兼容把 ID 写成数字或名字——服务端只认 ID，所以非数字要报错而不是静默丢掉。
func speakerOf(voice, fallback string) string {
	if v := strings.TrimSpace(voice); v != "" {
		return v
	}
	return fallback
}
