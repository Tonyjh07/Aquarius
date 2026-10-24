// Package gptsovitstts 实现 port.Synthesizer：走 GPT-SoVITS 的 api_v2
// （`api_v2.py`，仓库 github.com/RVC-Boss/GPT-SoVITS）。
//
//	POST {base_url}/tts
//	{ "text": "…", "text_lang": "zh",
//	  "ref_audio_path": "<参考音频绝对路径>", "prompt_text": "<参考音频的文字>",
//	  "prompt_lang": "zh", "text_split_method": "cut5",
//	  "media_type": "wav", "streaming_mode": false }
//	→ WAV 字节流
//
// 它的音色不是「名字」，而是一段参考音频（音色克隆）：`ref_audio_path` 指服务端
// 本机的文件，`prompt_text` 必须与那段音频的内容一致，否则音色会跑。所以设置里
// 的「音色」在这里填参考音频路径、「音色描述」填参考音频的文字。
package gptsovitstts

import (
	"bytes"
	"context"
	"encoding/json"
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
	ttsPath = "/tts"
	// maxAudioBytes 单次合成的音频上限。
	maxAudioBytes = 32 << 20
	maxErrorBody  = 4 << 10
	// maxTextBytes 单次合成的文本上限（服务端按句切分，过长会吃满显存）。
	maxTextBytes = 2000
	// defaultSplit 文本切分方式：cut5 = 按中英文标点切，最稳的一档。
	defaultSplit = "cut5"
)

var _ port.Synthesizer = (*Client)(nil)

// Config 适配器配置。
type Config struct {
	BaseURL string
	// TextLang 正文语种（auto | zh | ja | en | ko | yue）；空 = auto。
	TextLang string
	// PromptLang 参考音频文字的语种；空 = 与 TextLang 相同。
	PromptLang string
	// RefAudio 缺省参考音频路径（配置里的 audio.tts.voice 优先）。
	RefAudio string
	// PromptText 缺省参考音频的文字（配置里的 audio.tts.design 优先）。
	PromptText string
	// Speed 语速倍率；0 = 不传（服务端缺省 1.0）。
	Speed float64
	// Blobs 合成结果落这里（可重播、随 GC）。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 3 分钟整体超时的默认客户端。
	HTTP *http.Client
}

// Client 合成客户端。
type Client struct {
	base       string
	textLang   string
	promptLang string
	refAudio   string
	promptText string
	speed      float64
	blobs      port.AttachmentStore
	hc         *http.Client
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
	textLang := normalizeLang(cfg.TextLang)
	promptLang := normalizeLang(cfg.PromptLang)
	// 没单独指定参考文字语种时跟随正文语种：参考音频说的通常与要合成的是同一种话。
	if strings.TrimSpace(cfg.PromptLang) == "" {
		promptLang = textLang
	}
	return &Client{
		base:       strings.TrimRight(raw, "/"),
		textLang:   textLang,
		promptLang: promptLang,
		refAudio:   strings.TrimSpace(cfg.RefAudio),
		promptText: strings.TrimSpace(cfg.PromptText),
		speed:      cfg.Speed,
		blobs:      cfg.Blobs,
		hc:         hc,
	}, nil
}

// normalizeLang 语种词归一（空 = auto，服务端认 auto）。
func normalizeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "auto"
	}
	return s
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
	if tags := strings.TrimSpace(req.Tags); tags != "" {
		text = "(" + tags + ")" + text
	}
	if len(text) > maxTextBytes {
		return conversation.BlobRef{}, fmt.Errorf("tts: 单次合成最多 %d 字节文本，请分段播报", maxTextBytes)
	}
	ref := strings.TrimSpace(req.Voice)
	if ref == "" {
		ref = c.refAudio
	}
	if ref == "" {
		return conversation.BlobRef{}, errors.New("tts: 需要填写参考音频路径（音色就是那段录音）")
	}
	// 参考音频的文字用「音色描述」框：GPT-SoVITS 要求它与参考音频的实际内容
	// 一致，否则合成出来的人声会飘。
	prompt := strings.TrimSpace(req.Design)
	if prompt == "" {
		prompt = c.promptText
	}
	raw, err := c.fetch(ctx, text, ref, prompt)
	if err != nil {
		return conversation.BlobRef{}, err
	}
	name := fmt.Sprintf("speech-%d.wav", time.Now().UnixMilli())
	out, err := c.blobs.Put(ctx, bytes.NewReader(raw), "audio/wav", name)
	if err != nil {
		return conversation.BlobRef{}, fmt.Errorf("tts: 保存音频: %w", err)
	}
	return out, nil
}

// fetch 发一次合成请求，返回音频字节。
func (c *Client) fetch(ctx context.Context, text, refAudio, promptText string) ([]byte, error) {
	body := request{
		Text:         text,
		TextLang:     c.textLang,
		RefAudioPath: refAudio,
		PromptLang:   c.promptLang,
		TextSplit:    defaultSplit,
		MediaType:    "wav",
		Streaming:    false,
	}
	if promptText != "" {
		body.PromptText = promptText
	}
	if c.speed > 0 {
		body.SpeedFactor = c.speed
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("tts: 编码请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+ttsPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tts: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
		return nil, fmt.Errorf("tts: 服务端返回的不是 WAV（%s）", sniff(raw))
	}
	return raw, nil
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

type request struct {
	Text         string  `json:"text"`
	TextLang     string  `json:"text_lang"`
	RefAudioPath string  `json:"ref_audio_path"`
	PromptText   string  `json:"prompt_text,omitempty"`
	PromptLang   string  `json:"prompt_lang"`
	TextSplit    string  `json:"text_split_method"`
	MediaType    string  `json:"media_type"`
	SpeedFactor  float64 `json:"speed_factor,omitempty"`
	Streaming    bool    `json:"streaming_mode"`
}
