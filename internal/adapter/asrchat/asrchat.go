// Package asrchat 实现 port.Transcriber（D179）：走 chat/completions 的
// `input_audio` 内容块，而不是 /audio/transcriptions 那套 multipart。
//
//	POST {base_url}/chat/completions
//	{ "model": "...", "messages": [{ "role": "user", "content": [
//	    { "type": "input_audio", "input_audio": { "data": "data:audio/wav;base64,…" } } ] }],
//	  "asr_options": { "language": "zh" } }
//
// 转写文本在 `choices[0].message.content`。厂商差异只在适配器里：换协议就换
// `audio.asr.kind`，上层拿到的仍是 (BlobRef, lang) → string。
package asrchat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const (
	chatPath = "/chat/completions"
	// maxAudioBytes 单次转写音频上限：MiMo 的 Base64 体积上限 10MB，原字节还要
	// 再乘 3/4（DESIGN §12 的硬顶口径）。16kHz 单声道 16bit ≈ 4 分钟。
	maxAudioBytes = 10 << 20
	maxErrorBody  = 4 << 10
)

var _ port.Transcriber = (*Client)(nil)

// Config 适配器配置（base_url 与密钥来自被引用的 provider）。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	// Blobs 音频附件库：Transcribe 只拿到 BlobRef，字节在这里读。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 2 分钟整体超时的默认客户端（一次请求一次响应，
	// 不像流式生成那样需要无限等待）。
	HTTP *http.Client
}

// Client 转写客户端。
type Client struct {
	base  string
	key   string
	model string
	blobs port.AttachmentStore
	hc    *http.Client
}

// New 创建客户端并校验配置（fail-fast，装配期报因）。
func New(cfg Config) (*Client, error) {
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return nil, errors.New("asr: base_url 为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("asr: 非法 base_url %q", cfg.BaseURL)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("asr: 模型名为空")
	}
	if cfg.Blobs == nil {
		return nil, errors.New("asr: 未接附件库")
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{
		base:  strings.TrimRight(raw, "/"),
		key:   strings.TrimSpace(cfg.APIKey),
		model: model,
		blobs: cfg.Blobs,
		hc:    hc,
	}, nil
}

// Transcribe 把一段音频转成文本。lang 为 auto | zh | en（空 = auto）。
func (c *Client) Transcribe(ctx context.Context, audio conversation.BlobRef, lang string) (string, error) {
	if ctx == nil {
		return "", errors.New("asr: nil context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mime := audioMIME(audio.MIME)
	if mime == "" {
		return "", fmt.Errorf("asr: 不支持的音频格式 %q（只支持 wav / mp3）", audio.MIME)
	}
	rc, err := c.blobs.Get(ctx, audio)
	if err != nil {
		return "", fmt.Errorf("asr: 读取音频附件: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxAudioBytes+1))
	if err != nil {
		return "", fmt.Errorf("asr: 读取音频附件: %w", err)
	}
	if len(data) == 0 {
		return "", errors.New("asr: 音频附件为空")
	}
	if len(data) > maxAudioBytes {
		return "", fmt.Errorf("asr: 音频超过 %d MB 上限，请缩短录音", maxAudioBytes>>20)
	}

	lang = strings.TrimSpace(lang)
	if lang == "" {
		lang = "auto"
	}
	body := request{
		Model: c.model,
		Messages: []message{{
			Role: "user",
			Content: []contentPart{{
				Type: "input_audio",
				InputAudio: inputAudio{
					Data: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
				},
			}},
		}},
		ASROptions: &asrOptions{Language: lang},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("asr: 编码请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+chatPath, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("asr: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("asr: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("asr: 服务端返回 %d: %s", resp.StatusCode, snippet(resp.Body))
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("asr: 解析响应: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("asr: 响应里没有 choices")
	}
	text := strings.TrimSpace(out.Choices[0].Message.Content)
	if text == "" {
		return "", errors.New("asr: 没有识别出文字（录音可能为空或太短）")
	}
	return text, nil
}

// audioMIME 归一音频 MIME：只放行 MiMo 支持的 wav / mp3；
// 空 MIME 按 wav 处理（本地录音就是 WAV）。
func audioMIME(mime string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch m {
	case "", "audio/wav", "audio/x-wav", "audio/wave":
		return "audio/wav"
	case "audio/mpeg", "audio/mp3":
		return "audio/mpeg"
	default:
		return ""
	}
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
	Model      string      `json:"model"`
	Messages   []message   `json:"messages"`
	ASROptions *asrOptions `json:"asr_options,omitempty"`
}

type message struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type       string     `json:"type"`
	InputAudio inputAudio `json:"input_audio"`
}

type inputAudio struct {
	Data string `json:"data"`
}

type asrOptions struct {
	Language string `json:"language"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}
