// Package asrdeepseek 实现 port.Transcriber：走 DeepSeek 的语音识别端点（D316）。
//
//	POST {base_url}/tools/asr
//	Content-Type: multipart/form-data
//	  file=<音频字节>（16 位 PCM WAV 或 Ogg Opus）
//	→ {"text": "..."}
//
// 与 asropenai 的差异是刻意的：**没有 model 与 language 字段**——21 种语言按音频自动识别，
// `input.language` 在这个后端不起作用（配置词表照收，请求里根本不带）；单段上限 120 秒 / 8 MiB，
// 超限明确报错，不在适配器里静默切片（切在词中间只会让转写更差，D316 决策 2）。
package asrdeepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tonyjh07/Aquarius/internal/domain/conversation"
	"github.com/Tonyjh07/Aquarius/internal/i18n"
	"github.com/Tonyjh07/Aquarius/internal/port"
)

const transcribePath = "/tools/asr"

// maxAudioBytes 识别单段的字节上限（服务端口径：8 MiB，120 秒 PCM WAV 即触顶）。
const maxAudioBytes = 8 << 20

const (
	maxErrorBody = 4 << 10
	maxRespBody  = 1 << 20
)

var _ port.Transcriber = (*Client)(nil)

// Config 适配器配置（base_url 与密钥来自 audio.asr 自己的配置或借来的 provider，D222）。
type Config struct {
	BaseURL string
	APIKey  string
	// Blobs 音频附件库：Transcribe 只拿到 BlobRef，字节在这里读。
	Blobs port.AttachmentStore
	// HTTP 为 nil 时用带 2 分钟整体超时的默认客户端（一次请求一次响应）。
	HTTP *http.Client
}

// Client 转写客户端。
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
		return nil, errors.New(i18n.T("asr.err.base_url_empty"))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New(i18n.Tf("asr.err.base_url_invalid", "url", cfg.BaseURL))
	}
	if cfg.Blobs == nil {
		return nil, errors.New(i18n.T("asr.err.no_blobs"))
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Client{
		base:  strings.TrimRight(raw, "/"),
		key:   strings.TrimSpace(cfg.APIKey),
		blobs: cfg.Blobs,
		hc:    hc,
	}, nil
}

// Transcribe 把一段音频转成文本。lang 参数收下但不发——该后端没有语种字段（D316 决策 1）。
func (c *Client) Transcribe(ctx context.Context, audio conversation.BlobRef, _ string) (string, error) {
	if ctx == nil {
		return "", errors.New(i18n.T("asr.err.nil_context"))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	rc, err := c.blobs.Get(ctx, audio)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.read_audio"), err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxAudioBytes+1))
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.read_audio"), err)
	}
	if len(data) == 0 {
		return "", errors.New(i18n.T("asr.err.audio_empty"))
	}
	if len(data) > maxAudioBytes {
		return "", errors.New(i18n.Tf("asr.err.audio_too_large", "mb", maxAudioBytes>>20))
	}
	name := audio.Name
	if strings.TrimSpace(name) == "" {
		name = "audio.wav" // 本地录音就是 WAV（D316 的另一形态 Ogg Opus 由外部文件带扩展名进来）
	}
	body, contentType, err := buildMultipart(data, name)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+transcribePath, body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.build_request"), err)
	}
	req.Header.Set("Content-Type", contentType)
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.request_failed"), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", errors.New(i18n.Tf("asr.err.server_status", "code", resp.StatusCode, "body", snippet(resp.Body)))
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBody)).Decode(&out); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("asr.err.parse_response"), err)
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		return "", errors.New(i18n.T("asr.err.no_text"))
	}
	return text, nil
}

// buildMultipart 组请求体：只有 file 一个字段——model 与 language 该后端都不认。
func buildMultipart(audio []byte, name string) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	if _, err := fw.Write(audio); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", i18n.T("asr.err.encode_request"), err)
	}
	return &buf, mw.FormDataContentType(), nil
}

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

type response struct {
	Text string `json:"text"`
}
