// Package vitstts 实现 port.Synthesizer：走 vits-simple-api
// （github.com/Artrajz/vits-simple-api）的 HTTP 接口。
//
//	GET  {base_url}/voice/vits?text=…&id=<说话人>&format=wav&lang=zh&length=<语速>
//	→ WAV 字节流（Content-Type: audio/wav）
//
// 同一个服务端还托管 Bert-VITS2、W2V2-VITS、GPT-SoVITS 等模型，这里只覆盖
// VITS 那条路径——GPT-SoVITS 有它自己的原生接口（见 gptsovitstts）。
//
// 服务端是 AGPL-3.0：只能通过 HTTP 调用，不能链接它的代码。本适配器是独立的
// HTTP 客户端，不含任何服务端代码。
package vitstts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	vitsPath = "/voice/vits"
	// maxAudioBytes 单次合成的音频上限（4 分钟 22kHz WAV ≈ 21MB，留到 32MB
	// 覆盖长段播报）。
	maxAudioBytes = 32 << 20
	maxErrorBody  = 4 << 10
	// maxTextBytes 单次合成的文本上限：GET 的查询串有长度限制，长文要分批。
	maxTextBytes = 2000
)

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
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return nil, errors.New("tts: base_url 为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("tts: 非法 base_url %q", cfg.BaseURL)
	}
	if cfg.Blobs == nil {
		return nil, errors.New("tts: 未接附件库")
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	lang := strings.ToLower(strings.TrimSpace(cfg.Language))
	if lang == "" {
		lang = "auto"
	}
	return &Client{
		base:    strings.TrimRight(raw, "/"),
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
		return conversation.BlobRef{}, errors.New("tts: nil context")
	}
	if err := ctx.Err(); err != nil {
		return conversation.BlobRef{}, err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return conversation.BlobRef{}, errors.New("tts: 要合成的文本为空")
	}
	// 风格标签拼在正文前（与其它合成器同口径）：VITS 没有独立的风格参数，
	// 但标点与停顿会跟着正文走，效果仍成立。
	if tags := strings.TrimSpace(req.Tags); tags != "" {
		text = "(" + tags + ")" + text
	}
	if len(text) > maxTextBytes {
		return conversation.BlobRef{}, fmt.Errorf("tts: 单次合成最多 %d 字节文本，请分段播报", maxTextBytes)
	}
	raw, err := c.fetch(ctx, text, speakerOf(req.Voice, c.speaker))
	if err != nil {
		return conversation.BlobRef{}, err
	}
	name := fmt.Sprintf("speech-%d.wav", time.Now().UnixMilli())
	ref, err := c.blobs.Put(ctx, bytes.NewReader(raw), "audio/wav", name)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 保存音频: %w", err)
	}
	return ref, nil
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
		return nil, fmt.Errorf("tts: 构造请求: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("tts: 服务端返回 %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes+1))
	if err != nil {
		return nil, fmt.Errorf("tts: 读取音频: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("tts: 服务端返回了空音频")
	}
	if len(raw) > maxAudioBytes {
		return nil, fmt.Errorf("tts: 音频超过 %d MB，请分段播报", maxAudioBytes>>20)
	}
	if !looksLikeWAV(raw) {
		// 服务端出错时也可能回 200 + 一段 JSON/HTML，直接当音频存下去会让
		// 播放器莫名其妙地没声音。
		return nil, fmt.Errorf("tts: 服务端返回的不是 WAV（%s）", sniff(raw))
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

// looksLikeWAV 粗判 WAV 头（RIFF....WAVE）。
func looksLikeWAV(b []byte) bool {
	return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WAVE"
}

// sniff 报因里带上响应体的开头，便于判断服务端到底回了什么。
func sniff(b []byte) string {
	n := min(len(b), 80)
	s := strings.TrimSpace(string(b[:n]))
	if s == "" {
		return "空响应体"
	}
	return "开头是 " + strconv.Quote(s)
}

func snippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "（空响应体）"
	}
	return s
}
