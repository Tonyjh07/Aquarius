// Package ttsdeepseek 实现 port.Synthesizer：走 DeepSeek 的语音合成端点（D316）。
//
//	POST {base_url}/tools/tts
//	Content-Type: application/json
//	  {"text": …, "voice_id": "stella"|"tide", "format": "wav", "filter_markdown": true}
//	→ 裸音频字节（WAV）
//
// 与 mimotts 的差异是刻意的：音色只认 `stella` / `tide` 两档（配置侧 fail-fast，装配层把守；
// 这里再拒一道做纵深防御）；Style / Tags / Design 三项该端点不认——配置了非空值装配层已经
// 警告过「已忽略」，适配器不消费；超限（10000 计费字符，汉字算 2；64 KiB 请求体）明确报错，
// 不做切分拼接（D316 决策 5）。`filter_markdown` 固定开：Markdown 符号不念是服务端职责。
package ttsdeepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const synthesizePath = "/tools/tts"

const (
	// maxBilledChars 计费字符上限（服务端口径：10000；汉字等全宽字符按 2 计）。
	maxBilledChars = 10000
	// maxBodyBytes 请求体上限（服务端口径：64 KiB）。
	maxBodyBytes = 64 << 10
	// voiceStella / voiceTide 该端点仅有的两个音色（D316 决策 3）。
	voiceStella = "stella"
	voiceTide   = "tide"

	maxErrorBody = 4 << 10
)

var _ port.Synthesizer = (*Client)(nil)

// Config 适配器配置（base_url 与密钥来自 audio.tts 自己的配置或借来的 provider，D222）。
type Config struct {
	BaseURL string
	APIKey  string
	// Blobs 合成结果落附件库（可重播、可 GC，§5.6）。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 3 分钟整体超时的默认客户端（长文本合成慢于识别）。
	HTTP *http.Client
}

// Client 合成客户端。
type Client struct {
	base  string
	key   string
	blobs port.AttachmentStore
	hc    *http.Client
}

// New 创建客户端并校验配置（fail-fast，装配期报因）。
func New(cfg Config) (*Client, error) {
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return nil, errors.New(i18n.T("tts.err.base_url_empty"))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New(i18n.Tf("tts.err.base_url_invalid", "url", cfg.BaseURL))
	}
	if cfg.Blobs == nil {
		return nil, errors.New(i18n.T("tts.err.no_blobs"))
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	return &Client{
		base:  strings.TrimRight(raw, "/"),
		key:   strings.TrimSpace(cfg.APIKey),
		blobs: cfg.Blobs,
		hc:    hc,
	}, nil
}

// request 合成请求体（filter_markdown 固定开，D316 决策 6）。
type request struct {
	Text           string `json:"text"`
	VoiceID        string `json:"voice_id"`
	Format         string `json:"format"`
	FilterMarkdown bool   `json:"filter_markdown"`
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
	voice := strings.ToLower(strings.TrimSpace(req.Voice))
	if voice != voiceStella && voice != voiceTide {
		// 装配层已 fail-fast，这里是纵深防御：请求里的音色名只发服务端认的两个值。
		return conversation.BlobRef{}, errors.New(i18n.Tf("tts.deepseek.err_voice", "voice", req.Voice,
			"voices", voiceStella+", "+voiceTide))
	}
	if n := billedChars(text); n > maxBilledChars {
		return conversation.BlobRef{}, errors.New(i18n.Tf("tts.deepseek.err_text_too_long",
			"billed", n, "limit", maxBilledChars))
	}
	payload, err := json.Marshal(request{
		Text: text, VoiceID: voice, Format: "wav", FilterMarkdown: true,
	})
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("%s: %w", i18n.T("tts.err.encode_request"), err)
	}
	if len(payload) > maxBodyBytes {
		return conversation.BlobRef{}, errors.New(i18n.Tf("tts.deepseek.err_body_too_large",
			"size", len(payload), "limit", maxBodyBytes))
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+synthesizePath, bytes.NewReader(payload))
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("%s: %w", i18n.T("tts.err.build_request"), err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.hc.Do(hreq)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("%s: %w", i18n.T("tts.err.request_failed"), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return conversation.BlobRef{}, errors.New(i18n.Tf("tts.err.server_status", "code", resp.StatusCode, "body", snippet(resp.Body)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioResponseBytes))
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("%s: %w", i18n.T("tts.err.request_failed"), err)
	}
	if len(raw) == 0 {
		return conversation.BlobRef{}, errors.New(i18n.T("tts.err.empty_audio"))
	}
	name := fmt.Sprintf("speech-%d.wav", time.Now().UnixMilli())
	ref, err := c.blobs.Put(ctx, bytes.NewReader(raw), "audio/wav", name)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("%s: %w", i18n.T("tts.err.save_audio"), err)
	}
	return ref, nil
}

// billedChars 计费字符数：ASCII 等窄字符按 1，其余（汉字等全宽）按 2——服务端按这个口径
// 收钱，本地预检用同一个数，超限在请求发出前就报。
func billedChars(s string) int {
	n := 0
	for _, r := range s {
		if r > unicode.MaxASCII {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// maxAudioResponseBytes 合成响应的字节上限（10 分钟 22050Hz 16bit 单声道 ≈ 25MB，留足余量）。
const maxAudioResponseBytes = 32 << 20

func snippet(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, maxErrorBody))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return i18n.T("audio.detail.empty_body")
	}
	return s
}
